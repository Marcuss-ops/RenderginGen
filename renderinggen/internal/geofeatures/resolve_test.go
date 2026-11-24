package geofeatures

import "testing"

func TestRegistryResolveUsesIDsForCanonicalSelectors(t *testing.T) {
	registry := fixtureRegistry(t)

	byID, err := registry.Resolve("country:GBR")
	if err != nil {
		t.Fatalf("Resolve(country:GBR): %v", err)
	}
	if byID.ID != "country:GBR" {
		t.Fatalf("Resolve(country:GBR).ID = %q", byID.ID)
	}

	world, err := registry.Resolve(WorldID)
	if err != nil {
		t.Fatalf("Resolve(world): %v", err)
	}
	if world.ID != WorldID {
		t.Fatalf("Resolve(world).ID = %q", world.ID)
	}
}

func TestRegistryResolveFallsBackToName(t *testing.T) {
	registry := fixtureRegistry(t)
	uk, err := registry.ByID("country:GBR")
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}

	byName, err := registry.Resolve(uk.Name)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", uk.Name, err)
	}
	if byName.ID != uk.ID {
		t.Fatalf("Resolve(%q).ID = %q, want %q", uk.Name, byName.ID, uk.ID)
	}
}

func TestRegistryResolveRejectsUnknownSelector(t *testing.T) {
	registry := fixtureRegistry(t)
	if _, err := registry.Resolve("country:ZZZ"); err == nil {
		t.Fatal("Resolve(country:ZZZ) succeeded, want not found")
	}
}
