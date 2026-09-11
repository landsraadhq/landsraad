package render

import (
	"html/template"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// failFS wraps an fstest.MapFS, making reads of one named path fail with a
// real error.
//
// fstest.MapFS implements ReadFile and Stat directly (confirmed against
// $GOROOT/src/testing/fstest/mapfs.go), so io/fs's package-level ReadFile
// and Stat/WalkDir call straight through to those without ever going
// through Open — and MapFS never checks permission bits. Setting Mode: 0
// on a MapFile, as an earlier version of these tests did, changes nothing:
// fs.ReadFile against {Data: nil, Mode: 0} returns empty data and a nil
// error, so the failure path it claimed to test never ran.
type failFS struct {
	fstest.MapFS
	path string
}

func (f failFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadFile(name)
}

func (f failFS) Stat(name string) (fs.FileInfo, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.Stat(name)
}

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
// The URL tree is not a mirror of the source tree: docs/index.md is
// hoisted onto the entity page itself (ruling R18), one directory above
// where every other document under spec.docs ends up. So a link's
// rewritten href must be resolved relative to *where the site puts each
// page*, not to a context-free .md -> .html suffix swap of the source
// path.
//
// scaling.md's "../index.md" resolves, in source space, to docs/index.md
// — which the site does not put at docs/index.html (that page does not
// exist), but at the entity page two directories up from
// docs/ops/scaling.html. The correct href is therefore "../../index.html",
// not "../index.html": a previous version of this test asserted the
// latter, which was the bug written down as a passing assertion rather
// than a passing behaviour.
func TestRelativeMarkdownLinksBecomeHTMLLinks(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(documented(t), &c))
	if !strings.Contains(string(files["entity/service/api/index.html"]), `href="docs/runbook.html"`) {
		t.Errorf("the inlined index's link was not rewritten:\n%s", files["entity/service/api/index.html"])
	}
	if !strings.Contains(string(files["entity/service/api/docs/ops/scaling.html"]), `href="../../index.html"`) {
		t.Errorf("a nested doc's link to the hoisted index was not rewritten relative to the index's real URL:\n%s", files["entity/service/api/docs/ops/scaling.html"])
	}
}

// The previous rewrite was context-free, so this exact substring could
// pass by coincidence: the page's separate .runbook-link element
// independently produces href="docs/runbook.html" with link text
// "Runbook", masking a wrong href="runbook.html" sitting right next to it
// inside the inlined prose (whose link text is "the runbook"). Anchoring
// on the link text pins the actual element instead.
func TestIndexMarkdownsOwnLinkIsRewrittenNotJustTheRunbookLinkElement(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(documented(t), &c))["entity/service/api/index.html"])
	if !strings.Contains(page, `href="docs/runbook.html">the runbook</a>`) {
		t.Errorf("the inlined index's own link to the runbook was not correctly rewritten:\n%s", page)
	}
}

// A link between two ordinary sub-pages in the same directory is the
// common case and must not grow a spurious "docs/" prefix or any other
// adjustment — only a link that crosses the index.md hoist needs one.
func TestASiblingSubPageLinkStaysAPlainRelativeLink(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# API\n")},
		"services/api/docs/ops/deploy.md": {Data: []byte(
			"# Deploy\n\nSee [scaling](scaling.md).\n")},
		"services/api/docs/ops/scaling.md": {Data: []byte("# Scaling\n\nSteps.\n")},
	}
	var c diag.Collector
	page := string(siteMap(Site(input(t, files, e), &c))["entity/service/api/docs/ops/deploy.html"])
	if !strings.Contains(page, `href="scaling.html">scaling</a>`) {
		t.Errorf("a sibling sub-page link must stay a plain relative link:\n%s", page)
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
// reader the other pages. Uses failFS, since fstest.MapFS's Mode bits
// cannot make a read actually fail (see failFS's doc comment).
func TestAnUnreadableDocumentIsReportedAndSkipped(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md":  {Data: []byte("# API\n\nThe overview.\n")},
		"services/api/docs/broken.md": {Data: []byte("# Broken\n\nUnreachable.\n")},
	}
	in := input(t, files, e)
	in.Sources = catalog.SingleSource("", failFS{MapFS: files, path: "services/api/docs/broken.md"})

	var c diag.Collector
	site := siteMap(Site(in, &c))

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	got := ds[0]
	// The error itself, not just the path. "cannot read X" collapsed a
	// permission problem, an EISDIR and fetch.ErrNotFetched into one
	// sentence, and the last of those is a landsraad bug that reads as the
	// user's mistake.
	want := "cannot read services/api/docs/broken.md: open services/api/docs/broken.md: permission denied"
	if got.Message != want {
		t.Errorf("Message = %q, want %q", got.Message, want)
	}
	if got.Hint != "the file is named by spec.docs or spec.runbook" {
		t.Errorf("Hint = %q, want %q", got.Hint, "the file is named by spec.docs or spec.runbook")
	}
	if got.Line == 0 {
		t.Error("Line must not be 0")
	}

	page := string(site["entity/service/api/index.html"])
	if !strings.Contains(page, "The overview.") {
		t.Errorf("the other document (index.md) must still render despite broken.md failing:\n%s", page)
	}
}

