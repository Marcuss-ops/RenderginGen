// Command batch-build writes the renderinggen.batch-manifest.v1 master for a
// preset corpus from that corpus's source of record.
//
// Two corpora, one manifest contract:
//
//	-request <fixed request.json>           the Mike Tyson preset corpus: the ten
//	                                        phrases carried by the request's own
//	                                        extraction block (each checked against
//	                                        its source segment) plus five entity
//	                                        image overlays;
//	-translations <translations.json>       the multilingual overlay matrix: five
//	                                        corpus phrases × N languages plus the
//	                                        five official image presets.
//
// The plans are written through internal/renderbatch's single semantic writer and
// compiled there, so a template/preset/motion that cannot resolve is reported at
// build time, naming the job, instead of failing on the GPU later.
//
// The generated manifest is data: cmd/batch-submit submits it and cmd/batch-run
// waits, collects and reports on it.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlaybatch"
)

func main() {
	requestPath := flag.String("request", "", "fixed request JSON to build the Mike Tyson preset corpus from")
	translations := flag.String("translations", "", "translations.json to build the multilingual matrix from")
	batchID := flag.String("batch-id", "", "batch id that scopes every derived job id (required)")
	assetBaseURL := flag.String("asset-base-url", "", "HTTP(S) root the worker self-heals the corpus assets from (required)")
	repoRoot := flag.String("repo-root", ".", "RenderingGen checkout root, used to hash the corpus assets")
	out := flag.String("out", "", "manifest to write (required)")
	plansDir := flag.String("plans-dir", "", "optional directory for one semantic plan per job")
	langs := flag.String("langs", "", "comma-separated language subset (multilingual mode; default: all)")
	only := flag.String("only", "", "comma-separated overlay ids (multilingual mode; default: the full matrix)")
	flag.Parse()

	if strings.TrimSpace(*batchID) == "" {
		fatal("-batch-id is required")
	}
	if strings.TrimSpace(*out) == "" {
		fatal("-out is required")
	}
	if (*requestPath == "") == (*translations == "") {
		fatal("exactly one of -request and -translations must be set")
	}

	var (
		result *overlaybatch.BuildResult
		err    error
	)
	if *requestPath != "" {
		result, err = overlaybatch.BuildTysonManifest(overlaybatch.TysonBuildOptions{
			RequestPath:  *requestPath,
			BatchID:      *batchID,
			AssetBaseURL: *assetBaseURL,
			RepoRoot:     *repoRoot,
			OutPath:      *out,
			PlanDir:      *plansDir,
		})
	} else {
		result, err = overlaybatch.BuildMultilingualManifest(overlaybatch.MultilingualBuildOptions{
			TranslationsPath: *translations,
			BatchID:          *batchID,
			AssetBaseURL:     *assetBaseURL,
			RepoRoot:         *repoRoot,
			Languages:        splitList(*langs),
			Only:             splitList(*only),
			OutPath:          *out,
			PlanDir:          *plansDir,
		})
	}
	if err != nil {
		fatal("%v", err)
	}

	summary, _ := json.MarshalIndent(struct {
		BatchID  string `json:"batch_id"`
		Jobs     int    `json:"jobs"`
		Phrases  int    `json:"phrase_overlays"`
		Images   int    `json:"image_overlays"`
		Manifest string `json:"manifest"`
		Plans    string `json:"plans_dir,omitempty"`
	}{result.BatchID, result.Jobs, result.Phrases, result.Images, result.OutPath, result.PlanDir}, "", "  ")
	fmt.Println(string(summary))
}

func splitList(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "batch-build: "+format+"\n", args...)
	os.Exit(1)
}
