package render

import (
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/yuin/goldmark"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render/md"
)

// DocLink is one entry in an entity's documentation nav.
type DocLink struct {
	Title string
	// URL is relative to the entity's own page, so the nav needs no Root.
	URL string
}

// RenderedDoc is one document's contribution to the search index (Task 12).
type RenderedDoc struct {
	URL       string
	Title     string
	Text      string
	Headings  []md.Heading
	EntityRef string
}

// DocPage is one documentation sub-page.
//
// It has no Nav field of its own: DocPage embeds Page, whose Nav string
// drives base.html's top-nav highlighting ({{eq .Nav "catalog"}}). A same-
// named []DocLink field here would shadow that promoted field and break
// every doc page's base template — found by running this task's tests.
type DocPage struct {
	Page
	EntityName string
	EntityURL  string
	// HTML has been through md.Render, which runs goldmark WITHOUT
	// WithUnsafe. See the cast in mdToHTML.
	HTML template.HTML
}

// entityDocs is everything one entity's documentation contributes.
type entityDocs struct {
	Files []emit.File
	Nav   []DocLink
	// Index is docs/index.md, inlined on the entity page (ruling R18).
	Index template.HTML
	Docs  []RenderedDoc
}

// mdToHTML performs the single template.HTML conversion in this codebase.
//
// It is safe for a specific, tested reason: md.New() configures goldmark
// without WithUnsafe (spec §14.1), so raw HTML in a runbook was already
// escaped into text before these bytes existed —
// TestRawHTMLIsNeverInjected in internal/render/md asserts it.
//
// Do not reuse this on bytes that have not been through md.Render.
func mdToHTML(d md.Doc) template.HTML { return template.HTML(d.HTML) }

// htmlSuffix maps a repository-relative Markdown path to its page path.
func htmlSuffix(p string) string { return strings.TrimSuffix(p, ".md") + ".html" }

// rewriteMarkdownLinks turns a relative *.md destination into *.html,
// preserving any #fragment. A link that works on GitHub must work here.
func rewriteMarkdownLinks(dest string) string {
	frag := ""
	if i := strings.Index(dest, "#"); i >= 0 {
		dest, frag = dest[:i], dest[i:]
	}
	if !strings.HasSuffix(dest, ".md") {
		return dest + frag
	}
	return htmlSuffix(dest) + frag
}

// docsFor renders one entity's documentation.
//
// Every failure is reported and skipped. One unreadable document must not
// cost the reader the other pages (spec §12).
func docsFor(in Input, e *catalog.Entity, t *template.Template, m goldmark.Markdown, c *diag.Collector) entityDocs {
	var out entityDocs
	ref := e.Ref()
	entityDir := EntityURL(ref)

	render := func(repoPath, relURL string) (md.Doc, bool) {
		data, err := fs.ReadFile(in.FS, repoPath)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: repoPath, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-unreadable",
				Message: fmt.Sprintf("cannot read %s", repoPath),
				Hint:    "the file is named by spec.docs or spec.runbook",
			})
			return md.Doc{}, false
		}
		doc, err := md.Render(m, data, rewriteMarkdownLinks)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: repoPath, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-render",
				Message: fmt.Sprintf("cannot render %s: %v", repoPath, err),
			})
			return md.Doc{}, false
		}
		out.Docs = append(out.Docs, RenderedDoc{
			URL: entityDir + relURL, Title: docTitle(doc, repoPath),
			Text: doc.Text, Headings: nonNilHeadings(doc.Headings), EntityRef: ref.String(),
		})
		return doc, true
	}

	// 1. The docs directory.
	var mdPaths []string
	if e.Spec.Docs != "" {
		err := fs.WalkDir(in.FS, e.Spec.Docs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				mdPaths = append(mdPaths, p)
			}
			return nil
		})
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: e.Spec.Docs, Line: 1,
				Entity:  e.Metadata.Name,
				Check:   "docs-unreadable",
				Message: fmt.Sprintf("cannot read the documentation directory %s: %v", e.Spec.Docs, err),
			})
		}
		sort.Strings(mdPaths)
	}

	for _, repoPath := range mdPaths {
		rel, err := relativeTo(e.Spec.Docs, repoPath)
		if err != nil {
			continue
		}
		if rel == "index.md" {
			// Inlined on the entity page, and NOT also a sub-page: one
			// document, one URL (ruling R18).
			if doc, ok := render(repoPath, "index.html"); ok {
				out.Index = mdToHTML(doc)
			}
			continue
		}
		relURL := "docs/" + htmlSuffix(rel)
		sitePath := entityDir + relURL
		doc, ok := render(repoPath, relURL)
		if !ok {
			continue
		}
		out.Nav = append(out.Nav, DocLink{Title: docTitle(doc, repoPath), URL: relURL})
		view := DocPage{
			Page:       newPage(in, sitePath, docTitle(doc, repoPath), "catalog"),
			EntityName: e.Metadata.Name,
			EntityURL:  entityDir,
			HTML:       mdToHTML(doc),
		}
		if f, ok := renderPage(t, sitePath, view, c); ok {
			out.Files = append(out.Files, f)
		}
	}

	// 2. The runbook, when it is not already one of the documents above.
	if e.Spec.Runbook != "" && !underDir(e.Spec.Docs, e.Spec.Runbook) {
		relURL := "runbook.html"
		sitePath := entityDir + relURL
		if doc, ok := render(e.Spec.Runbook, relURL); ok {
			view := DocPage{
				Page:       newPage(in, sitePath, docTitle(doc, e.Spec.Runbook), "catalog"),
				EntityName: e.Metadata.Name,
				EntityURL:  entityDir,
				HTML:       mdToHTML(doc),
			}
			if f, ok := renderPage(t, sitePath, view, c); ok {
				out.Files = append(out.Files, f)
			}
		}
	}

	return out
}

// nonNilHeadings normalises md.Render's result for a document with no
// headings.
//
// md.Doc.Headings is only ever appended to, so a document with none leaves
// it nil rather than an empty slice — verified directly against md.Render.
// Task 12 serialises RenderedDoc into search-index.json, where a nil slice
// marshals to `null` and an empty one to `[]`; a JavaScript client doing
// doc.headings.length would crash on the former. RenderedDoc always carries
// [] instead, so Task 12 never has to know md.Doc's nil is possible.
func nonNilHeadings(h []md.Heading) []md.Heading {
	if h == nil {
		return []md.Heading{}
	}
	return h
}

// docTitle prefers the document's own H1 and falls back to its filename.
// It never invents a title from the entity: a document called
// "rollback.md" with no heading is "rollback", not "api".
func docTitle(d md.Doc, repoPath string) string {
	if d.Title != "" {
		return d.Title
	}
	return strings.TrimSuffix(path.Base(repoPath), ".md")
}

// relativeTo returns p relative to dir.
func relativeTo(dir, p string) (string, error) {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" || !strings.HasPrefix(p, dir+"/") {
		return "", fmt.Errorf("%s is not under %s", p, dir)
	}
	return strings.TrimPrefix(p, dir+"/"), nil
}

// underDir reports whether p lives inside dir.
func underDir(dir, p string) bool {
	if dir == "" {
		return false
	}
	return strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// runbookURL is where an entity's runbook page ended up, relative to the
// entity's own page — "" when it has none.
func runbookURL(e *catalog.Entity) string {
	if e.Spec.Runbook == "" {
		return ""
	}
	if rel, err := relativeTo(e.Spec.Docs, e.Spec.Runbook); err == nil {
		return "docs/" + htmlSuffix(rel)
	}
	return "runbook.html"
}
