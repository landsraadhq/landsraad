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

func TestFindRejectsAbsolutePattern(t *testing.T) {
	_, err := Find(mem(), []string{"/etc/*"})
	if err == nil {
		t.Fatal("an absolute pattern must be rejected, not silently match nothing")
	}
	want := `path pattern "/etc/*" must not be absolute; write a path relative to the repository root, for example "etc/*"`
	if err.Error() != want {
		t.Errorf("error\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestFindRejectsPatternEscapingRoot(t *testing.T) {
	_, err := Find(mem(), []string{"../*"})
	if err == nil {
		t.Fatal("a pattern containing .. must be rejected, not silently match nothing")
	}
	want := `path pattern "../*" escapes the repository root via ".."; patterns must stay under the repository root`
	if err.Error() != want {
		t.Errorf("error\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestFindRejectsPatternEscapingRootViaNestedDotDot(t *testing.T) {
	_, err := Find(mem(), []string{"services/../../*"})
	if err == nil {
		t.Fatal("a pattern escaping the root partway through must be rejected")
	}
	want := `path pattern "services/../../*" escapes the repository root via ".."; patterns must stay under the repository root`
	if err.Error() != want {
		t.Errorf("error\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestFindRejectsRedundantDotElement(t *testing.T) {
	_, err := Find(mem(), []string{"a/./b"})
	if err == nil {
		t.Fatal("a pattern containing a redundant \".\" element must be rejected")
	}
	want := `path pattern "a/./b" contains a redundant "." element; write it without that segment`
	if err.Error() != want {
		t.Errorf("error\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestFindRejectsTrailingSlash(t *testing.T) {
	_, err := Find(mem(), []string{"services/"})
	if err == nil {
		t.Fatal("a pattern with a trailing slash must be rejected")
	}
	want := `path pattern "services/" has a trailing slash; write "services" instead`
	if err.Error() != want {
		t.Errorf("error\n got: %s\nwant: %s", err.Error(), want)
	}
}

// The counterpart to the five rejections above: a well-formed pattern that
// simply matches nothing must still succeed with no error. A fix that turned
// "no services here" into an error would be worse than the bug it fixed.
// (TestFindPatternMatchingNothingIsNotAnError above already asserts this for
// "nonexistent/*"; this one uses a distinct valid-but-empty shape to make the
// contrast with the rejections explicit.)
func TestFindStructurallyValidPatternMatchingNothingStillSucceeds(t *testing.T) {
	got, err := Find(mem(), []string{"services/nonexistent-dir/*"})
	if err != nil {
		t.Fatalf("a structurally valid pattern that matches nothing must not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want none", got)
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
