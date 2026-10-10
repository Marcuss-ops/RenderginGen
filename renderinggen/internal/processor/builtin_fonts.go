package processor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/overlay"
)

// materializeBuiltinFonts copies any official preset fonts referenced by the
// concrete plan into this job's asset root. Semantic caption templates name a
// bundled font path, but that font is not a producer asset and therefore is
// not part of job.Assets. Chronon still validates every referenced logical
// path against the mounted workspace, so the worker must stage the bundle
// before handing the plan off.
func materializeBuiltinFonts(root string, plan *overlay.Plan) error {
	if plan == nil {
		return nil
	}
	fonts := map[string]struct{}{}
	for _, layer := range plan.Layers {
		if layer.Style == nil {
			continue
		}
		font := filepath.ToSlash(strings.TrimSpace(layer.Style.Font))
		if strings.HasPrefix(font, "assets/fonts/") {
			fonts[font] = struct{}{}
		}
	}
	if len(fonts) == 0 {
		return nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("processor: locate official font bundle: %w", err)
	}
	for font := range fonts {
		target := filepath.Join(root, filepath.FromSlash(font))
		if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
			continue
		}
		var source string
		for ancestor := wd; ; ancestor = filepath.Dir(ancestor) {
			for _, bundle := range []string{
				filepath.Join(ancestor, "renderinggen", "out", "editorial_v1"),
				filepath.Join(ancestor, "out", "editorial_v1"),
				// Shipped module assets are the last resort: the out/
				// bundles above are gitignored render artifacts, so a fresh
				// checkout has no other source for official preset fonts.
				filepath.Join(ancestor, "renderinggen"),
			} {
				candidate := filepath.Join(bundle, filepath.FromSlash(font))
				if info, statErr := os.Stat(candidate); statErr == nil && info.Mode().IsRegular() {
					source = candidate
					break
				}
			}
			if source != "" || filepath.Dir(ancestor) == ancestor {
				break
			}
		}
		if source == "" {
			return fmt.Errorf("processor: official preset font %q is missing from the editorial_v1 bundle", font)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("processor: create font asset directory for %q: %w", font, err)
		}
		in, err := os.Open(source)
		if err != nil {
			return fmt.Errorf("processor: open bundled font %q: %w", font, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			in.Close()
			if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
				continue
			}
			return fmt.Errorf("processor: stage bundled font %q: %w", font, err)
		}
		_, copyErr := io.Copy(out, in)
		closeOutErr, closeInErr := out.Close(), in.Close()
		if copyErr != nil || closeOutErr != nil || closeInErr != nil {
			_ = os.Remove(target)
			return fmt.Errorf("processor: copy bundled font %q: %v", font, errors.Join(copyErr, closeOutErr, closeInErr))
		}
	}
	return nil
}
