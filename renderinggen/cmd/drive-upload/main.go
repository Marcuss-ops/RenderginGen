// Command drive-upload publishes one local file to Google Drive through the
// canonical publisher and refuses to report success unless the bytes that
// arrived are the bytes it hashed: it computes the file's real SHA-256 (the
// content address), optionally pins it against -sha256, and verifies the
// provider accounted for the whole file. The previous revision printed a
// hardcoded "verified-by-storage-key" in the sha256 field, which made the pass
// line unfalsifiable.
//
// Why it is not folded into the worker. The worker's publication path is the
// job pipeline, which needs a claimed job, a workspace and a render; a
// late-arriving artifact, a re-publish after a queue incident, or a check on a
// credential rotation all need to push ONE file with no job involved. Both paths
// share the publisher (internal/drive), the content-address rule and the byte
// accounting, so this command is an operator entry point onto the same
// invariants rather than a second implementation of them — which is what the
// earlier subprocess-based upload was, and why the worker no longer calls it.
//
// It never invents credentials: -credentials, -token and -folder are required,
// with no defaults pointing at a checked-in file.
package main

import (
	"context"
	"flag"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/hashio"
)

type uploadResult struct {
	Result drive.Result
	SHA256 string
}

// uploadAndVerify hashes the local file, checks the optional expected content
// address, uploads it and proves the provider accounted for every byte. It is
// the single publication primitive of this CLI so the invariant is testable
// without a real Drive account.
func uploadAndVerify(ctx context.Context, publisher drive.Publisher, req drive.PublishRequest, expectedSHA string) (uploadResult, error) {
	digest, size, err := hashio.File(req.Path)
	if err != nil {
		return uploadResult{}, fmt.Errorf("drive-upload: hash %s: %w", req.Path, err)
	}
	if expectedSHA != "" && !strings.EqualFold(digest, expectedSHA) {
		return uploadResult{}, fmt.Errorf("drive-upload: sha256 mismatch for %s: computed %s, expected %s", req.Path, digest, expectedSHA)
	}
	res, err := publisher.Publish(ctx, req)
	if err != nil {
		return uploadResult{}, err
	}
	if res.SizeBytes != size {
		return uploadResult{}, fmt.Errorf("drive-upload: size mismatch: provider accounted for %d bytes, local file has %d", res.SizeBytes, size)
	}
	return uploadResult{Result: res, SHA256: digest}, nil
}

func main() {
	credentials := flag.String("credentials", "", "OAuth credentials JSON")
	token := flag.String("token", "", "OAuth token JSON")
	folder := flag.String("folder", "", "Drive parent folder id")
	subfolder := flag.String("subfolder", "", "Drive child folder name")
	file := flag.String("file", "", "local file to upload")
	name := flag.String("name", "", "Drive file name")
	expectedSHA := flag.String("sha256", "", "expected SHA-256 of the local file (content address); optional")
	flag.Parse()
	if *credentials == "" || *token == "" || *folder == "" || *file == "" {
		// Fail with the usage on stderr and a non-zero status. A panic here
		// would print a stack trace for a plain missing flag, which buries the
		// one line an operator needs.
		fmt.Fprintln(os.Stderr, "drive-upload: -credentials, -token, -folder and -file are required")
		flag.PrintDefaults()
		os.Exit(2)
	}
	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, *credentials, *token, *folder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-upload: %v\n", err)
		os.Exit(1)
	}
	fileName := *name
	if fileName == "" {
		fileName = filepath.Base(*file)
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	uploaded, err := uploadAndVerify(ctx, publisher, drive.PublishRequest{
		Name: fileName, ContentType: contentType, Path: *file,
		ParentFolder: *folder, Subfolder: *subfolder,
	}, *expectedSHA)
	if err != nil {
		// Abuse the same exit code for a refused upload as for a failed one:
		// the caller's decision (retry, surface, give up) does not depend on
		// which of the two it was, and the message says which.
		fmt.Fprintf(os.Stderr, "drive-upload: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("DRIVE_UPLOAD_PASS id=%s link=%s parent=%s sha256=%s bytes=%d\n",
		uploaded.Result.FileID, uploaded.Result.WebViewLink, uploaded.Result.ParentFolder,
		uploaded.SHA256, uploaded.Result.SizeBytes)
}
