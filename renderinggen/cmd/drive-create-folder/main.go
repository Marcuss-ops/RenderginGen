// Command drive-create-folder creates one folder on Google Drive through the
// canonical find-or-create rule and refuses to report success unless the
// provider handed back a usable folder id.
//
// Why it is not folded into drive-upload. Publication needs an artifact to
// upload; a destination folder often has to exist before the artifact that will
// land in it — an operator preparing tomorrow's destination, a job whose
// per-language subfolder is created ahead of the render, a destination an
// operator names in a runbook. Until now the only way to create such a folder
// from RenderingGen was to publish a file into it (PublishRequest.Subfolder),
// which couples "make the destination" to "have something to put there". Both
// paths share internal/drive's ensureFolder, so the folder this command creates
// and the folder a publication creates are the same folder rather than two
// candidates with the same name.
//
// It never invents credentials: -credentials, -token, -folder and -name are
// required, with no defaults pointing at a checked-in file (same rule as
// drive-upload).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/drive"
)

// folderURL is the canonical Drive folder link for an id. Kept as one function
// so the printed link and any future caller cannot drift into two spellings.
func folderURL(folderID string) string {
	return "https://drive.google.com/drive/folders/" + folderID
}

// createAndVerify creates the folder and rejects a blank result. "Success" has
// to mean the provider returned a handle the next step can use: a silent empty
// id would make the command print a PASS line whose URL points at nothing, and
// the caller would only find out when it tried to upload into it.
func createAndVerify(ctx context.Context, creator drive.FolderCreator, parentFolderID, name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", fmt.Errorf("drive-create-folder: folder name is required")
	}
	id, err := creator.EnsureFolder(ctx, parentFolderID, trimmed)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("drive-create-folder: provider returned an empty folder id for %q under %q", trimmed, parentFolderID)
	}
	return id, nil
}

func main() {
	credentials := flag.String("credentials", "", "OAuth credentials JSON")
	token := flag.String("token", "", "OAuth token JSON")
	folder := flag.String("folder", "", "Drive parent folder id")
	name := flag.String("name", "", "folder name to create under the parent")
	flag.Parse()
	if *credentials == "" || *token == "" || *folder == "" || *name == "" {
		// Fail with the usage on stderr and a non-zero status: a panic here
		// would print a stack trace for a plain missing flag, which buries the
		// one line an operator needs.
		fmt.Fprintln(os.Stderr, "drive-create-folder: -credentials, -token, -folder and -name are required")
		flag.PrintDefaults()
		os.Exit(2)
	}
	ctx := context.Background()
	publisher, err := drive.NewGoogleOAuth(ctx, *credentials, *token, *folder)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-create-folder: %v\n", err)
		os.Exit(1)
	}
	id, err := createAndVerify(ctx, publisher, *folder, *name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "drive-create-folder: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("DRIVE_FOLDER_PASS id=%s name=%s parent=%s url=%s\n",
		id, strings.TrimSpace(*name), *folder, folderURL(id))
}
