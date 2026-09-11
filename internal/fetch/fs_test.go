package fetch

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testEntries() []Entry {
	return []Entry{
		{Path: "service.yaml", SHA: "aaa", Size: 12},
		{Path: "docs", Dir: true},
		{Path: "docs/index.md", SHA: "bbb", Size: 30},
		{Path: "docs/deep", Dir: true},
		{Path: "docs/deep/more.md", SHA: "ccc", Size: 7},
	}
}

// Metadata is answerable the moment the tree is listed, before a single blob
// is fetched. This is ruling R24's cost model: CheckFiles is free.
func TestStatBeforeAnyContent(t *testing.T) {
	f := FromEntries(testEntries())
	info, err := fs.Stat(f, "docs/index.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if info.Size() != 30 {
		t.Errorf("Size() = %d, want 30", info.Size())
	}
	if info.Name() != "index.md" {
		t.Errorf("Name() = %q, want %q", info.Name(), "index.md")
	}
}

func TestGlobAndWalkBeforeAnyContent(t *testing.T) {
	f := FromEntries(testEntries())

	got, err := fs.Glob(f, "docs/*")
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if diff := cmp.Diff([]string{"docs/deep", "docs/index.md"}, got); diff != "" {
		t.Errorf("Glob mismatch (-want +got):\n%s", diff)
	}

	var walked []string
	if err := fs.WalkDir(f, "docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, p)
		return nil
	}); err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	want := []string{"docs", "docs/deep", "docs/deep/more.md", "docs/index.md"}
	if diff := cmp.Diff(want, walked); diff != "" {
		t.Errorf("WalkDir mismatch (-want +got):\n%s", diff)
	}
}

// Reading a path that is in the tree but was never fetched is a bug in the
// content planner, not a missing file. It must not look like one.
func TestReadUnfetchedIsNotNotExist(t *testing.T) {
	f := FromEntries(testEntries())
	_, err := fs.ReadFile(f, "docs/index.md")
	if err == nil {
		t.Fatal("ReadFile of an unfetched path succeeded")
	}
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("unfetched content reported as ErrNotExist; it would be diagnosed as a missing file")
	}
	if !errors.Is(err, ErrNotFetched) {
		t.Errorf("error = %v, want ErrNotFetched", err)
	}
}

func TestPutThenRead(t *testing.T) {
	f := FromEntries(testEntries())
	f.Put("docs/index.md", []byte("# Docs\n"))
	got, err := fs.ReadFile(f, "docs/index.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "# Docs\n" {
		t.Errorf("ReadFile = %q, want %q", got, "# Docs\n")
	}
}

// A genuinely absent path in a listed directory is ErrNotExist, which is
// what CheckFiles must report as a missing file.
func TestAbsentInAListedDirectoryIsNotExist(t *testing.T) {
	f := FromEntries(testEntries())
	_, err := fs.Stat(f, "docs/nope.md")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error = %v, want ErrNotExist", err)
	}
}

// A path whose directory was never listed is a third answer. Under R28's
// truncated-tree fallback only some directories are walked, and reporting
// "does not exist" for one that was never looked at is a false statement
// about the user's repository (spec 14.1).
func TestUnlistedDirectoryIsNotNotExist(t *testing.T) {
	f := NewFS()
	f.AddDir(".", []Entry{{Path: "services", Dir: true}})

	_, err := fs.Stat(f, "services/api/runbook.md")
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("unlisted path reported as ErrNotExist; landsraad would call it a missing file")
	}
	if !errors.Is(err, ErrNotListed) {
		t.Errorf("error = %v, want ErrNotListed", err)
	}
}

func TestAddDirMakesADirectoryKnown(t *testing.T) {
	f := NewFS()
	f.AddDir(".", []Entry{{Path: "services", Dir: true}})
	f.AddDir("services", []Entry{{Path: "services/api", Dir: true}})
	f.AddDir("services/api", []Entry{{Path: "services/api/service.yaml", SHA: "d", Size: 3}})

	if _, err := fs.Stat(f, "services/api/service.yaml"); err != nil {
		t.Fatalf("Stat after AddDir: %v", err)
	}
	if _, err := fs.Stat(f, "services/api/nope.yaml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("absent file in a listed directory: error = %v, want ErrNotExist", err)
	}
}

// Successfully listing a directory is proof the directory itself exists —
// a fact AddDir must record, not merely the fact of its children. This is
// exactly the case Expand exists to serve: spec.docs naming a directory no
// Open pattern ever covered. Before this fix, AddDir recorded only the
// children, so fs.Stat and fs.WalkDir on the directory itself — which is
// precisely what render/docs.go calls — failed even though the directory
// unambiguously exists.
func TestAddDirMakesTheDirectoryItselfKnown(t *testing.T) {
	f := NewFS()
	f.AddDir("shared/docs", []Entry{
		{Path: "shared/docs/index.md", SHA: "a"},
	})

	info, err := fs.Stat(f, "shared/docs")
	if err != nil {
		t.Fatalf("Stat shared/docs: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("shared/docs.IsDir() = false, want true")
	}

	var walked []string
	if err := fs.WalkDir(f, "shared/docs", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, p)
		return nil
	}); err != nil {
		t.Fatalf("WalkDir shared/docs: %v", err)
	}
	if diff := cmp.Diff([]string{"shared/docs", "shared/docs/index.md"}, walked); diff != "" {
		t.Errorf("WalkDir mismatch (-want +got):\n%s", diff)
	}
}

// AddDir's new ancestor-recording must not also mark those ancestors
// listed: listed means "I know this directory's whole contents", and
// AddDir("shared/docs", ...) only ever told us about shared/docs, not
// about shared. Collapsing the two would turn an honest "never looked"
// into a false "does not exist" for every other entry under shared.
func TestAddDirDoesNotMarkAncestorsListed(t *testing.T) {
	f := NewFS()
	f.AddDir("shared/docs", []Entry{
		{Path: "shared/docs/index.md", SHA: "a"},
	})

	// shared now exists...
	info, err := fs.Stat(f, "shared")
	if err != nil {
		t.Fatalf("Stat shared: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("shared.IsDir() = false, want true")
	}

	// ...but nothing ever listed shared's own contents, so a sibling of
	// shared/docs must read as "never looked", not "does not exist".
	_, err = fs.Stat(f, "shared/other")
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("shared/other reported as ErrNotExist; shared was never listed, so this is a false claim")
	}
	if !errors.Is(err, ErrNotListed) {
		t.Errorf("error = %v, want ErrNotListed", err)
	}
}

func TestEntriesAreSorted(t *testing.T) {
	f := FromEntries([]Entry{
		{Path: "z.md", SHA: "1"}, {Path: "a.md", SHA: "2"}, {Path: "m.md", SHA: "3"},
	})
	var paths []string
	for _, e := range f.Entries() {
		paths = append(paths, e.Path)
	}
	if diff := cmp.Diff([]string{"a.md", "m.md", "z.md"}, paths); diff != "" {
		t.Errorf("Entries mismatch (-want +got):\n%s", diff)
	}
}

func TestInvalidPathIsRejected(t *testing.T) {
	f := FromEntries(testEntries())
	for _, p := range []string{"/etc/passwd", "../escape", "docs//index.md"} {
		if _, err := fs.Stat(f, p); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Stat(%q) error = %v, want ErrInvalid", p, err)
		}
	}
}
