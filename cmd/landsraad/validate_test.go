package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// validate is the shared harness. out carries only the format payload;
// errOut carries the human lines. Keeping them separate in the tests is what
// stops the two being conflated in the implementation.
func validate(fsys fstest.MapFS) (code int, out, errOut string) {
	var o, e bytes.Buffer
	c := Validate(fsys, &o, &e, diag.Text{}, false)
	return c, o.String(), e.String()
}

func TestValidateOnGoodRepoExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.Text{}, false)
	if code != exitOK {
		t.Errorf("exit code = %d, want %d\nout:\n%s\nerr:\n%s", code, exitOK, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "ok: 3 entities validated") {
		t.Errorf("expected 3 entities (2 services + 1 topic), got:\n%s", errOut.String())
	}
}

func TestValidateOnBrokenRepoExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{}, false); code != exitValidation {
		t.Errorf("exit code = %d, want %d", code, exitValidation)
	}
}

// The point of the collector: one run reports every problem, not the first.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{}, false)
	got := out.String()
	for _, want := range []string{
		"unknown-owner",    // owner: team-payment
		"duplicate-name",   // two entities named api
		"missing-file",     // runbook does not exist
		"dependency-cycle", // loop-a <-> loop-b
	} {
		if !strings.Contains(got, want) {
			t.Errorf("a single run must report %q; output was:\n%s", want, got)
		}
	}
}

// reportCycles has no other coverage: assert its rendered message exactly.
func TestValidateRendersTheCycleMessage(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{}, false)
	if !strings.Contains(out.String(), "dependency cycle: service:loop-a -> service:loop-b -> service:loop-a") {
		t.Errorf("cycle message missing or malformed:\n%s", out.String())
	}
}

// validate is hermetic: a reference to another repo is not an error here.
func TestValidateToleratesCrossRepoRefs(t *testing.T) {
	repo := fstest.MapFS{
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\nspec:\n" +
				"  dependsOn:\n    - service:lives-in-another-repo\n")},
	}
	code, out, _ := validate(repo)
	if code != exitOK {
		t.Errorf("a cross-repo ref must not fail validate, got exit %d:\n%s", code, out)
	}
	if strings.Contains(out, "dangling-ref") {
		t.Errorf("validate sees one repo and must not report cross-repo refs:\n%s", out)
	}
}

// The whole pipeline runs against an in-memory filesystem with no disk at all.
func TestValidateRunsEntirelyInMemory(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n    members: [alice]\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\nspec:\n  language: go\n")},
	}
	if code, out, _ := validate(repo); code != exitOK {
		t.Errorf("exit code = %d, want %d\n%s", code, exitOK, out)
	}
}

func TestValidateRequiresTeamsFile(t *testing.T) {
	repo := fstest.MapFS{
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	code, out, _ := validate(repo)
	if code != exitValidation {
		t.Errorf("a missing teams.yaml must fail validation, got exit %d", code)
	}
	if !strings.Contains(out, "teams.yaml") {
		t.Errorf("the diagnostic must name teams.yaml:\n%s", out)
	}
}

// A repo whose services live somewhere the patterns do not reach must FAIL.
// Exiting 0 here is the silently-inert-linter bug: a team wires landsraad into
// CI, gets a green check forever, and validates nothing.
func TestValidateFailsWhenNothingMatches(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"apps/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	code, out, _ := validate(repo)
	if code != exitValidation {
		t.Errorf("matching zero files must fail, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "no-entities") {
		t.Errorf("the diagnostic must explain that nothing matched:\n%s", out)
	}
}

// A single-service repo with service.yaml at the root must be discovered.
func TestValidateFindsARootLevelService(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: edge-gateway\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	if code, out, _ := validate(repo); code != exitOK {
		t.Errorf("a root-level service.yaml must be found, got exit %d:\n%s", code, out)
	}
}

// A missing repos.yaml is tolerated but must not be silent (spec §12).
func TestValidateAnnouncesDefaultPatterns(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	_, out, _ := validate(repo)
	if !strings.Contains(out, "default-patterns") {
		t.Errorf("defaulting must be visible in the output, not only in a log:\n%s", out)
	}
}

// --format json must be pipeable to jq on SUCCESS, which is the common case.
// The ok-line belongs on stderr; if it lands on stdout the payload is corrupt.
func TestJSONOutputIsParseableOnSuccess(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.JSON{}, false)
	if code != exitOK {
		t.Fatalf("fixture must be clean, got exit %d:\n%s", code, out.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Errorf("stdout must be valid JSON on success, got %q: %v", out.String(), err)
	}
	if strings.Contains(out.String(), "ok:") {
		t.Error("the ok-line must go to stderr, never into the format payload")
	}
}

func TestJSONOutputIsParseableOnFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.JSON{}, false)
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Errorf("stdout must be valid JSON on failure too: %v", err)
	}
	if len(ds) == 0 {
		t.Error("the broken fixture must produce diagnostics")
	}
}

