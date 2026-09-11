package config

import (
	"slices"
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
	got, why := r.LocalPatterns()
	if why != LocalMarked {
		t.Errorf("LocalPatterns() why = %v, want LocalMarked — a file that lists paths is not a fallback", why)
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
	if got, _ := r.LocalPatterns(); len(got) != len(DefaultPatterns()) {
		t.Errorf("LocalPatterns() after a parse failure = %v, want DefaultPatterns %v", got, DefaultPatterns())
	}
}

func TestLocalPatternsFallsBackToDefaultsWhenEmpty(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos: []\n"), &c)
	if c.HasErrors() {
		t.Fatalf("an empty repos list is not an error: %+v", c.Diagnostics())
	}
	got, why := r.LocalPatterns()
	if why != LocalDefaulted {
		t.Errorf("LocalPatterns() why = %v, want LocalDefaulted", why)
	}
	if len(got) != len(DefaultPatterns()) {
		t.Fatalf("LocalPatterns() = %v, want DefaultPatterns %v", got, DefaultPatterns())
	}
	for i := range DefaultPatterns() {
		if got[i] != DefaultPatterns()[i] {
			t.Errorf("LocalPatterns()[%d] = %q, want %q", i, got[i], DefaultPatterns()[i])
		}
	}
}

// Regression: "." missing from DefaultPatterns means a single-service repo
// with service.yaml at its root is invisible to `validate` and it exits 0
// having examined nothing.
func TestDefaultPatternsIncludesRoot(t *testing.T) {
	if len(DefaultPatterns()) == 0 || DefaultPatterns()[0] != "." {
		t.Errorf("DefaultPatterns = %v, want it to start with \".\" so a root-level service.yaml is found", DefaultPatterns())
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
	if got, _ := r.LocalPatterns(); len(got) != len(DefaultPatterns()) {
		t.Errorf("LocalPatterns() after a parse failure = %v, want DefaultPatterns %v", got, DefaultPatterns())
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
	got, why := r.LocalPatterns()
	if why != LocalDefaulted {
		t.Fatalf("LocalPatterns() why = %v, want LocalDefaulted", why)
	}
	if len(got) != len(DefaultPatterns()) {
		t.Errorf("LocalPatterns() = %v, want DefaultPatterns %v", got, DefaultPatterns())
	}
}

func TestRepoIdentity(t *testing.T) {
	for _, tt := range []struct {
		name string
		repo Repo
		want string
	}{
		{"basename", Repo{URL: "https://github.com/org/monorepo"}, "monorepo"},
		{"trailing slash", Repo{URL: "https://github.com/org/monorepo/"}, "monorepo"},
		{"git suffix", Repo{URL: "https://github.com/org/monorepo.git"}, "monorepo"},
		{"explicit name wins", Repo{URL: "https://github.com/org/monorepo", Name: "core"}, "core"},
		// A zero-valued Repo is what loadReposFile's no-repos.yaml fallback
		// constructs (Name and URL both unset). path.Base("") is ".", which
		// is not a name -- it is path.Base answering a question nobody
		// asked. Entity.Location() renders "" as a bare path and anything
		// else as "<repo>:<path>", so returning "." here would print
		// ".:services/api/service.yaml" for the single-repository case,
		// which is most first runs.
		{"no url or name", Repo{}, ""},
		{"name only, no url", Repo{Name: "x"}, "x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.repo.Identity(); got != tt.want {
				t.Errorf("Identity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRepoHostKind(t *testing.T) {
	for _, tt := range []struct {
		name      string
		repo      Repo
		wantKind  string
		wantKnown bool
	}{
		{"github.com", Repo{URL: "https://github.com/org/a"}, "github", true},
		{"gitlab.com", Repo{URL: "https://gitlab.com/org/a"}, "gitlab", true},
		{"explicit beats hostname", Repo{URL: "https://gl.internal/org/a", Host: "gitlab"}, "gitlab", true},
		{"self-hosted, unstated", Repo{URL: "https://git.example.com/org/a"}, "", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			kind, known := tt.repo.HostKind()
			if kind != tt.wantKind || known != tt.wantKnown {
				t.Errorf("HostKind() = (%q, %v), want (%q, %v)", kind, known, tt.wantKind, tt.wantKnown)
			}
		})
	}
}

func TestLoadReposDiagnostics(t *testing.T) {
	for _, tt := range []struct {
		name        string
		yaml        string
		wantCheck   string
		wantLine    int
		wantMessage string
		wantHint    string
	}{
		{
			name:        "two locals",
			yaml:        "repos:\n  - url: https://github.com/org/monorepo\n    local: true\n  - url: https://github.com/org/edge\n    local: true\n",
			wantCheck:   "repos-local",
			wantLine:    4,
			wantMessage: `two entries in repos.yaml are marked local: true — "monorepo" (line 2) and "edge" (line 4)`,
			wantHint:    "exactly one entry is the repository you are standing in; remove local: true from the other",
		},
		{
			name:        "duplicate identity",
			yaml:        "repos:\n  - url: https://github.com/org1/api\n  - url: https://github.com/org2/api\n",
			wantCheck:   "repos-duplicate-name",
			wantLine:    3,
			wantMessage: `two repositories resolve to the name "api": https://github.com/org1/api (line 2) and https://github.com/org2/api (line 3)`,
			wantHint:    "the name is the last path segment of the url unless you set name:; give one of them an explicit name:",
		},
		{
			name:        "unknown host key",
			yaml:        "repos:\n  - url: https://bitbucket.org/org/api\n    host: bitbucket\n",
			wantCheck:   "repos-host",
			wantLine:    2,
			wantMessage: `unknown host "bitbucket" for https://bitbucket.org/org/api`,
			wantHint:    "host must be github or gitlab; landsraad v1 supports no others (design decision D5)",
		},
		{
			name: "host not inferable",
			yaml: "repos:\n  - url: https://github.com/org/platform\n    local: true\n" +
				"  - url: https://git.example.com/org/api\n",
			wantCheck:   "repos-host",
			wantLine:    4,
			wantMessage: `cannot tell which host https://git.example.com/org/api is`,
			wantHint:    "add host: github or host: gitlab to this entry",
		},
		{
			name:        "ssh url",
			yaml:        "repos:\n  - url: git@github.com:org/api.git\n",
			wantCheck:   "repos-url",
			wantLine:    2,
			wantMessage: `repository url must begin with https://, got "git@github.com:org/api.git"`,
			wantHint:    "write it as https://github.com/org/api",
		},
		{
			name:        "absolute path pattern",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [/services/*]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `path pattern "/services/*" must not be absolute; write a path relative to the repository root, for example "services/*"`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
		{
			name:        "path pattern escaping the root",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [../shared/*]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `path pattern "../shared/*" escapes the repository root via ".."; patterns must stay under the repository root`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
		{
			name:        "malformed glob",
			yaml:        "repos:\n  - url: https://github.com/org/api\n    paths: [\"services/[\"]\n",
			wantCheck:   "repos-path",
			wantLine:    2,
			wantMessage: `bad path pattern "services/[": syntax error in pattern`,
			wantHint:    "paths: are globs relative to the repository root, such as services/*",
		},
		{
			// Pins the ordering the host-check split depends on: a stated
			// host: value landsraad does not support is checked before the
			// local exemption, so marking the entry local: true does not
			// excuse it.
			name:        "local entry with an unsupported host still errors",
			yaml:        "repos:\n  - url: https://bitbucket.org/org/api\n    local: true\n    host: bitbucket\n",
			wantCheck:   "repos-host",
			wantLine:    2,
			wantMessage: `unknown host "bitbucket" for https://bitbucket.org/org/api`,
			wantHint:    "host must be github or gitlab; landsraad v1 supports no others (design decision D5)",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c diag.Collector
			LoadRepos("repos.yaml", []byte(tt.yaml), &c)
			ds := c.Diagnostics()
			if len(ds) != 1 {
				t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
			}
			d := ds[0]
			if d.Check != tt.wantCheck {
				t.Errorf("Check = %q, want %q", d.Check, tt.wantCheck)
			}
			if d.Line != tt.wantLine {
				t.Errorf("Line = %d, want %d", d.Line, tt.wantLine)
			}
			if d.Message != tt.wantMessage {
				t.Errorf("Message = %q, want %q", d.Message, tt.wantMessage)
			}
			if d.Hint != tt.wantHint {
				t.Errorf("Hint = %q, want %q", d.Hint, tt.wantHint)
			}
		})
	}
}

// The case the host-check split exists for: a single-entry repos.yaml on a
// hostname landsraad cannot infer, with no host: key, is the repository this
// command is standing in by LocalRepo()'s own rule (marked local, or the
// sole entry) — and a local repository is read from disk, never fetched, so
// there is no host to validate.
func TestLoadReposLocalRepoOnUnrecognisedHostNeedsNoHostKey(t *testing.T) {
	var c diag.Collector
	LoadRepos("repos.yaml", []byte("repos:\n  - url: https://git.example.com/org/api\n"), &c)
	if got := c.Diagnostics(); len(got) != 0 {
		t.Fatalf("got %d diagnostics, want 0: %+v", len(got), got)
	}
}

func TestLocalPatterns(t *testing.T) {
	for _, tt := range []struct {
		name    string
		yaml    string
		want    []string
		wantWhy LocalSource
	}{
		{
			name:    "marked entry wins over order",
			yaml:    "repos:\n  - url: https://github.com/org/other\n    paths: [apps/*]\n  - url: https://github.com/org/mine\n    local: true\n    paths: [services/*]\n",
			want:    []string{"services/*"},
			wantWhy: LocalMarked,
		},
		{
			name:    "single entry needs no marking",
			yaml:    "repos:\n  - url: https://github.com/org/mine\n    paths: [services/*]\n",
			want:    []string{"services/*"},
			wantWhy: LocalMarked,
		},
		{
			name:    "several entries, none marked",
			yaml:    "repos:\n  - url: https://github.com/org/a\n    paths: [apps/*]\n  - url: https://github.com/org/b\n    paths: [services/*]\n",
			want:    []string{"apps/*"},
			wantWhy: LocalAssumedFirst,
		},
		{
			name:    "no paths anywhere",
			yaml:    "repos:\n  - url: https://github.com/org/a\n",
			want:    DefaultPatterns(),
			wantWhy: LocalDefaulted,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var c diag.Collector
			r := LoadRepos("repos.yaml", []byte(tt.yaml), &c)
			got, why := r.LocalPatterns()
			if !slices.Equal(got, tt.want) {
				t.Errorf("patterns = %v, want %v", got, tt.want)
			}
			if why != tt.wantWhy {
				t.Errorf("why = %v, want %v", why, tt.wantWhy)
			}
		})
	}
}

func TestLoadReposRecordsLines(t *testing.T) {
	const y = "repos:\n  - url: https://github.com/org/a\n    paths: [.]\n  - url: https://github.com/org/b\n"
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte(y), &c)
	if len(r.Repos) != 2 {
		t.Fatalf("got %d repos, want 2", len(r.Repos))
	}
	if r.Repos[0].Line != 2 {
		t.Errorf("Repos[0].Line = %d, want 2", r.Repos[0].Line)
	}
	if r.Repos[1].Line != 4 {
		t.Errorf("Repos[1].Line = %d, want 4", r.Repos[1].Line)
	}
}

// Ruling R36: a rejected pattern is reported once, where it is written, and
// never reaches discover.Find. The valid patterns beside it still search. An
// entry left with none falls back to DefaultPatterns the way a repos.yaml
// that failed to parse does — silently, because the rejection is the
// diagnostic and the fallback is its consequence.
func TestLoadReposDropsRejectedPatterns(t *testing.T) {
	var c diag.Collector
	r := LoadRepos("repos.yaml", []byte("repos:\n  - url: https://github.com/org/api\n    paths: [services/*, /workers/*]\n"), &c)
	if got, why := r.LocalPatterns(); !slices.Equal(got, []string{"services/*"}) || why != LocalMarked {
		t.Errorf("LocalPatterns = %v, %v; want [services/*], LocalMarked", got, why)
	}
	if r.Repos[0].PathsRejected() {
		t.Error("PathsRejected() = true for an entry with a pattern left to search")
	}

	c = diag.Collector{}
	r = LoadRepos("repos.yaml", []byte("repos:\n  - url: https://github.com/org/api\n    paths: [/services/*]\n"), &c)
	if got, why := r.LocalPatterns(); !slices.Equal(got, DefaultPatterns()) || why != LocalRejected {
		t.Errorf("LocalPatterns = %v, %v; want DefaultPatterns, LocalRejected", got, why)
	}
	if !r.Repos[0].PathsRejected() {
		t.Error("PathsRejected() = false for an entry whose every pattern was rejected")
	}
}
