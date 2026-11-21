// Package assets contains immutable files shipped with the RenderingGen worker.
package assets

import "embed"

// Fonts embeds the official preset fonts so workers do not depend on their
// process working directory or a separately staged editorial bundle.
//
//go:embed fonts/*.ttf
var Fonts embed.FS
