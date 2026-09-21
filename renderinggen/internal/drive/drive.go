// Package drive publishes rendered artifacts to Google Drive and materialises
// the destination folders an operator asks for.
//
// Rendering is decoupled from external publication: the worker first renders
// and stores the artifact in the content-addressed object store, then publishes
// it to Drive. If the Drive upload fails, the job is kept in the "rendered"
// state and a retry only re-runs the publication — never the GPU render.
//
// Two ports leave this package: Publisher (worker path: upload an artifact) and
// FolderCreator (operator path: find-or-create a destination folder). Both sit
// on the same find-or-create rule, so a folder a publication created and one an
// operator asked for by name resolve to the same folder.
package drive

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
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
	// SizeBytes is the provider-reported size when Drive returns one, else the
	// local file size. Preferring the provider's value makes the caller's
	// size check a real end-to-end assertion instead of a local restatement.
	SizeBytes int64
	// MD5Checksum is Drive's server-side content checksum, when returned. It is
	// the only provider-computed content digest the Drive API exposes; callers
	// can compare it against the local bytes to prove the upload is complete
	// and uncorrupted.
	MD5Checksum string
}

// Publisher uploads a rendered artifact to Google Drive. It is an interface so
// the real Google API client can be swapped for a Mock in tests and the local
// e2e smoke.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (Result, error)
}

// FolderCreator materialises one folder on the storage provider under an
// explicit parent and returns its stable id.
//
// It is a port next to Publisher rather than a tenth method on it because the
// two serve different callers: publication is what a worker does to an
// artifact, folder creation is what an operator does to a destination. Folding
// them together would force every Publisher (today the Google client and the
// Mock) to grow a folder method whether or not it can honour the contract.
//
// The parent is mandatory and explicit: there is no implicit "My Drive" root,
// because an empty parent silently writing into the credential owner's root is
// exactly the failure mode this contract exists to prevent.
type FolderCreator interface {
	EnsureFolder(ctx context.Context, parentFolderID, name string) (string, error)
}

// The two ports and their two implementations must stay in step: a publisher
// that stops satisfying one of them is a broken seam, not a compile error at
// the call site.
var (
	_ Publisher     = (*Google)(nil)
	_ FolderCreator = (*Google)(nil)
	_ Publisher     = (*Mock)(nil)
	_ FolderCreator = (*Mock)(nil)
)

// Google publishes artifacts to the real Google Drive API using a service
// account JSON key.
type Google struct {
	service      *gdrive.Service
	parentFolder string
	resumable    bool
	chunkBytes   int
}

// Options carries the publisher settings that are deployment choices rather
// than credentials. It is a struct so adding a setting does not change the
// constructors' arity again.
type Options struct {
	// ChunkBytes is the resumable-upload chunk size. Zero (or anything below
	// googleapi.MinUploadChunkSize, which the API rejects) uses
	// defaultResumableChunkBytes. It arrives from configuration
	// (drive.chunk_bytes) rather than being read from the environment inside
	// this package: the operator could previously set
	// RENDERINGGEN_DRIVE_CHUNK_BYTES with no way to see, from the worker's
	// configuration, what it had been set to.
	ChunkBytes int64
}

// chunkBytesFor applies the publisher's chunk-size policy to a configured
// value: an unusable size falls back to the default rather than producing a
// publisher whose every resumable request the API rejects.
func chunkBytesFor(configured int64) int {
	if configured < googleapi.MinUploadChunkSize {
		return defaultResumableChunkBytes
	}
	return int(configured)
}

// NewGoogle builds a Drive publisher from a service-account JSON credentials
// file. The file is created at the artifact's parent folder unless parentFolder
// is empty. Upload chunking uses the publisher default; use
// NewGoogleWithOptions to configure it.
func NewGoogle(ctx context.Context, credentialsFile, parentFolder string) (*Google, error) {
	return NewGoogleWithOptions(ctx, credentialsFile, parentFolder, Options{})
}