// --- Additional regression and exact-message coverage below. The brief's
// tests above check *that* a check name shows up in rendered text; these
// check the exact Message/Hint each diagnostic carries, following this
// codebase's established pattern (see catalog/merge_test.go, config/teams_test.go).

// Regression: a missing repos.yaml must default AND say so (already proven
// end to end by TestValidateAnnouncesDefaultPatterns above); this pins the
// exact wording so a phrasing regression fails a test, not just a review.
func TestPatternsForAnnouncesDefaultPatternsExactMessage(t *testing.T) {
	var c diag.Collector
	got, known := patternsFor(fstest.MapFS{}, &c)
	// An absent repos.yaml is a deliberate, announced fallback, not a guess:
	// no-entities must still fire against these patterns (defect 4).
	if !known {
		t.Error("an absent repos.yaml must still yield known patterns")
	}
	if len(got) != len(config.DefaultPatterns()) {
		t.Fatalf("patternsFor with no repos.yaml = %v, want DefaultPatterns %v", got, config.DefaultPatterns())
	}
	if c.Len() != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %+v", c.Len(), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Severity != diag.SevInfo {
		t.Errorf("Severity = %v, want SevInfo — a missing repos.yaml is tolerated, not an error", d.Severity)
	}
	if d.Check != "default-patterns" {
		t.Errorf("Check = %q, want %q", d.Check, "default-patterns")
	}
	want := "no repos.yaml found; using default paths (., services/*, workers/*, libs/*, topics/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "add repos.yaml if your services live elsewhere"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// Regression (defect 3 — the silent repos.yaml fallback): a malformed
// repos.yaml must be reported loudly, with an exact message, never dropped
// silently in favour of DefaultPatterns.
func TestPatternsForReportsMalformedReposYAML(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
	}
	var c diag.Collector
	got, known := patternsFor(fsys, &c)
	if !c.HasErrors() {
		t.Fatal("malformed repos.yaml must be an error, not a silent fallback to defaults")
	}
	// The patterns are a guess standing in for a file nobody could read, and
	// saying so is what stops callers reporting what the guess matched
	// (defect 4).
	if known {
		t.Error("patternsFor must report patterns as not known when repos.yaml failed to parse")
	}
	d := c.Diagnostics()[0]
	if d.Check != "repos-parse" {
		t.Errorf("Check = %q, want %q", d.Check, "repos-parse")
	}
	want := "cannot parse repos file: yaml: line 2: mapping values are not allowed in this context"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	// Even while reporting the error, patternsFor still returns something
	// usable so the run can proceed and report everything else wrong with
	// the repo in the same pass, rather than aborting outright.
	if len(got) != len(config.DefaultPatterns()) {
		t.Errorf("patternsFor fallback = %v, want DefaultPatterns %v", got, config.DefaultPatterns())
	}
}

