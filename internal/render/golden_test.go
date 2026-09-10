package render

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// update regenerates the golden files. Spec §14 asks for golden-file tests
// for the renderer with a -update flag.
//
// ALWAYS read the diff before committing a regeneration. A golden test whose
// output is refreshed without being looked at asserts nothing at all.
var update = flag.Bool("update", false, "regenerate golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden file %s — run `go test ./internal/render/ -update`", path)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("%s differs (-want +got):\n%s", name, diff)
	}
}
