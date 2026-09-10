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
// Pages that fail to render are reported and skipped. A single broken
// runbook must not cost the reader the other forty pages.
func Site(in Input, c *diag.Collector) []emit.File {
	return siteFrom(webFS, in, c)
}

// siteFrom is Site's logic, taking the filesystem as a parameter (IO at the
// edges) instead of Site reading the package-level embed directly. webFS's
// real content cannot fail to compile against any legitimate Input — every
// field a template touches comes from Go code, never from a user's YAML —
// so proving Site continues past a broken page needs a filesystem a test
// can break, not a global it would have to reassign.
func siteFrom(fsys fs.FS, in Input, c *diag.Collector) []emit.File {
	var files []emit.File

	if t, err := templateSetFrom(fsys, "catalog.html"); err != nil {
		c.Add(templateCompileError("catalog.html", err))
	} else if f, ok := renderPage(t, "index.html", catalogPage(in), c); ok {
		files = append(files, f)
	}

	files = append(files, assetsFrom(fsys, in, c)...)
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
