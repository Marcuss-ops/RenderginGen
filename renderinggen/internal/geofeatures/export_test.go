package geofeatures

// resetDefaultRegistryForTest clears the successful-load cache. It lives in a
// _test.go file so the production worker carries no test-only mutator: the
// cache is process-wide only because a successful load is immutable, not
// because production code may reset it.
func resetDefaultRegistryForTest() {
	defaultRegistryMu.Lock()
	defaultRegistry = nil
	defaultRegistryMu.Unlock()
}
