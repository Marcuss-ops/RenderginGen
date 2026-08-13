package version

import (
	"regexp"
	"testing"
)

var semverRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func TestRenderingGenVersionIsSemver(t *testing.T) {
	if !semverRe.MatchString(RenderingGen) {
		t.Fatalf("RenderingGen %q is not semver", RenderingGen)
	}
}

func TestOverlaySchemaPositive(t *testing.T) {
	if OverlaySchema <= 0 {
		t.Fatalf("OverlaySchema should be positive, got %d", OverlaySchema)
	}
}
