package md

import (
	"strings"
	"testing"
)

const hugoIndex = `---
title: "Resort Service"
description: "Details about the resort service"
lead: ""
date: 2024-04-03T09:00:00+00:00
draft: false
weight: 10020
---

The real overview prose starts here.
`

// Ruling R51 made this urgent rather than creating it. landsraad never
// stripped front matter, so every documented page rendered it as body text —
// in the monorepo that found it, 203 of 203 files under docs/content carry a
// block. While _index.md was not hoisted the damage stayed on secondary doc
// pages; hoisting it put the dump at the top of the entity page, which is the
// most-read page landsraad produces.
func TestRenderStripsYAMLFrontMatter(t *testing.T) {
	d, err := Render(New(), []byte(hugoIndex), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, leaked := range []string{"title:", "description:", "draft:", "weight:", "2024-04-03"} {
		if strings.Contains(d.Text, leaked) {
			t.Errorf("front matter %q leaked into the rendered text:\n%s", leaked, d.Text)
		}
	}
	if !strings.Contains(d.Text, "The real overview prose starts here.") {
		t.Errorf("the body must survive stripping:\n%s", d.Text)
	}
}

// Hugo's TOML spelling. Same block, same job, different delimiter.
func TestRenderStripsTOMLFrontMatter(t *testing.T) {
	src := "+++\ntitle = \"Resort Service\"\nweight = 10020\n+++\n\nBody text.\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(d.Text, "title =") || strings.Contains(d.Text, "weight =") {
		t.Errorf("TOML front matter leaked:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "Body text.") {
		t.Errorf("the body must survive stripping:\n%s", d.Text)
	}
}

// The ambiguity this has to get right: `---` on line one is also a valid
// thematic break. Front matter requires a CLOSING delimiter, so a document
// that merely opens with a rule is left alone rather than having its first
// section eaten.
func TestRenderLeavesALeadingThematicBreakAlone(t *testing.T) {
	src := "---\n\n# Heading\n\nBody.\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if d.Title != "Heading" {
		t.Errorf("Title = %q, want %q — an unterminated --- is a rule, not front matter", d.Title, "Heading")
	}
	if !strings.Contains(d.Text, "Body.") {
		t.Errorf("body missing:\n%s", d.Text)
	}
}

// A setext H1 underlines its text with ---, so the delimiter is on line two.
// Requiring line one to be exactly the delimiter keeps that case safe.
func TestRenderLeavesASetextHeadingAlone(t *testing.T) {
	src := "Resort Service\n---\n\nBody.\n"
	d, err := Render(New(), []byte(src), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(d.Text, "Resort Service") {
		t.Errorf("a setext heading must survive:\n%s", d.Text)
	}
}