// The fs.WalkDir failure path: a whole docs/ directory that cannot be
// listed must be reported with an exact diagnostic, and must not cost the
// reader anything else the entity has — here, a runbook outside spec.docs.
func TestADocsDirectoryThatCannotBeReadIsReportedAndSkipped(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# API\n\nThe overview.\n")},
		"services/api/RUNBOOK.md":    {Data: []byte("# Runbook\n\nSteps.\n")},
	}
	in := input(t, files, e)
	in.Sources = catalog.SingleSource("", failFS{MapFS: files, path: "services/api/docs"})

	var c diag.Collector
	site := siteMap(Site(in, &c))

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	got := ds[0]
	wantMsg := "cannot read the documentation directory services/api/docs: stat services/api/docs: permission denied"
	if got.Message != wantMsg {
		t.Errorf("Message =\n%q\nwant\n%q", got.Message, wantMsg)
	}
	if got.Line == 0 {
		t.Error("Line must not be 0")
	}

	// Accumulate, never fail fast: the runbook lives outside the broken
	// docs directory and must still render.
	if _, ok := site["entity/service/api/runbook.html"]; !ok {
		t.Errorf("the runbook must still render despite the docs directory failing; got %v", keys(site))
	}
}

// Spec §12's Global Constraints, verbatim: "A missing docs/ directory... and
// an entity with no documentation at all are three different answers and
// must render differently." A reader must be able to tell "this service
// declared no documentation" from "this service says it has docs and we
// could not read them" — those imply different actions.
func TestADocsDirectoryThatCannotBeReadRendersDistinctlyFromNoDocumentation(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# API\n")}}
	in := input(t, files, e)
	in.Sources = catalog.SingleSource("", failFS{MapFS: files, path: "services/api/docs"})

	var c diag.Collector
	page := string(siteMap(Site(in, &c))["entity/service/api/index.html"])

	if !strings.Contains(page, "spec.docs names a directory that could not be read") {
		t.Errorf("an unreadable docs directory must say so distinctly:\n%s", page)
	}
	if strings.Contains(page, "No documentation.") {
		t.Errorf("an unreadable docs directory must not read as \"no documentation at all\":\n%s", page)
	}
}

// The other half of the same constraint: an unreadable runbook must not
// misrepresent failure as success. runbookURL used to be computed from
// spec.runbook alone, so a runbook that failed to render still got a
// confident-looking .runbook-link pointing at a page nothing emitted —
// exactly the silent fallback spec §12 forbids by name.
func TestAnUnreadableRunbookRendersDistinctlyAndNeverLinksToAMissingPage(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nSteps.\n")}}
	in := input(t, files, e)
	in.Sources = catalog.SingleSource("", failFS{MapFS: files, path: "services/api/RUNBOOK.md"})

	var c diag.Collector
	site := siteMap(Site(in, &c))
	page := string(site["entity/service/api/index.html"])

	if strings.Contains(page, `class="runbook-link"`) {
		t.Errorf("a runbook that failed to render must not get a working-looking link:\n%s", page)
	}
	if !strings.Contains(page, "spec.runbook is set, but the runbook could not be rendered") {
		t.Errorf("an unreadable runbook must say so distinctly:\n%s", page)
	}
	if strings.Contains(page, "No documentation.") {
		t.Errorf("an unreadable runbook must not read as \"no documentation at all\":\n%s", page)
	}
	if _, ok := site["entity/service/api/runbook.html"]; ok {
		t.Error("no runbook page should have been emitted when the read failed")
	}

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	got := ds[0]
	want := "cannot read services/api/RUNBOOK.md: open services/api/RUNBOOK.md: permission denied"
	if got.Message != want {
		t.Errorf("Message = %q, want %q", got.Message, want)
	}
	if got.Line == 0 {
		t.Error("Line must not be 0")
	}
}

