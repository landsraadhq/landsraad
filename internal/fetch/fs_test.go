package fetch

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/sparsefs"
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

// A complete listing proves absence at every depth, not just one level
// down.
//
// lookup used to consult the IMMEDIATE parent's listed flag alone, so a
// dangling path whose parent directory is itself absent read as
// ErrNotListed — "landsraad never looked" — about a repository landsraad
// had listed in full. CheckFiles then told the reader to widen a `paths:`
// that was already `.`, about a file that really was missing, and the
// identical mistake in a local checkout said missing-file correctly.
func TestACompleteListingProvesAbsenceAtAnyDepth(t *testing.T) {
	f := FromEntries([]Entry{
		{Path: "docs/index.md", SHA: "a"},
	})

	for _, name := range []string{
		"docs/runbooks/api.md", // two levels below the deepest listing
		"docs/runbooks",        // the absent directory itself
		"apps/edge/runbook.md", // no ancestor of this exists at all
	} {
		_, err := fs.Stat(f, name)
		if errors.Is(err, ErrNotListed) {
			t.Errorf("Stat %s = ErrNotListed; the whole repository was listed, so this is not "+
				"a file nobody looked for", name)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat %s: error = %v, want ErrNotExist", name, err)
		}
	}
}

// The climb stops at the nearest LISTED ancestor, and a present-but-unlisted
// directory on the way down still means "never looked".
//
// This is R28's truncated-tree fallback: the root listing saw apps/ and the
// descent never went into it. Collapsing this into ErrNotExist would be the
// false claim the sentinel exists to prevent, in the other direction.
func TestAPresentButUnlistedAncestorStillReadsAsNeverLooked(t *testing.T) {
	f := NewFS()
	f.AddDir(".", []Entry{
		{Path: "services", Dir: true},
		{Path: "apps", Dir: true},
	})
	f.AddDir("services", []Entry{{Path: "services/api", Dir: true}})

	// apps is in the root listing and was never descended into.
	_, err := fs.Stat(f, "apps/edge/runbook.md")
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("apps/edge/runbook.md reported as ErrNotExist; apps was never listed, so this is a false claim")
	}
	if !errors.Is(err, ErrNotListed) {
		t.Errorf("error = %v, want ErrNotListed", err)
	}

	// services/api is present but unlisted for the same reason.
	if _, err := fs.Stat(f, "services/api/runbook.md"); !errors.Is(err, ErrNotListed) {
		t.Errorf("services/api/runbook.md: error = %v, want ErrNotListed", err)
	}
}

