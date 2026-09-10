package render

import (
	"fmt"
	"io/fs"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// Site renders the whole portal.
//
// It is spec §7's stage 8 for sub-project D: a pure function from a
// validated, scored catalog to the bytes of a static site. Nothing here
// writes a file — cmd/ owns the one loop that does (spec §3.1), which is
// also what lets `serve --watch` hold a rebuilt site in memory instead of
// watching its own output.
//
// Pages that fail to render are reported and skipped rather than aborting the
// walk, so a run yields every diagnostic instead of only the first. What
// becomes of the site is Build's decision, not this function's, and it is
// worth being exact about it: every skip reachable from here carries
// SevError except one, and build.go answers a collector with errors by
// returning exitValidation and writing nothing. A single broken runbook
// therefore does cost the reader the other forty pages — deliberately.
// Publishing a portal quietly missing a page is the silent fallback spec §12
// forbids, and a render failure is a bug in landsraad, not in the catalog.
//
// The one survivable skip is a documentation file whose name cannot become a
// page path (docs-filename, SevWarn): that page is dropped and the build
// still succeeds. docs.go appends to the nav only after the page is emitted,
// so nothing links to what was skipped. That ordering is the whole reason no
// index here has to filter its rows against what was actually written: a
// published portal cannot contain a link to a page this function dropped.
func Site(in Input, c *diag.Collector) []emit.File {
	return siteFrom(webFS, in, c)
}

// siteFrom is Site's logic, taking the template filesystem as a parameter (IO
// at the edges) instead of Site reading the package-level embed directly.
// webFS's real content cannot fail to compile against any legitimate Input —
// every field a template touches comes from Go code, never from a user's YAML
// — so proving Site continues past a broken page needs a filesystem a test can
// break, not a global it would have to reassign.
//
// web is threaded into EVERY page builder, not only the two it started with.
// A seam that reaches the catalog page and the assets while entity, doc, map,
// team and scorecard pages read webFS directly is not a seam: a test handing
// this a broken filesystem would still be watching the real embed render five
// of the seven page types, and would pass for that reason rather than for the
// property it claims to check.
func siteFrom(web fs.FS, in Input, c *diag.Collector) []emit.File {
	var files []emit.File

	if t, err := templateSet(web, "catalog.html"); err != nil {
		c.Add(templateCompileError("catalog.html", err))
	} else if f, ok := renderPage(t, "index.html", catalogPage(in), c); ok {
		files = append(files, f)
	}

	// Reported here, once. The page builders use teamSlugMap for their
	// links; this call is what turns a collision into a build failure.
	TeamSlugs(in.Teams, c)
	pages, docs := entityPages(web, in, c)
	files = append(files, pages...)

	if f, ok := mapPage(web, in, c); ok {
		files = append(files, f)
	}

	files = append(files, teamPages(web, in, c)...)

	if f, ok := scorecardPage(web, in, c); ok {
		files = append(files, f)
	}

	// Built from the same rows the catalog renders and the same documents
	// the entity pages do, so it cannot disagree with them about what
	// exists or what a document says.
	if f, ok := searchIndexFile(catalogRows(in), docs, c); ok {
		files = append(files, f)
	}

	files = append(files, assets(web, in, c)...)
	return files
}

// templateCompileError is the one place a broken embedded template is
// reported, so the wording cannot drift between page types.
func templateCompileError(name string, err error) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevError, File: "templates/" + name, Line: 1,
		Check:   "template",
		Message: fmt.Sprintf("cannot compile the embedded template %s: %v", name, err),
		Hint:    "this is a bug in landsraad, not in your catalog",
	}
}
