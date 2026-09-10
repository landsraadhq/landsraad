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

// webFSData holds the templates and static files.
//
// Spec §13's layout sketch puts these at a top-level web/. They live here
// instead (ruling R10): go:embed patterns are relative to the package
// directory and may not contain "..", and a root-level `package web` would
// be a public import path, which D8 forbids. The sketch predates the
// constraint and Task 15 amends it.
//
//go:embed web
var webFSData embed.FS

// webFS is what templateSet and assets actually read from, typed as the
// fs.FS interface rather than the concrete embed.FS above it. The real,
// well-formed embedded content has no reachable way to fail a page's
// template compile — every field a template touches is populated by Go
// code, never by a user's YAML — so a test proving Site continues past a
// broken page has nowhere to inject the break except here, by substituting
// a fake filesystem for the duration of one test.
var webFS fs.FS = webFSData

// templateSet parses base.html plus exactly one page template.
//
// One set per page, not one set for all of them: every page file defines
// "content", so a shared set would silently keep only the last one parsed
// and render the same body on every page.
func templateSet(page string) (*template.Template, error) {
	return template.New("base.html").
		Funcs(funcs()).
		ParseFS(webFS, "web/templates/base.html", "web/templates/"+page)
}

func funcs() template.FuncMap {
	return template.FuncMap{
		"lower": lower,
		"pct":   pct,
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
// stylesheet or a client script is one new file and no code change.
func assets(in Input, c *diag.Collector) []emit.File {
	var out []emit.File

	entries, err := fs.ReadDir(webFS, "web/static")
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
		data, err := fs.ReadFile(webFS, path.Join("web/static", e.Name()))
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
