package motion

import "testing"

// TestCanonicalCatalogInitializationRegistersMotions exercises the package
// initializer whose panic is an initialization invariant. An empty registry
// would make every overlay render as empty pixels, so it must fail loudly.
func TestCanonicalCatalogInitializationRegistersMotions(t *testing.T) {
	catalog, err := Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if len(catalog.Motions) == 0 {
		t.Fatal("canonical motion catalog is empty")
	}
	if len(Registry.List()) == 0 {
		t.Fatal("motion registry registered no motions")
	}
}