func TestCheckOwnersReportsMissingTeamsFileExactMessage(t *testing.T) {
	var c diag.Collector
	checkOwners(fstest.MapFS{}, nil, &c)
	if c.Len() != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %+v", c.Len(), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", d.Severity)
	}
	if d.Check != "missing-teams" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-teams")
	}
	if d.File != "teams.yaml" {
		t.Errorf("File = %q, want %q", d.File, "teams.yaml")
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	want := "teams.yaml not found at the repository root, so no owner can be resolved"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "run `landsraad init` to create one, or pass --satellite if this repository's owners are defined in the platform repository's teams.yaml"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// Ruling R43: one condition, one check id. gen, score and build said
// teams-missing while validate said missing-teams, with a different message
// and hint, about the same absent file. The id and message are now shared;
// the hint is each command's own, because the remedy differs: validate has
// --satellite, and gen, score and build have no such mode.
func TestLoadCatalogReportsMissingTeamsLikeValidate(t *testing.T) {
	fsys := genFS()
	delete(fsys, "teams.yaml")
	var c diag.Collector

	loadCatalog(fsys, &c)

	want := []diag.Diagnostic{{
		Severity: diag.SevError, File: "teams.yaml", Line: 1,
		Check:   "missing-teams",
		Message: "teams.yaml not found at the repository root, so no owner can be resolved",
		Hint:    "run `landsraad init` to create one",
	}}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// reportCycles' Hint is never asserted anywhere else in the suite.
func TestReportCyclesExactMessage(t *testing.T) {
	loopA := &catalog.Entity{Kind: catalog.KindService}
	loopA.Metadata.Name = "loop-a"
	loopA.SourcePath = "services/loop-a/service.yaml"
	loopA.NameLine = 4
	loopA.Spec.DependsOn = []string{"service:loop-b"}

	loopB := &catalog.Entity{Kind: catalog.KindService}
	loopB.Metadata.Name = "loop-b"
	loopB.SourcePath = "services/loop-b/service.yaml"
	loopB.NameLine = 4
	loopB.Spec.DependsOn = []string{"service:loop-a"}

	var c diag.Collector
	cat := catalog.NewCatalog([]*catalog.Entity{loopA, loopB}, &c)
	g := cat.Resolve(catalog.LocalOnly, &c)

	var rc diag.Collector
	reportCycles(cat, g, &rc)

	if rc.Len() != 1 {
		t.Fatalf("expected exactly one cycle diagnostic, got %d: %+v", rc.Len(), rc.Diagnostics())
	}
	d := rc.Diagnostics()[0]
	if d.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", d.Severity)
	}
	if d.Check != "dependency-cycle" {
		t.Errorf("Check = %q, want %q", d.Check, "dependency-cycle")
	}
	want := "dependency cycle: service:loop-a -> service:loop-b -> service:loop-a"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "break the loop, or model one direction as a shared library"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
	if d.File != "services/loop-a/service.yaml" {
		t.Errorf("File = %q, want %q", d.File, "services/loop-a/service.yaml")
	}
	if d.Entity != "loop-a" {
		t.Errorf("Entity = %q, want %q", d.Entity, "loop-a")
	}
}

// Regression (defect 1 — zero matches treated as success): pins the exact
// message and hint the end user sees, not just the check name.
func TestValidateReportsNoEntitiesExactMessage(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"apps/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	var out, errOut bytes.Buffer
	code := Validate(repo, &out, &errOut, diag.JSON{}, false)
	if code != exitValidation {
		t.Fatalf("exit code = %d, want %d", code, exitValidation)
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("stdout must be valid JSON: %v", err)
	}
	var found *diag.Diagnostic
	for i := range ds {
		if ds[i].Check == "no-entities" {
			found = &ds[i]
		}
	}
	if found == nil {
		t.Fatalf("no no-entities diagnostic in %+v", ds)
	}
	if found.Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", found.Severity)
	}
	if found.File != "repos.yaml" {
		t.Errorf("File = %q, want %q", found.File, "repos.yaml")
	}
	want := "no service.yaml found under any configured path (., services/*, workers/*, libs/*, topics/*)"
	if found.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", found.Message, want)
	}
	wantHint := "add a repos.yaml listing the paths your services live under"
	if found.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", found.Hint, wantHint)
	}
}

