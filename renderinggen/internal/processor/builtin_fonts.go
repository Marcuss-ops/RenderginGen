package processor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/assets"
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
	for font := range fonts {
		target := filepath.Join(root, filepath.FromSlash(font))
		if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
			continue
		}
		data, err := assets.Fonts.ReadFile(strings.TrimPrefix(font, "assets/"))
		if err != nil {
			return fmt.Errorf("processor: official preset font %q is missing from the editorial_v1 bundle", font)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("processor: create font asset directory for %q: %w", font, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() {
				continue
			}
			return fmt.Errorf("processor: stage bundled font %q: %w", font, err)
		}
		// No fsync: the workspace is scratch, and a crash re-runs the job (the
		// font comes from the embedded read-only bundle, not from the network.
		_, writeErr := out.Write(data)
		closeErr := out.Close()
		if writeErr != nil || closeErr != nil {
			_ = os.Remove(target)
			return fmt.Errorf("processor: stage bundled font %q: %v", font, errors.Join(writeErr, closeErr))
		}
	}
	return nil
}
