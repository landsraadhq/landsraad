package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// binPath is the landsraad binary these tests exercise as a real subprocess.
// Some behavior under test — cobra flag validation, the process exit code —
// lives only in newXCmd's RunE, which calls os.Exit directly and so cannot
// be exercised by calling Gen/Score/Validate in-process: that would end the
// test binary. Built once in TestMain rather than per-test.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "landsraad-integration-*")
	if err != nil {
		panic(err)
	}
	binPath = filepath.Join(dir, "landsraad")
	build := exec.Command("go", "build", "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("building landsraad for integration tests: " + err.Error() + "\n" + string(out))
	}

	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type runResult struct {
	stdout, stderr string
	exitCode       int
}

// run executes the built binary with dir as its working directory, the way
// a user invokes it from inside their repository.
func run(t *testing.T, dir string, args ...string) runResult {
	t.Helper()
	cmd := exec.Command(binPath, args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("running landsraad %v: %v", args, err)
		}
	}
	return runResult{stdout: out.String(), stderr: errOut.String(), exitCode: exitCode}
}

// materialize writes files (path -> content) under a fresh temp directory
// and returns its path.
func materialize(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// integrationFixture is one tier-1 service with no runbook, SLO or alerts —
// enough to exercise hermetic checks, ownership resolution and generation,
// while leaving every required check failing so score's gate has something
// to report.
func integrationFixture() map[string]string {
	return map[string]string{
		"teams.yaml": "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n",
		"repos.yaml": "repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n",
		"services/api/service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: services/api
`,
	}
}

// TestIntegrationGenValidateAndScoreChainOnARealCheckout exercises Phase 1
// (validate) and Phase 2 (gen, score) together against one real checkout:
// gen's artifacts are written to disk, then validate and gen --check read
// them back, then score runs against the same tree. Unit tests cover each
// function against an in-memory fs.FS; nothing else runs them in sequence
// against a real filesystem the way a user's CI job does.
func TestIntegrationGenValidateAndScoreChainOnARealCheckout(t *testing.T) {
	dir := materialize(t, integrationFixture())

	gen := run(t, dir, "gen")
	if gen.exitCode != exitOK {
		t.Fatalf("gen exit = %d, want %d; stderr:\n%s", gen.exitCode, exitOK, gen.stderr)
	}
	for _, f := range []string{"CODEOWNERS", "alertmanager-routes.yaml", "slack-channels.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("gen did not write %s: %v", f, err)
		}
	}

	val := run(t, dir, "validate")
	if val.exitCode != exitOK {
		t.Fatalf("validate exit = %d, want %d after gen; stdout:\n%s", val.exitCode, exitOK, val.stdout)
	}

	check := run(t, dir, "gen", "--check")
	if check.exitCode != exitOK {
		t.Fatalf("gen --check exit = %d, want %d immediately after gen; stderr:\n%s", check.exitCode, exitOK, check.stderr)
	}

	score := run(t, dir, "score")
	if score.exitCode != exitScorecard {
		t.Fatalf("score exit = %d, want %d (this fixture has no runbook, SLO or alerts); stderr:\n%s", score.exitCode, exitScorecard, score.stderr)
	}
}

// TestIntegrationGenCheckDetectsAHandEditedArtifact locks in the anti-drift
// gate itself: a generated file edited by hand (or by a stale commit) must
// fail --check rather than silently being trusted.
func TestIntegrationGenCheckDetectsAHandEditedArtifact(t *testing.T) {
	dir := materialize(t, integrationFixture())
	if r := run(t, dir, "gen"); r.exitCode != exitOK {
		t.Fatalf("gen exit = %d, want %d; stderr:\n%s", r.exitCode, exitOK, r.stderr)
	}

	codeowners := filepath.Join(dir, "CODEOWNERS")
	data, err := os.ReadFile(codeowners)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codeowners, append(data, []byte("\n# hand-edited drift\n")...), 0o644); err != nil {
		t.Fatal(err)
	}

	check := run(t, dir, "gen", "--check")
	if check.exitCode != exitValidation {
		t.Fatalf("gen --check exit = %d, want %d after a hand edit; stderr:\n%s", check.exitCode, exitValidation, check.stderr)
	}
	if !strings.Contains(check.stderr, "stale: CODEOWNERS is out of date") {
		t.Errorf("gen --check stderr must name the stale file, got %q", check.stderr)
	}
}

// TestIntegrationEmptyMatchingRepoGlobRefusesGenAndScore locks in the
// Critical fix from the whole-branch review: a repos.yaml whose paths match
// no service.yaml at all used to produce a valid-looking empty catalog,
// which gen --check then certified as up to date forever and score treated
// as zero entities to grade. Both must now refuse instead.
func TestIntegrationEmptyMatchingRepoGlobRefusesGenAndScore(t *testing.T) {
	dir := materialize(t, map[string]string{
		"teams.yaml": "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n",
		"repos.yaml": "repos:\n  - url: https://github.com/org/monorepo\n    paths: [nonexistent-glob/*]\n",
	})

	gen := run(t, dir, "gen", "--check")
	if gen.exitCode != exitValidation {
		t.Fatalf("gen --check exit = %d, want %d against a repos.yaml matching nothing; stdout:\n%s", gen.exitCode, exitValidation, gen.stdout)
	}
	if !strings.Contains(gen.stdout, "no-entities") {
		t.Errorf("gen --check must report no-entities, got stdout %q", gen.stdout)
	}

	score := run(t, dir, "score")
	if score.exitCode != exitValidation {
		t.Fatalf("score exit = %d, want %d against a repos.yaml matching nothing; stdout:\n%s", score.exitCode, exitValidation, score.stdout)
	}
	if !strings.Contains(score.stdout, "no-entities") {
		t.Errorf("score must report no-entities, got stdout %q", score.stdout)
	}
}

// TestIntegrationTwoProducersReportingTheSameCheckAtTheSameTimeRefusesToScore
// locks in the Important fix that made computeScore re-check c.HasErrors()
// after Ingest runs: a genuine tie between two producers must gate the exit
// code rather than silently resolving to whichever file sorts last.
func TestIntegrationTwoProducersReportingTheSameCheckAtTheSameTimeRefusesToScore(t *testing.T) {
	dir := materialize(t, integrationFixture())
	writeCheckResults(t, dir, "ci-a.yaml", `apiVersion: landsraad/v1
kind: CheckResults
producer: ci/smoke-test
generatedAt: 2026-09-09T22:02:00Z
results:
  - entity: service:api
    check: dashboard-resolves
    status: pass
`)
	writeCheckResults(t, dir, "ci-b.yaml", `apiVersion: landsraad/v1
kind: CheckResults
producer: ci/other-pipeline
generatedAt: 2026-09-09T22:02:00Z
results:
  - entity: service:api
    check: dashboard-resolves
    status: fail
    detail: "conflicting report from a second producer"
`)

	r := run(t, dir, "score")
	if r.exitCode != exitValidation {
		t.Fatalf("score exit = %d, want %d when two producers tie; stdout:\n%s", r.exitCode, exitValidation, r.stdout)
	}
	if !strings.Contains(r.stdout, "[checks-tie]") {
		t.Errorf("score must report checks-tie, got stdout %q", r.stdout)
	}
	want := `producers "ci/smoke-test" and "ci/other-pipeline" both report dashboard-resolves for service:api at 2026-09-09T22:02:00Z, so neither can win`
	if !strings.Contains(r.stdout, want) {
		t.Errorf("score stdout must explain the tie exactly, got %q", r.stdout)
	}
}

// writeCheckResults writes one .landsraad/checks file under dir.
func writeCheckResults(t *testing.T, dir, name, content string) {
	t.Helper()
	checksDir := filepath.Join(dir, ".landsraad", "checks")
	if err := os.MkdirAll(checksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checksDir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scoreJSON mirrors the payload writeScoreJSON produces — only the fields
// these tests need.
type scoreJSON struct {
	Entities []struct {
		Ref        string `json:"ref"`
		Applicable int    `json:"applicable"`
		Results    []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"results"`
	} `json:"entities"`
}

// scoreJSONOf parses r.stdout as a score --format json payload. Parsing it
// through encoding/json rather than matching substrings doubles as a
// stream-purity check: any stray human-readable text on stdout breaks the
// unmarshal.
func scoreJSONOf(t *testing.T, r runResult) scoreJSON {
	t.Helper()
	var doc scoreJSON
	if err := json.Unmarshal([]byte(r.stdout), &doc); err != nil {
		t.Fatalf("stdout is not valid score JSON: %v\nstdout:\n%s", err, r.stdout)
	}
	return doc
}

func findResult(t *testing.T, doc scoreJSON, ref, check string) (status, detail string) {
	t.Helper()
	for _, e := range doc.Entities {
		if e.Ref != ref {
			continue
		}
		for _, r := range e.Results {
			if r.Check == check {
				return r.Status, r.Detail
			}
		}
	}
	t.Fatalf("no result for check %q on %q", check, ref)
	return "", ""
}

func applicableFor(t *testing.T, doc scoreJSON, ref string) int {
	t.Helper()
	for _, e := range doc.Entities {
		if e.Ref == ref {
			return e.Applicable
		}
	}
	t.Fatalf("no entity %q", ref)
	return 0
}

// TestIntegrationIngestedCheckResultOverridesNotReported chains gen's
// sibling half of the pipeline: an external check with no reported result
// renders not-reported (Ruling R3), and a real .landsraad/checks file
// written to disk flips it, exactly as CI reporting in is meant to.
func TestIntegrationIngestedCheckResultOverridesNotReported(t *testing.T) {
	dir := materialize(t, integrationFixture())

	before := scoreJSONOf(t, run(t, dir, "score", "--format", "json"))
	status, detail := findResult(t, before, "service:api", "dashboard-resolves")
	if status != "not-reported" {
		t.Fatalf("baseline dashboard-resolves status = %q, want not-reported", status)
	}
	if detail != "no result reported in .landsraad/checks" {
		t.Fatalf("baseline dashboard-resolves detail = %q", detail)
	}

	writeCheckResults(t, dir, "ci.yaml", `apiVersion: landsraad/v1
kind: CheckResults
producer: ci/smoke-test
generatedAt: 2026-09-09T22:02:00Z
results:
  - entity: service:api
    check: dashboard-resolves
    status: pass
`)

	after := scoreJSONOf(t, run(t, dir, "score", "--format", "json"))
	status, _ = findResult(t, after, "service:api", "dashboard-resolves")
	if status != "pass" {
		t.Fatalf("after ingest, dashboard-resolves status = %q, want pass", status)
	}
}

// serviceWithExemption returns integrationFixture with an exemption on
// slo-defined, valid until the given date (YYYY-MM-DD).
func serviceWithExemption(until string) map[string]string {
	f := integrationFixture()
	f["services/api/service.yaml"] = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: services/api
  exemptions:
    - check: slo-defined
      reason: "SLO tracked externally, migrating in Q1"
      until: "` + until + `"
`
	return f
}

// TestIntegrationInForceExemptionRemovesCheckFromDenominator locks in
// Ruling R4's first half: an in-force exemption removes the check from the
// denominator, not just from the failing list.
func TestIntegrationInForceExemptionRemovesCheckFromDenominator(t *testing.T) {
	baseline := scoreJSONOf(t, run(t, materialize(t, integrationFixture()), "score", "--format", "json"))
	baseApplicable := applicableFor(t, baseline, "service:api")

	exempt := scoreJSONOf(t, run(t, materialize(t, serviceWithExemption("2027-01-01")), "score", "--format", "json"))
	status, detail := findResult(t, exempt, "service:api", "slo-defined")
	if status != "exempt" {
		t.Fatalf("slo-defined status = %q, want exempt", status)
	}
	if want := "exempt until 2027-01-01: SLO tracked externally, migrating in Q1"; detail != want {
		t.Errorf("slo-defined detail = %q, want %q", detail, want)
	}
	if got, want := applicableFor(t, exempt, "service:api"), baseApplicable-1; got != want {
		t.Errorf("applicable = %d, want %d (an in-force exemption removes the check from the denominator)", got, want)
	}
}

// TestIntegrationExpiredExemptionDoesNotShrinkDenominatorAndWarns locks in
// Ruling R4's second half: an exemption that has lapsed must not waive
// anything, or the dataset the whole scorecard rests on can be gamed by
// exemptions nobody renews.
func TestIntegrationExpiredExemptionDoesNotShrinkDenominatorAndWarns(t *testing.T) {
	baseline := scoreJSONOf(t, run(t, materialize(t, integrationFixture()), "score", "--format", "json"))
	baseApplicable := applicableFor(t, baseline, "service:api")

	dir := materialize(t, serviceWithExemption("2020-01-01"))
	r := run(t, dir, "score", "--format", "json")
	doc := scoreJSONOf(t, r)

	if status, _ := findResult(t, doc, "service:api", "slo-defined"); status == "exempt" {
		t.Error("an expired exemption must not waive the check")
	}
	if got := applicableFor(t, doc, "service:api"); got != baseApplicable {
		t.Errorf("applicable = %d, want %d — an expired exemption must not shrink the denominator", got, baseApplicable)
	}
	wantWarn := "warn: exemption for slo-defined on service:api expired on 2020-01-01 and no longer waives anything"
	if !strings.Contains(r.stderr, wantWarn) {
		t.Errorf("stderr must warn about the expired exemption, got %q", r.stderr)
	}
}

// TestIntegrationScoreFormatJSONStaysPureOnTheBrokenCatalogPath locks in the
// Important fix where newScoreCmd used to hardcode the text formatter
// regardless of --format: on the broken-catalog path, stdout must still be
// nothing but the JSON payload.
func TestIntegrationScoreFormatJSONStaysPureOnTheBrokenCatalogPath(t *testing.T) {
	dir := materialize(t, map[string]string{
		"teams.yaml": "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n",
		"repos.yaml": "repos:\n  - url: https://github.com/org/monorepo\n    paths: [nonexistent-glob/*]\n",
	})

	r := run(t, dir, "score", "--format", "json")
	if r.exitCode != exitValidation {
		t.Fatalf("score exit = %d, want %d; stderr:\n%s", r.exitCode, exitValidation, r.stderr)
	}
	var payload []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &payload); err != nil {
		t.Fatalf("stdout is not pure JSON: %v\nstdout:\n%s", err, r.stdout)
	}
	if !strings.Contains(r.stderr, "refusing to score") {
		t.Errorf("stderr must explain the refusal, got %q", r.stderr)
	}
}

// TestIntegrationScoreRejectsAnUnknownFormat locks in the Important fix that
// added --format validation to newScoreCmd; before it, an unrecognised
// value silently fell back to a formatter chosen from the wrong condition.
func TestIntegrationScoreRejectsAnUnknownFormat(t *testing.T) {
	dir := materialize(t, integrationFixture())
	r := run(t, dir, "score", "--format", "bogus")
	if r.exitCode != exitUsage {
		t.Fatalf("score --format bogus exit = %d, want %d; stderr:\n%s", r.exitCode, exitUsage, r.stderr)
	}
	want := `Error: --format must be text or json, got "bogus"`
	if !strings.Contains(r.stderr, want) {
		t.Errorf("stderr = %q, want to contain %q", r.stderr, want)
	}
}
