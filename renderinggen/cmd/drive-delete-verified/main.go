package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

func main() {
	credentials := flag.String("credentials", "", "OAuth client credentials JSON")
	tokenPath := flag.String("token", "", "OAuth token JSON")
	folder := flag.String("folder", "", "expected exact parent folder ID")
	fileID := flag.String("id", "", "Drive file ID to remove")
	expectedName := flag.String("name", "", "expected exact Drive file name")
	dryRun := flag.Bool("dry-run", false, "verify identity and parent without deleting")
	flag.Parse()
	if *credentials == "" || *tokenPath == "" || *folder == "" || *fileID == "" || *expectedName == "" {
		fmt.Fprintln(os.Stderr, "drive-delete-verified: credentials, token, folder, id, and name are all required")
		flag.PrintDefaults()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	credentialBytes, err := os.ReadFile(*credentials)
	if err != nil {
		fail("read OAuth client credentials: %v", err)
	}
	config, err := google.ConfigFromJSON(credentialBytes, gdrive.DriveScope)
	if err != nil {
		fail("parse OAuth client credentials: %v", err)
	}
	tokenBytes, err := os.ReadFile(*tokenPath)
	if err != nil {
		fail("read OAuth token: %v", err)
	}
	var token oauth2.Token
	if err := json.Unmarshal(tokenBytes, &token); err != nil {
		fail("parse OAuth token: %v", err)
	}
	if token.AccessToken == "" {
		var alternate struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal(tokenBytes, &alternate); err != nil || alternate.Token == "" {
			fail("OAuth token has no access token")
		}
		token.AccessToken = alternate.Token
	}
	if token.TokenType == "" {
		token.TokenType = "Bearer"
	}
	if err := os.Chmod(*tokenPath, 0o600); err != nil {
		fail("secure OAuth token file mode: %v", err)
	}
	source := config.TokenSource(ctx, &token)
	client := oauth2.NewClient(ctx, source)
	freshToken, err := source.Token()
	if err != nil {
		fail("authorize Drive request: %v", err)
	}
	if freshToken.AccessToken != token.AccessToken || freshToken.RefreshToken != token.RefreshToken ||
		!freshToken.Expiry.Equal(token.Expiry) {
		persisted, err := json.Marshal(freshToken)
		if err != nil {
			fail("encode refreshed OAuth token: %v", err)
		}
		if err := os.WriteFile(*tokenPath, persisted, 0o600); err != nil {
			fail("persist refreshed OAuth token: %v", err)
		}
	}
	service, err := gdrive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fail("create Drive API client: %v", err)
	}
	metadata, err := service.Files.Get(*fileID).Fields("id,name,parents,trashed").Context(ctx).Do()
	if err != nil {
		fail("read Drive file %s metadata: %v", *fileID, err)
	}
	if metadata.Id != *fileID || metadata.Name != *expectedName {
		fail("refusing delete: file identity mismatch (id=%q name=%q)", metadata.Id, metadata.Name)
	}
	parentMatches := false
	for _, parent := range metadata.Parents {
		parentMatches = parentMatches || parent == *folder
	}
	if !parentMatches {
		fail("refusing delete: Drive file %s is not directly inside requested folder", *fileID)
	}
	if metadata.Trashed {
		fmt.Printf("DRIVE_DELETE_PASS id=%s name=%q parent=%s state=already_trashed\n", *fileID, metadata.Name, *folder)
		return
	}
	if *dryRun {
		fmt.Printf("DRIVE_DELETE_DRY_RUN id=%s name=%q parent=%s verified=true\n", *fileID, metadata.Name, *folder)
		return
	}
	if err := service.Files.Delete(*fileID).Context(ctx).Do(); err != nil {
		fail("delete verified Drive file %s: %v", *fileID, err)
	}
	fmt.Printf("DRIVE_DELETE_PASS id=%s name=%q parent=%s state=deleted\n", *fileID, metadata.Name, *folder)
}

func fail(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	message = strings.ReplaceAll(message, "\n", " ")
	fmt.Fprintf(os.Stderr, "drive-delete-verified: %s\n", message)
	os.Exit(1)
}
