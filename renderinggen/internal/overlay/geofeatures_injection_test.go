package overlay

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/renderinggen/internal/geofeatures"
)

// loadTestGeoRegistry loads the versioned catalog without touching the
// process-wide cache or CHRONONTEMPLATE_CATALOG.
func loadTestGeoRegistry(t *testing.T) *geofeatures.Registry {
	t.Helper()
	registry, err := geofeatures.LoadDir(filepath.Join("..", "geofeatures", "testdata", "catalog"))
	if err != nil {
		t.Fatalf("load test catalog: %v", err)
	}
	return registry
}

func decodeTestSemanticPlan(t *testing.T, plan map[string]any) semanticPlan {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	src, err := decodeSemanticPlan(raw)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestFlyToFeatureUsesInjectedRegistry pins the injection contract: fly-to
// resolution must read CompileOptions.GeoRegistry, and a nil registry fails
// closed with an actionable message instead of silently reaching for a global.
func TestFlyToFeatureUsesInjectedRegistry(t *testing.T) {
	plan := cameraMapPlan()
	item := plan["items"].([]any)[0].(map[string]any)
	mapSpec := item["map"].(map[string]any)
	// Same offline LOD window the fly-to certification test uses: the fit needs
	// a second level at or below the zoom where the country still fits.
	mapSpec["center"] = map[string]any{"latitude": 51.0, "longitude": -5.0}
	mapSpec["zoom"] = 4
	lods := mapSpec["lods"].([]any)
	lods[0].(map[string]any)["center"] = mapSpec["center"]
	lods[0].(map[string]any)["zoom"] = 4
	lods[1].(map[string]any)["zoom"] = 5
	lods[1].(map[string]any)["center"] = map[string]any{"latitude": 51.0, "longitude": 0.0}
	delete(mapSpec, "motion_id")
	delete(mapSpec, "camera_move")
	mapSpec["fly_to_feature"] = map[string]any{"feature": "United Kingdom", "padding": 0.12}

	withoutRegistry := decodeTestSemanticPlan(t, plan)
	err := resolveMapFeatureMoves(&withoutRegistry, nil)
	if err == nil || !strings.Contains(err.Error(), "CompileOptions.GeoRegistry") {
		t.Fatalf("fly_to_feature without a registry = %v, want an error naming CompileOptions.GeoRegistry", err)
	}
	if withoutRegistry.Items[0].Map.CameraMove != nil {
		t.Fatal("a failed resolve must not leave a camera move behind")
	}

	withRegistry := decodeTestSemanticPlan(t, plan)
	if err := resolveMapFeatureMoves(&withRegistry, loadTestGeoRegistry(t)); err != nil {
		t.Fatalf("fly_to_feature with an injected registry: %v", err)
	}
	if withRegistry.Items[0].Map.CameraMove == nil {
		t.Fatal("fly_to_feature did not produce a camera move with the injected registry")
	}
}

// TestModernMapCompileIsIndependentOfWorkingDirectory proves the fixture
// catalog is addressed by the injected registry, not by a parent-directory
// search: the compile runs from an unrelated temporary directory.
func TestModernMapCompileIsIndependentOfWorkingDirectory(t *testing.T) {
	registry := loadTestGeoRegistry(t)
	raw, err := json.Marshal(modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())

	result, err := CompileSemanticWithOptions(raw, CompileOptions{GeoRegistry: registry})
	if err != nil {
		t.Fatalf("ModernMap1 compile from a temporary cwd: %v", err)
	}
	if len(result.Plan.Layers) == 0 {
		t.Fatal("ModernMap1 compile produced no layers")
	}
}

// TestModernMapCompileWithoutRegistryFailsClosed makes the missing dependency
// visible: a map plan cannot fall back to whatever catalog the process
// happens to have cached.
func TestModernMapCompileWithoutRegistryFailsClosed(t *testing.T) {
	raw, err := json.Marshal(modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatal(err)
	}
	_, err = CompileSemanticWithOptions(raw, CompileOptions{})
	if err == nil || !strings.Contains(err.Error(), "CompileOptions.GeoRegistry") {
		t.Fatalf("ModernMap1 without a registry = %v, want an error naming CompileOptions.GeoRegistry", err)
	}
}

// TestConcurrentMapCompilesShareOneRegistry runs the injection under -race:
// the registry is read-only after load, so concurrent plans must not mutate it
// or each other's decoded document.
func TestConcurrentMapCompilesShareOneRegistry(t *testing.T) {
	registry := loadTestGeoRegistry(t)
	raw, err := json.Marshal(modernMapPlan("Italy", nil))
	if err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wait sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			// Each compile owns its decoded document; only the registry is shared.
			if _, err := CompileSemanticWithOptions(raw, CompileOptions{GeoRegistry: registry}); err != nil {
				errs <- err
			}
		}()
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent ModernMap1 compile: %v", err)
	}
}
