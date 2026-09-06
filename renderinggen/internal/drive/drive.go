// Package drive publishes rendered artifacts to Google Drive.
//
// Rendering is decoupled from external publication: the worker first renders
// and stores the artifact in the content-addressed object store, then publishes
// it to Drive. If the Drive upload fails, the job is kept in the "rendered"
// state and a retry only re-runs the publication — never the GPU render.
package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const defaultResumableChunkBytes = 8 * 1024 * 1024

// PublishRequest is a single artifact publication to Google Drive.
type PublishRequest struct {
	Name         string // file name on Drive
	ContentType  string
	Path         string
	ParentFolder string // Drive folder ID to place the file into (optional)
	Subfolder    string // deterministic child folder name under ParentFolder (optional)
	// UploadProgress is called after each resumable chunk. It must be cheap;
	// the callback runs on the Drive upload path and must not block rendering.
	UploadProgress func(uploaded, total int64)
}

// Result is the outcome of a Drive publication.
type Result struct {
	FileID       string
	WebViewLink  string
	ParentFolder string
	SizeBytes    int64
}

// Publisher uploads a rendered artifact to Google Drive. It is an interface so
// the real Google API client can be swapped for a Mock in tests and the local
// e2e smoke.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (Result, error)
}

// Google publishes artifacts to the real Google Drive API using a service
// account JSON key.
type Google struct {
	service      *gdrive.Service
	parentFolder string
	resumable    bool
	chunkBytes   int
}

// NewGoogle builds a Drive publisher from a service-account JSON credentials
// file. The file is created at the artifact's parent folder unless parentFolder
// is empty.
func NewGoogle(ctx context.Context, credentialsFile, parentFolder string) (*Google, error) {
	b, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("drive: read credentials %s: %w", credentialsFile, err)
	}
	creds, err := google.CredentialsFromJSON(ctx, b, gdrive.DriveFileScope)
	if err != nil {
		return nil, fmt.Errorf("drive: parse service-account credentials: %w", err)
	}
	svc, err := gdrive.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("drive: create service: %w", err)
	}
	return &Google{service: svc, parentFolder: parentFolder, resumable: true,
		chunkBytes: configuredChunkBytes()}, nil
}

// NewGoogleOAuth builds a Drive publisher from a Google OAuth2 client
// credentials file (credentials.json) and an authorized token file
// (token.json — the shape PipelineGen's generate_drive_token.py writes:
// access_token / token_type / refresh_token / expiry). The token is refreshed
// and persisted back to tokenFile when it expires. The user account owning
// the token must have write access to the parent folder.
func NewGoogleOAuth(ctx context.Context, credentialsFile, tokenFile, parentFolder string) (*Google, error) {
	b, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("drive: read credentials %s: %w", credentialsFile, err)
	}
	cfg, err := google.ConfigFromJSON(b, gdrive.DriveScope)
	if err != nil {
		return nil, fmt.Errorf("drive: parse oauth client credentials: %w", err)
	}
	tok, err := loadOAuthToken(tokenFile)
	if err != nil {
		return nil, fmt.Errorf("drive: read token %s: %w", tokenFile, err)
	}
	src := &refreshingTokenSource{source: cfg.TokenSource(ctx, tok), tokenFile: tokenFile}
	svc, err := gdrive.NewService(ctx, option.WithHTTPClient(oauth2.NewClient(ctx, src)))
	if err != nil {
		return nil, fmt.Errorf("drive: create service: %w", err)
	}
	return &Google{service: svc, parentFolder: parentFolder, resumable: true,
		chunkBytes: configuredChunkBytes()}, nil
}

func configuredChunkBytes() int {
	if raw := os.Getenv("RENDERINGGEN_DRIVE_CHUNK_BYTES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= googleapi.MinUploadChunkSize {
			return n
		}
	}
	return defaultResumableChunkBytes
}

