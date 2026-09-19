package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// The content set is every file a later stage will read. It is the whole
// contract between phase 1 and phase 2: a path missing from it reads as
// fetch.ErrNotFetched deep inside the renderer.
func TestContentSetIsEveryFileALaterStageReads(t *testing.T) {
	fsys := fstest.MapFS{
		"services/api/service.yaml":     {Data: []byte("x")},
		"services/api/runbook.md":       {Data: []byte("x")},
		"services/api/alerts.yaml":      {Data: []byte("x")},
		"services/api/docs/index.md":    {Data: []byte("x")},
		"services/api/docs/deep/why.md": {Data: []byte("x")},
		"services/api/docs/diagram.png": {Data: []byte("x")}, // not markdown
		"services/api/NOTES.md":         {Data: []byte("x")}, // not under docs
		".landsraad/checks/scan.yaml":   {Data: []byte("x")},
		".landsraad/checks/notes.txt":   {Data: []byte("x")}, // not a results file
	}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "services/api/service.yaml"}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	e.Spec.Runbook = "services/api/runbook.md"
	e.Spec.Alerts = "services/api/alerts.yaml"
	e.Spec.Docs = "services/api/docs"

	want := []string{
		".landsraad/checks/scan.yaml",
		"services/api/alerts.yaml",
		"services/api/docs/deep/why.md",
		"services/api/docs/index.md",
		"services/api/runbook.md",
	}
	if diff := cmp.Diff(want, contentSet(expanded{fsys: fsys, entities: []*catalog.Entity{e}})); diff != "" {
		t.Errorf("contentSet mismatch (-want +got):\n%s", diff)
	}
}

// Fix round 2, the Critical one. fetchBlobs rejects a path it has no Entry
// for, openRepos turns that into a repoFailure, and a repoFailure drops the
// whole repository — so asking for a spec.runbook that is not in the listing
// cost the catalog an entire repository instead of producing the
// missing-file diagnostic and the failed runbook-present that a dangling
// path is a case of. CheckFiles diagnoses it; a fetch does not.
func TestContentSetSkipsAPathThatIsNotInTheListing(t *testing.T) {
	fsys := fstest.MapFS{
		"services/api/service.yaml": {Data: []byte("x")},
		"services/api/runbook.md":   {Data: []byte("x")},
	}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "services/api/service.yaml"}
	e.Kind = "Service"
	e.Metadata.Name = "api"
	e.Spec.Runbook = "services/api/runbook.md" // there
	e.Spec.Alerts = "services/api/alerts.yaml" // not there
	e.Spec.Docs = "services/api/docs"          // not there either

	want := []string{"services/api/runbook.md"}
	if diff := cmp.Diff(want, contentSet(expanded{fsys: fsys, entities: []*catalog.Entity{e}})); diff != "" {
		t.Errorf("contentSet mismatch (-want +got):\n%s", diff)
	}
}

func TestContentSetSkipsUnsetFields(t *testing.T) {
	fsys := fstest.MapFS{"service.yaml": {Data: []byte("x")}}
	e := &catalog.Entity{SourceRepo: "mono", SourcePath: "service.yaml"}
	e.Kind = "Library"
	e.Metadata.Name = "lib"
	if got := contentSet(expanded{fsys: fsys, entities: []*catalog.Entity{e}}); len(got) != 0 {
		t.Errorf("contentSet = %v, want none: the entity names no files", got)
	}
}

func TestDocsDirs(t *testing.T) {
	a := &catalog.Entity{}
	a.Spec.Docs = "services/api/docs"
	b := &catalog.Entity{}
	b.Spec.Docs = "services/api/docs" // duplicate
	c := &catalog.Entity{}            // no docs

	// The runbook's and the alerts file's directories are listed too. A
	// satellite with `paths: [services/*]` and `runbook: docs/runbooks/api.md`
	// has its runbook outside every prefix GitLab.Open lists and outside every
	// directory GitHub's truncated-tree descent walks, so without these the
	// file is in no listing and landsraad reports a runbook that is sitting in
	// the repository as missing.
	d := &catalog.Entity{}
	d.Spec.Runbook = "docs/runbooks/api.md"
	d.Spec.Alerts = "ops/alerts/api.yaml"

	// "." is never returned: both adapters already list the repository root,
	// and asking GitHub to expand "." would recursively list the whole
	// repository — the one thing ruling R28's descent exists to avoid.
	e := &catalog.Entity{}
	e.Spec.Runbook = "runbook.md"

	want := []string{".landsraad/checks", "docs/runbooks", "ops/alerts", "services/api/docs"}
	if diff := cmp.Diff(want, docsDirs([]*catalog.Entity{a, b, c, d, e})); diff != "" {
		t.Errorf("docsDirs mismatch (-want +got):\n%s", diff)
	}
}

