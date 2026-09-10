package render

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

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

// webFSWithout copies the embedded web/ tree, omitting exactly one template.
//
// Every other template is the real one, so the other page builders genuinely
// render — which is the whole point. A hand-written stub filesystem carrying
// only base.html breaks all seven page types at once, and a test that asserts
// "everything else was still emitted" against it is asserting nothing, because
// there is no everything else.
//
// It fails the test if the named template was not there to remove: a typo in
// the name would otherwise leave a complete filesystem and a test that passes
// while exercising no failure at all.
func webFSWithout(t *testing.T, omit string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	err := fs.WalkDir(webFS, "web", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(webFS, p)
		if err != nil {
			return err
		}
		out[p] = &fstest.MapFile{Data: data}
		return nil
	})
	if err != nil {
		t.Fatalf("copying the embedded templates: %v", err)
	}
	target := "web/templates/" + omit
	if _, ok := out[target]; !ok {
		t.Fatalf("there is no embedded template %s to omit; the test would prove nothing", target)
	}
	delete(out, target)
	return out
}

// TestSiteContinuesPastAPageThatFailsToCompile pins spec §12's core promise
// for this task: one broken page must not hide the other forty. The real
// embedded templates always compile — every field a template touches is
// populated by Go code, never by a user's catalog, so there is no legitimate
// Input that makes this happen. The only way to exercise the path is to call
// siteFrom, Site's logic with the template filesystem taken as a parameter,
// with one page's template removed — exactly the shape a future landsraad bug
// could take. No package state is touched, so this test needs no save/restore
// and can run in parallel with the rest of the package.
//
// It is table-driven over every page builder on purpose. The previous version
// broke only catalog.html and asserted "everything else survived", which held
// for a reason that had nothing to do with accumulation: entity, doc, map,
// team and scorecard pages still called templateSet(webFS, ...) directly, so
// the substituted filesystem never reached them and the only file the test
// actually proved anything about was assets/style.css. Breaking each template
// in turn and demanding that exactly the pages it feeds disappear fails if any
// builder stops accumulating AND fails if any builder stops honouring the
// seam.
func TestSiteContinuesPastAPageThatFailsToCompile(t *testing.T) {
	t.Parallel()
	in := twoEntities(t)

	var full diag.Collector
	complete := siteMap(siteFrom(webFS, in, &full))
	if ds := full.Diagnostics(); len(ds) != 0 {
		t.Fatalf("the reference render must be clean: %+v", ds)
	}
	if len(complete) < 10 {
		t.Fatalf("the fixture renders only %d files; too few to prove that other pages survive", len(complete))
	}

	cases := []struct {
		omit string
		// gone is every path that must stop being emitted, and nothing else
		// may.
		gone []string
	}{
		{"catalog.html", []string{"index.html"}},
		// entity.html feeds one page per entity.
		{"entity.html", []string{
			"entity/service/ledger-api/index.html",
			"entity/topic/payments-events/index.html",
		}},
		// doc.html is compiled by entityPages before the entity loop, so
		// losing it costs the entity pages too — no partial entity page that
		// silently drops its documentation nav.
		{"doc.html", []string{
			"entity/service/ledger-api/index.html",
			"entity/topic/payments-events/index.html",
		}},
		{"map.html", []string{"map/index.html"}},
		{"team.html", []string{"team/team-payments/index.html"}},
		{"scorecard.html", []string{"scorecard/index.html"}},
	}

	for _, tc := range cases {
		t.Run(tc.omit, func(t *testing.T) {
			var c diag.Collector
			got := siteMap(siteFrom(webFSWithout(t, tc.omit), in, &c))

			gone := map[string]bool{}
			for _, p := range tc.gone {
				gone[p] = true
				if _, ok := complete[p]; !ok {
					t.Fatalf("%s is not in a complete render, so omitting %s cannot remove it", p, tc.omit)
				}
				if _, ok := got[p]; ok {
					t.Errorf("%s was emitted although its template failed to compile", p)
				}
			}
			for p := range complete {
				if gone[p] {
					continue
				}
				if _, ok := got[p]; !ok {
					t.Errorf("%s vanished because %s failed to compile; siteFrom must accumulate, not stop", p, tc.omit)
				}
			}
			for p := range got {
				if _, ok := complete[p]; !ok {
					t.Errorf("%s was emitted that a complete render does not produce", p)
				}
			}

			ds := c.Diagnostics()
			if len(ds) != 1 {
				t.Fatalf("want exactly one diagnostic, got %d: %+v", len(ds), ds)
			}
			d := ds[0]
			wantMsg := "cannot compile the embedded template " + tc.omit +
				": template: pattern matches no files: `web/templates/" + tc.omit + "`"
			if d.Message != wantMsg {
				t.Errorf("Message =\n%q\nwant\n%q", d.Message, wantMsg)
			}
			wantHint := "this is a bug in landsraad, not in your catalog"
			if d.Hint != wantHint {
				t.Errorf("Hint = %q, want %q", d.Hint, wantHint)
			}
			if d.File != "templates/"+tc.omit {
				t.Errorf("File = %q, want %q", d.File, "templates/"+tc.omit)
			}
			if d.Line != 1 {
				t.Errorf("Line = %d, want 1", d.Line)
			}
		})
	}
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
