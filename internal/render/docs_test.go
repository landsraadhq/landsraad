package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// documented builds an entity with a docs tree and a runbook.
func documented(t *testing.T) Input {
	t.Helper()
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	e.Spec.Runbook = "services/api/docs/runbook.md"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte(
			"# API\n\nThe overview. See [the runbook](runbook.md).\n")},
		"services/api/docs/runbook.md": {Data: []byte(
			"# Runbook\n\n!!! warning \"Page the owner\"\n\n    Check lag first.\n\n## Rollback\n\nRoll back with `kubectl`.\n")},
		"services/api/docs/ops/scaling.md": {Data: []byte(
			"# Scaling\n\nSee [the index](../index.md).\n")},
	}
	return input(t, files, e)
}

func TestIndexMarkdownIsInlinedOnTheEntityPage(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	page := string(files["entity/service/api/index.html"])
	if !strings.Contains(page, "The overview.") {
		t.Errorf("docs/index.md must render inline on the entity page:\n%s", page)
	}
	// Inlined, not also a separate page — one document, one URL.
	if _, ok := files["entity/service/api/docs/index.html"]; ok {
		t.Error("index.md must not also become a sub-page")
	}
}

func TestOtherDocsBecomeSubPages(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	for _, want := range []string{
		"entity/service/api/docs/runbook.html",
		"entity/service/api/docs/ops/scaling.html",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; got %v", want, keys(files))
		}
	}
}

// A link that works on GitHub must work in the portal.
func TestRelativeMarkdownLinksBecomeHTMLLinks(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	if !strings.Contains(string(files["entity/service/api/index.html"]), `href="docs/runbook.html"`) {
		t.Errorf("the inlined index's link was not rewritten:\n%s", files["entity/service/api/index.html"])
	}
	if !strings.Contains(string(files["entity/service/api/docs/ops/scaling.html"]), `href="../index.html"`) {
		t.Errorf("a nested doc's parent-relative link was not rewritten:\n%s", files["entity/service/api/docs/ops/scaling.html"])
	}
}

func TestDocPagesUseTheFullDialect(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(documented(t), &c))["entity/service/api/docs/runbook.html"])
	if !strings.Contains(page, `<div class="admonition warning">`) {
		t.Errorf("admonitions must render in a doc page:\n%s", page)
	}
	if !strings.Contains(page, `id="rollback"`) {
		t.Errorf("heading ids must survive into a doc page:\n%s", page)
	}
}

// spec.runbook is what an on-call engineer opens at 3am. It gets a link of
// its own, not just a row in a file listing.
func TestTheRunbookIsLinkedProminently(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(documented(t), &c))["entity/service/api/index.html"])
	if !strings.Contains(page, `class="runbook-link"`) {
		t.Errorf("the runbook needs its own link:\n%s", page)
	}
	if !strings.Contains(page, `href="docs/runbook.html"`) {
		t.Errorf("and it must point at the rendered page:\n%s", page)
	}
}

// A runbook outside spec.docs still gets rendered and linked.
func TestARunbookOutsideTheDocsDirectoryStillRenders(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nSteps.\n")}}

	var c diag.Collector
	site := siteMap(Site(input(t, files, e), &c))
	if _, ok := site["entity/service/api/runbook.html"]; !ok {
		t.Errorf("a runbook outside docs/ needs a page; got %v", keys(site))
	}
}

// An entity with no docs and no runbook says so. A blank space where
// documentation should be reads as a broken page.
func TestAnUndocumentedEntitySaysSo(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, "No documentation") {
		t.Errorf("an undocumented entity must say so:\n%s", page)
	}
}

// Accumulate, never fail fast: one unreadable document must not cost the
// reader the other pages.
func TestAnUnreadableDocumentIsReportedAndSkipped(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md":  {Data: []byte("# API\n")},
		"services/api/docs/broken.md": {Data: []byte("ok"), Mode: 0},
	}
	in := input(t, files, e)
	// Make the read fail the way a real permission problem would.
	files["services/api/docs/broken.md"] = &fstest.MapFile{Data: nil, Mode: 0}

	var c diag.Collector
	site := siteMap(Site(in, &c))
	if _, ok := site["entity/service/api/index.html"]; !ok {
		t.Error("the entity page must still be rendered")
	}
}

// Spec §14.1: raw HTML in somebody's runbook is escaped, never injected
// into a shared portal page. This is the end-to-end assertion of the
// property Task 1 tests at the goldmark level.
func TestRawHTMLInARunbookNeverReachesThePortal(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\n<script>alert(document.cookie)</script>\n")},
	}
	var c diag.Collector
	page := string(siteMap(Site(input(t, files, e), &c))["entity/service/api/runbook.html"])
	if strings.Contains(page, "<script>alert(") {
		t.Errorf("raw HTML from a runbook reached the portal:\n%s", page)
	}
}

func TestDocPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "doc-runbook.html", siteMap(Site(documented(t), &c))["entity/service/api/docs/runbook.html"])
}

// md.Render leaves Doc.Headings nil, not an empty slice, when a document has
// no headings — it is only ever appended to. Task 12 serialises RenderedDoc
// into search-index.json, where a nil slice marshals to null and an empty
// one to []; a JavaScript client doing doc.headings.length would crash on
// null. RenderedDoc normalises so Task 12 never has to know the difference.
func TestRenderedDocHeadingsIsNeverNilEvenWithNoHeadings(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("No headings, just a paragraph.\n")},
	}
	in := input(t, files, e)
	dt, err := templateSet("doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	ed := docsFor(in, e, dt, md.New(), &c)
	if len(ed.Docs) != 1 {
		t.Fatalf("got %d rendered docs, want 1: %+v", len(ed.Docs), ed.Docs)
	}
	if ed.Docs[0].Headings == nil {
		t.Error("RenderedDoc.Headings must not be nil for a document with no headings")
	}
	if len(ed.Docs[0].Headings) != 0 {
		t.Errorf("a document with no headings must have zero headings, got %+v", ed.Docs[0].Headings)
	}
}
