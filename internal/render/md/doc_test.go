package md

import (
	"strings"
	"testing"
)

func TestRenderReturnsTheFirstH1AsTheTitle(t *testing.T) {
	d, err := Render(New(), []byte("# Payments worker\n\nBody.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "Payments worker" {
		t.Errorf("Title = %q, want %q", d.Title, "Payments worker")
	}
}

// A document with no H1 has no title. The caller decides what to show
// instead — inventing one here would be a silent fallback.
func TestRenderReportsNoTitleWhenThereIsNoH1(t *testing.T) {
	d, err := Render(New(), []byte("## Only an H2\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "" {
		t.Errorf("Title = %q, want the empty string", d.Title)
	}
}

func TestRenderCollectsHeadingsWithTheirIDs(t *testing.T) {
	src := "# Runbook\n\n## Rollback procedure\n\n### Step one\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	want := []Heading{
		{Level: 1, Text: "Runbook", ID: "runbook"},
		{Level: 2, Text: "Rollback procedure", ID: "rollback-procedure"},
		{Level: 3, Text: "Step one", ID: "step-one"},
	}
	if len(d.Headings) != len(want) {
		t.Fatalf("got %d headings, want %d: %+v", len(d.Headings), len(want), d.Headings)
	}
	for i := range want {
		if d.Headings[i] != want[i] {
			t.Errorf("heading %d = %+v, want %+v", i, d.Headings[i], want[i])
		}
	}
}

func TestRenderCollectsSearchText(t *testing.T) {
	d, err := Render(New(), []byte("# Title\n\nFirst para.\n\nSecond para.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"Title", "First para.", "Second para."} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("search text is missing %q, got %q", want, d.Text)
		}
	}
}

// ast.Node.Text(source) returns the RAW SOURCE SPAN for a block node — and
// is deprecated besides. Using it here would put "[the runbook](runbook.md)"
// into the index, so every search would match on URLs and bracket syntax.
func TestSearchTextHasNoMarkupAndNoURLs(t *testing.T) {
	src := "# Payments **worker**\n\nSee [the runbook](runbook.md) and `code`.\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, unwanted := range []string{"[", "]", "(", "runbook.md", "**"} {
		if strings.Contains(d.Text, unwanted) {
			t.Errorf("search text contains markup %q: %q", unwanted, d.Text)
		}
	}
	if !strings.Contains(d.Text, "the runbook") {
		t.Errorf("link text must be indexed, got %q", d.Text)
	}
}

// List items and table cells come free with the leaf walk. Fenced code does
// not: a code block is not ast.Text, and indexing it would swamp prose.
func TestSearchTextCoversListsAndTablesButNotCode(t *testing.T) {
	src := "- list item\n\n| a | b |\n|---|---|\n| cell1 | cell2 |\n\n```go\nfunc secretHelper() {}\n```\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"list item", "cell1", "cell2"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("search text is missing %q, got %q", want, d.Text)
		}
	}
	if strings.Contains(d.Text, "secretHelper") {
		t.Errorf("fenced code must not be indexed, got %q", d.Text)
	}
}

// The leaf walk emits one fragment per inline node, so `Payments **worker**`
// arrives as "Payments " + "worker". Whitespace is normalised once at the
// end; per-fragment normalisation cannot tell a real space from a boundary.
func TestSearchTextHasNoDoubledSpaces(t *testing.T) {
	d, err := Render(New(), []byte("# Payments **worker**\n\nFirst **bold** para.\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(d.Text, "  ") {
		t.Errorf("search text has a doubled space: %q", d.Text)
	}
}

// Emphasis in a heading must not leak into the title shown on the page.
func TestTitleStripsInlineMarkup(t *testing.T) {
	d, err := Render(New(), []byte("# Payments **worker**\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "Payments worker" {
		t.Errorf("Title = %q, want %q", d.Title, "Payments worker")
	}
}

// Ruling R17. A forty-service monorepo with long runbooks would otherwise
// ship a multi-megabyte index to every visitor.
func TestSearchTextIsCappedAtTwoThousandRunes(t *testing.T) {
	src := "# T\n\n" + strings.Repeat("word ", 2000)
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if n := len([]rune(d.Text)); n > 2000 {
		t.Errorf("search text is %d runes, want at most 2000", n)
	}
}

// Truncating mid-character produces invalid UTF-8, which encoding/json turns
// into U+FFFD — a corrupt index for anyone writing docs with accents.
func TestSearchTextTruncatesOnARuneBoundary(t *testing.T) {
	src := "# T\n\n" + strings.Repeat("é", 3000)
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.ContainsRune(d.Text, '�') {
		t.Error("search text was cut mid-character")
	}
}

func TestRenderRewritesRelativeMarkdownLinks(t *testing.T) {
	rewrite := func(dest string) string {
		if strings.HasSuffix(dest, ".md") {
			return strings.TrimSuffix(dest, ".md") + ".html"
		}
		return dest
	}
	d, err := Render(New(), []byte("See [the runbook](runbook.md) and [ops](../ops/index.md).\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="runbook.html"`) {
		t.Errorf("sibling link not rewritten:\n%s", d.HTML)
	}
	if !strings.Contains(string(d.HTML), `href="../ops/index.html"`) {
		t.Errorf("parent-relative link not rewritten:\n%s", d.HTML)
	}
}

func TestRenderLeavesAbsoluteLinksAlone(t *testing.T) {
	rewrite := func(dest string) string { return dest + "?rewritten" }
	d, err := Render(New(), []byte("[grafana](https://grafana/d/pay)\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(string(d.HTML), "?rewritten") {
		t.Errorf("an absolute URL must not be handed to the rewriter:\n%s", d.HTML)
	}
}

func TestRenderLeavesFragmentLinksAlone(t *testing.T) {
	rewrite := func(dest string) string { return "REWRITTEN" }
	d, err := Render(New(), []byte("[jump](#rollback)\n"), rewrite)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="#rollback"`) {
		t.Errorf("an in-page anchor must not be rewritten:\n%s", d.HTML)
	}
}

// A nil rewriter is the ordinary case for a runbook rendered on its own.
func TestRenderAcceptsANilRewriter(t *testing.T) {
	d, err := Render(New(), []byte("[x](y.md)\n"), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(string(d.HTML), `href="y.md"`) {
		t.Errorf("a nil rewriter leaves links untouched:\n%s", d.HTML)
	}
}
