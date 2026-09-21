package drive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// TestGooglePublishUsesConfiguredParentFolder guards the fix where Publish
// ignored the configured parent folder and uploaded to "My Drive" root.
func TestGooglePublishUsesConfiguredParentFolder(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-1","webViewLink":"https://drive.google.com/file/d/file-1/view"}`))
	}))
	defer srv.Close()

	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	g := &Google{service: svc, parentFolder: "folder-123"}
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:        "x.mp4",
		ContentType: "video/mp4",
		Path:        path,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.FileID != "file-1" || res.WebViewLink != "https://drive.google.com/file/d/file-1/view" {
		t.Fatalf("result = %+v", res)
	}
	if !strings.Contains(string(body), `"parents":["folder-123"]`) {
		t.Fatalf("upload did not use the configured parent folder; body=%s", body)
	}
}

// TestGooglePublishRequestParentOverridesConfigured ensures a per-request
// parent wins, and the configured folder never leaks into the request.
func TestGooglePublishRequestParentOverridesConfigured(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"file-2","webViewLink":""}`))
	}))
	defer srv.Close()

	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	g := &Google{service: svc, parentFolder: "configured-folder"}
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := g.Publish(context.Background(), PublishRequest{
		Name:         "x.mp4",
		Path:         path,
		ParentFolder: "override-folder",
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.Contains(string(body), `"parents":["override-folder"]`) {
		t.Fatalf("request parent should override the configured folder; body=%s", body)
	}
	if strings.Contains(string(body), "configured-folder") {
		t.Fatalf("configured folder leaked into request; body=%s", body)
	}
	// Empty webViewLink must fall back to the canonical URL shape.
	if res.WebViewLink != "https://drive.google.com/file/d/file-2/view" {
		t.Fatalf("webViewLink fallback = %q", res.WebViewLink)
	}
}

// driveStub returns a fixed Create response body, so the provider-side
// integrity checks can be exercised without a real Drive account.
func driveStub(t *testing.T, body string) *Google {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &Google{service: svc}
}

func writeABC(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x.mp4")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// const abcMD5 is the MD5 of the three bytes "abc", the content used by the
// integrity tests below.
const abcMD5 = "900150983cd24fb0d6963f7d28e17f72"

// TestGooglePublishUsesProviderReportedIntegrity pins that the returned size
// and checksum come from the provider when Drive reports them: the caller's
// size comparison is then a real end-to-end assertion, not a local echo.
func TestGooglePublishUsesProviderReportedIntegrity(t *testing.T) {
	g := driveStub(t, `{"id":"file-3","size":"3","md5Checksum":"`+abcMD5+`"}`)
	res, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if res.SizeBytes != 3 {
		t.Fatalf("SizeBytes = %d, want provider-reported 3", res.SizeBytes)
	}
	if res.MD5Checksum != abcMD5 {
		t.Fatalf("MD5Checksum = %q, want %q", res.MD5Checksum, abcMD5)
	}
}

// TestGooglePublishRejectsChecksumMismatch pins that a provider checksum that
// disagrees with the uploaded bytes fails the publication — the truncation /
// corruption detector the local-size-only check could never provide.
func TestGooglePublishRejectsChecksumMismatch(t *testing.T) {
	g := driveStub(t, `{"id":"file-4","size":"3","md5Checksum":"`+strings.Repeat("0", 32)+`"}`)
	_, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err == nil || !strings.Contains(err.Error(), "md5") {
		t.Fatalf("want md5 mismatch, got %v", err)
	}
}

// TestGooglePublishRejectsProviderSizeMismatch pins the size half of the same
// check: a provider that stored fewer bytes than the local file is a failure.
func TestGooglePublishRejectsProviderSizeMismatch(t *testing.T) {
	g := driveStub(t, `{"id":"file-5","size":"4"}`)
	_, err := g.Publish(context.Background(), PublishRequest{
		Name: "x.mp4", Path: writeABC(t), ContentType: "video/mp4"})
	if err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("want size mismatch, got %v", err)
	}
}

// ── EnsureFolder (the FolderCreator port) ──────────────────────────────────

// folderProvider is the Drive stub behind EnsureFolder. It answers the lookup
// (Files.List) with listBody, the create (Files.Create) with createID, and
// records what it was asked, so a test can prove which calls the port made and
// what it sent.
type folderProvider struct {
	listBody    string
	createID    string
	createBody  string
	createCalls int
	listCalls   int
}

// newFolderProviderGoogle wires a *Google against the stub above.
func newFolderProviderGoogle(t *testing.T, p *folderProvider) *Google {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			p.listCalls++
			_, _ = w.Write([]byte(p.listBody))
		case http.MethodPost:
			raw, _ := io.ReadAll(r.Body)
			p.createBody = string(raw)
			p.createCalls++
			_, _ = w.Write([]byte(`{"id":"` + p.createID + `"}`))
		default:
			t.Errorf("unexpected Drive call %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(srv.Close)
	svc, err := gdrive.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &Google{service: svc}
}

// TestGoogleEnsureFolderReturnsExistingFolder pins the get half of the
// get-or-create rule: when a folder of that name already sits under the parent,
// the port returns its id and never issues a Create — a second folder with the
// same name under the same parent is the duplicate the contract forbids.
func TestGoogleEnsureFolderReturnsExistingFolder(t *testing.T) {
	p := &folderProvider{listBody: `{"files":[{"id":"existing-folder","name":"boxe","parents":["parent-1"]}]}`}
	g := newFolderProviderGoogle(t, p)

	id, err := g.EnsureFolder(context.Background(), "parent-1", "boxe")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if id != "existing-folder" {
		t.Fatalf("id = %q, want the existing folder id", id)
	}
	if p.createCalls != 0 {
		t.Fatalf("Create was called %d time(s) for a folder that already exists", p.createCalls)
	}
	if p.listCalls != 1 {
		t.Fatalf("lookup called %d time(s), want 1", p.listCalls)
	}
}

// TestGoogleEnsureFolderCreatesMissingFolder pins the create half: the folder
// is created with the Drive folder MIME type under the requested parent, and
// the provider's id is what the caller receives.
func TestGoogleEnsureFolderCreatesMissingFolder(t *testing.T) {
	p := &folderProvider{listBody: `{"files":[]}`, createID: "created-folder"}
	g := newFolderProviderGoogle(t, p)

	id, err := g.EnsureFolder(context.Background(), "parent-1", "  boxe  ")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	if id != "created-folder" {
		t.Fatalf("id = %q, want the created folder id", id)
	}
	if p.createCalls != 1 {
		t.Fatalf("Create called %d time(s), want 1", p.createCalls)
	}
	for _, want := range []string{`"name":"boxe"`, `"parents":["parent-1"]`, "application/vnd.google-apps.folder"} {
		if !strings.Contains(p.createBody, want) {
			t.Errorf("create body %s does not contain %s", p.createBody, want)
		}
	}
}

// TestGoogleEnsureFolderRejectsMissingArguments pins the fail-closed half of
// the contract: a blank name or a blank parent is refused before any Drive call
// (a blank parent would drop the folder into the credential owner's root).
func TestGoogleEnsureFolderRejectsMissingArguments(t *testing.T) {
	cases := []struct {
		name       string
		parent     string
		folderName string
		wantErr    string
	}{
		{"blank name", "parent-1", "   ", "folder name is required"},
		{"blank parent", "   ", "boxe", "parent folder id is required"},
		{"empty parent", "", "boxe", "parent folder id is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &folderProvider{listBody: `{"files":[]}`, createID: "unused"}
			g := newFolderProviderGoogle(t, p)

			_, err := g.EnsureFolder(context.Background(), tc.parent, tc.folderName)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
			if p.listCalls != 0 || p.createCalls != 0 {
				t.Fatalf("a rejected argument still issued Drive calls (list=%d create=%d)", p.listCalls, p.createCalls)
			}
		})
	}
}

// TestGoogleEnsureFolderRejectsEmptyProviderID pins that a provider answering
// Create with no id is a failure, not a silent "folder-000": an empty handle
// cannot be used for the next step (uploading into it) and would be reported as
// success by anything that only checked the error.
func TestGoogleEnsureFolderRejectsEmptyProviderID(t *testing.T) {
	p := &folderProvider{listBody: `{"files":[]}`, createID: ""}
	g := newFolderProviderGoogle(t, p)

	_, err := g.EnsureFolder(context.Background(), "parent-1", "boxe")
	if err == nil || !strings.Contains(err.Error(), "empty id") {
		t.Fatalf("err = %v, want an empty-id failure", err)
	}
}

// TestGoogleEnsureFolderFailsClosedWithoutService pins that an unwired
// publisher refuses instead of panicking on a nil client.
func TestGoogleEnsureFolderFailsClosedWithoutService(t *testing.T) {
	var g *Google
	_, err := g.EnsureFolder(context.Background(), "parent-1", "boxe")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("err = %v, want a not-configured failure", err)
	}
}

// TestMockEnsureFolderIsDeterministicAndCreatesTheFolder pins the Mock half of
// the port: the same (parent, name) always resolves to the same id — which is
// what makes the get-or-create contract observable without a Drive account —
// and the folder exists on disk afterwards, so the operator command can be
// smoke-tested locally.
func TestMockEnsureFolderIsDeterministicAndCreatesTheFolder(t *testing.T) {
	dir := t.TempDir()
	m := NewMock(dir, 0)

	first, err := m.EnsureFolder(context.Background(), "parent-1", "boxe")
	if err != nil {
		t.Fatalf("EnsureFolder: %v", err)
	}
	second, err := m.EnsureFolder(context.Background(), "parent-1", "boxe")
	if err != nil {
		t.Fatalf("second EnsureFolder: %v", err)
	}
	if first != second {
		t.Fatalf("ids differ for the same (parent, name): %q vs %q", first, second)
	}
	if _, err := os.Stat(filepath.Join(dir, "boxe")); err != nil {
		t.Fatalf("folder was not materialised: %v", err)
	}
}

// TestMockEnsureFolderRejectsMissingArguments mirrors the Google contract, so a
// caller swapping one for the other cannot get a different answer for the same
// bad input.
func TestMockEnsureFolderRejectsMissingArguments(t *testing.T) {
	m := NewMock(t.TempDir(), 0)
	if _, err := m.EnsureFolder(context.Background(), "parent-1", "  "); err == nil || !strings.Contains(err.Error(), "folder name is required") {
		t.Fatalf("blank name: err = %v, want a name failure", err)
	}
	if _, err := m.EnsureFolder(context.Background(), "", "boxe"); err == nil || !strings.Contains(err.Error(), "parent folder id is required") {
		t.Fatalf("blank parent: err = %v, want a parent failure", err)
	}
}
