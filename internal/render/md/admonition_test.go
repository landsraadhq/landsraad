package md

import (
	"strings"
	"testing"
)

func TestAdmonitionWithATitle(t *testing.T) {
	got := render(t, "!!! warning \"Page the owner first\"\n\n    Do **not** restart.\n")
	want := "<div class=\"admonition warning\">" +
		"<p class=\"admonition-title\">Page the owner first</p>\n" +
		"<p>Do <strong>not</strong> restart.</p>\n" +
		"</div>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

// MkDocs uses the type as the title when none is given.
func TestAdmonitionWithoutATitleUsesItsClass(t *testing.T) {
	got := render(t, "!!! note\n\n    Body text.\n")
	want := "<div class=\"admonition note\">" +
		"<p class=\"admonition-title\">note</p>\n" +
		"<p>Body text.</p>\n" +
		"</div>\n"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAdmonitionHoldsMultipleBlocks(t *testing.T) {
	got := render(t, "!!! danger\n\n    First para.\n\n    Second para.\n\n    - a list item\n")
	for _, want := range []string{"<p>First para.</p>", "<p>Second para.</p>", "<li>a list item</li>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestAdmonitionClosesAtTheFirstUnindentedLine(t *testing.T) {
	got := render(t, "!!! note\n\n    Inside.\n\nOutside.\n")
	if !strings.Contains(got, "</div>\n<p>Outside.</p>") {
		t.Errorf("the unindented paragraph must fall outside the admonition:\n%s", got)
	}
}

// The class reaches a class= attribute, so the opener matches [a-z]+ and
// nothing else. Anything else is not an admonition and stays a paragraph.
func TestAnUnsafeTypeIsNotAnAdmonition(t *testing.T) {
	got := render(t, "!!! x\" onload=\"alert(1)\n\n    body\n")
	if strings.Contains(got, "onload") && strings.Contains(got, "class=\"admonition") {
		t.Errorf("an unsanitised class reached the output:\n%s", got)
	}
	if !strings.Contains(got, "<p>") {
		t.Errorf("a non-matching opener stays ordinary text, got:\n%s", got)
	}
}

func TestAdmonitionTitleIsEscaped(t *testing.T) {
	got := render(t, "!!! note \"A <b>bold</b> title\"\n\n    body\n")
	if strings.Contains(got, "<b>bold</b>") {
		t.Errorf("the title must be escaped:\n%s", got)
	}
	if !strings.Contains(got, "&lt;b&gt;bold&lt;/b&gt;") {
		t.Errorf("expected an escaped title, got:\n%s", got)
	}
}

func TestThreeBangsAloneAreNotAnAdmonition(t *testing.T) {
	got := render(t, "!!! \n\nplain\n")
	if strings.Contains(got, "admonition") {
		t.Errorf("an opener with no type is not an admonition:\n%s", got)
	}
}

// A paragraph must be interruptible: MkDocs authors do not reliably leave a
// blank line before the opener.
func TestAdmonitionInterruptsAParagraph(t *testing.T) {
	got := render(t, "Some text.\n!!! note\n\n    Inside.\n")
	if !strings.Contains(got, `<div class="admonition note">`) {
		t.Errorf("the opener must interrupt a paragraph:\n%s", got)
	}
}