// Ruling R29: the per-repository variable wins, then the host-wide one.
func TestTokenFor(t *testing.T) {
	env := map[string]string{
		"GITHUB_TOKEN":            "host-wide",
		"GITLAB_TOKEN":            "gl-wide",
		"LANDSRAAD_TOKEN_EDGE_GW": "per-repo",
	}
	look := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	for _, tt := range []struct {
		name string
		repo config.Repo
		want string
	}{
		{"per-repo wins", config.Repo{Name: "edge-gw", URL: "https://github.com/o/edge-gw"}, "per-repo"},
		{"github falls back to host", config.Repo{URL: "https://github.com/o/api"}, "host-wide"},
		{"gitlab falls back to host", config.Repo{URL: "https://gitlab.com/o/api"}, "gl-wide"},
		{"nothing set", config.Repo{URL: "https://github.com/o/x", Name: "x", Host: "bitbucket"}, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tokenFor(tt.repo, look); got != tt.want {
				t.Errorf("tokenFor = %q, want %q", got, tt.want)
			}
		})
	}
}

// The name is normalised the way the ruling states: uppercased, every
// character that is not an ASCII letter or digit replaced by one
// underscore. A repository called "edge-gateway" must not need a
// variable nobody could guess.
func TestTokenVarName(t *testing.T) {
	for _, tt := range []struct{ name, want string }{
		{"edge-gateway", "LANDSRAAD_TOKEN_EDGE_GATEWAY"},
		{"my.repo", "LANDSRAAD_TOKEN_MY_REPO"},
		{"api", "LANDSRAAD_TOKEN_API"},
		// One underscore for É, which is two bytes in UTF-8: characters are
		// replaced, not bytes.
		{"café", "LANDSRAAD_TOKEN_CAF_"},
	} {
		if got := tokenVarName(tt.name); got != tt.want {
			t.Errorf("tokenVarName(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// Fix round 2 (coordinator review): silent defaulting with no diagnostic
// contradicts patternsFor's identical single-repository case
// (defaultPatternsNote) and CLAUDE.md's guidance that degraded mode must be
// visible in the artifact, not only in a log. This pins loadReposFile's
// side of it: repos.yaml parses but names zero repositories.
func TestLoadReposFileWithEmptyReposListAnnouncesDefaultPatterns(t *testing.T) {
	fsys := fstest.MapFS{"repos.yaml": {Data: []byte("repos: []\n")}}
	var c diag.Collector
	repos := loadReposFile(fsys, &c)

	if len(repos) != 1 || !repos[0].Local {
		t.Fatalf("loadReposFile = %+v, want a single local fallback entry", repos)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Severity != diag.SevInfo {
		t.Errorf("Severity = %v, want SevInfo — this is tolerated, not an error", d.Severity)
	}
	if d.Check != "default-patterns" {
		t.Errorf("Check = %q, want %q", d.Check, "default-patterns")
	}
	want := "repos.yaml lists no repositories; using default paths (., services/*, workers/*, libs/*, topics/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add repos.yaml if your services live elsewhere"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// openRepos's side of the same fix: one entry in an otherwise normal
// repos.yaml names no paths:.
func TestOpenReposEntryWithNoPathsAnnouncesDefaultPatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "teams.yaml",
		"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")
	writeFile(t, dir, "repos.yaml",
		"repos:\n  - url: https://github.com/org/monorepo\n    local: true\n")
	writeFile(t, dir, "services/api/service.yaml", `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: dir, RootFS: os.DirFS(dir),
		Lookup: func(string) (string, bool) { return "", false },
		ErrOut: io.Discard,
	}, &c)
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("Failures() = %+v, want none", got)
	}

	var found []diag.Diagnostic
	for _, d := range c.Diagnostics() {
		if d.Check == "default-patterns" {
			found = append(found, d)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d default-patterns diagnostics, want exactly 1: %+v", len(found), c.Diagnostics())
	}
	d := found[0]
	if d.Severity != diag.SevInfo {
		t.Errorf("Severity = %v, want SevInfo", d.Severity)
	}
	// Empty, not "monorepo": the note is about repos.yaml, which lives in the
	// repository the command is standing in, and Repo names the repository
	// that holds File (ruling R41). The message names the entry it is about.
	if d.Repo != "" {
		t.Errorf("Repo = %q, want empty", d.Repo)
	}
	if d.Line != 2 {
		t.Errorf("Line = %d, want 2 (the url: key's line)", d.Line)
	}
	want := "monorepo names no paths; using default paths (., services/*, workers/*, libs/*, topics/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add paths: to this entry if its services live elsewhere"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// openRepos's only path this task can exercise without an HTTP fixture: no
// repos.yaml at all, so loadReposFile's single-local-repository fallback is
// what runs. It still drives the real Open path (os.DirFS over Root, not a
// fetch.FS), so this is a genuine smoke test of the wiring in this file
// rather than of any adapter.
func TestOpenReposWithNoReposYAMLReadsTheLocalRepository(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "teams.yaml",
		"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")
	writeFile(t, dir, "services/api/service.yaml", `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root:   dir,
		RootFS: os.DirFS(dir),
		Lookup: func(string) (string, bool) { return "", false },
		ErrOut: io.Discard,
	}, &c)

	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("Failures() = %+v, want none", got)
	}
	if got := w.Sources().Names(); len(got) != 1 {
		t.Fatalf("Sources().Names() = %v, want exactly one repository", got)
	}

	v := defaultValidator(&c)
	if v == nil {
		t.Fatalf("defaultValidator returned nil; diagnostics: %+v", c.Diagnostics())
	}
	entities := w.ParseAll(v, &c).entities
	if len(entities) != 1 || entities[0].Metadata.Name != "api" {
		t.Fatalf("ParseAll = %+v, want exactly the api entity", entities)
	}

	// The property that actually matters, not just the Identity() unit: a
	// checkout with no repos.yaml is most first runs, and Entity.Location()
	// renders SourceRepo == "" as a bare path. SourceRepo == "." (what
	// path.Base("") used to hand back through Repo.Identity()) would print
	// ".:services/api/service.yaml" in every diagnostic instead.
	if got := entities[0].SourceRepo; got != "" {
		t.Errorf("SourceRepo = %q, want \"\" (no repos.yaml means no repository name)", got)
	}
	if got, want := entities[0].Location(), "services/api/service.yaml"; got != want {
		t.Errorf("Location() = %q, want %q", got, want)
	}
}

func writeFile(t *testing.T, root, path, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", full, err)
	}
}

// The split must not change single-repository behaviour. This asserts the
// two halves compose back into what loadCatalogScoped always did.
func TestParseRepoThenAssembleMatchesTheOldPipeline(t *testing.T) {
	fsys := genFS() // the fixture gen_test.go uses for a valid in-memory repository
	var c diag.Collector
	cat, g, teams := loadCatalogScoped(fsys, catalog.FullCatalog, &c)
	if cat == nil || g == nil || teams == nil {
		t.Fatalf("loadCatalogScoped returned nils; diagnostics: %+v", c.Diagnostics())
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("got %d diagnostics on a good fixture: %+v", len(ds), ds)
	}
}

// Fix round 2 (coordinator review): a single-repository run must produce
// the ONE rich "no entities" diagnostic main always did, carrying the
// searched paths — not the thinner per-repository warning that exists for
// telling several repositories apart. This is parseRepo's half of that
// property, pinned exactly rather than by substring.
func TestParseRepoZeroFoundSoloExactMessage(t *testing.T) {
	fsys := fstest.MapFS{"teams.yaml": {Data: []byte("x")}}
	v := schemaValidatorForTest(t)
	var c diag.Collector
	p := parseRepo("monorepo", fsys, []string{"services/*"}, true, true, v, &c)
	if p.entities != nil || p.found != 0 {
		t.Fatalf("parseRepo = %+v, want no entities and nothing found", p)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError — this is what gates a single-repository build", d.Severity)
	}
	if d.Check != "no-entities" {
		t.Errorf("Check = %q, want %q", d.Check, "no-entities")
	}
	want := "no service.yaml found under any configured path (services/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add a repos.yaml listing the paths your services live under"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// parseRepo's other half: solo=false keeps the per-repository warning,
// worded and severed differently on purpose (see TestParseRepoZeroFoundSoloExactMessage).
func TestParseRepoZeroFoundMultiExactMessage(t *testing.T) {
	fsys := fstest.MapFS{"teams.yaml": {Data: []byte("x")}}
	v := schemaValidatorForTest(t)
	var c diag.Collector
	p := parseRepo("monorepo", fsys, []string{"services/*"}, false, true, v, &c)
	if p.entities != nil || p.found != 0 {
		t.Fatalf("parseRepo = %+v, want no entities and nothing found", p)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn — one repository among several being empty is not fatal by itself", d.Severity)
	}
	want := "no service.yaml found in monorepo under any configured path (services/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "check this repository's `paths:` in repos.yaml"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// parseRepo's third zero-found state, and the one the R46 completion added:
// the patterns are a guess, because repos.yaml is present and did not parse,
// so neither wording above is a fact about the user's layout and repos-parse
// already carries the cause. Zero diagnostics from here — but note what this
// asserts and what it does not: parseRepo returns early only because nothing
// was found. Whatever it finds under a guessed pattern set it still reports
// (see TestGenSuppressesOnlyNoEntitiesWhenReposYAMLFailedToParse), which is
// the half the first R46 fix lost.
func TestParseRepoZeroFoundPatternsUnknownAddsNoDiagnostic(t *testing.T) {
	fsys := fstest.MapFS{"teams.yaml": {Data: []byte("x")}}
	v := schemaValidatorForTest(t)
	for _, solo := range []bool{true, false} {
		var c diag.Collector
		p := parseRepo("monorepo", fsys, []string{"services/*"}, solo, false, v, &c)
		if p.entities != nil || p.found != 0 {
			t.Errorf("solo=%v: parseRepo = %+v, want no entities and nothing found", solo, p)
		}
		if ds := c.Diagnostics(); len(ds) != 0 {
			t.Errorf("solo=%v: parseRepo added %d diagnostics off a guessed pattern set, want 0: %+v", solo, len(ds), ds)
		}
	}
}

// The residual hole in R46's "ordering as a compile error", documented as
// deliberate rather than left to be rediscovered. Giving assemble a
// *config.Teams instead of an fs.FS stops it reading teams.yaml, so it cannot
// run against an unread one — but *config.Teams's zero value is valid and
// load-bearing: nil means "loadTeamsFor already reported why there is none",
// and assemble returns all-nil rather than resolving owners against nothing.
// So a caller CAN pass a literal nil and skip loadTeamsFor, which compiles
// and is silent. The compiler narrows the hole; it does not close it, and the
// arrangement that closes it — no give-up path above the loadTeamsFor call —
// is held by behaviour, here and in TestGenReportsTeamsProblemsWhenReposYAMLFailedToParse.
func TestAssembleWithNilTeamsReturnsAllNil(t *testing.T) {
	entities := catalog.ParseAll("monorepo", []discover.File{{
		Path: "services/api/service.yaml",
		Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\n"),
	}}, &diag.Collector{})
	if len(entities) != 1 {
		t.Fatalf("fixture parsed %d entities, want 1 — the nil-teams branch is only reachable with a catalog", len(entities))
	}

	var c diag.Collector
	cat, g, teams := assemble(parseResult{entities: entities, found: 1},
		catalog.SingleSource("monorepo", fstest.MapFS{}), catalog.FullCatalog, nil, &c)

	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble(..., nil, ...) = (%v, %v, %v), want all nil: a nil teams means "+
			"loadTeamsFor already reported why there is none, so stage 5 must stop rather than "+
			"resolve owners against nothing", cat, g, teams)
	}
}

// assemble's half of the solo property: when there is at most one source,
// a solo parseRepo call has already reported the single rich error, so
// assemble must add nothing more — not even a thinner echo of it.
func TestAssembleZeroEntitiesSoloAddsNoDiagnostic(t *testing.T) {
	var c diag.Collector
	cat, g, teams := assemble(parseResult{}, catalog.SingleSource("monorepo", fstest.MapFS{}), catalog.FullCatalog, loadTeamsFor(teamsOnly(), &c), &c)
	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble = (%v, %v, %v), want all nil", cat, g, teams)
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("assemble added %d diagnostics for a solo empty catalog, want 0: %+v", len(ds), ds)
	}
}

