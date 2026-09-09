package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// validate is the shared harness. out carries only the format payload;
// errOut carries the human lines. Keeping them separate in the tests is what
// stops the two being conflated in the implementation.
func validate(fsys fstest.MapFS) (code int, out, errOut string) {
	var o, e bytes.Buffer
	c := Validate(fsys, &o, &e, diag.Text{})
	return c, o.String(), e.String()
}

func TestValidateOnGoodRepoExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.Text{})
	if code != exitOK {
		t.Errorf("exit code = %d, want %d\nout:\n%s\nerr:\n%s", code, exitOK, out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "ok: 3 entities validated") {
		t.Errorf("expected 3 entities (2 services + 1 topic), got:\n%s", errOut.String())
	}
}

func TestValidateOnBrokenRepoExitsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{}); code != exitValidation {
		t.Errorf("exit code = %d, want %d", code, exitValidation)
	}
}

// The point of the collector: one run reports every problem, not the first.
func TestValidateReportsAllProblemsAtOnce(t *testing.T) {
	var out, errOut bytes.Buffer
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{})
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
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.Text{})
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
	code := Validate(os.DirFS("../../testdata/monorepo-ok"), &out, &errOut, diag.JSON{})
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
	Validate(os.DirFS("../../testdata/monorepo-broken"), &out, &errOut, diag.JSON{})
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
	got := patternsFor(fstest.MapFS{}, &c)
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
	got := patternsFor(fsys, &c)
	if !c.HasErrors() {
		t.Fatal("malformed repos.yaml must be an error, not a silent fallback to defaults")
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
	want := "teams.yaml not found at the repository root"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "every entity's owner must resolve to a team defined there"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
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
	code := Validate(repo, &out, &errOut, diag.JSON{})
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
	got := patternsFor(fsys, &c)
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
	want := "repos.yaml lists no paths; using default paths (., services/*, workers/*, libs/*, topics/*)"
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
