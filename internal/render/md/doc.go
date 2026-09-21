package md

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// searchTextLimit caps the body text kept per document for the search index
// (ruling R17). Search over a static site is a client-side substring match
// over a JSON file every visitor downloads; a full-text index is a server
// component, which spec §15 rules out.
const searchTextLimit = 2000

// Heading is one heading in a document, with the id goldmark assigned it so
// the search results and the table of contents can deep-link.
type Heading struct {
	Level int
	Text  string
	ID    string
}

// Doc is one rendered Markdown file, in the three shapes its three consumers
// need — all produced from a single AST walk.
type Doc struct {
	HTML []byte
	// Title is the first H1, or "" when the document has none. The caller
	// decides what to show instead; inventing one here would be a silent
	// fallback.
	Title    string
	Headings []Heading
	// Text is headings and paragraph text for the search index, capped at
	// searchTextLimit runes.
	Text string
}

// LinkRewriter maps a relative link destination to where it lives in the
// generated site. Only relative destinations are passed to it.
type LinkRewriter func(dest string) string

// Render converts source to HTML, collecting the title, the headings and the
// search text on the way, and rewriting relative links through rewrite.
//
// One walk rather than three passes: the AST is already built, and the
// alternative is three traversals that can disagree about what the document
// contains.
func Render(m goldmark.Markdown, source []byte, rewrite LinkRewriter) (Doc, error) {
	// Before the parser sees it: a front-matter block is not Markdown, and
	// goldmark renders it as body text. Every caller goes through here, so
	// docs pages, runbooks and the hoisted index are stripped alike.
	source = stripFrontMatter(source)
	reader := text.NewReader(source)
	root := m.Parser().Parse(reader)

	var d Doc
	var textBuf strings.Builder

	err := ast.Walk(root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node := n.(type) {
		case *ast.Heading:
			h := Heading{Level: node.Level, Text: leafText(node, source)}
			if id, ok := node.AttributeString("id"); ok {
				if b, ok := id.([]byte); ok {
					h.ID = string(b)
				}
			}
			d.Headings = append(d.Headings, h)
			if node.Level == 1 && d.Title == "" {
				d.Title = h.Text
			}
		case *ast.Text:
			// Every inline text leaf, in document order: headings,
			// paragraphs, list items, table cells. NOT fenced code, which is
			// not an ast.Text and would swamp prose matches.
			seg := node.Segment
			appendText(&textBuf, string(seg.Value(source)))
		case *ast.String:
			// Some extensions synthesise text that has no source segment.
			appendText(&textBuf, string(node.Value))
		case *ast.Link:
			if rewrite != nil {
				node.Destination = []byte(rewriteRelative(string(node.Destination), rewrite))
			}
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return Doc{}, err
	}

	var out bytes.Buffer
	if err := m.Renderer().Render(&out, source, root); err != nil {
		return Doc{}, err
	}
	d.HTML = out.Bytes()
	// Normalise whitespace once, at the end. The walk emits one fragment per
	// inline node, so `Payments **worker**` arrives as "Payments " +
	// "worker"; a per-fragment rule cannot tell a real space from a node
	// boundary. Fields collapses both without having to.
	d.Text = truncateRunes(strings.Join(strings.Fields(textBuf.String()), " "), searchTextLimit)
	return d, nil
}

// leafText concatenates the inline text under n with its markup removed, so
// a heading reads "Payments worker" rather than "Payments **worker**".
//
// This is what ast.Node.Text(source) looks like it does. It is not: goldmark
// deprecates that method, and on a block node it returns the raw source span
// — including link syntax and URLs.
func leafText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch t := c.(type) {
		case *ast.Text:
			seg := t.Segment
			b.Write(seg.Value(source))
		case *ast.String:
			b.Write(t.Value)
		}
		return ast.WalkContinue, nil
	})
	return strings.Join(strings.Fields(b.String()), " ")
}

// rewriteRelative hands only repository-relative destinations to the
// rewriter. An absolute URL points outside the site and a fragment points
// inside the current page; rewriting either would break a working link.
func rewriteRelative(dest string, rewrite LinkRewriter) string {
	if dest == "" || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "/") {
		return dest
	}
	// A scheme means it is not ours: https:, mailto:, slack:.
	if i := strings.Index(dest, ":"); i > 0 && !strings.ContainsAny(dest[:i], "/.") {
		return dest
	}
	return rewrite(dest)
}

// appendText accumulates search text, stopping once the cap is reached so a
// 400 KB runbook does not build a 400 KB string to throw away.
//
// The separator keeps two adjacent blocks from running together ("itema").
// Where the fragments already had a space it produces two, which the
// strings.Fields pass in Render collapses.
func appendText(b *strings.Builder, s string) {
	if s == "" || b.Len() > searchTextLimit*4 {
		return
	}
	if b.Len() > 0 {
		b.WriteByte(' ')
	}
	b.WriteString(s)
}

// truncateRunes cuts to at most n runes, never mid-character. Cutting inside
// a multi-byte rune yields invalid UTF-8, which encoding/json escapes to
// U+FFFD — a corrupt index for documentation written with accents.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
