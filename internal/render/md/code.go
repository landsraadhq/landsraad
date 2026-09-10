package md

import (
	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// chromaStyle is the highlighting theme. One name, in one place, so the
// stylesheet ChromaCSS writes and the classes codeRenderer emits cannot drift.
const chromaStyle = "github"

// Code is the fenced-code-block extension.
//
// It exists instead of goldmark-highlighting/v2 for two reasons. That module
// has no tagged release — only a 2023 pseudo-version — and it offers no clean
// way for one language to bypass the highlighter, which is exactly what a
// ```mermaid fence needs: Mermaid source is not code to colour, it is a
// diagram the browser lays out (spec §10).
type Code struct{}

func (Code) Extend(m goldmark.Markdown) {
	// Priority 1 — ahead of goldmark's own fenced-code renderer, which is
	// registered at 100. The lower number wins.
	m.Renderer().AddOptions(renderer.WithNodeRenderers(
		util.Prioritized(newCodeRenderer(), 1),
	))
}

type codeRenderer struct {
	style     *chroma.Style
	formatter *chromahtml.Formatter
}

func newCodeRenderer() *codeRenderer {
	s := styles.Get(chromaStyle)
	if s == nil {
		// Chroma returns nil for an unknown name rather than an error. A typo
		// in the constant above would otherwise produce an uncoloured portal
		// and no complaint; the fallback style is at least legible.
		s = styles.Fallback
	}
	return &codeRenderer{
		style: s,
		// WithClasses: tokens carry class names and the stylesheet ships once
		// as assets/chroma.css, rather than an inline style= on every span
		// (ruling R14).
		formatter: chromahtml.New(chromahtml.WithClasses(true)),
	}
}

func (r *codeRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(ast.KindFencedCodeBlock, r.renderFenced)
}

func (r *codeRenderer) renderFenced(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.FencedCodeBlock)
	lang := string(n.Language(source))
	code := fencedLines(n, source)

	// Mermaid: hand the source to the browser, escaped. The client calls
	// mermaid.initialize with securityLevel "strict" (spec §14.1).
	if lang == "mermaid" {
		w.WriteString(`<pre class="mermaid">`)
		w.Write(util.EscapeHTML(code))
		w.WriteString("</pre>\n")
		return ast.WalkSkipChildren, nil
	}

	lexer := lexers.Get(lang)
	if lexer == nil {
		return plainCode(w, code)
	}
	it, err := chroma.Coalesce(lexer).Tokenise(nil, string(code))
	if err != nil {
		// The content is more important than the colours. A lexer that chokes
		// on a fragment must not lose the fragment.
		return plainCode(w, code)
	}
	if err := r.formatter.Format(w, r.style, it); err != nil {
		return ast.WalkStop, err
	}
	return ast.WalkSkipChildren, nil
}

// plainCode is the fallback: escaped, unstyled, still readable (ruling R15).
func plainCode(w util.BufWriter, code []byte) (ast.WalkStatus, error) {
	w.WriteString("<pre><code>")
	w.Write(util.EscapeHTML(code))
	w.WriteString("</code></pre>\n")
	return ast.WalkSkipChildren, nil
}

// fencedLines joins a block's line segments back into its source bytes.
//
// text.Segment.Value has a POINTER receiver, so the obvious
// `l.At(i).Value(source)` does not compile. The local variable is required.
func fencedLines(n ast.Node, source []byte) []byte {
	var out []byte
	l := n.Lines()
	for i := 0; i < l.Len(); i++ {
		seg := l.At(i)
		out = append(out, seg.Value(source)...)
	}
	return out
}
