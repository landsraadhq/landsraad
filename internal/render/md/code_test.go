package md

import (
	"bytes"
	"strings"
	"testing"
)

// render is the shared helper for every test in this package.
func render(t *testing.T, source string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := New().Convert([]byte(source), &buf); err != nil {
		t.Fatalf("Convert: %v", err)
	}
	return buf.String()
}

func TestMermaidFenceIsPassedThroughToTheBrowser(t *testing.T) {
	got := render(t, "```mermaid\ngraph LR\n  a --> b\n```\n")
	want := "<pre class=\"mermaid\">graph LR\n  a --&gt; b\n</pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAKnownLanguageIsHighlightedWithClasses(t *testing.T) {
	got := render(t, "```go\nfunc main() {}\n```\n")
	if !strings.Contains(got, `<pre class="chroma">`) {
		t.Errorf("no chroma wrapper in:\n%s", got)
	}
	if !strings.Contains(got, `class="kd"`) {
		t.Errorf("no keyword class for `func` in:\n%s", got)
	}
	// WithClasses(true): tokens carry classes, never inline styles. The
	// stylesheet ships once as assets/chroma.css (ruling R14).
	if strings.Contains(got, "style=") {
		t.Errorf("inline styles must not appear, chroma is configured WithClasses:\n%s", got)
	}
}

// Ruling R15: an unknown language is not a metadata problem. Chroma has ~250
// lexers and a runbook naming one it lacks must still show its content.
func TestAnUnknownLanguageFallsBackToEscapedPlainText(t *testing.T) {
	got := render(t, "```notalanguage\n<raw & stuff>\n```\n")
	want := "<pre><code>&lt;raw &amp; stuff&gt;\n</code></pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAFenceWithNoLanguageIsEscapedPlainText(t *testing.T) {
	got := render(t, "```\njust text\n```\n")
	want := "<pre><code>just text\n</code></pre>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// Spec §14.1, decided in advance rather than under pressure: goldmark runs
// WITHOUT WithUnsafe, so raw HTML in somebody's runbook is escaped rather than
// injected into a shared portal page. Deleting this test re-opens the hole.
func TestRawHTMLIsNeverInjected(t *testing.T) {
	got := render(t, "<script>alert(1)</script>\n\nInline <b>html</b> too.\n")
	if strings.Contains(got, "<script>") {
		t.Errorf("raw HTML reached the output — WithUnsafe must never be set:\n%s", got)
	}
	if strings.Contains(got, "<b>html</b>") {
		t.Errorf("raw inline HTML reached the output:\n%s", got)
	}
}

func TestGFMTablesRender(t *testing.T) {
	got := render(t, "| a | b |\n|---|---|\n| 1 | 2 |\n")
	if !strings.Contains(got, "<table>") {
		t.Errorf("GFM tables must render, got:\n%s", got)
	}
}

// Spec D2 names footnotes. extension.GFM does NOT include them: with GFM
// alone this renders the literal text "[^1]", silently dropping the feature.
func TestFootnotesRender(t *testing.T) {
	got := render(t, "Text with a note[^1].\n\n[^1]: The note body.\n")
	if !strings.Contains(got, `class="footnote-ref"`) {
		t.Errorf("footnotes need extension.Footnote alongside extension.GFM, got:\n%s", got)
	}
}

func TestTaskListsRender(t *testing.T) {
	got := render(t, "- [ ] todo\n- [x] done\n")
	if !strings.Contains(got, `type="checkbox"`) {
		t.Errorf("GFM task lists must render, got:\n%s", got)
	}
}

func TestHeadingsGetIDsForDeepLinking(t *testing.T) {
	got := render(t, "## Rollback procedure\n")
	if !strings.Contains(got, `id="rollback-procedure"`) {
		t.Errorf("parser.WithAutoHeadingID must be set, got:\n%s", got)
	}
}

func TestChromaCSSCoversTheClassesTheRendererEmits(t *testing.T) {
	css, err := ChromaCSS()
	if err != nil {
		t.Fatalf("ChromaCSS: %v", err)
	}
	for _, sel := range []string{".chroma", ".chroma .kd"} {
		if !strings.Contains(string(css), sel) {
			t.Errorf("stylesheet is missing %q", sel)
		}
	}
}

// The goldmark value is constructed, never shared. Two portals with two
// configurations must be able to coexist in one process, which is the whole
// reason the project forbids package-level state below cmd/.
func TestNewReturnsAFreshValueEachTime(t *testing.T) {
	if New() == New() {
		t.Error("New must return a fresh value, not a package-level singleton")
	}
}
