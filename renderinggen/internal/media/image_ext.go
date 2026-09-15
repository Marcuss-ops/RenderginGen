// image_ext.go owns the mapping from sniffed image bytes to the file extension
// the engine's loader expects.
//
// Why this exists. Chronon's image loader selects its decoder from the file
// EXTENSION, not from the bytes: a producer that publishes JPEG bytes behind a
// logical path ending in `.png` produces a valid-looking job that renders black.
// RenderingGen therefore has to make the materialized path agree with the bytes,
// and it has to do so with ONE vocabulary — the same extension set is consulted
// by the processor's post-materialization normalization and would be consulted
// by any future materialization-time check. Keeping the mapping next to the
// rest of the media-format knowledge (media owns ffprobe/ffmpeg invocation and
// the probe vocabulary) keeps it out of the render pipeline's arithmetic.
package media

import (
	"net/http"
	"strings"
)

// Image extensions, as the engine's loader spells them.
const (
	ExtPNG  = ".png"
	ExtJPEG = ".jpg"
	ExtWebP = ".webp"
	ExtGIF  = ".gif"
)

// ImageExtensionForData reports the extension matching head's bytes and whether
// head is an image format whose extension the loader keys on.
//
// head is a prefix of the file (512 bytes is what net/http needs for its full
// signature set); a short prefix is fine as long as it covers the format's magic
// number. ok=false means "these bytes are not an image format RenderingGen knows
// how to name", and the caller must leave the path alone rather than guess:
// renaming a file to an extension whose decoder cannot read it would convert a
// visible mismatch into a silent black frame.
//
// SVG is deliberately absent. It is an `image/*` type by content, but the
// loader's decoder set is raster, and normalizing an SVG to a raster extension
// would hand the engine a file it cannot decode.
func ImageExtensionForData(head []byte) (string, bool) {
	switch contentType := strings.TrimSpace(http.DetectContentType(head)); contentType {
	case "image/png":
		return ExtPNG, true
	case "image/jpeg":
		return ExtJPEG, true
	case "image/webp":
		return ExtWebP, true
	case "image/gif":
		return ExtGIF, true
	default:
		return "", false
	}
}

// ImageExtensionMatches reports whether path already carries the extension the
// loader would select for head. It is the predicate the normalization pass uses
// to decide whether a rename is needed, so the comparison lives with the
// vocabulary instead of being re-spelled at the call site.
func ImageExtensionMatches(path string, head []byte) bool {
	ext, ok := ImageExtensionForData(head)
	if !ok {
		// An unrecognized format is never "matching": the caller keeps the
		// producer's path, and reporting a match would also record it as
		// already-correct in any audit.
		return false
	}
	return strings.EqualFold(extensionOf(path), ext)
}

// extensionOf returns the path's extension including the dot, or "" when it has
// none. It is a local copy of filepath.Ext's contract for a bare path string
// (the media package deliberately does not import path/filepath for this
// one-line need, and callers pass logical slash-paths).
func extensionOf(path string) string {
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		if sep := strings.LastIndexAny(path, `/\`); sep > i {
			return ""
		}
		return path[i:]
	}
	return ""
}
