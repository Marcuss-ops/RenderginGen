package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

func main() {
	raw, err := os.ReadFile(os.Args[1])
	if err != nil { panic(err) }
	result, err := overlay.CompileSemantic(raw)
	if err != nil { panic(err) }
	fmt.Printf("layers=%d\n", len(result.Plan.Layers))
	for _, layer := range result.Plan.Layers {
		b, _ := json.Marshal(layer)
		fmt.Println(string(b))
	}
	out, err := json.Marshal(result.Plan)
	if err != nil { panic(err) }
	if err := os.WriteFile("/tmp/kaufmann-backdrop-compiled.json", out, 0o644); err != nil { panic(err) }
}
