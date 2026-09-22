// Package mdtext answers text-level questions about a Markdown document —
// what it actually says — before any renderer is involved.
//
// It exists because two packages need the same answers and must not disagree.
// internal/render/md strips front matter so the renderer does not print it as
// body text (ruling R55); internal/scorecard asks whether a document says
// anything at all, which it cannot decide without stripping the same block
// first. Two copies of "what is front matter" is how the scorecard comes to
// certify a document the portal renders as empty.
//
// No dependencies, deliberately. The alternative was internal/scorecard
// importing internal/render/md, which inverts the pipeline — stage 7 reaching
// into stage 8 — and drags goldmark into a stage that has no use for it.
package mdtext

import (
	"bytes"
	"strings"
)

// frontMatterDelims are the block delimiters a static-site generator uses,
// with the closers each one accepts. YAML's `---` may also be closed with
// `...`, which is valid YAML document punctuation; TOML's `+++` closes only
// with itself.
var frontMatterDelims = [...]struct {
	open    string
	closers []string
}{
	{"---", []string{"---", "..."}},
	{"+++", []string{"+++"}},
}

// StripFrontMatter removes a leading Hugo/Jekyll front-matter block.
//
// landsraad renders documentation that is also read in the repository, and a
// docs tree written for a static site generator carries a metadata block the
// generator consumes and Markdown does not. In the monorepo that prompted
// ruling R51, 203 of 203 files under the docs tree carried one.
//
// The values are discarded, not used (ruling R55). Feeding `title` into the
// page title would suit a Hugo tree — whose `_index.md` often has no H1 at
// all, because the title lives in the block — but that changes the title of
// every documented page and the row it contributes to the search index. That
// is a visible change to the published artifact and a separate decision from
// stopping the leak.
func StripFrontMatter(source []byte) []byte {
	for _, d := range frontMatterDelims {
		if body, ok := cutFrontMatter(source, d.open, d.closers); ok {
			return body
		}
	}
	return source
}

// cutFrontMatter returns what follows a leading open..close block.
//
// The ambiguity this exists to get right: `---` alone on the first line is
// also a valid thematic break, and `---` on the SECOND line is a setext H1
// underline. So the opening delimiter must be the whole of line one, and a
// closing delimiter must actually appear. An unterminated `---` is a rule and
// is left alone.
//
// That asymmetry is deliberate and is the conservative direction. Failing to
// strip a block renders ugly and is obvious in the output; over-stripping
// silently eats a document's first section, and the reader has no way to tell
// it is missing.
func cutFrontMatter(source []byte, open string, closers []string) ([]byte, bool) {
	lines := bytes.SplitAfter(source, []byte("\n"))
	// A UTF-8 BOM sits BEFORE the opening delimiter, so line one did not
	// compare equal to it and the whole block was declined.
	//
	// That is not cosmetic. The unstripped block stays in the document, so
	// BodyIsEmpty reads its YAML lines as content and a Hugo section stub
	// stops being empty — restoring exactly the false certification ruling
	// R56 removed. Measured: a BOM'd stub scored 100% with docs-fresh
	// passing, against 0% for the same bytes without the BOM.
	//
	// Only line one is checked, which is the only place a BOM may legally
	// appear, and cutFrontMatter is always called with the whole file.
	if len(lines) == 0 || strings.TrimPrefix(lineText(lines[0]), "\ufeff") != open {
		return nil, false
	}
	consumed := len(lines[0])
	for _, l := range lines[1:] {
		consumed += len(l)
		for _, c := range closers {
			if lineText(l) == c {
				return source[consumed:], true
			}
		}
	}
	return nil, false
}

// lineText is one SplitAfter line without its line ending or any trailing
// horizontal whitespace, so a CRLF file reads the same as an LF one and a
// delimiter an editor padded with a space or a tab still matches.
//
// Trailing-space tolerance adds no over-strip risk. A padded `--- ` is still
// only treated as an opener when a closing delimiter actually appears later
// in the file, which is the same ambiguity a bare `---` already carries and
// which the closing-delimiter requirement already decides.
func lineText(l []byte) string {
	return strings.TrimRight(strings.TrimRight(string(l), "\r\n"), " \t")
}

// BodyIsEmpty reports whether a Markdown document says nothing: no content
// beyond front matter, headings, blank lines and HTML comments.
//
// Front matter is stripped first, and that is the whole reason this lives
// beside StripFrontMatter. A Hugo section stub is front matter and nothing
// else, and without stripping it every one of its lines reads as content — so
// a document with no prose in it at all would be certified as documentation.
func BodyIsEmpty(data []byte) bool {
	for _, line := range strings.Split(string(StripFrontMatter(data)), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
		case strings.HasPrefix(t, "<!--"):
		default:
			return false
		}
	}
	return true
}
