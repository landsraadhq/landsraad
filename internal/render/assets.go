package render

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// webFS holds the templates and static files.
//
// The client scripts under web/static target modern, evergreen browsers. They
// already require fetch, Promise, document.currentScript and
// Element.replaceWith, none of which exist in IE11, and there is no polyfill
// or transpiler anywhere in this repository. The ES5-looking syntax in those
// files is house style, not a compatibility contract — CONTRIBUTING.md,
// "Browser support", says so in full. Working without JavaScript at all is a
// separate requirement, and a real one.
//
// Spec §13's layout sketch puts these at a top-level web/. They live here
// instead (ruling R10): go:embed patterns are relative to the package
// directory and may not contain "..", and a root-level `package web` would
// be a public import path, which D8 forbids. The sketch predates the
// constraint and Task 15 amends it.
//
//go:embed web
var webFS embed.FS

// templateSet parses base.html plus exactly one page template.
//
// One set per page, not one set for all of them: every page file defines
// "content", so a shared set would silently keep only the last one parsed
// and render the same body on every page.
//
// web is the template filesystem — webFS in production — taken as a parameter
// (composition's "IO at the edges") rather than read from the package-level
// embed. It is NOT Input.FS, which is the user's repository. webFS's real,
// well-formed content has no reachable way to fail a page's template compile —
// every field a template touches is populated by Go code, never by a user's
// YAML — so this parameter is what lets a test hand one page builder a
// filesystem missing its template, without a package-level var anything could
// reassign. Every page builder takes it for that reason: a seam two of seven
// consumers honour is not a seam, it is a test that proves nothing.
func templateSet(web fs.FS, page string) (*template.Template, error) {
	return template.New("base.html").
		Funcs(funcs()).
		ParseFS(web, "web/templates/base.html", "web/templates/"+page)
}

func funcs() template.FuncMap {
	return template.FuncMap{
		"lower": lower,
		"pct":   pct,
		// mul exists because Go templates have no arithmetic and a score is
		// stored as a fraction but read as a percentage.
		"mul": func(f float64, by float64) float64 { return f * by },
	}
}

// pct renders a score as a whole percentage. It takes a pointer because
// "not scored" and "zero" are different answers (Plan 2, ruling R1) and the
// template asks which one it has before calling this.
func pct(f *float64) string {
	if f == nil {
		return ""
	}
	return fmt.Sprintf("%.0f%%", *f*100)
}

// renderPage executes one template into an emit.File.
//
// A template that fails to execute is reported and skipped, never fatal: one
// broken page must not hide the other forty (spec §12).
func renderPage(t *template.Template, outPath string, data any, c *diag.Collector) (emit.File, bool) {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base.html", data); err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: outPath, Line: 1,
			Check:   "template",
			Message: fmt.Sprintf("cannot render %s: %v", outPath, err),
		})
		return emit.File{}, false
	}
	return emit.File{Path: outPath, Data: buf.Bytes()}, true
}

// assets returns every static file the site serves.
//
// It walks the embedded directory rather than listing names, so adding a
// stylesheet or a client script is one new file and no code change. web is
// the template filesystem, for the same reason templateSet takes it.
func assets(web fs.FS, in Input, c *diag.Collector) []emit.File {
	var out []emit.File

	entries, err := fs.ReadDir(web, "web/static")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "assets", Line: 1,
			Check:   "assets",
			Message: fmt.Sprintf("cannot read the embedded static files: %v", err),
		})
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := fs.ReadFile(web, path.Join("web/static", e.Name()))
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "assets/" + e.Name(), Line: 1,
				Check:   "assets",
				Message: fmt.Sprintf("cannot read the embedded file %s: %v", e.Name(), err),
			})
			continue
		}
		out = append(out, emit.File{Path: "assets/" + e.Name(), Data: data})
	}

	css, err := md.ChromaCSS()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "assets/chroma.css", Line: 1,
			Check:   "assets",
			Message: fmt.Sprintf("cannot generate the syntax-highlighting stylesheet: %v", err),
		})
	} else {
		out = append(out, emit.File{Path: "assets/chroma.css", Data: css})
	}

	// A locally served Mermaid bundle, when --mermaid-src named a file. cmd/
	// reads the bytes before building Input; this only places them. Src
	// naming the local path with no bytes behind it is not something a
	// user's catalog can cause — it means cmd/ built Input wrong — so the
	// diagnostic says that rather than blaming the catalog (spec §12: no
	// silent fallbacks).
	if in.Mermaid.Src == LocalMermaidPath {
		if len(in.Mermaid.Data) > 0 {
			out = append(out, emit.File{Path: LocalMermaidPath, Data: in.Mermaid.Data})
		} else {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: LocalMermaidPath, Line: 1,
				Check:   "assets",
				Message: fmt.Sprintf("cannot emit %s: Mermaid.Data is empty", LocalMermaidPath),
				Hint:    "this is a landsraad bug, not a problem with your catalog: cmd/ must read the file named by --mermaid-src before calling Site",
			})
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
