package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRootWalksUp(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "services", "payments-worker", "docs")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "teams.yaml"), []byte("teams: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findRoot(deep)
	if err != nil {
		t.Fatalf("findRoot: %v", err)
	}
	want, _ := filepath.EvalSymlinks(base)
	gotEval, _ := filepath.EvalSymlinks(got)
	if gotEval != want {
		t.Errorf("findRoot(%q) = %q, want %q — running from a subdirectory is "+
			"the normal case and must work", deep, gotEval, want)
	}
}

// A typo'd subdirectory of a perfectly valid repo (e.g. `serivces/api`) must
// not resolve upward to the repo's real root and validate that instead — the
// whole point of the argument is to name which directory to look at, and
// silently substituting an ancestor produces a clean pass for a path that
// was never actually inspected.
func TestFindRootRejectsANonexistentSubdirectory(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "teams.yaml"), []byte("teams: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	typo := filepath.Join(base, "serivces", "api") // does not exist

	got, err := findRoot(typo)
	if err == nil {
		t.Fatalf("findRoot(%q) = %q, <nil> — a nonexistent path must be an "+
			"error, not silently resolve to an ancestor's root", typo, got)
	}
	want := fmt.Sprintf("%s is not a directory", typo)
	if err.Error() != want {
		t.Errorf("findRoot(%q) error = %q, want %q", typo, err.Error(), want)
	}
}

func TestFindRootFailsWithAClearMessage(t *testing.T) {
	_, err := findRoot(t.TempDir())
	if err == nil {
		t.Fatal("a directory with no markers above it must be an error")
	}
	if !strings.Contains(err.Error(), "repos.yaml") {
		t.Errorf("the error must say what it looked for, got %q", err)
	}
}

func TestVersionFallsBackToTheConstant(t *testing.T) {
	if version() == "" {
		t.Error("version() must never be empty — every bug report quotes it")
	}
}