// A file that is in the repository and whose content was never fetched is a
// landsraad bug, and must read as one.
//
// fetch.ErrNotFetched exists precisely so this is not reported as a missing
// file — "a missing-file diagnostic would send somebody to look for a file
// that is sitting in their repository". The renderer then dropped the error
// and said "cannot read X", which sends them exactly there.
func TestADocumentThatWasListedButNeverFetchedReadsAsALandsraadBug(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	in := input(t, fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# API\n\nThe overview.\n")},
	}, e)

	// A sparse filesystem that listed the page and never fetched its bytes:
	// what cmd/'s content planner leaves behind when it forgets a file.
	remote := fetch.NewFS()
	remote.AddDir("services/api/docs", []fetch.Entry{
		{Path: "services/api/docs/index.md", SHA: "0123456789abcdef0123456789abcdef01234567", Size: 20},
	})
	in.Sources = catalog.SingleSource("", remote)

	var c diag.Collector
	Site(in, &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	got := ds[0]
	wantMsg := "services/api/docs/index.md is in the repository but its content was never fetched"
	if got.Message != wantMsg {
		t.Errorf("Message = %q, want %q", got.Message, wantMsg)
	}
	wantHint := "this is a landsraad bug, not a problem with your catalog: " +
		"cmd/'s contentSet must name every file a stage reads"
	if got.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", got.Hint, wantHint)
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
	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	ed := docsFor(in, e, dt, md.New(), &c)
	// docs/index.md is hoisted onto the entity page (ruling R18), so its
	// search entry is IndexDoc rather than a row in Docs — entityPages records
	// it only once that page has actually been emitted.
	if ed.IndexDoc == nil {
		t.Fatal("docs/index.md produced no search entry")
	}
	if len(ed.Docs) != 0 {
		t.Errorf("the hoisted index must not also be a sub-page entry: %+v", ed.Docs)
	}
	if ed.IndexDoc.Headings == nil {
		t.Error("RenderedDoc.Headings must not be nil for a document with no headings")
	}
	if len(ed.IndexDoc.Headings) != 0 {
		t.Errorf("a document with no headings must have zero headings, got %+v", ed.IndexDoc.Headings)
	}
}

// brokenDocTemplate compiles and then fails to execute, which is what
// renderPage reports and skips. A template that fails to COMPILE would never
// reach docsFor at all — entityPages gives up before the entity loop — so the
// only way to observe one doc page failing while its siblings succeed is a
// template that executes badly.
func brokenDocTemplate() *template.Template {
	return template.Must(template.New("base.html").Parse(`{{.NoSuchFieldOnDocPage}}`))
}

// A doc page that failed to render must leave no trace claiming otherwise.
//
// out.Nav and out.Docs were appended to BEFORE renderPage was called, so a
// document that failed to render still put a link in the entity page's
// documentation nav and a row in search-index.json, both pointing at a page
// nothing emitted. RunbookURL two lines below was already guarded for exactly
// this reason; these were not.
func TestADocumentThatFailsToRenderLeavesNoNavLinkAndNoSearchEntry(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/runbook.md": {Data: []byte("# Runbook\n\nDrain the queue.\n")},
	}
	in := input(t, files, e)

	var c diag.Collector
	ed := docsFor(in, e, brokenDocTemplate(), md.New(), &c)

	if len(ed.Files) != 0 {
		t.Fatalf("nothing rendered, so nothing may be emitted: %+v", ed.Files)
	}
	if len(ed.Nav) != 0 {
		t.Errorf("the entity page must not link to a doc page nothing emitted: %+v", ed.Nav)
	}
	if len(ed.Docs) != 0 {
		t.Errorf("search-index.json must not carry a row for a page nothing emitted: %+v", ed.Docs)
	}

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("want exactly one diagnostic, got %d: %+v", len(ds), ds)
	}
	wantMsg := "cannot render entity/service/api/docs/runbook.html: " +
		"template: base.html:1:2: executing \"base.html\" at <.NoSuchFieldOnDocPage>: " +
		"can't evaluate field NoSuchFieldOnDocPage in type render.DocPage"
	if ds[0].Message != wantMsg {
		t.Errorf("Message =\n%q\nwant\n%q", ds[0].Message, wantMsg)
	}
	if ds[0].File != "entity/service/api/docs/runbook.html" {
		t.Errorf("File = %q, want %q", ds[0].File, "entity/service/api/docs/runbook.html")
	}
	if ds[0].Line != 1 {
		t.Errorf("Line = %d, want 1", ds[0].Line)
	}
}