// Regression (defect 3 — the silent repos.yaml fallback), at the Validate
// level: a malformed repos.yaml must never let a run exit clean, and the
// problem must be visible in the diagnostics, not only in a log.
func TestValidateMalformedReposYAMLIsNotSilent(t *testing.T) {
	repo := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	code, out, _ := validate(repo)
	if code == exitOK {
		t.Fatalf("a malformed repos.yaml must not exit clean, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "repos-parse") {
		t.Errorf("a malformed repos.yaml must be visible in the diagnostics, not silently defaulted:\n%s", out)
	}
}

// Regression (defect 4 — no-entities cascading off a parse error): when
// repos.yaml itself failed to parse, patternsFor falls back to
// DefaultPatterns. Whatever that fallback then finds or fails to find is a
// consequence of the parse error, so reporting no-entities as well is exactly
// the "two diagnostics for one cause" that patternsFor already suppresses
// default-patterns to avoid — and no-entities' hint tells the reader to add a
// repos.yaml that is sitting right there.
//
// TestValidateMalformedReposYAMLIsNotSilent above cannot catch this: its
// service.yaml sits under services/*, which the fallback happens to match.
func TestValidateSuppressesNoEntitiesWhenReposYAMLFailedToParse(t *testing.T) {
	repo := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		// Deliberately under no DefaultPatterns glob, so the fallback finds
		// nothing — precisely when the spurious no-entities appeared.
		"svc/api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
				"  owner: team-a\n  tier: 1\n  lifecycle: production\n")},
	}
	var out, errOut bytes.Buffer
	code := Validate(repo, &out, &errOut, diag.JSON{}, false)
	if code != exitValidation {
		t.Fatalf("exit code = %d, want %d", code, exitValidation)
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("stdout must be valid JSON: %v", err)
	}
	var parse, entities *diag.Diagnostic
	for i := range ds {
		switch ds[i].Check {
		case "repos-parse":
			parse = &ds[i]
		case "no-entities":
			entities = &ds[i]
		}
	}
	if parse == nil {
		t.Fatalf("no repos-parse diagnostic in %+v", ds)
	}
	want := "cannot parse repos file: yaml: line 2: mapping values are not allowed in this context"
	if parse.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", parse.Message, want)
	}
	if entities != nil {
		t.Errorf("no-entities must be suppressed when repos.yaml failed to parse: "+
			"it is a second diagnostic for one cause, and its hint (%q) names a file that already exists",
			entities.Hint)
	}
}