// assemble's multi-repository case: with more than one source, no single
// per-repository warning can say the WHOLE catalog is empty, so assemble's
// own error is the one diagnostic that must fire, pinned exactly.
func TestAssembleZeroEntitiesMultiExactMessage(t *testing.T) {
	src := catalog.Sources{"repo-a": fstest.MapFS{}, "repo-b": fstest.MapFS{}}
	var c diag.Collector
	cat, g, teams := assemble(parseResult{}, src, catalog.FullCatalog, loadTeamsFor(teamsOnly(), &c), &c)
	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble = (%v, %v, %v), want all nil", cat, g, teams)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want exactly 1: %+v", len(ds), ds)
	}
	d := ds[0]
	if d.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", d.Severity)
	}
	want := "no service.yaml found in any configured repository"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add a repos.yaml listing the paths your services live under"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// The property end to end, composed the way workspace.ParseAll and a future
// Build actually would: two repositories, neither matching anything, must
// produce BOTH per-repository warnings AND the one aggregate error — the
// multi-repository half of the coordinator's ruling, so the single-vs-multi
// distinction is tested on both sides, not just asserted about parseRepo
// and assemble in isolation.
func TestWorkspaceParseAllThenAssembleMultiRepoZeroMatchKeepsBothDiagnostics(t *testing.T) {
	w := &workspace{
		sources: catalog.Sources{
			"repo-a": fstest.MapFS{"teams.yaml": {Data: []byte("x")}},
			"repo-b": fstest.MapFS{"teams.yaml": {Data: []byte("x")}},
		},
		patterns: map[string][]string{
			"repo-a": {"services/*"},
			"repo-b": {"services/*"},
		},
		fetchers: map[string]fetch.Fetcher{},
	}
	v := schemaValidatorForTest(t)
	var c diag.Collector
	p := w.ParseAll(v, &c)
	if len(p.entities) != 0 || p.found != 0 {
		t.Fatalf("ParseAll = %+v, want no entities and nothing found", p)
	}
	cat, g, teams := assemble(p, w.Sources(), catalog.FullCatalog, loadTeamsFor(teamsOnly(), &c), &c)
	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble = (%v, %v, %v), want all nil", cat, g, teams)
	}

	ds := c.Diagnostics()
	if len(ds) != 3 {
		t.Fatalf("got %d diagnostics, want exactly 3 (two per-repository warnings, one aggregate error): %+v", len(ds), ds)
	}
	var warns, errs int
	var warnMessages []string
	for _, d := range ds {
		if d.Check != "no-entities" {
			t.Errorf("unexpected Check %q in %+v", d.Check, d)
			continue
		}
		switch d.Severity {
		case diag.SevWarn:
			warns++
			// Repo is empty: the warning is about repos.yaml, in the repository
			// the command is standing in (ruling R41). The message names the
			// repository it is about.
			if d.Repo != "" {
				t.Errorf("warning Repo = %q, want empty", d.Repo)
			}
			warnMessages = append(warnMessages, d.Message)
		case diag.SevError:
			errs++
			want := "no service.yaml found in any configured repository"
			if d.Message != want {
				t.Errorf("error Message\n got: %s\nwant: %s", d.Message, want)
			}
		default:
			t.Errorf("unexpected Severity %v in %+v", d.Severity, d)
		}
	}
	if warns != 2 {
		t.Errorf("got %d per-repository warnings, want 2", warns)
	}
	if errs != 1 {
		t.Errorf("got %d aggregate errors, want 1", errs)
	}
	// Collector.Diagnostics sorts by message after file, line and check.
	wantWarnings := []string{
		"no service.yaml found in repo-a under any configured path (services/*)",
		"no service.yaml found in repo-b under any configured path (services/*)",
	}
	if diff := cmp.Diff(wantWarnings, warnMessages); diff != "" {
		t.Errorf("warning messages mismatch (-want +got):\n%s", diff)
	}
}

