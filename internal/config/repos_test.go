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
	got, defaulted := r.LocalPatterns()
	if defaulted {
		t.Error("LocalPatterns() reported a fallback for a file that lists paths")
	}
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
	// Line 2 is where the bad indent is. This assertion used to read `want 1`,
	// because LoadRepos hardcoded Line: 1 while quoting an error that named a
	// different line — the diagnostic pointed away from the problem.
	if got.Line != 2 {
		t.Errorf("Line = %d, want 2 — the line the bad indent is on", got.Line)
	}
	if got.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", got.Severity)
	}
	// A syntax error's wording is the library's and passes through unchanged:
	// it names a YAML construct, not a Go type, so there is nothing to
	// translate.
	want := "cannot parse repos file: yaml: line 2: mapping values are not allowed in this context"
	if got.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", got.Message, want)
	}
	if got.Hint != reposParseHint {
		t.Errorf("Hint\n got: %s\nwant: %s", got.Hint, reposParseHint)
	}
	// Even on a parse failure, LoadRepos must still return a usable value —
	// callers need no nil checks — and that value must fall back to the
	// conventional layout so the rest of the run can still say something
	// useful about the repo, rather than aborting outright.
	if got, _ := r.LocalPatterns(); len(got) != len(DefaultPatterns) {
		t.Errorf("LocalPatterns() after a parse failure = %v, want DefaultPatterns %v", got, DefaultPatterns)
	}
}

func TestLocalPatternsFallsBackToDefaultsWhenEmpty(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos: []\n"), &c)
	if c.HasErrors() {
		t.Fatalf("an empty repos list is not an error: %+v", c.Diagnostics())
	}
	got, defaulted := r.LocalPatterns()
	if !defaulted {
		t.Error("LocalPatterns() fell back to defaults but did not say so")
	}
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

// A yaml.v3 type error names a Go type: "cannot unmarshal !!str `oops` into
// []config.Repo". The person reading it is editing YAML and has never heard of
// config.Repo. Spec §14: assert the exact string.
func TestLoadReposReportsAScalarWhereTheRepoListBelongs(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos: oops\n"), &c)
	if !c.HasErrors() {
		t.Fatal("a scalar where the repo list belongs must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "repos-parse" {
		t.Errorf("Check = %q, want %q", d.Check, "repos-parse")
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	want := `expected a list of repository entries, found a string ("oops")`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Hint != reposParseHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, reposParseHint)
	}
	if got, _ := r.LocalPatterns(); len(got) != len(DefaultPatterns) {
		t.Errorf("LocalPatterns() after a parse failure = %v, want DefaultPatterns %v", got, DefaultPatterns)
	}
}

// The line number matters as much as the wording: this diagnostic used to be
// hardcoded to line 1 while the error it quoted said line 3.
func TestLoadReposReportsTheLineOfAScalarPaths(t *testing.T) {
	var c diag.Collector
	LoadRepos("repos.yaml", []byte("repos:\n  - url: https://x/y\n    paths: \"services/*\"\n"), &c)
	if !c.HasErrors() {
		t.Fatal("a scalar where the paths list belongs must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Line != 3 {
		t.Errorf("Line = %d, want 3 — the line `paths:` is on", d.Line)
	}
	want := `expected a list of strings, found a string ("services/*")`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// Finding 1, the highest-cost defect the pre-merge audit found: `path:` for
// `paths:` silently dropped to DefaultPatterns, which happen to cover
// services/*, so a repo whose services live in apps/ validated one directory,
// reported "no problems found", and exited 0 having never opened the other.
//
// teams.yaml already rejects unknown keys, and its reason (teams.go) is
// strictly stronger here: repos.yaml decides what the tool looks at at all.
func TestLoadReposRejectsAnUnknownKey(t *testing.T) {
	var c diag.Collector
	LoadRepos("repos.yaml", []byte("repos:\n  - url: https://x/y\n    path: [apps/*]\n"), &c)
	if !c.HasErrors() {
		t.Fatal("an unknown key in repos.yaml must be an error, not a silent fallback")
	}
	d := c.Diagnostics()[0]
	if d.Check != "repos-parse" {
		t.Errorf("Check = %q, want %q", d.Check, "repos-parse")
	}
	if d.Line != 3 {
		t.Errorf("Line = %d, want 3 — the line the unknown key is on", d.Line)
	}
	want := `unknown key "path" in repos.yaml`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Hint != reposParseHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, reposParseHint)
	}
}

// Every rejected key is reported in one run, so fixing them is not a
// one-per-rerun crawl.
func TestLoadReposReportsEveryUnknownKey(t *testing.T) {
	var c diag.Collector
	LoadRepos("repos.yaml", []byte("repos:\n  - url: https://x/y\n    path: [a]\n    branch: main\n"), &c)
	if c.Len() != 2 {
		t.Fatalf("expected one diagnostic per rejected key, got %d: %+v", c.Len(), c.Diagnostics())
	}
}

// A repos.yaml that exists but lists no paths used to fall back to
// DefaultPatterns emitting nothing, while an absent repos.yaml announced it.
// Same degraded mode, half of it invisible. The second return value is what
// makes the caller unable to forget.
func TestLocalPatternsSaysWhenItDefaulted(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos:\n  - url: https://x/y\n    paths: []\n"), &c)
	if c.HasErrors() {
		t.Fatalf("an empty paths list is not a parse error: %+v", c.Diagnostics())
	}
	got, defaulted := r.LocalPatterns()
	if !defaulted {
		t.Fatal("LocalPatterns() used DefaultPatterns without reporting it")
	}
	if len(got) != len(DefaultPatterns) {
		t.Errorf("LocalPatterns() = %v, want DefaultPatterns %v", got, DefaultPatterns)
	}
}
