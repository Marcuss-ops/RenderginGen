// Command motion-catalog emits RenderingGen's compiled, versioned animation
// catalog for picker and tooling consumers.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("motion-catalog", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "write the catalog to this file instead of stdout")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "motion-catalog: unexpected arguments: %v\n", flags.Args())
		return 2
	}
	var encoded bytes.Buffer
	if err := overlay.WriteRuntimeAnimationCatalog(&encoded); err != nil {
		fmt.Fprintf(stderr, "motion-catalog: compile catalog: %v\n", err)
		return 1
	}
	if *output == "" {
		if _, err := io.Copy(stdout, &encoded); err != nil {
			fmt.Fprintf(stderr, "motion-catalog: write stdout: %v\n", err)
			return 1
		}
		return 0
	}
	if err := os.WriteFile(*output, encoded.Bytes(), 0o644); err != nil {
		fmt.Fprintf(stderr, "motion-catalog: write %s: %v\n", *output, err)
		return 1
	}
	return 0
}
