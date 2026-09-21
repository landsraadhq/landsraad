package md

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

// stripFrontMatter removes a leading Hugo/Jekyll front-matter block.
//
// landsraad renders documentation that is also read in the repository, and a
// docs tree written for a static site generator carries a metadata block the
// generator consumes and Markdown does not. Left in place it renders as body
// text: in the monorepo that prompted ruling R51, 203 of 203 files under the
// docs tree carried one.
//
// R51 made this urgent rather than creating it. While `_index.md` was not
// recognised as an index the damage stayed on secondary documentation pages;
// hoisting it put the block at the top of the entity page, which is the
// most-read page landsraad produces.
//
// The values are discarded, not used. Feeding `title` into the page title
// would suit a Hugo tree — whose `_index.md` often has no H1 at all, because
// the title lives in the block — but that changes the title of every
// documented page and the row it contributes to the search index. That is a
// visible change to the published artifact and a separate decision from
// stopping the leak.
func stripFrontMatter(source []byte) []byte {
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
	if len(lines) == 0 || lineText(lines[0]) != open {
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

// lineText is one SplitAfter line without its line ending, so a CRLF file is
// read the same as an LF one.
func lineText(l []byte) string {
	return strings.TrimRight(string(l), "\r\n")
}