// Ruling R49's third leg. R43 is a claim about validate, gen and score
// agreeing on one directory, and R49 was diagnosed from validate reporting
// owners-skipped where gen and score did not — so validate is the reference
// the other two were corrected against, and until this test it was the only
// leg nothing pinned. checkOwners calls ValidateOwners unconditionally, which
// is why validate was right by construction; an early return added there for
// an empty catalog would reopen the disagreement in the opposite direction
// with the rest of the suite green.
//
// The fixture is the true trigger, not the one R49's first draft named: no
// repos.yaml at all, and the only service.yaml is unparseable. The catalog is
// empty because everything found failed to parse, which is ruling R42's own
// defect note N5. The gen and score siblings use the repos.yaml variant; both
// shapes reach the same empty-catalog branch.
func TestValidateReportsOwnersSkippedWhenEveryServiceYAMLFailedToParse(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml":                {Data: []byte("teams:\n  - name: platform\n   slack: \"#x\"\n")},
		"services/api/service.yaml": {Data: []byte("apiVersion: landsraad/v1\nkind: Service\n  bad: indent\n")},
	}
	var out, errOut bytes.Buffer

	code := Validate(repo, &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	want := "info: repos.yaml:1 [default-patterns]\n" +
		"  no repos.yaml found; using default paths (., services/*, workers/*, libs/*, topics/*)\n" +
		"  hint: add repos.yaml if your services live elsewhere\n" +
		"error: services/api/service.yaml:3 [yaml-parse]\n" +
		"  cannot parse YAML: yaml: line 3: mapping values are not allowed in this context\n" +
		"info: teams.yaml:1 [owners-skipped]\n" +
		"  owner validation skipped: teams.yaml did not parse\n" +
		"  hint: no owner in this repository has been checked; fix the parse error in teams.yaml and rerun\n" +
		"error: teams.yaml:1 [teams-parse]\n" +
		"  cannot parse teams file: yaml: line 1: did not find expected '-' indicator\n" +
		"  hint: teams.yaml is a list under `teams:` with name, members, slack and pagerduty\n"
	if out.String() != want {
		t.Errorf("stdout\n got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// The schema/parse pairing: schema.Validate deliberately returns false with
// ZERO diagnostics when the bytes are not YAML at all (that is
// catalog.ParseFile's diagnostic to make). This is only safe if Validate's
// composition runs both stages over the same bytes — if a file could reach
// the validator without also reaching the parser, malformed YAML would be
// silently accepted with no diagnostic at all.
func TestValidateRunsSchemaAndParseOverSameBytes(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml":                   {Data: []byte("teams:\n  - name: team-a\n")},
		"services/broken/service.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
	}
	code, out, _ := validate(repo)
	if code != exitValidation {
		t.Fatalf("malformed YAML must fail validation, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "cannot parse YAML") {
		t.Errorf("the parser's diagnostic must be present — a malformed file must never be silently accepted:\n%s", out)
	}
}

// Both stages decoded only the first document, so a second entity in the same
// file — the Kubernetes habit this schema invites — was never looked at and
// the run still reported a clean pass. The whole file is rejected instead.
func TestValidateRejectsAMultiDocumentFile(t *testing.T) {
	repo := fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-a\n")},
		"services/api/service.yaml": {Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  owner: team-a
  tier: 1
  lifecycle: production
---
apiVersion: landsraad/v1
kind: Topic
metadata:
  name: NOT_A_LEGAL_NAME
  owner: no-such-team
  lifecycle: production
`)},
	}
	code, out, errOut := validate(repo)
	if code != exitValidation {
		t.Fatalf("a multi-document file must fail validation, got exit %d:\n%s%s", code, out, errOut)
	}
	if !strings.Contains(out, "service.yaml must contain exactly one document, found 2") {
		t.Errorf("the run must say why it rejected the file:\n%s", out)
	}
	if strings.Contains(errOut, "no problems found") {
		t.Errorf("a file whose second half was never validated must not report a clean pass:\n%s", errOut)
	}
}

// The present-but-empty half of the degraded mode, which used to be silent
// while the absent-file half announced itself.
func TestPatternsForAnnouncesDefaultsWhenReposYAMLListsNoPaths(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte("repos:\n  - url: https://x/y\n    paths: []\n")},
	}
	var c diag.Collector
	got, known := patternsFor(fsys, &c)
	// Present and parsed, just empty: the file was read, so what these
	// patterns match is still worth reporting (defect 4).
	if !known {
		t.Error("a parsed repos.yaml listing no paths must still yield known patterns")
	}
	if len(got) != len(config.DefaultPatterns()) {
		t.Fatalf("patternsFor = %v, want DefaultPatterns %v", got, config.DefaultPatterns())
	}
	if c.Len() != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %+v", c.Len(), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Check != "default-patterns" {
		t.Errorf("Check = %q, want %q", d.Check, "default-patterns")
	}
	want := "repos.yaml names no paths; using default paths (., services/*, workers/*, libs/*, topics/*)"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// A malformed repos.yaml gets exactly one diagnostic: the parse error. The
// fallback to DefaultPatterns is a consequence of it, not a second finding.
func TestPatternsForDoesNotStackANoteOnAParseError(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte("kind: Service\n  bad: indent\n")},
	}
	var c diag.Collector
	patternsFor(fsys, &c)
	if c.Len() != 1 {
		t.Fatalf("expected exactly one diagnostic for one cause, got %d: %+v", c.Len(), c.Diagnostics())
	}
}

// Regression: a multi-entry repos.yaml with no entry marked local: true used
// to pick the first entry silently, which is exactly what stamped a banner
// naming the wrong repositories into every page of a generated site (see
// build.go's partial-notice tests). patternsFor must say so instead of
// picking silently, and the exact wording — including that %s really does
// interpolate the first entry's Identity(), not its raw URL — is pinned
// here rather than left to a review to notice a regression in.
func TestPatternsForWarnsWhenNoEntryIsMarkedLocal(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml": {Data: []byte(
			"repos:\n" +
				"  - url: https://github.com/org/monorepo\n    paths: [services/*]\n" +
				"  - url: https://github.com/org/edge\n    paths: [.]\n")},
	}
	var c diag.Collector
	got, known := patternsFor(fsys, &c)
	if !known {
		t.Error("a parsed repos.yaml must yield known patterns")
	}
	want := []string{"services/*"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("patternsFor = %v, want %v", got, want)
	}
	if c.Len() != 1 {
		t.Fatalf("expected exactly one diagnostic, got %d: %+v", c.Len(), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn — an assumption is not an error", d.Severity)
	}
	if d.Check != "repos-local-assumed" {
		t.Errorf("Check = %q, want %q", d.Check, "repos-local-assumed")
	}
	if d.File != "repos.yaml" {
		t.Errorf("File = %q, want %q", d.File, "repos.yaml")
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1", d.Line)
	}
	want2 := `no entry in repos.yaml is marked local: true, so the first (monorepo) is assumed to be this repository`
	if d.Message != want2 {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want2)
	}
	wantHint := "add `local: true` to the entry for the repository you are standing in"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// "1 entities validated" is the most-read line the tool prints.
func TestPluralRendersTheRightNoun(t *testing.T) {
	for _, tc := range []struct {
		n    int
		want string
	}{{0, "0 entities"}, {1, "1 entity"}, {2, "2 entities"}} {
		if got := plural(tc.n, "entity", "entities"); got != tc.want {
			t.Errorf("plural(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// Ruling R8, closing the gap spec §7.1 flagged: until Plan 2 shipped the
// CheckResults schema, a malformed results file was first caught by the
// platform build rather than by the PR that introduced it. That is the
// exit-0-over-something-unexamined shape this project keeps finding.
func TestValidateRejectsAMalformedCheckResultsFile(t *testing.T) {
	fsys := genFS()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: not-a-date\nresults:\n  - { entity: service:api, check: x, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText(), false); code != exitValidation {
		t.Fatalf("exit = %d, want %d — a malformed results file must fail the PR", code, exitValidation)
	}
}

// validate stays hermetic: it checks the document's shape and says nothing
// about whether the entities exist or which producer wins. Those need the
// merged catalog and a clock, and belong to score.
func TestValidateDoesNotResolveCheckResultEntities(t *testing.T) {
	fsys := genFS()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:ghost, check: x, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText(), false); code != exitOK {
		t.Fatalf("exit = %d, want %d — a well-formed file naming an unknown entity is score's problem, not validate's; stderr:\n%s",
			code, exitOK, errOut.String())
	}
}

func TestValidateAcceptsAWellFormedCheckResultsFile(t *testing.T) {
	fsys := genFS()
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")}

	var out, errOut bytes.Buffer
	if code := Validate(fsys, &out, &errOut, diagText(), false); code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
}

// failPathFS is a MapFS on which one path cannot be read: ReadDir and
// ReadFile of it fail with a permission error, the shape os.DirFS gives
// without depending on the test process's own permissions. Byte-identical
// to internal/scorecard/ingest_test.go's failPathFS; compare also render's
// failFS, which overrides ReadFile+Stat instead of ReadDir+ReadFile.
type failPathFS struct {
	fstest.MapFS
	path string
}

func (f failPathFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadDir(name)
}

func (f failPathFS) ReadFile(name string) ([]byte, error) {
	if name == f.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return f.MapFS.ReadFile(name)
}

// A .landsraad/checks that cannot be read used to validate clean.
// validateCheckResults treated every ReadDir error as "no directory", so a
// PR could break the directory and still ship green, and a results file it
// could not read was reported without saying why.
func TestValidateReportsUnreadableCheckResults(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		want       diag.Diagnostic
	}{
		{
			name: "directory", path: ".landsraad/checks",
			want: diag.Diagnostic{
				Severity: diag.SevError, File: ".landsraad/checks", Line: 1,
				Check:   "checks-unreadable",
				Message: "cannot read .landsraad/checks: readdir .landsraad/checks: permission denied",
			},
		},
		{
			name: "file", path: ".landsraad/checks/scan.yaml",
			want: diag.Diagnostic{
				Severity: diag.SevError, File: ".landsraad/checks/scan.yaml", Line: 1,
				Check:   "checks-unreadable",
				Message: "cannot read .landsraad/checks/scan.yaml: open .landsraad/checks/scan.yaml: permission denied",
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			files := genFS()
			files[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
				"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/x\ngeneratedAt: 2026-09-08T14:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")}

			var out, errOut bytes.Buffer
			code := Validate(failPathFS{MapFS: files, path: tt.path}, &out, &errOut, diag.JSON{}, false)
			if code != exitValidation {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
			}
			var ds []diag.Diagnostic
			if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
				t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
			}
			if diff := cmp.Diff([]diag.Diagnostic{tt.want}, ds); diff != "" {
				t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Carry-forward from Task 4/12: both the service.yaml schema-validate loop
// and validateCheckResults used to hardcode Validate("", ...), so a
// diagnostic never said which repository it came from once a build read
// more than one. diag.Text.Write does not render Repo at all, so the exact-
// message text was untouched by the bug and by this fix; JSON is the only
// format that carries the field, so this test decodes it directly rather
// than pattern-matching stderr.
func TestValidateThreadsTheRepoNameIntoSchemaDiagnostics(t *testing.T) {
	fsys := genFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte("apiVersion: landsraad/v1\nkind: Service\n")}
	fsys[".landsraad/checks/scan.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\ngeneratedAt: not-a-date\nresults:\n  - { entity: service:api, check: x, status: pass }\n")}

	var out, errOut bytes.Buffer
	Validate(fsys, &out, &errOut, diag.JSON{}, false)

	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("stdout must be valid JSON: %v; stderr:\n%s", err, errOut.String())
	}

	var sawServiceSchema, sawChecksSchema bool
	for _, d := range ds {
		if d.Check != "schema" {
			continue
		}
		switch d.File {
		case "services/api/service.yaml":
			sawServiceSchema = true
			if d.Repo != "monorepo" {
				t.Errorf("service.yaml schema diagnostic Repo = %q, want %q", d.Repo, "monorepo")
			}
		case ".landsraad/checks/scan.yaml":
			sawChecksSchema = true
			if d.Repo != "monorepo" {
				t.Errorf("checks-file schema diagnostic Repo = %q, want %q", d.Repo, "monorepo")
			}
		}
	}
	if !sawServiceSchema {
		t.Fatalf("expected a schema diagnostic for services/api/service.yaml, got %+v", ds)
	}
	if !sawChecksSchema {
		t.Fatalf("expected a schema diagnostic for .landsraad/checks/scan.yaml, got %+v", ds)
	}
}

// TestValidateToleratesTheFixturesCrossRepoRef is the validate half of the
// claim Task 15's commit made about testdata/multirepo: edge-gateway's
// spec.dependsOn names service:api, which is defined only in platform/. Spec
// §7.1 says validate (LocalOnly) must not report that as dangling, because
// the target may simply live in another repo. TestBuildMergesALocalAndARemoteRepository
// is the other half: build (FullCatalog) resolves the very same reference.
//
// TestValidateToleratesCrossRepoRefs already covers this behavior against a
// synthetic single-field fixture built to isolate it. This test runs the
// real thing: `landsraad validate` against testdata/multirepo/edge-gateway
// exactly as fetched, so the fixture's own commit message is not asserting
// something nothing actually exercises.
//
// It does NOT exit 0. edge-gateway, read on its own, carries no teams.yaml —
// only the platform root does (ruling R34) — so without --satellite this is
// missing-teams. Its own CI passes --satellite (ruling R37), which
// TestValidateSatelliteDefersOwnersToThePlatformBuild covers. The failure is
// unrelated and expected, and asserting it here — rather than picking a
// repo-less fixture that would hide it — is what proves the *only*
// diagnostic in play is the one about ownership, and specifically not one
// about the cross-repo ref.
func TestValidateToleratesTheFixturesCrossRepoRef(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/multirepo/edge-gateway"), &out, &errOut, diag.JSON{}, false)
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d (missing-teams, unrelated to the cross-repo ref); out:\n%s", code, exitValidation, out.String())
	}

	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("out is not valid diagnostics JSON: %v\n%s", err, out.String())
	}
	if len(ds) != 2 {
		t.Fatalf("got %d diagnostics, want exactly 2 (default-patterns, missing-teams): %+v", len(ds), ds)
	}
	var sawDefaultPatterns, sawMissingTeams bool
	for _, d := range ds {
		switch d.Check {
		case "default-patterns":
			sawDefaultPatterns = true
		case "missing-teams":
			sawMissingTeams = true
			if d.Severity != diag.SevError {
				t.Errorf("missing-teams Severity = %v, want SevError", d.Severity)
			}
		case "dangling-ref":
			t.Errorf("the cross-repo dependsOn must not be reported as dangling under validate: %+v", d)
		default:
			t.Errorf("unexpected diagnostic %+v — the cross-repo ref must be the only thing tolerated silently", d)
		}
	}
	if !sawDefaultPatterns {
		t.Error("expected a default-patterns note: edge-gateway carries no repos.yaml of its own")
	}
	if !sawMissingTeams {
		t.Error("expected missing-teams: edge-gateway carries no teams.yaml of its own")
	}
}

// Ruling R36: a bad paths: entry is a mistake in a file the user wrote, so it
// is a diagnostic at its line and exit 2. validate used to exit 1 for it,
// through discover.Find's error, while exiting 2 for a bad url: in the same
// file. The entry falls back to the default paths without a second word:
// the rejection is the diagnostic.
func TestValidateReportsARejectedPathPatternAtItsLine(t *testing.T) {
	fsys := genFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [/services/*]\n")}

	var out, errOut bytes.Buffer
	code := Validate(fsys, &out, &errOut, diag.JSON{}, false)
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
	}
	want := []diag.Diagnostic{{
		Severity: diag.SevError, File: "repos.yaml", Line: 2,
		Check:   "repos-path",
		Message: `path pattern "/services/*" must not be absolute; write a path relative to the repository root, for example "services/*"`,
		Hint:    "paths: are globs relative to the repository root, such as services/*",
	}}
	if diff := cmp.Diff(want, ds); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// Ruling R37. A satellite's own CI has no teams.yaml to resolve owners
// against — ruling R34 keeps it in the platform repository — so validate
// failed every satellite's PR on missing-teams. --satellite leaves owners to
// the platform build, and says so, so the skipped check is visible rather
// than silent.
func TestValidateSatelliteDefersOwnersToThePlatformBuild(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/multirepo/edge-gateway"), &out, &errOut, diag.JSON{}, true)
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; out:\n%s\nstderr:\n%s", code, exitOK, out.String(), errOut.String())
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("out is not diagnostics JSON: %v\n%s", err, out.String())
	}
	want := []diag.Diagnostic{
		{
			Severity: diag.SevInfo, File: "repos.yaml", Line: 1,
			Check:   "default-patterns",
			Message: "no repos.yaml found; using default paths (., services/*, workers/*, libs/*, topics/*)",
			Hint:    "add repos.yaml if your services live elsewhere",
		},
		{
			Severity: diag.SevInfo, File: "teams.yaml", Line: 1,
			Check:   "owners-deferred",
			Message: "owners are not checked in a satellite repository; the platform build resolves them against its teams.yaml",
		},
	}
	if diff := cmp.Diff(want, ds); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// Refusing --satellite beside a teams.yaml is the reversible choice (R37): it
// can be relaxed later, where accepting it could never be tightened, and it
// stops a platform repository switching off its own owner checks by copying
// a satellite's CI configuration.
func TestValidateSatelliteRefusesARepositoryWithATeamsFile(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(genFS(), &out, &errOut, diag.Text{}, true)
	if code != exitUsage {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitUsage, errOut.String())
	}
	want := "error: --satellite skips owner checks, but this repository has a teams.yaml; " +
		"drop the flag, or delete the file if the platform repository's teams.yaml is the real one\n"
	if got := errOut.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing: a refusal has no diagnostics to format", out.String())
	}
}

// Ruling R42. Since Plan 4, assemble returned on an empty catalog before it
// read teams.yaml, so a run where every service.yaml failed to parse hid
// every teams.yaml problem too, and the user met them one run later. The
// catalog is still empty; teams.yaml is still read.
func TestLoadCatalogStillReportsTeamsWhenEveryServiceFailsToParse(t *testing.T) {
	fsys := genFS()
	delete(fsys, "teams.yaml")
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte("apiVersion: [unterminated\n")}
	var c diag.Collector

	loadCatalog(fsys, &c)

	// Collector.Diagnostics sorts by file, so services/ comes before teams.yaml.
	want := []diag.Diagnostic{
		{
			Severity: diag.SevError, Repo: "monorepo", File: "services/api/service.yaml", Line: 1,
			Check:   "yaml-parse",
			Message: "cannot parse YAML: yaml: line 1: did not find expected ',' or ']'",
		},
		{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		},
	}
	if diff := cmp.Diff(want, c.Diagnostics()); diff != "" {
		t.Errorf("diagnostics mismatch (-want +got):\n%s", diff)
	}
}

// Ruling R39: validate, gen and score name the repository they stand in by
// the entry LocalPatterns reads its paths from — the one marked local: true,
// or else the first — and by that entry's Identity(), as build does.
// localRepoName used to take the first entry's url basename whatever local:
// and name: said, and kept a trailing .git that Identity() trims.
func TestLocalRepoNameIsTheLocalEntrysIdentity(t *testing.T) {
	for _, tt := range []struct{ name, reposYAML, want string }{
		{"no repos.yaml", "", ""},
		{"sole entry", "repos:\n  - url: https://github.com/org/monorepo\n", "monorepo"},
		{"sole entry with .git", "repos:\n  - url: https://github.com/org/monorepo.git\n", "monorepo"},
		{"local entry is not first",
			"repos:\n  - url: https://github.com/org/edge-gateway\n  - url: https://github.com/org/platform\n    local: true\n",
			"platform"},
		{"name: wins over the url",
			"repos:\n  - url: https://github.com/org/platform\n    local: true\n    name: core\n",
			"core"},
		{"none marked: the first, as LocalPatterns assumes",
			"repos:\n  - url: https://github.com/org/platform\n  - url: https://github.com/org/edge-gateway\n",
			"platform"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			if tt.reposYAML != "" {
				fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(tt.reposYAML)}
			}
			if got := localRepoName(fsys); got != tt.want {
				t.Errorf("localRepoName = %q, want %q", got, tt.want)
			}
		})
	}
}
