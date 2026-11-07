package main

import (
	"encoding/json"
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
	"os"
)

func main() {
	b, _ := os.ReadFile("/tmp/r20j-overlay-plan.json")
	r, e := overlay.CompileSemantic(b)
	if e != nil {
		panic(e)
	}
	p, e := json.MarshalIndent(r.Plan, "", "  ")
	if e != nil {
		panic(e)
	}
	if e = os.WriteFile("/tmp/r20j-render-plan.json", p, 0600); e != nil {
		panic(e)
	}
}