// schemaValidatorForTest compiles the embedded schema once per test that
// calls parseRepo directly. Failure here means the embedded schema itself
// is broken, which every other test in this package would also catch —
// t.Fatal is appropriate rather than folding it into the diagnostics under
// test.
func schemaValidatorForTest(t *testing.T) *schema.Validator {
	t.Helper()
	v, err := schema.Default()
	if err != nil {
		t.Fatalf("schema.Default(): %v", err)
	}
	return v
}

// teamsOnly is a repository root holding nothing but a valid teams.yaml.
// Since ruling R42, assemble reads teams.yaml even for an empty catalog, so
// a test about something else gives it one with nothing to report.
func teamsOnly() fstest.MapFS {
	return fstest.MapFS{"teams.yaml": genFS()["teams.yaml"]}
}

// The other half of ruling R42. In a multi-repository run, "no service.yaml
// found in any configured repository" is a claim about files, and it used to
// be made about entities: when every file that was found failed to parse, it
// fired on top of the parse errors that already said why the catalog was
// empty, and told the reader their files were not there.
func TestAssembleDoesNotSayNothingWasFoundWhenFilesFailedToParse(t *testing.T) {
	src := catalog.Sources{"repo-a": fstest.MapFS{}, "repo-b": fstest.MapFS{}}
	var c diag.Collector

	cat, g, teams := assemble(parseResult{found: 2}, src, catalog.FullCatalog, loadTeamsFor(teamsOnly(), &c), &c)

	if cat != nil || g != nil || teams != nil {
		t.Fatalf("assemble = (%v, %v, %v), want all nil for an empty catalog", cat, g, teams)
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Errorf("assemble added %+v; the parse errors that emptied the catalog have already said why", ds)
	}
}

