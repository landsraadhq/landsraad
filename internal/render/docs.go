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
//
// This is a context-free suffix swap: it is correct only when the source
// tree and the URL tree are mirrors of each other. docsFor's docLinker
// uses it as the fallback for a destination it cannot resolve any other
// way — a predictable dead link, never a panic or an empty href. It is not
// used for any link inside spec.docs directly; see docLinker.
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

// docLinker rewrites the relative Markdown links inside one document by
// mapping between the repository's source tree and the site's URL tree.
//
// The two trees are not mirrors of each other: docs/index.md is hoisted
// onto the entity page itself (ruling R18), one directory above every
// other document under spec.docs. A context-free .md -> .html suffix swap
// gets any link crossing that hoist wrong, in both directions — verified
// by printing the actual rendered HTML: docs/index.md's own link to
// docs/runbook.md came out as "runbook.html" (a dead link; the real page
// is one level down, at "docs/runbook.html"), and a nested doc's link back
// to docs/index.md came out as "../index.html" (a dead link; index.md has
// no page of its own to point at).
//
// The fix resolves a link's destination against the *source* directory of
// the document containing it, maps that source path to its URL (with the
// index.md special case), and computes the relative path from the
// containing document's own URL to the target's — the only URL-tree fact
// that source-relative Markdown links cannot already encode.
//
// A destination this cannot resolve — outside anything this entity
// renders, or one that climbs above the docs root — falls back to
// rewriteMarkdownLinks's plain suffix swap. That is a predictable dead
// link, not a panic or an empty href; this package does not validate link
// targets, which is a separate, deliberately out-of-scope feature.
type docLinker struct {
	docsDir   string
	runbook   string
	entityDir string
	// srcDir is the source-repository directory of the document currently
	// being rendered; a relative destination is resolved against it.
	srcDir string
	// selfURL is that document's own rendered URL, site-root-relative —
	// the point every rewritten link is computed relative to.
	selfURL string
}

func (l docLinker) rewrite(dest string) string {
	base, frag := dest, ""
	if i := strings.Index(dest, "#"); i >= 0 {
		base, frag = dest[:i], dest[i:]
	}
	if !strings.HasSuffix(base, ".md") {
		return dest
	}
	targetURL, ok := l.sourceToURL(path.Join(l.srcDir, base))
	if !ok {
		return rewriteMarkdownLinks(dest)
	}
	return relativeURL(l.selfURL, targetURL) + frag
}

// sourceToURL maps a repository-relative source path to the URL it renders
// at, when docsFor renders that path at all.
func (l docLinker) sourceToURL(src string) (string, bool) {
	if l.docsDir != "" {
		if src == l.docsDir+"/index.md" {
			// Ruling R18: hoisted onto the entity page, not docs/index.html.
			return l.entityDir + "index.html", true
		}
		if rel, err := relativeTo(l.docsDir, src); err == nil {
			return l.entityDir + "docs/" + htmlSuffix(rel), true
		}
	}
	if l.runbook != "" && src == l.runbook && !underDir(l.docsDir, l.runbook) {
		return l.entityDir + "runbook.html", true
	}
	return "", false
}

// relativeURL computes the relative link from the page at fromURL to the
// page at toURL, both site-root-relative, slash-separated paths — the
// standard "shared prefix, then climb, then descend" construction.
func relativeURL(fromURL, toURL string) string {
	from := strings.Split(fromURL, "/")
	from = from[:len(from)-1] // the directory containing fromURL
	to := strings.Split(toURL, "/")
	toDir, toBase := to[:len(to)-1], to[len(to)-1]

	common := 0
	for common < len(from) && common < len(toDir) && from[common] == toDir[common] {
		common++
	}
	var parts []string
	for i := common; i < len(from); i++ {
		parts = append(parts, "..")
	}
	parts = append(parts, toDir[common:]...)
	parts = append(parts, toBase)
	return strings.Join(parts, "/")
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
		linker := docLinker{
			docsDir: e.Spec.Docs, runbook: e.Spec.Runbook,
			entityDir: entityDir, srcDir: path.Dir(repoPath), selfURL: entityDir + relURL,
		}
		doc, err := md.Render(m, data, linker.rewrite)
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
