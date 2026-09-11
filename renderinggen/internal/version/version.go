// Package version holds build/version metadata shared across the worker.
package version

import (
	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// These values are overridden at build time via
// -ldflags "-X github.com/Marcuss-ops/RenderingGen/renderinggen/internal/version.RenderingGen=...".
var (
	RenderingGen = "0.1.0"

	// OverlaySchema is the overlay contract version advertised on /health and
	// in the worker registry.
	//
	// It is DERIVED from the contract that is actually enforced
	// (overlay.SemanticSchema, i.e. renderinggen.overlay-plan.v1), not an
	// independent counter. It previously drifted: the worker advertised 3
	// while accepting only v1, so no consumer could relate the advertised
	// integer to the document schema it would be sent. A future contract bump
	// changes the owner (overlay.SemanticSchemaVersion) and this value follows
	// by construction.
	OverlaySchema = overlay.SemanticSchemaVersion
)
