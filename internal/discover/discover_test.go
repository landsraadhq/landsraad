package discover

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// The whole point of the fs.FS seam: these tests touch no disk at all.
func mem() fstest.MapFS {
	return fstest.MapFS{
		"services/a/service.yaml":  {Data: []byte("kind: Service\n")},
		"services/b/service.yaml":  {Data: []byte("kind: Service\n")},
		"services/b/docs/index.md": {Data: []byte("# b\n")},
		"topics/t/service.yaml":    {Data: []byte("kind: Topic\n")},
		"libs/nocatalog/README.md": {Data: []byte("no service.yaml here\n")},
	}
}

func TestFindMatchesGlobPatterns(t *testing.T) {
	got, err := Find(mem(), []string{"services/*", "topics/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	want := []string{
		"services/a/service.yaml",
		"services/b/service.yaml",
		"topics/t/service.yaml",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("position %d: got %q, want %q (results must be sorted)", i, got[i], want[i])
		}
	}
}

func TestFindSkipsDirectoriesWithoutServiceYAML(t *testing.T) {
	got, err := Find(mem(), []string{"libs/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a directory with no service.yaml must be skipped, got %v", got)
	}
}

func TestFindPatternMatchingNothingIsNotAnError(t *testing.T) {
	got, err := Find(mem(), []string{"nonexistent/*"})
	if err != nil {
		t.Fatalf("a pattern matching nothing must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
	}
}

func TestFindDotMeansRoot(t *testing.T) {
	root := fstest.MapFS{"service.yaml": {Data: []byte("kind: Service\n")}}
	got, err := Find(root, []string{"."})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 1 || got[0] != "service.yaml" {
		t.Errorf(`pattern "." must find the root service.yaml, got %v`, got)
	}
}

func TestFindDeduplicatesOverlappingPatterns(t *testing.T) {
	got, err := Find(mem(), []string{"services/*", "services/a"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("overlapping patterns must not yield duplicates, got %v", got)
	}
}

func TestLoadReadsBytes(t *testing.T) {
	var c diag.Collector
	files := Load(mem(), []string{"services/a/service.yaml"}, &c)
	if c.HasErrors() {
		t.Fatalf("unexpected errors: %+v", c.Diagnostics())
	}
	if len(files) != 1 || string(files[0].Data) != "kind: Service\n" {
		t.Errorf("Load did not return the file bytes: %+v", files)
	}
}

func TestLoadReportsUnreadableFile(t *testing.T) {
	var c diag.Collector
	files := Load(mem(), []string{"services/gone/service.yaml"}, &c)
	if len(files) != 0 {
		t.Errorf("a missing file must not be returned, got %+v", files)
	}
	if !c.HasErrors() {
		t.Fatal("a missing file must produce an error diagnostic")
	}
	d := c.Diagnostics()[0]
	if d.Line == 0 {
		t.Error("every diagnostic carries a line, even this one")
	}
	want := `cannot read file: open services/gone/service.yaml: file does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Check != "unreadable" {
		t.Errorf("Check = %q, want %q", d.Check, "unreadable")
	}
}

// The same functions must work against a real directory, because that is what
// the command does. os.DirFS is the only place `os` appears.
func TestFindWorksOverOsDirFS(t *testing.T) {
	got, err := Find(os.DirFS("../../testdata/monorepo-ok"), []string{"services/*", "topics/*"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("got %d entries from the fixture repo, want 3: %v", len(got), got)
	}
}
