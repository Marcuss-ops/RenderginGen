// Package version holds build/version metadata shared across the worker.
package version

// These values are overridden at build time via
// -ldflags "-X github.com/Marcuss-ops/RenderginGen/renderinggen/internal/version.RenderingGen=...".
var (
	RenderingGen  = "0.1.0"
	OverlaySchema = 3
)
