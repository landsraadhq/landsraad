package config

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestLoadReposParsesEntries(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte(
		"repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*, topics/*]\n"), &c)
	if c.HasErrors() {
		t.Fatalf("valid repos.yaml must load: %+v", c.Diagnostics())
	}
	if len(r.Repos) != 1 || r.Repos[0].URL != "https://github.com/org/monorepo" {
		t.Fatalf("Repos not read: %+v", r.Repos)
	}
	got := r.LocalPatterns()
	want := []string{"services/*", "topics/*"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("LocalPatterns() = %v, want %v", got, want)
	}
}

// Spec §14: assert the exact string. This is also the regression test for
// one of the three exit-0-on-an-unexamined-repo defects: a malformed
// repos.yaml must produce a loud diagnostic, never a silent fallback.
func TestLoadReposReportsMalformedYAML(t *testing.T) {
	var c diag.Collector
	// The same malformed indentation already proven (in catalog/parse_test.go)
	// to produce a deterministic yaml.v3 error string.
	r := LoadRepos("repos.yaml", []byte("kind: Service\n  bad: indent\n"), &c)
	if !c.HasErrors() {
		t.Fatal("malformed repos.yaml must be an error, not a silent fallback")
	}
	got := c.Diagnostics()[0]
	if got.Check != "repos-parse" {
		t.Errorf("Check = %q, want %q", got.Check, "repos-parse")
	}
	if got.File != "repos.yaml" {
		t.Errorf("File = %q, want %q", got.File, "repos.yaml")
	}
	if got.Line != 1 {
		t.Errorf("Line = %d, want 1", got.Line)
	}
	if got.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", got.Severity)
	}
	want := "cannot parse repos file: yaml: line 2: mapping values are not allowed in this context"
	if got.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", got.Message, want)
	}
	// Even on a parse failure, LoadRepos must still return a usable value —
	// callers need no nil checks — and that value must fall back to the
	// conventional layout so the rest of the run can still say something
	// useful about the repo, rather than aborting outright.
	if got := r.LocalPatterns(); len(got) != len(DefaultPatterns) {
		t.Errorf("LocalPatterns() after a parse failure = %v, want DefaultPatterns %v", got, DefaultPatterns)
	}
}

func TestLocalPatternsFallsBackToDefaultsWhenEmpty(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos: []\n"), &c)
	if c.HasErrors() {
		t.Fatalf("an empty repos list is not an error: %+v", c.Diagnostics())
	}
	got := r.LocalPatterns()
	if len(got) != len(DefaultPatterns) {
		t.Fatalf("LocalPatterns() = %v, want DefaultPatterns %v", got, DefaultPatterns)
	}
	for i := range DefaultPatterns {
		if got[i] != DefaultPatterns[i] {
			t.Errorf("LocalPatterns()[%d] = %q, want %q", i, got[i], DefaultPatterns[i])
		}
	}
}

// Regression: "." missing from DefaultPatterns means a single-service repo
// with service.yaml at its root is invisible to `validate` and it exits 0
// having examined nothing.
func TestDefaultPatternsIncludesRoot(t *testing.T) {
	if len(DefaultPatterns) == 0 || DefaultPatterns[0] != "." {
		t.Errorf("DefaultPatterns = %v, want it to start with \".\" so a root-level service.yaml is found", DefaultPatterns)
	}
}
