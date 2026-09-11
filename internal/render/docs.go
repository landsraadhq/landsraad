package render

import (
	"errors"
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
	"github.com/landsraadhq/landsraad/internal/sparsefs"
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
	// Docs describes the sub-pages this entity emitted, one entry per page
	// actually written.
	Docs []RenderedDoc
	// IndexDoc is docs/index.md's search entry, kept out of Docs because the
	// page it describes is the ENTITY page, which docsFor does not emit.
	// entityPages records it only after that page renders, for the same reason
	// Docs and Nav are only appended to after renderPage succeeds.
	IndexDoc *RenderedDoc
	// RunbookURL is where spec.runbook actually rendered, "" when it is
	// unset or failed to render. This reflects render success, never spec
	// metadata alone: a link to a page that was not actually emitted is
	// the silent fallback spec §12 forbids by name.
	RunbookURL string
	// DocsUnreadable is set when spec.docs names a directory this build
	// could not read — a state distinct from spec.docs being unset (spec
	// §12: these are different answers and must render differently).
	DocsUnreadable bool
	// RunbookUnreadable is set when spec.runbook names a file that failed
	// to read or render — a state distinct from spec.runbook being unset.
	RunbookUnreadable bool
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

// unreadableDoc reports a document that could not be read, saying which of
// the two very different reasons it was.
//
// sparsefs.ErrNotFetched means the file is sitting in the repository and cmd/'s
// content planner never asked the host for it. That is a landsraad bug, and
// it gets catalog.MissingSourceDiagnostic's treatment: saying so is the
// difference between somebody fixing their catalog (which is fine) and
// somebody filing this. Discarding the error made the two indistinguishable
// — and made a plain permission error indistinguishable from both.
func unreadableDoc(e *catalog.Entity, repoPath string, err error) diag.Diagnostic {
	if errors.Is(err, sparsefs.ErrNotFetched) {
		return diag.Diagnostic{
			Severity: diag.SevError, File: repoPath, Line: 1,
			Repo:    e.SourceRepo,
			Entity:  e.Metadata.Name,
			Check:   "docs-unreadable",
			Message: fmt.Sprintf("%s is in the repository but its content was never fetched", repoPath),
			Hint: "this is a landsraad bug, not a problem with your catalog: cmd/'s contentSet " +
				"must name every file a stage reads",
		}
	}
	return diag.Diagnostic{
		Severity: diag.SevError, File: repoPath, Line: 1,
		Repo:    e.SourceRepo,
		Entity:  e.Metadata.Name,
		Check:   "docs-unreadable",
		Message: fmt.Sprintf("cannot read %s: %v", repoPath, err),
		Hint:    "the file is named by spec.docs or spec.runbook",
	}
}

// docsFor renders one entity's documentation.
//
// Every failure is reported and skipped. One unreadable document must not
// cost the reader the other pages (spec §12).
func docsFor(in Input, e *catalog.Entity, t *template.Template, m goldmark.Markdown, c *diag.Collector) entityDocs {
	var out entityDocs
	ref := e.Ref()
	entityDir := EntityURL(ref)

	// Resolved once, at the top: every read below reaches through fsys, never
	// in.Sources again. When it is missing, this returns immediately rather
	// than falling into the loops below, unlike every other failure in this
	// function. Elsewhere docsFor reports and carries on, because one
	// unreadable document must not cost the reader the other pages. Here
	// there is no filesystem at all, so every subsequent read would produce
	// an identical diagnostic — one per document, for a bug that has nothing
	// to do with any of them.
	fsys, ok := in.Sources.For(e)
	if !ok {
		c.Add(catalog.MissingSourceDiagnostic(e, "docs-unreadable"))
		return out
	}

	// render reads and renders one Markdown file, returning the document and
	// the search-index entry that would describe it.
	//
	// It deliberately records NOTHING on out. Every caller places the entry
	// itself, after the page it points at has actually been emitted: a search
	// entry or a nav link written before renderPage succeeded points the reader
	// at a page nothing produced, which is the silent fallback spec §12 forbids
	// by name. RunbookURL was already guarded for exactly this reason; Docs and
	// Nav were not, so a document that failed to render still got a doc-nav
	// link on the entity page and a row in search-index.json.
	//
	// relURL is where the page is EMITTED, relative to the entity directory,
	// and is the point every rewritten link is computed from. searchURL is the
	// URL the search index should point at. They differ for exactly one
	// document: ruling R18 hoists docs/index.md onto the entity page, whose
	// canonical URL is the directory itself — CatalogRow.URL is EntityURL(ref),
	// with no "index.html" — so recording it at entityDir+"index.html" spelled
	// one page two ways and put two rows in the index for every documented
	// entity, one labelled "Service" and one not.
	render := func(repoPath, relURL, searchURL string) (md.Doc, RenderedDoc, bool) {
		data, err := fs.ReadFile(fsys, repoPath)
		if err != nil {
			c.Add(unreadableDoc(e, repoPath, err))
			return md.Doc{}, RenderedDoc{}, false
		}
		linker := docLinker{
			docsDir: e.Spec.Docs, runbook: e.Spec.Runbook,
			entityDir: entityDir, srcDir: path.Dir(repoPath), selfURL: entityDir + relURL,
		}
		doc, err := md.Render(m, data, linker.rewrite)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: repoPath, Line: 1,
				Repo:    e.SourceRepo,
				Entity:  e.Metadata.Name,
				Check:   "docs-render",
				Message: fmt.Sprintf("cannot render %s: %v", repoPath, err),
			})
			return md.Doc{}, RenderedDoc{}, false
		}
		return doc, RenderedDoc{
			URL: searchURL, Title: docTitle(doc, repoPath),
			Text: doc.Text, Headings: nonNilHeadings(doc.Headings), EntityRef: ref.String(),
		}, true
	}

	// 1. The docs directory.
	var mdPaths []string
	if e.Spec.Docs != "" {
		err := fs.WalkDir(fsys, e.Spec.Docs, func(p string, d fs.DirEntry, err error) error {
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
				Repo:    e.SourceRepo,
				Entity:  e.Metadata.Name,
				Check:   "docs-unreadable",
				Message: fmt.Sprintf("cannot read the documentation directory %s: %v", e.Spec.Docs, err),
			})
			out.DocsUnreadable = true
		}
		sort.Strings(mdPaths)
	}

	// runbookRendered tracks whether spec.runbook's page was actually
	// emitted, from either loop below — never recomputed from e.Spec.Runbook
	// alone, which cannot tell success from failure.
	var runbookRendered bool

	for _, repoPath := range mdPaths {
		rel, err := relativeTo(e.Spec.Docs, repoPath)
		if err != nil {
			continue
		}
		if rel == "index.md" {
			// Inlined on the entity page, and NOT also a sub-page: one
			// document, one URL (ruling R18). The search entry therefore
			// carries the entity's own URL, and entityPages decides whether it
			// is recorded at all.
			if doc, rd, ok := render(repoPath, "index.html", entityDir); ok {
				out.Index = mdToHTML(doc)
				out.IndexDoc = &rd
				if repoPath == e.Spec.Runbook {
					// An entity whose whole documentation IS its runbook is a
					// legal and unremarkable layout, and this branch used to
					// return before anything noticed. runbookRendered then
					// stayed false and the page told the reader "spec.runbook
					// is set, but the runbook could not be rendered — see the
					// build diagnostics", about a runbook that had rendered
					// perfectly well, inlined a few lines further down, and
					// about diagnostics that do not exist. No separate link is
					// wanted: the runbook is already the body of this page.
					runbookRendered = true
				}
			}
			continue
		}
		relURL := "docs/" + htmlSuffix(rel)
		sitePath := entityDir + relURL
		// This is the only place a user-controlled string becomes a page path.
		// rel is a filename out of the repository — WalkDir found it, relativeTo
		// is a bare TrimPrefix and htmlSuffix a bare suffix swap — so a file
		// called "2024-06-01T09:00-incident.md" produces a path cmd/'s writeSite
		// will not write. Caught here, the entity loses that one page, by name,
		// and the portal is still built. Caught there, `landsraad build` aborts
		// after the output directory is already half-updated and tells the user
		// they have found a landsraad bug, when what they have is a filename.
		//
		// Reported at warn: the artifact is correct about everything it does
		// publish, and one skipped document is not a reason to refuse to publish
		// a whole portal. The other documents under the same spec.docs still
		// render — accumulate and skip, never stop.
		if !emit.ValidPath(sitePath) {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn, File: repoPath, Line: 1,
				Repo:   e.SourceRepo,
				Entity: e.Metadata.Name,
				Check:  "docs-filename",
				Message: fmt.Sprintf("cannot publish %s: its name would make the page path %q, which landsraad cannot write",
					repoPath, sitePath),
				Hint: `rename the file without ":" or "\" — a documentation filename becomes part of its page's URL`,
			})
			continue
		}
		doc, rd, ok := render(repoPath, relURL, sitePath)
		if !ok {
			continue
		}
		view := DocPage{
			Page:       newPage(in, sitePath, docTitle(doc, repoPath), "catalog"),
			EntityName: e.Metadata.Name,
			EntityURL:  entityDir,
			HTML:       mdToHTML(doc),
		}
		f, ok := renderPage(t, sitePath, view, c)
		if !ok {
			continue
		}
		out.Files = append(out.Files, f)
		out.Docs = append(out.Docs, rd)
		if repoPath == e.Spec.Runbook {
			// The runbook already gets a prominent link of its own — it is
			// what an on-call engineer opens at 3am, not a row in a file
			// listing. Adding it to the nav as well printed "Runbook" twice,
			// stacked, for every entity whose spec.runbook lives inside
			// spec.docs, which is the layout `landsraad init` scaffolds.
			out.RunbookURL = relURL
			runbookRendered = true
			continue
		}
		out.Nav = append(out.Nav, DocLink{Title: docTitle(doc, repoPath), URL: relURL})
	}

	// 2. The runbook, when it is not already one of the documents above.
	if e.Spec.Runbook != "" && !underDir(e.Spec.Docs, e.Spec.Runbook) {
		relURL := "runbook.html"
		sitePath := entityDir + relURL
		if doc, rd, ok := render(e.Spec.Runbook, relURL, sitePath); ok {
			view := DocPage{
				Page:       newPage(in, sitePath, docTitle(doc, e.Spec.Runbook), "catalog"),
				EntityName: e.Metadata.Name,
				EntityURL:  entityDir,
				HTML:       mdToHTML(doc),
			}
			if f, ok := renderPage(t, sitePath, view, c); ok {
				out.Files = append(out.Files, f)
				out.Docs = append(out.Docs, rd)
				out.RunbookURL = relURL
				runbookRendered = true
			}
		}
	}

	// A declared runbook that never actually rendered is a state distinct
	// from no runbook at all: entity.html must not show a working-looking
	// link to a page nothing emitted.
	if e.Spec.Runbook != "" && !runbookRendered {
		out.RunbookUnreadable = true
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