// NewGoogleWithOptions is NewGoogle with explicit publisher settings.
func NewGoogleWithOptions(ctx context.Context, credentialsFile, parentFolder string, opts Options) (*Google, error) {
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
		chunkBytes: chunkBytesFor(opts.ChunkBytes)}, nil
}

// NewGoogleOAuth builds a Drive publisher from a Google OAuth2 client
// credentials file (credentials.json) and an authorized token file
// (token.json — the shape PipelineGen's generate_drive_token.py writes:
// access_token / token_type / refresh_token / expiry). The token is refreshed
// and persisted back to tokenFile when it expires. The user account owning
// the token must have write access to the parent folder. Upload chunking uses
// the publisher default; use NewGoogleOAuthWithOptions to configure it.
func NewGoogleOAuth(ctx context.Context, credentialsFile, tokenFile, parentFolder string) (*Google, error) {
	return NewGoogleOAuthWithOptions(ctx, credentialsFile, tokenFile, parentFolder, Options{})
}

// NewGoogleOAuthWithOptions is NewGoogleOAuth with explicit publisher settings.
func NewGoogleOAuthWithOptions(ctx context.Context, credentialsFile, tokenFile, parentFolder string, opts Options) (*Google, error) {
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
		chunkBytes: chunkBytesFor(opts.ChunkBytes)}, nil
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
	// Hash the bytes before handing the handle to the uploader. Drive reports
	// size + md5Checksum for the stored object, so this local digest makes the
	// provider's answer falsifiable: a truncated or corrupted upload is caught
	// here instead of being silently recorded as a successful publication.
	localMD5, err := fileMD5(input)
	if err != nil {
		return Result{}, fmt.Errorf("drive: md5 %s: %w", req.Path, err)
	}
	create := g.service.Files.Create(file).
		Fields("id", "webViewLink", "parents", "size", "mimeType", "md5Checksum")
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
	// The provider's view of the uploaded object must agree with the bytes we
	// sent. Both fields are optional in the response (folders/shortcuts have no
	// size, and some backends omit the checksum); a value that is present but
	// disagrees is always a hard failure.
	size := info.Size()
	if res.Size != 0 {
		if res.Size != info.Size() {
			return Result{}, fmt.Errorf("drive: uploaded %s is %d bytes, local file is %d", req.Name, res.Size, info.Size())
		}
		size = res.Size
	}
	if res.Md5Checksum != "" && !strings.EqualFold(res.Md5Checksum, localMD5) {
		return Result{}, fmt.Errorf("drive: uploaded %s md5 %s, local md5 %s", req.Name, res.Md5Checksum, localMD5)
	}
	return Result{FileID: res.Id, WebViewLink: link, ParentFolder: parent,
		SizeBytes: size, MD5Checksum: res.Md5Checksum}, nil
}

// fileMD5 streams an open file through MD5 and rewinds it, so the uploader can
// consume the very same handle afterwards.
func fileMD5(f *os.File) (string, error) {
	hasher := md5.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// EnsureFolder returns the id of the folder `name` under parentFolderID,
// creating it only when it does not exist yet. It is the operator entry point
// onto the same find-or-create rule Publish applies for PublishRequest.Subfolder,
// so a folder created by this call and one created as the side effect of a
// publication are the same folder — not two folders with the same name.
//
// Both arguments are required and are trimmed before use: a blank name would
// ask Drive to materialise a nameless folder, and a blank parent would place it
// in the credential owner's root.
func (g *Google) EnsureFolder(ctx context.Context, parentFolderID, name string) (string, error) {
	if g == nil || g.service == nil {
		return "", fmt.Errorf("drive: ensure folder %q: publisher is not configured", name)
	}
	parent := strings.TrimSpace(parentFolderID)
	if parent == "" {
		return "", fmt.Errorf("drive: ensure folder %q: parent folder id is required", name)
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("drive: ensure folder: folder name is required")
	}
	return g.ensureFolder(ctx, parent, trimmed)
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