// The sentinels are the same values as internal/sparsefs', not copies of
// them.
//
// internal/catalog, internal/render and internal/scorecard match on
// sparsefs.ErrNotFetched while *FS returns fetch.ErrNotFetched. Re-declaring
// either with its own errors.New would make every errors.Is in those three
// packages silently false, and every one of their tests reads the resulting
// message rather than the identity — so nothing else here would notice.
func TestSentinelsAreTheSameValuesAsSparsefs(t *testing.T) {
	if ErrNotFetched != sparsefs.ErrNotFetched {
		t.Error("fetch.ErrNotFetched is not sparsefs.ErrNotFetched; every stage's errors.Is is now false")
	}
	if ErrNotListed != sparsefs.ErrNotListed {
		t.Error("fetch.ErrNotListed is not sparsefs.ErrNotListed; every stage's errors.Is is now false")
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

// dir.ReadDir(n<=0) must return the entries REMAINING after any partial
// read, not the whole listing again.
//
// io/fs's own reference implementation settles what "all" means here:
// fstest.MapFS, after ReadDir(1) on a four-entry directory, answers
// ReadDir(-1) with the remaining three. Returning four would hand a caller
// that paged through a directory one entry at a time a duplicate of
// everything it had already seen.
//
// Unreachable today, because *FS implements ReadDirFS and so fs.ReadDir,
// fs.WalkDir and fs.Glob all call FS.ReadDir directly and never Open a
// directory at all. That is exactly why it needs a test: nothing else in
// the suite drives this method, and the next type to be handed a *dir
// inherits the bug silently.
func TestDirReadDirResumesFromTheOffset(t *testing.T) {
	f := FromEntries([]Entry{
		{Path: "d", Dir: true},
		{Path: "d/a", SHA: "a"}, {Path: "d/b", SHA: "b"},
		{Path: "d/c", SHA: "c"}, {Path: "d/e", SHA: "e"},
	})
	opened, err := f.Open("d")
	if err != nil {
		t.Fatalf("Open d: %v", err)
	}
	rd, ok := opened.(fs.ReadDirFile)
	if !ok {
		t.Fatal("an opened directory is not an fs.ReadDirFile")
	}
	first, err := rd.ReadDir(1)
	if err != nil {
		t.Fatalf("ReadDir(1): %v", err)
	}
	if len(first) != 1 || first[0].Name() != "a" {
		t.Fatalf("ReadDir(1) = %v, want [a]", names(first))
	}
	rest, err := rd.ReadDir(-1)
	if err != nil {
		t.Fatalf("ReadDir(-1): %v", err)
	}
	if got := names(rest); !cmp.Equal(got, []string{"b", "c", "e"}) {
		t.Errorf("ReadDir(-1) after ReadDir(1) = %v, want [b c e]; "+
			"re-returning an already-read entry is a duplicate, not a listing", got)
	}
}

// ReadDir on a path that is a FILE says so, rather than claiming landsraad
// never listed it.
//
// A file is never in f.listed, so the listed check answered ErrNotListed —
// "nothing is known about this path's directory" — for a path whose entry
// landsraad is holding. That sends a reader to widen a `paths:` for a file
// that was fetched successfully, which is the same false-diagnostic class
// ErrNotListed exists to prevent.
func TestReadDirOnAFileSaysItIsNotADirectory(t *testing.T) {
	f := FromEntries(testEntries())

	_, err := f.ReadDir("service.yaml")
	if err == nil {
		t.Fatal("ReadDir on a file succeeded")
	}
	if errors.Is(err, ErrNotListed) {
		t.Errorf("ReadDir on a file = ErrNotListed; landsraad holds this entry, "+
			"so %q is not something nobody looked for", "service.yaml")
	}
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		t.Fatalf("error = %v (%T), want *fs.PathError", err, err)
	}
	if pe.Err.Error() != "not a directory" {
		t.Errorf("error = %q, want %q", pe.Err.Error(), "not a directory")
	}
}

// Ruling R45: a directory is listed only when a listing covered it. NewFS
// used to mark "." at construction, which is how a GitLab repository opened
// at a non-root prefix came to answer fs.ErrNotExist for every root-level
// file.
func TestNewFSListsNothing(t *testing.T) {
	f := NewFS()
	if f.Listed(".") {
		t.Error(`NewFS marked "." listed without a listing`)
	}
	if _, err := fs.Stat(f, "README.md"); !errors.Is(err, ErrNotListed) {
		t.Errorf("Stat(README.md) on an unlisted root = %v, want ErrNotListed", err)
	}
}

// A complete listing did enumerate the root, so FromEntries marks it — even
// for a repository whose every file sits at the top level, where no entry's
// parent chain would otherwise reach ".".
func TestFromEntriesListsTheRoot(t *testing.T) {
	f := FromEntries([]Entry{{Path: "README.md", SHA: "a"}})
	if !f.Listed(".") {
		t.Error(`FromEntries did not mark "." listed`)
	}
	if _, err := fs.Stat(f, "missing.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing.md) = %v, want fs.ErrNotExist", err)
	}
}

// names is the entry names of a directory listing, in order.
func names(ds []fs.DirEntry) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Name())
	}
	return out
}
