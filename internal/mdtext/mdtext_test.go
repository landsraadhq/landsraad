package mdtext

import (
	"strings"
	"testing"
)

func TestBodyIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
		want bool
	}{
		// The case ruling R56 exists for: a Hugo section stub. Without
		// stripping, every one of these lines reads as content.
		{"front matter only", "---\ntitle: \"API\"\nweight: 10\n---\n", true},
		{"front matter and heading", "---\ntitle: \"API\"\n---\n\n# API\n", true},
		{"heading only", "# API\n", true},
		{"empty", "", true},
		{"html comment only", "<!-- TODO -->\n", true},
		{"front matter then prose", "---\ntitle: \"API\"\n---\n\nThe overview.\n", false},
		{"prose only", "The overview.\n", false},
		// An unterminated --- is a thematic break, so it is content and the
		// lines after it are too.
		{"unterminated delimiter", "---\n\n# API\n\nProse.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := BodyIsEmpty([]byte(tc.src)); got != tc.want {
				t.Errorf("BodyIsEmpty(%q) = %v, want %v", tc.src, got, tc.want)
			}
		})
	}
}

// A setext H1 underlines its text with ---, so the delimiter is on line two.
// Requiring line one to be exactly the delimiter keeps that document intact.
func TestStripFrontMatterLeavesASetextHeadingAlone(t *testing.T) {
	src := []byte("Resort Service\n---\n\nBody.\n")
	if got := string(StripFrontMatter(src)); got != string(src) {
		t.Errorf("StripFrontMatter mangled a setext heading:\n got: %q\nwant: %q", got, src)
	}
}

// CRLF must read the same as LF, or a file authored on Windows keeps its
// front matter and renders it as body text.
func TestStripFrontMatterHandlesCRLF(t *testing.T) {
	src := []byte("---\r\ntitle: \"API\"\r\n---\r\n\r\nThe overview.\r\n")
	got := string(StripFrontMatter(src))
	if got != "\r\nThe overview.\r\n" {
		t.Errorf("StripFrontMatter with CRLF = %q", got)
	}
}

// A UTF-8 BOM before the opening delimiter, and trailing horizontal
// whitespace after it, both made lineText compare unequal to the delimiter so
// cutFrontMatter declined the whole block.
//
// That is not cosmetic. An unstripped block stays in the document, so
// BodyIsEmpty reads its YAML lines as content and a Hugo section stub stops
// being empty — which restores exactly the false certification ruling R56
// exists to prevent. Measured: a BOM'd stub scored 100% with docs-fresh
// passing, against 0% for the same file without the BOM.
func TestStripFrontMatterToleratesABOMAndTrailingSpace(t *testing.T) {
	const bom = "\ufeff"
	for _, tc := range []struct{ name, src string }{
		{"bom before the opener", bom + "---\ntitle: \"API\"\n---\n\nThe overview.\n"},
		{"trailing space on the opener", "--- \ntitle: \"API\"\n---\n\nThe overview.\n"},
		{"trailing space on the closer", "---\ntitle: \"API\"\n--- \n\nThe overview.\n"},
		{"tab after the opener", "---\t\ntitle: \"API\"\n---\n\nThe overview.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(StripFrontMatter([]byte(tc.src)))
			if strings.Contains(got, "title:") {
				t.Errorf("front matter not stripped:\n%q", got)
			}
			if !strings.Contains(got, "The overview.") {
				t.Errorf("body lost:\n%q", got)
			}
		})
	}
}

// The consequence that makes the above a scoring bug rather than an ugly page.
func TestBodyIsEmptySeesThroughABOMedStub(t *testing.T) {
	const bom = "\ufeff"
	stub := bom + "---\ntitle: \"API\"\nweight: 10\n---\n"
	if !BodyIsEmpty([]byte(stub)) {
		t.Error("a section stub with a BOM is still a section stub; reporting it as content " +
			"restores the false certification R56 removed")
	}
}