// The same guard on the second loop: spec.runbook living outside spec.docs
// takes a different code path to the same kind of page, and had the same
// defect.
func TestARunbookOutsideTheDocsDirectoryThatFailsToRenderLeavesNoSearchEntry(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/api/RUNBOOK.md"
	files := fstest.MapFS{
		"services/api/RUNBOOK.md": {Data: []byte("# Runbook\n\nDrain the queue.\n")},
	}
	in := input(t, files, e)

	var c diag.Collector
	ed := docsFor(in, e, brokenDocTemplate(), md.New(), &c)

	if len(ed.Docs) != 0 {
		t.Errorf("search-index.json must not carry a row for a runbook nothing emitted: %+v", ed.Docs)
	}
	if ed.RunbookURL != "" {
		t.Errorf("RunbookURL = %q, want empty for a runbook that failed to render", ed.RunbookURL)
	}
	if !ed.RunbookUnreadable {
		t.Error("a declared runbook that never rendered must be reported as unreadable, not as absent")
	}
}

// A documentation filename is user data and it becomes part of a page path.
// "2024-06-01T09:00-incident.md" is a perfectly legal filename on Linux and
// macOS, and the path it produces is one cmd/'s writeSite will not write —
// so it has to be diagnosed here, by name, while there is still a file to
// name. Caught downstream instead, `landsraad build` aborts with the output
// directory half-updated and blames a landsraad bug for the user's filename.
func TestADocumentationFilenameThatCannotBecomeAPagePathIsReportedAndSkipped(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/2024-06-01T09:00-incident.md": {Data: []byte("# Incident\n\nWhat happened.\n")},
		"services/api/docs/rollback.md":                  {Data: []byte("# Rollback\n\nHow to.\n")},
	}
	in := input(t, files, e)
	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	ed := docsFor(in, e, dt, md.New(), &c)

	// Accumulate and skip: the sibling document is untouched.
	if len(ed.Files) != 1 || ed.Files[0].Path != "entity/service/api/docs/rollback.html" {
		t.Fatalf("the other document must still render, got %+v", ed.Files)
	}
	for _, f := range ed.Files {
		if !emit.ValidPath(f.Path) {
			t.Errorf("emitted a path writeSite would refuse: %q", f.Path)
		}
	}
	if len(ed.Nav) != 1 || ed.Nav[0].URL != "docs/rollback.html" {
		t.Errorf("the nav must not link to the skipped page: %+v", ed.Nav)
	}
	if len(ed.Docs) != 1 || ed.Docs[0].URL != "entity/service/api/docs/rollback.html" {
		t.Errorf("the search index must not carry the skipped page: %+v", ed.Docs)
	}

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("want exactly one diagnostic, got %d: %+v", len(ds), ds)
	}
	d := ds[0]
	// Warn, not error: Build refuses on c.HasErrors(), and one unpublishable
	// filename is not a reason to withhold the whole portal.
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %q, want %q — an error here would fail the whole build", d.Severity, diag.SevWarn)
	}
	wantMsg := "cannot publish services/api/docs/2024-06-01T09:00-incident.md: " +
		"its name would make the page path \"entity/service/api/docs/2024-06-01T09:00-incident.html\", " +
		"which landsraad cannot write"
	if d.Message != wantMsg {
		t.Errorf("Message =\n%q\nwant\n%q", d.Message, wantMsg)
	}
	wantHint := `rename the file without ":" or "\" — a documentation filename becomes part of its page's URL`
	if d.Hint != wantHint {
		t.Errorf("Hint =\n%q\nwant\n%q", d.Hint, wantHint)
	}
	if d.File != "services/api/docs/2024-06-01T09:00-incident.md" {
		t.Errorf("File = %q, want the offending file", d.File)
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	if d.Check != "docs-filename" {
		t.Errorf("Check = %q, want %q", d.Check, "docs-filename")
	}
}

