package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/Marcuss-ops/RenderginGen/renderinggen/internal/drive"
)

func main() {
	credentials := flag.String("credentials", "", "OAuth credentials JSON")
	token := flag.String("token", "", "OAuth token JSON")
	folder := flag.String("folder", "", "Drive parent folder id")
	subfolder := flag.String("subfolder", "", "Drive child folder name")
	file := flag.String("file", "", "local file to upload")
	name := flag.String("name", "", "Drive file name")
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
	result, err := publisher.Publish(ctx, drive.PublishRequest{
		Name: fileName, ContentType: "video/mp4", Path: *file,
		ParentFolder: *folder, Subfolder: *subfolder,
	})
	if err != nil {
		panic(err)
	}
	fmt.Printf("DRIVE_UPLOAD_PASS id=%s link=%s parent=%s sha256=%s bytes=%d\n", result.FileID, result.WebViewLink, result.ParentFolder, result.SHA256, result.SizeBytes)
}
