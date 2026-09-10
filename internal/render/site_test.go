package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// siteMap indexes a rendered site by path, for tests that ask about one file.
func siteMap(files []emit.File) map[string][]byte {
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out
}

// twoEntities is the fixture most site tests render.
func twoEntities(t *testing.T) Input {
	t.Helper()
	return input(t, nil,
		ent("ledger-api", catalog.KindService, "team-payments", 1),
		ent("payments-events", catalog.KindTopic, "team-payments", 0),
	)
}

func TestSiteEmitsTheCatalogAndItsAssets(t *testing.T) {
	var c diag.Collector
	files := Site(twoEntities(t), &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("clean input produced diagnostics: %+v", ds)
	}
	got := siteMap(files)
	for _, want := range []string{"index.html", "assets/style.css", "assets/chroma.css"} {
		if _, ok := got[want]; !ok {
			t.Errorf("site is missing %s; got %v", want, keys(got))
		}
	}
}

// Every path is slash-separated and relative to a root the renderer never
// names (spec §3.1). An absolute path here would escape cmd/'s write loop.
func TestEverySitePathIsRelativeAndSlashSeparated(t *testing.T) {
	var c diag.Collector
	for _, f := range Site(twoEntities(t), &c) {
		if strings.HasPrefix(f.Path, "/") {
			t.Errorf("absolute path %q", f.Path)
		}
		if strings.Contains(f.Path, `\`) {
			t.Errorf("backslash in path %q", f.Path)
		}
		if strings.Contains(f.Path, "..") {
			t.Errorf("parent traversal in path %q", f.Path)
		}
	}
}

func TestSiteIsDeterministic(t *testing.T) {
	var c1, c2 diag.Collector
	a := siteMap(Site(twoEntities(t), &c1))
	b := siteMap(Site(twoEntities(t), &c2))
	if len(a) != len(b) {
		t.Fatalf("file counts differ: %d vs %d", len(a), len(b))
	}
	for path, want := range a {
		if string(b[path]) != string(want) {
			t.Errorf("%s differs between two renders of the same input", path)
		}
	}
}

func TestCatalogPageIsGolden(t *testing.T) {
	var c diag.Collector
	files := Site(twoEntities(t), &c)
	golden(t, "index.html", siteMap(files)["index.html"])
}

// An entity that was not scored renders "not scored", never "0%".
func TestCatalogShowsNotScoredRatherThanZero(t *testing.T) {
	var c diag.Collector
	index := string(siteMap(Site(twoEntities(t), &c))["index.html"])
	if !strings.Contains(index, "not scored") {
		t.Errorf("the untiered topic must render as not scored:\n%s", index)
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
