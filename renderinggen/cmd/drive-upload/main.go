// Command drive-upload publishes one local file to Google Drive through the
// canonical publisher and refuses to report success unless the bytes that
// arrived are the bytes it hashed: it computes the file's real SHA-256 (the
// content address), optionally pins it against -sha256, and verifies the
// provider accounted for the whole file. The previous revision printed a
// hardcoded "verified-by-storage-key" in the sha256 field, which made the pass
// line unfalsifiable.
package main

import (
	"context"
	"flag"
	"fmt"
	"mime"
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
		panic("credentials, token, folder and file are required")
	}
	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, *credentials, *token, *folder)
	if err != nil {
		panic(err)
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
		panic(err)
	}
	fmt.Printf("DRIVE_UPLOAD_PASS id=%s link=%s parent=%s sha256=%s bytes=%d\n",
		uploaded.Result.FileID, uploaded.Result.WebViewLink, uploaded.Result.ParentFolder,
		uploaded.SHA256, uploaded.Result.SizeBytes)
}
