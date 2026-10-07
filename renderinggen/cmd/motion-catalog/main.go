// Command motion-catalog emits the compiled runtime animation catalog used by
// selector and UI consumers. The payload is derived from the embedded canonical
// motion registry and semantic compiler use cases; it is never a second source
// of motion IDs.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func main() {
	output := flag.String("output", "-", "output JSON file, or - for stdout")
	flag.Parse()

	var destination io.Writer = os.Stdout
	if *output != "-" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "motion-catalog: create %s: %v\n", *output, err)
			os.Exit(1)
		}
		defer file.Close()
		destination = file
	}
	if err := overlay.WriteRuntimeAnimationCatalog(destination); err != nil {
		fmt.Fprintf(os.Stderr, "motion-catalog: %v\n", err)
		os.Exit(1)
	}
}