// The sweep, pinned. Every other site path this package builds comes from a
// source that cannot produce an invalid one, and the test says which:
// EntityURL from a schema-constrained name, TeamURL from Slug, and the runbook
// and hoisted-index pages from literals. If a future change routes a
// user-controlled string into any of them, this fails.
func TestEverySitePathSurvivesAHostileButLegalRepository(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	// spec.runbook's own filename never becomes a page path — the page is the
	// literal "runbook.html" — so a colon in it must be harmless.
	e.Spec.Runbook = "services/api/2024:06:01-RUNBOOK.md"
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/2024:06:01-RUNBOOK.md": {Data: []byte("# Runbook\n\nDrain.\n")},
		"services/api/docs/index.md":         {Data: []byte("# api\n\nThe front door.\n")},
		"services/api/docs/ok.md":            {Data: []byte("# OK\n\nFine.\n")},
	}
	in := input(t, files, e)

	var c diag.Collector
	for _, f := range Site(in, &c) {
		if !emit.ValidPath(f.Path) {
			t.Errorf("Site emitted a path writeSite would refuse: %q", f.Path)
		}
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Errorf("nothing here is unpublishable, so nothing may be reported: %+v", ds)
	}
}

// An entity page that fails to render takes its hoisted docs/index.md search
// entry with it, for the same reason: the page that entry points at is the
// entity page, and nothing emitted it.
func TestAnEntityPageThatFailsToRenderLeavesNoHoistedIndexSearchEntry(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# api\n\nThe front door.\n")},
	}
	in := input(t, files, e)

	// entity.html is real; base.html is not, so every entity page fails to
	// execute while doc.html's pages are unaffected.
	web := webFSWithout(t, "catalog.html")
	web["web/templates/base.html"] = &fstest.MapFile{Data: []byte(`{{.NoSuchFieldOnEntityView}}`)}

	var c diag.Collector
	_, docs := entityPages(web, in, &c)
	for _, d := range docs {
		if d.URL == "entity/service/api/" {
			t.Errorf("the hoisted index was indexed although its page never rendered: %+v", d)
		}
	}
}

// The runbook gets a prominent link of its own (spec §5.3, and
// TestTheRunbookIsLinkedProminently), so listing it AGAIN as an ordinary row
// in the documentation nav printed "Runbook" twice, stacked, on every entity
// whose spec.runbook lives inside spec.docs -- which is the common layout.
// Noticed by looking at a rendered page, not at the code.
func TestTheRunbookIsNotAlsoAnOrdinaryRowInTheDocNav(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	e.Spec.Runbook = "services/api/docs/runbook.md"
	files := fstest.MapFS{
		"services/api/docs/runbook.md": {Data: []byte("# Runbook\n\nDrain the queue.\n")},
		"services/api/docs/scaling.md": {Data: []byte("# Scaling\n\nAdd replicas.\n")},
	}
	in := input(t, files, e)
	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	ed := docsFor(in, e, dt, md.New(), &c)

	// It still renders and is still reachable and searchable.
	if ed.RunbookURL != "docs/runbook.html" {
		t.Errorf("RunbookURL = %q, want the rendered runbook", ed.RunbookURL)
	}
	if len(ed.Files) != 2 {
		t.Errorf("both documents must still be emitted, got %+v", ed.Files)
	}
	if len(ed.Docs) != 2 {
		t.Errorf("both documents must still be in the search index, got %+v", ed.Docs)
	}

	// But it is not repeated as a plain nav row.
	for _, l := range ed.Nav {
		if l.URL == "docs/runbook.html" {
			t.Errorf("the runbook has its own link; it must not also be a nav row: %+v", ed.Nav)
		}
	}
	if len(ed.Nav) != 1 || ed.Nav[0].URL != "docs/scaling.html" {
		t.Errorf("the other document must still be listed, got %+v", ed.Nav)
	}
}

