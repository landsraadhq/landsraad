package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
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

// The portal stamps version() into the footer of every page it generates, so
// a wrong answer here is published everywhere. The old version() returned
// bi.Main.Version, which for any build from a checkout is a pseudo-version
// like v0.0.0-20260910182806-2f17ea624567+dirty: the leading v0.0.0 is not a
// release, it is Go's placeholder for "no tag is reachable". Every preview
// footer therefore advertised a version that was never cut.
func TestBuildRevisionNamesTheCommitAndSaysWhenItIsDirty(t *testing.T) {
	const sha = "2f17ea624567bf2fe12b5b2e9710666d05c7b71d"
	for _, tc := range []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{"clean checkout", []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: sha},
			{Key: "vcs.modified", Value: "false"},
		}, "dev-2f17ea624567"},
		{"uncommitted changes", []debug.BuildSetting{
			{Key: "vcs.revision", Value: sha},
			{Key: "vcs.modified", Value: "true"},
		}, "dev-2f17ea624567-dirty"},
		// An installed module carries no VCS stamp; version() then falls
		// through to Main.Version, where a real tag lives.
		{"no vcs stamp", []debug.BuildSetting{{Key: "-buildmode", Value: "exe"}}, ""},
		{"no settings at all", nil, ""},
	} {
		if got := buildRevision(tc.settings); got != tc.want {
			t.Errorf("%s: buildRevision = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// Each way this binary can be built, and what it must call itself. The first
// draft of this test called version() directly and asserted "not v0.0.0",
// which passed without touching the branch it named: a test binary always
// reports Main.Version "(devel)" and carries no VCS stamp, so version() took
// the fallback every time. Taking the build info as a parameter is what makes
// the decision checkable at all.
func TestVersionFromCoversEveryWayThisBinaryIsBuilt(t *testing.T) {
	const sha = "2f17ea624567bf2fe12b5b2e9710666d05c7b71d"
	vcsStamp := []debug.BuildSetting{
		{Key: "vcs.revision", Value: sha},
		{Key: "vcs.modified", Value: "false"},
	}
	for _, tc := range []struct {
		name    string
		stamped string
		bi      *debug.BuildInfo
		want    string
	}{
		{"release, -ldflags -X main.Version", "v1.4.0",
			&debug.BuildInfo{Main: debug.Module{Version: "v1.4.0"}}, "v1.4.0"},
		{"release stamp outranks everything else", "v1.4.0",
			&debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcsStamp}, "v1.4.0"},
		{"go build from a checkout", "dev",
			&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260910182806-2f17ea624567"}, Settings: vcsStamp},
			"dev-2f17ea624567"},
		{"go install ...@v1.2.3 — real tag, no VCS stamp", "dev",
			&debug.BuildInfo{Main: debug.Module{Version: "v1.2.3"}}, "v1.2.3"},
		// The case that was published on every page: a pseudo-version whose
		// v0.0.0 reads as a release and is not one. With no commit to fall
		// back on, "dev" is the only honest answer left.
		{"go install ...@main — pseudo-version, no VCS stamp", "dev",
			&debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20260910182806-2f17ea624567"}}, "dev"},
		{"no build info at all", "dev", nil, "dev"},
	} {
		if got := versionFrom(tc.stamped, tc.bi); got != tc.want {
			t.Errorf("%s: versionFrom = %q, want %q", tc.name, got, tc.want)
		}
	}
}
