package md

import (
	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// KindAdmonition identifies the node type this extension adds.
var KindAdmonition = ast.NewNodeKind("Admonition")

// Admonition is one MkDocs-style callout: `!!! warning "Title"` followed by
// four-space-indented block content.
type Admonition struct {
	ast.BaseBlock
	// Class is the admonition type. It is matched as [a-z]+ and nothing
	// else, because it is written directly into a class= attribute.
	Class string
	Title string
}

func (n *Admonition) Kind() ast.NodeKind { return KindAdmonition }

func (n *Admonition) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{
		"Class": n.Class, "Title": n.Title,
	}, nil)
}

// openRE matches `!!! note` and `!!! note "A title"`.
//
// [a-z]+ is a security boundary, not a convenience: Class reaches a class=
// attribute, and a permissive pattern would make `!!! x" onload="…` a stored
// XSS on a shared internal portal. Widening this needs escaping at the
// renderer instead — do not widen it without adding that.
var openRE = regexp.MustCompile(`^!!!\s+([a-z]+)\s*(?:"([^"]*)")?\s*$`)

type admonitionParser struct{}

func (admonitionParser) Trigger() []byte { return []byte{'!'} }

func (admonitionParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, _ := reader.PeekLine()
	m := openRE.FindSubmatch(util.TrimRightSpace(line))
	if m == nil {
		return nil, parser.NoChildren
	}
	reader.AdvanceLine()
	return &Admonition{Class: string(m[1]), Title: string(m[2])}, parser.HasChildren
}

func (admonitionParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, _ := reader.PeekLine()
	// A blank line does not close the block: MkDocs admonitions routinely
	// hold several paragraphs, and the opener is followed by one by
	// convention.
	if util.IsBlank(line) {
		return parser.Continue | parser.HasChildren
	}
	pos, padding := util.IndentPosition(line, reader.LineOffset(), 4)
	if pos < 0 {
		return parser.Close
	}
	reader.AdvanceAndSetPadding(pos, padding)
	return parser.Continue | parser.HasChildren
}

func (admonitionParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

// CanInterruptParagraph: authors do not reliably leave a blank line before
// the opener, and silently rendering `!!! warning` as body text is the kind
// of quiet wrong answer this project exists to avoid.
func (admonitionParser) CanInterruptParagraph() bool { return true }

// CanAcceptIndentedLine: false — an indented line belongs to the enclosing
// block, not to a new admonition.
func (admonitionParser) CanAcceptIndentedLine() bool { return false }

type admonitionRenderer struct{}

func (r admonitionRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(KindAdmonition, r.render)
}

func (admonitionRenderer) render(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	a := n.(*Admonition)
	if !entering {
		w.WriteString("</div>\n")
		return ast.WalkContinue, nil
	}
	w.WriteString(`<div class="admonition `)
	// Class is [a-z]+ by construction; escaping it would be theatre. The
	// title is arbitrary text and is escaped.
	w.WriteString(a.Class)
	w.WriteString(`">`)
	title := a.Title
	if title == "" {
		title = a.Class
	}
	w.WriteString(`<p class="admonition-title">`)
	w.Write(util.EscapeHTML([]byte(title)))
	w.WriteString("</p>\n")
	return ast.WalkContinue, nil
}

// Admonitions is spec D2's "one custom extension for MkDocs-style `!!! note`".
//
// It exists because runbooks genuinely use callouts, and because the syntax
// keeps these files paste-compatible with documentation written for MkDocs —
// which matters when the same file is read in the repository and in the
// portal.
type Admonitions struct{}

func (Admonitions) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(parser.WithBlockParsers(
		// 799 — ahead of goldmark's paragraph parser so the opener can
		// interrupt a paragraph, behind the fenced-code and list parsers so
		// `!!!` inside a code block stays literal.
		util.Prioritized(admonitionParser{}, 799),
	))
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(admonitionRenderer{}, 500),
	))
}
