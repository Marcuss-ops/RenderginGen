package testsupport

import "testing"

func TestDedicatedTestDatabaseName(t *testing.T) {
	for _, dsn := range []string{
		"postgres://user:pass@127.0.0.1:5432/test_renderinggen?sslmode=disable",
		"postgres://user:pass@127.0.0.1:5432/renderinggen_test?sslmode=disable",
	} {
		if _, err := DedicatedTestDatabaseName(dsn); err != nil {
			t.Errorf("DedicatedTestDatabaseName(%q): %v", dsn, err)
		}
	}
	for _, dsn := range []string{
		"postgres://user:pass@127.0.0.1:5432/renderinggen?sslmode=disable",
		"host=127.0.0.1 dbname=pipelinegen-postgres-test user=test",
	} {
		if _, err := DedicatedTestDatabaseName(dsn); err == nil {
			t.Errorf("DedicatedTestDatabaseName(%q) accepted a non-test database", dsn)
		}
	}
}
