// Package md is landsraad's Markdown dialect.
//
// Spec D2 fixes it: goldmark GFM — tables, footnotes, task lists — plus
// Chroma highlighting, Mermaid diagrams, and one custom extension for
// MkDocs-style `!!! note` admonitions. Tabs and snippet-includes are
// deliberately excluded: they break GitHub's rendering, and these files are
// read in the repository as well as in the portal.
//
// Spec §14.1 fixes the safety posture, decided in advance rather than under
// pressure: goldmark runs WITHOUT WithUnsafe, so raw HTML in a runbook is
// escaped rather than injected into a shared portal page.
package md

import (
	"bytes"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
)

// New returns a configured Markdown renderer.
//
// It is a value, not package state. Two portals with two dialects can coexist
// in one process, and a test can build its own — the same reason
// schema.Validator is a value and not a package-level singleton (spec §3.1).
func New() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			// Footnote is NOT part of extension.GFM. Without this line,
			// `note[^1]` renders as the literal text "[^1]" — a feature spec
			// D2 names, dropped in silence.
			extension.Footnote,
			Code{},
		),
		// Heading IDs make a runbook section deep-linkable, which is what an
		// incident channel actually pastes.
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		// Note what is absent: html.WithUnsafe. See the package comment.
	)
}

// ChromaCSS is the stylesheet for the classes the fenced-code renderer emits.
// The site writes it once as assets/chroma.css (ruling R14).
func ChromaCSS() ([]byte, error) {
	s := styles.Get(chromaStyle)
	if s == nil {
		s = styles.Fallback
	}
	var buf bytes.Buffer
	if err := chromahtml.New(chromahtml.WithClasses(true)).WriteCSS(&buf, s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
