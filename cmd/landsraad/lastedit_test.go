package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/landsraadhq/landsraad/internal/fetch"
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

// The local repository is answered by git, never by a fetcher: it is what
// the user is editing, and its history is on disk. openRepos never gives
// the local repository a fetcher; this one is here so that asking it would
// show.
func TestMultiLastEditAsksGitForTheLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	when := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	// git's internal date format, "<unix seconds> <offset>": no parsing, no
	// timezone left to the machine running the test.
	stamp := fmt.Sprintf("%d +0000", when.Unix())
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
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

	w := &workspace{
		local:    "platform",
		fetchers: map[string]fetch.Fetcher{"platform": fixedEditFetcher{t: time.Unix(0, 0).UTC(), known: true}},
		edits:    newLastEditLog(),
	}
	got, ok := multiLastEdit(context.Background(), root, w)("platform", "docs")
	if !ok || !got.Equal(when) {
		t.Errorf("LastEdit(platform, docs) = %v, %v; want %v, true — from git", got, ok, when)
	}
}
