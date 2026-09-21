package catalog

import (
	"testing"
	"testing/fstest"
)

// Ruling R51. Both spellings are a documentation index, and index.md wins
// when a repository holds both — so widening this cannot move a verdict that
// already existed: a repository scored and rendered off index.md yesterday is
// scored and rendered off exactly that file today.
func TestDocsIndexPrefersIndexMdOverUnderscore(t *testing.T) {
	both := fstest.MapFS{
		"docs/index.md":  {Data: []byte("# plain\n")},
		"docs/_index.md": {Data: []byte("# hugo\n")},
	}
	got, ok := DocsIndex(both, "docs")
	if !ok {
		t.Fatal("a directory holding both spellings has an index")
	}
	if got != "docs/index.md" {
		t.Errorf("DocsIndex = %q, want docs/index.md — index.md wins so an existing verdict cannot move", got)
	}
}

func TestDocsIndexFindsEitherSpellingAlone(t *testing.T) {
	for _, name := range DocsIndexNames() {
		only := fstest.MapFS{"docs/" + name: {Data: []byte("# x\n")}}
		got, ok := DocsIndex(only, "docs")
		if !ok || got != "docs/"+name {
			t.Errorf("DocsIndex with only %s = (%q, %v), want (docs/%s, true)", name, got, ok, name)
		}
	}
}

// An entity with no spec.docs has no documentation at all, which is a
// different fact from a docs directory holding no index. Only one of the two
// is the user's to fix, so DocsIndex reports absence and lets the caller say
// which it is.
func TestDocsIndexIsAbsentForAnEmptyDocsDir(t *testing.T) {
	if _, ok := DocsIndex(fstest.MapFS{"docs/index.md": {Data: []byte("x")}}, ""); ok {
		t.Error("an empty docsDir has no index, whatever the filesystem holds")
	}
}

// DocsIndexNames hands out a copy, for the reason AllKinds does: an exported
// slice is package state any importer can rewrite, and a test mutating it
// without a t.Cleanup poisons every test that runs after it.
func TestDocsIndexNamesCannotBeMutatedByACaller(t *testing.T) {
	got := DocsIndexNames()
	got[0] = "nonsense.md"
	if DocsIndexNames()[0] != "index.md" {
		t.Error("a caller rewrote the package's own list of index names")
	}
}