// Ruling R47: len(r.Repos) == 0 is true both for a file that parsed and named
// nothing and for a file that did not parse. Repos.Loaded() carries the
// distinction, and patternsFor already uses it. Announcing default-patterns
// for a parse failure is two diagnostics for one cause, sorted ahead of its
// own cause, with a hint naming a file that exists — and a message that is
// false, since the file names one repository.
func TestLoadReposFileReturnsNoReposWhenParseFailed(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
	}
	var c diag.Collector

	got := loadReposFile(fsys, &c)

	if len(got) != 0 {
		t.Fatalf("loadReposFile = %+v, want no repositories: a file that did not parse configures nothing", got)
	}
	var parse bool
	for _, d := range c.Diagnostics() {
		switch d.Check {
		case "repos-parse":
			parse = true
		case "default-patterns":
			t.Errorf("default-patterns must not be announced for a file that did not parse; got message %q", d.Message)
		}
	}
	if !parse {
		t.Fatalf("the cause must still be reported; no repos-parse in %+v", c.Diagnostics())
	}
}

// Ruling R47's coverage at the seam build and serve actually share. Neither
// command can be driven in-process for this scenario: both gate on a
// malformed repos.yaml with os.Exit(exitValidation) from inside their RunE
// closure, which would kill the test binary rather than let it observe the
// result. openRepos is the highest seam that is both in-process reachable
// and common to the two commands this fix changes the output of -- it is
// where loadReposFile's return value turns into a workspace, and where
// TestOpenReposEntryWithNoPathsAnnouncesDefaultPatterns above already
// exercises the sibling degraded mode the same way.
func TestOpenReposReturnsNoSourcesWhenReposYAMLFailedToParse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "repos.yaml", "kind: Service\n  bad: indent\n")

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: dir, RootFS: os.DirFS(dir),
		Lookup: func(string) (string, bool) { return "", false },
		ErrOut: io.Discard,
	}, &c)

	if got := w.Sources().Names(); len(got) != 0 {
		t.Fatalf("Sources().Names() = %v, want none: a repos.yaml that did not parse configures no repositories", got)
	}

	var sawParse, sawDefault bool
	var parseMessage string
	for _, d := range c.Diagnostics() {
		switch d.Check {
		case "repos-parse":
			sawParse = true
			parseMessage = d.Message
		case "default-patterns":
			sawDefault = true
		}
	}
	if !sawParse {
		t.Fatalf("no repos-parse diagnostic in %+v", c.Diagnostics())
	}
	want := "cannot parse repos file: yaml: line 2: mapping values are not allowed in this context"
	if parseMessage != want {
		t.Errorf("Message\n got: %s\nwant: %s", parseMessage, want)
	}
	if sawDefault {
		t.Errorf("default-patterns must not be announced for a file that did not parse: %+v", c.Diagnostics())
	}
}
