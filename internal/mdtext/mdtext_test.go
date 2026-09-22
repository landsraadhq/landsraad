package mdtext

import "testing"

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