// An entity whose entire documentation is its runbook is a legal layout, and
// docsFor's index.md branch returned before anything noticed. runbookRendered
// stayed false, so the entity page told the reader "spec.runbook is set, but
// the runbook could not be rendered -- see the build diagnostics", about a
// runbook that had rendered perfectly well inlined a few lines below, and
// about diagnostics that were never emitted.
func TestARunbookThatIsTheDocsIndexIsNotReportedUnreadable(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Docs = "services/api/docs"
	e.Spec.Runbook = "services/api/docs/index.md"
	files := fstest.MapFS{
		"services/api/docs/index.md": {Data: []byte("# API\n\nEverything, including how to page.\n")},
	}
	in := input(t, files, e)
	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	ed := docsFor(in, e, dt, md.New(), &c)

	if ed.RunbookUnreadable {
		t.Error("the runbook rendered — inlined as the index — so the page must not claim it could not be")
	}
	if ed.Index == "" {
		t.Error("and it must actually be inlined")
	}

	// End to end: the false notice must not reach the page, and neither must
	// the no-documentation fallback.
	var c2 diag.Collector
	page := string(siteMap(Site(in, &c2))["entity/service/api/index.html"])
	if strings.Contains(page, "could not be rendered") {
		t.Errorf("the entity page must not claim the runbook failed:\n%s", page)
	}
	if strings.Contains(page, "No documentation.") {
		t.Errorf("the entity page must not claim there is no documentation:\n%s", page)
	}
	if !strings.Contains(page, "Everything, including how to page.") {
		t.Errorf("the runbook's text must be on the page:\n%s", page)
	}
}

// Ruling R41: Repo names the repository that holds File, not the
// repository the command is standing in. A satellite entity's docs
// diagnostic must therefore carry the satellite's repository — otherwise a
// multi-repository build prints this line with no repository prefix beside
// lines that have one, telling the reader the file is local when it is in
// a fetched repository.
func TestADocDiagnosticFromASatelliteEntityCarriesItsRepository(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.SourceRepo = "edge-gateway"
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{
		"services/api/docs/broken.md": {Data: []byte("# Broken\n\nUnreachable.\n")},
	}
	in := Input{Sources: catalog.Sources{
		"edge-gateway": failFS{MapFS: files, path: "services/api/docs/broken.md"},
	}}
	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	var c diag.Collector
	docsFor(in, e, dt, md.New(), &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	got := ds[0]
	if got.Repo != "edge-gateway" {
		t.Errorf("Repo = %q, want %q", got.Repo, "edge-gateway")
	}
	if got.File != "services/api/docs/broken.md" {
		t.Errorf("File = %q, want %q", got.File, "services/api/docs/broken.md")
	}
	if got.Check != "docs-unreadable" {
		t.Errorf("Check = %q, want %q", got.Check, "docs-unreadable")
	}
	wantMsg := "cannot read services/api/docs/broken.md: open services/api/docs/broken.md: permission denied"
	if got.Message != wantMsg {
		t.Errorf("Message = %q, want %q", got.Message, wantMsg)
	}
}

// Two entities in different repositories naming the same relative docs path
// must render different documents. Before Plan 4 there was one fs.FS for the
// whole catalog, so the second entity would have silently rendered the
// first's index.
func TestDocsForReadsTheEntitysOwnRepository(t *testing.T) {
	mono := fstest.MapFS{
		"docs/index.md": {Data: []byte("# API\n\nThe monorepo's own index.\n")},
	}
	edge := fstest.MapFS{
		"docs/index.md": {Data: []byte("# API\n\nThe edge-gateway's own index.\n")},
	}
	src := catalog.Sources{"monorepo": mono, "edge-gateway": edge}

	dt, err := templateSet(webFS, "doc.html")
	if err != nil {
		t.Fatalf("templateSet: %v", err)
	}

	for _, tt := range []struct {
		repo string
		want string
	}{
		{"monorepo", "The monorepo's own index."},
		{"edge-gateway", "The edge-gateway's own index."},
	} {
		t.Run(tt.repo, func(t *testing.T) {
			e := ent("api", catalog.KindService, "team-payments", 1)
			e.SourceRepo = tt.repo
			e.Spec.Docs = "docs"

			var c diag.Collector
			in := Input{Sources: src}
			ed := docsFor(in, e, dt, md.New(), &c)
			if !strings.Contains(string(ed.Index), tt.want) {
				t.Errorf("rendered index does not contain %q:\n%s", tt.want, ed.Index)
			}
			if ds := c.Diagnostics(); len(ds) != 0 {
				t.Errorf("want no diagnostics, got %+v", ds)
			}
		})
	}
}