// loadOAuthToken reads an oauth2 token file, tolerating the "token" and
// missing-token_type field spellings so the on-disk shape stays compatible
// with PipelineGen's token generator.
func loadOAuthToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if t, ok := raw["token"].(string); ok {
		raw["access_token"] = t
		delete(raw, "token")
	}
	if tt, _ := raw["token_type"].(string); tt == "" {
		raw["token_type"] = "Bearer"
	}
	normalized, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var tok oauth2.Token
	if err := json.Unmarshal(normalized, &tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// refreshingTokenSource wraps an oauth2 token source and persists refreshed
// tokens back to disk so a worker restart stays authorized.
type refreshingTokenSource struct {
	source    oauth2.TokenSource
	tokenFile string
	mu        sync.Mutex
}

func (r *refreshingTokenSource) Token() (*oauth2.Token, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	tok, err := r.source.Token()
	if err != nil {
		return nil, err
	}
	if r.tokenFile != "" {
		b, err := json.Marshal(tok)
		if err != nil {
			log.Printf("drive: persist refreshed token to %s: marshal: %v", r.tokenFile, err)
		} else if err := os.WriteFile(r.tokenFile, b, 0o600); err != nil {
			// A read-only mount or perms break means every restart does a fresh
			// auth dance; the in-process refresh still works, so this is a
			// durable-observability warning, not a failure.
			log.Printf("drive: persist refreshed token to %s: %v", r.tokenFile, err)
		}
	}
	return tok, nil
}

// Publish uploads the artifact and returns its Drive file ID and web link.
func (g *Google) Publish(ctx context.Context, req PublishRequest) (Result, error) {
	file := &gdrive.File{Name: req.Name}
	// A per-request parent wins over the publisher's configured default, but
	// the configured folder must still apply when the caller leaves it empty
	// (the worker relies on this: it never sets ParentFolder itself).
	parent := req.ParentFolder
	if parent == "" {
		parent = g.parentFolder
	}
	if parent != "" {
		if req.Subfolder != "" {
			child, err := g.ensureFolder(ctx, parent, req.Subfolder)
			if err != nil {
				return Result{}, err
			}
			parent = child
		}
		file.Parents = []string{parent}
	}
	if req.Path == "" {
		return Result{}, fmt.Errorf("drive: publish %s has empty path", req.Name)
	}
	input, err := os.Open(req.Path)
	if err != nil {
		return Result{}, fmt.Errorf("drive: open %s: %w", req.Path, err)
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return Result{}, fmt.Errorf("drive: stat %s: %w", req.Path, err)
	}
	create := g.service.Files.Create(file).
		Fields("id", "webViewLink", "parents", "size", "mimeType")
	var call *gdrive.FilesCreateCall
	if g.resumable {
		chunkBytes := g.chunkBytes
		if chunkBytes < googleapi.MinUploadChunkSize {
			chunkBytes = defaultResumableChunkBytes
		}
		call = create.Media(input,
			googleapi.ContentType(req.ContentType),
			googleapi.ChunkSize(chunkBytes),
		).ProgressUpdater(func(uploaded, total int64) {
			if req.UploadProgress != nil {
				req.UploadProgress(uploaded, total)
			}
		}).Context(ctx)
	} else {
		call = create.Media(input, googleapi.ContentType(req.ContentType)).Context(ctx)
	}
	res, err := call.Do()
	if err != nil {
		return Result{}, fmt.Errorf("drive: upload %s: %w", req.Name, err)
	}
	// Files.Create only returns webViewLink when it is requested via Fields;
	// fall back to the canonical URL shape if it is still empty.
	link := res.WebViewLink
	if link == "" {
		link = "https://drive.google.com/file/d/" + res.Id + "/view"
	}
	if len(res.Parents) > 0 && parent != "" && res.Parents[0] != parent {
		return Result{}, fmt.Errorf("drive: uploaded file parent %q, want %q", res.Parents[0], parent)
	}
	return Result{FileID: res.Id, WebViewLink: link, ParentFolder: parent,
		SizeBytes: info.Size()}, nil
}

func (g *Google) ensureFolder(ctx context.Context, parent, name string) (string, error) {
	q := fmt.Sprintf("mimeType='application/vnd.google-apps.folder' and trashed=false and name='%s' and '%s' in parents", escapeDriveQuery(name), escapeDriveQuery(parent))
	list, err := g.service.Files.List().Q(q).Fields("files(id,name,parents)").PageSize(10).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("drive: find subfolder %s: %w", name, err)
	}
	if len(list.Files) > 0 && list.Files[0].Id != "" {
		return list.Files[0].Id, nil
	}
	f, err := g.service.Files.Create(&gdrive.File{
		Name: name, MimeType: "application/vnd.google-apps.folder", Parents: []string{parent},
	}).Fields("id,parents").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("drive: create subfolder %s: %w", name, err)
	}
	if f.Id == "" {
		return "", fmt.Errorf("drive: create subfolder %s returned empty id", name)
	}
	return f.Id, nil
}

func escapeDriveQuery(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}
