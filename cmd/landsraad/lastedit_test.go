package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// This test needs a real git repository, which is why it lives in cmd/ — the
// rule that keeps tests off disk applies to internal/, and this code exists
// precisely to be the boundary where that stops being possible.
func TestGitLastEditReadsRealHistory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "index.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "docs/index.md")
	runGit("commit", "-qm", "add docs")

	got, ok := gitLastEdit(root)("", "docs")
	if !ok {
		t.Fatal("a committed path must have a last-edit date")
	}
	if time.Since(got) > time.Hour {
		t.Errorf("last edit = %v, want approximately now", got)
	}
}

// The honest answer for a path git knows nothing about is "unknown", never a
// date. A date we do not have silently passes docs-fresh.
func TestGitLastEditReportsUnknownOutsideARepository(t *testing.T) {
	if _, ok := gitLastEdit(t.TempDir())("", "docs"); ok {
		t.Error("a directory that is not a git repository has no history")
	}
}

func TestNoLastEditAlwaysReportsUnknown(t *testing.T) {
	if _, ok := noLastEdit()("", "anything"); ok {
		t.Error("noLastEdit must never claim to know a date")
	}
}
