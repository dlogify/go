// Package conformance locates the language-neutral fixtures in
// clients/conformance for the tests of this module.
package conformance

import (
	"os"
	"path/filepath"
	"testing"
)

// Dir returns clients/conformance/<sub>, found from the module root (the
// nearest directory with a go.mod whose module is dlogify.com/sdk/go, or any
// go.mod for the adapters' modules). It skips the test when the fixtures are
// absent, as in the public mirror.
func Dir(t testing.TB, sub string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		candidate := filepath.Join(dir, "..", "conformance", sub)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Skipf("conformance fixtures not found (clients/conformance/%s); skipped outside the monorepo", sub)
		}
		dir = parent
	}
}
