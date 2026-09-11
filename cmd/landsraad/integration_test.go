package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/render"
)

// runTimeout bounds every subprocess run() starts. Two tests -- both
// exercising serve without --addr, so the flag default localhost:8080
// applies -- only pass because serve is expected to refuse and exit before
// ever binding that port. If that gate regresses, the child blocks in
// ListenAndServe forever: 60s turns that into a fast, precisely-attributed
// test failure instead of a `go test -timeout` panic that does not reap the
// child, leaving an orphaned listener on a real port between CI runs. Every
// call this suite makes today finishes in milliseconds.
const runTimeout = 60 * time.Second

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
//
// Bounded by runTimeout via exec.CommandContext: when the deadline passes,
// the context kills the child and Run still returns (it calls Wait
// internally), so the process is reaped rather than orphaned.
func run(t *testing.T, dir string, args ...string) runResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("running landsraad %v: killed after %s without exiting; stdout:\n%s\nstderr:\n%s",
			args, runTimeout, out.String(), errOut.String())
	}
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
	// The whole diagnostic, not just its first line: score now renders through
	// diag.Text, so the file, the line, the check name and the hint reach the
	// terminal instead of being thrown away by a "%s: %s" that kept only the
	// severity and the message.
	wantWarn := "warn: services/api/service.yaml:4 [exemption-expired]\n" +
		"  exemption for slo-defined on service:api expired on 2020-01-01 and no longer waives anything\n" +
		"  hint: renew it with a new `until`, or fix the check and remove the exemption\n"
	if !strings.Contains(r.stderr, wantWarn) {
		t.Errorf("stderr must warn about the expired exemption in full, want:\n%s\ngot:\n%s", wantWarn, r.stderr)
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

// A portal built from the fixture repository must be complete: every entity
// has a page, every page the catalog links to exists, and the site is
// internally consistent. This is the assertion that the fourteen unit-level
// tasks actually compose.
func TestBuildProducesACompletePortal(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dist")

	fsys := os.DirFS("../../testdata/monorepo-ok")
	files, code := Build(fsys, buildWorkspace(t, fsys), io.Discard, BuildOptions{
		Now:      time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		LastEdit: noLastEdit(),
		Version:  "v0.0.0-test",
		Mermaid:  render.Mermaid{Src: render.DefaultMermaidSrc, Integrity: render.DefaultMermaidIntegrity},
	})
	if code != exitOK {
		t.Fatalf("build of the known-good fixture failed with exit %d", code)
	}
	if err := writeSite(out, files, false, io.Discard); err != nil {
		t.Fatalf("writeSite: %v", err)
	}

	present := map[string]bool{}
	for _, f := range files {
		present[f.Path] = true
	}
	for _, want := range []string{
		"index.html",
		"scorecard/index.html",
		"map/index.html",
		"search-index.json",
		"assets/style.css",
		"assets/chroma.css",
		"assets/search.js",
		"assets/catalog.js",
		"assets/runtime.js",
		"assets/mermaid.js",
		"entity/service/ledger-api/index.html",
		"entity/service/payments-worker/index.html",
		// The topic's metadata.name is "payments.events" (a literal dot,
		// spec §4's namespaced-name convention for Topic/Database/API) even
		// though its directory is topics/payments-events (a hyphen) -- the
		// URL is built from the entity name, not the directory. Confirmed
		// against testdata/monorepo-ok/topics/payments-events/service.yaml
		// and a real `landsraad build` of this fixture.
		"entity/topic/payments.events/index.html",
		"team/team-payments/index.html",
	} {
		if !present[want] {
			t.Errorf("the portal is missing %s", want)
		}
	}

	// Every internal href must resolve to a file the build produced. A
	// portal that links to its own 404s is the failure this whole plan's
	// URL scheme exists to prevent.
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".html") {
			continue
		}
		for _, href := range hrefs(string(f.Data)) {
			if strings.HasPrefix(href, "http") || strings.HasPrefix(href, "#") || href == "" {
				continue
			}
			target := path.Join(path.Dir(f.Path), href)
			// path.Join runs path.Clean, which strips any trailing slash
			// target ever had -- so checking target's suffix here can never
			// fire; it must be checked on href before Join eats it. Without
			// this, a directory-style link whose last segment happens to
			// contain a literal "." (e.g. the payments.events topic, or the
			// root "./" self-link, which Joins down to ".") reads as an
			// already-named file and is never resolved to its index.html.
			if target == "." || strings.HasSuffix(href, "/") || !strings.Contains(path.Base(target), ".") {
				target = path.Join(target, "index.html")
			}
			if !present[target] {
				t.Errorf("%s links to %q which resolves to %q, and nothing generated it", f.Path, href, target)
			}
		}
	}
}

// hrefs pulls every href and src value out of a page. Deliberately crude:
// this is a link checker for our own generated markup, not an HTML parser.
func hrefs(page string) []string {
	var out []string
	for _, attr := range []string{`href="`, `src="`} {
		rest := page
		for {
			i := strings.Index(rest, attr)
			if i < 0 {
				break
			}
			rest = rest[i+len(attr):]
			j := strings.Index(rest, `"`)
			if j < 0 {
				break
			}
			out = append(out, rest[:j])
			rest = rest[j:]
		}
	}
	return out
}

// A second build over the same directory must remove the page of an entity
// that has left the catalog (ruling R16).
func TestRebuildPrunesADeletedEntity(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "dist")
	opts := BuildOptions{
		Now:      time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		LastEdit: noLastEdit(), Version: "v0.0.0-test",
	}

	fsys := os.DirFS("../../testdata/monorepo-ok")
	full, code := Build(fsys, buildWorkspace(t, fsys), io.Discard, opts)
	if code != exitOK {
		t.Fatalf("first build exited %d", code)
	}
	if err := writeSite(out, full, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(out, "entity", "topic", "payments.events", "index.html")
	if _, err := os.Stat(page); err != nil {
		t.Fatalf("first build did not write the topic's page: %v", err)
	}

	// Rebuild with that entity's page absent from the set.
	var without []emit.File
	for _, f := range full {
		if f.Path != "entity/topic/payments.events/index.html" {
			without = append(without, f)
		}
	}
	if err := writeSite(out, without, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(page); !os.IsNotExist(err) {
		t.Error("the removed entity's page survived the rebuild")
	}
}

// The whole feature, end to end: two repositories, one on disk and one over
// a host API, merged into one catalog with a reference crossing between
// them.
func TestBuildMergesALocalAndARemoteRepository(t *testing.T) {
	srv := fakeGitHub(t, filepath.Join("..", "..", "testdata", "multirepo", "edge-gateway"))
	root := multirepoRoot(t, srv.Listener.Addr().String())

	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: root, RootFS: os.DirFS(root),
		Cache:  fetch.NopCache{},
		Lookup: func(string) (string, bool) { return "test-token", true },
		ErrOut: io.Discard,
		HTTP:   srv.Client(),
	}, &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("openRepos reported %d diagnostics: %+v", len(ds), ds)
	}
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("openRepos failed for %+v", got)
	}

	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test",
		LastEdit: func(string, string) (time.Time, bool) { return testNow, true },
	})
	if code != exitOK {
		t.Fatalf("Build exit = %d, stderr:\n%s", code, errOut.String())
	}

	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	// A page for the remote repository's entity.
	if _, ok := byPath["entity/service/edge/index.html"]; !ok {
		t.Error("no page for the entity defined in the fetched repository")
	}
	// Its runbook, fetched as a blob and rendered.
	if _, ok := byPath["entity/service/edge/runbook.html"]; !ok {
		t.Error("the fetched repository's runbook was not rendered")
	}
	// And the cross-repository dependency resolved: build runs at
	// FullCatalog, so a dangling ref would have been a hard failure.
	if got := string(byPath["entity/service/edge/index.html"]); !strings.Contains(got, "service:api") {
		t.Error("the cross-repository dependsOn did not resolve")
	}
	// No banner: nothing failed.
	for p, data := range byPath {
		if strings.Contains(string(data), "This portal is incomplete") {
			t.Errorf("%s carries a degraded-mode banner on a clean build", p)
		}
	}
}

// remotePlatformRoot materialises a platform repository holding one local
// service and naming one remote repository at host, whose entry's paths: is
// patterns.
//
// multirepoRoot's fixture hardcodes `paths: [.]`, which is the layout where a
// satellite's whole tree is inside the configured path. The interesting cases
// below are the ones where it is not.
func remotePlatformRoot(t *testing.T, host, patterns string) string {
	t.Helper()
	return materialize(t, map[string]string{
		"teams.yaml": "teams:\n  - name: team-platform\n    members: [alice]\n    slack: \"#plat\"\n    pagerduty: PLAT\n",
		"repos.yaml": "repos:\n" +
			"  - url: https://example.invalid/org/platform\n    local: true\n    paths: [services/*]\n" +
			"  - url: https://" + host + "/org/edge-gateway\n    host: github\n    ref: main\n    paths: [" + patterns + "]\n",
		"services/api/service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The platform API.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/api
`,
	})
}

// openRemoteWorkspace runs openRepos against root with srv standing in for
// the host, and fails the test if repos.yaml itself did not parse.
func openRemoteWorkspace(t *testing.T, root string, srv *httptest.Server) *workspace {
	t.Helper()
	var c diag.Collector
	w := openRepos(context.Background(), reposOptions{
		Root: root, RootFS: os.DirFS(root),
		Cache:  fetch.NopCache{},
		Lookup: func(string) (string, bool) { return "test-token", true },
		ErrOut: io.Discard,
		HTTP:   srv.Client(),
	}, &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("openRepos reported %d diagnostics: %+v", len(ds), ds)
	}
	return w
}

// A spec.runbook naming a file that is not in the fetched repository is a
// mistake in that repository's service.yaml, and it must be reported as one.
//
// It used to cost the whole repository. contentSet asked for every
// spec.runbook it saw, fetchBlobs rejected a path with no Entry ("runbook.md
// is not in the repository listing" — correctly, since that means the planner
// is wrong), openRepos turned that into a repoFailure, and a repoFailure
// drops the repository from the workspace entirely. So the headline
// diagnostic of this whole product — "your runbook is missing", plus a failed
// runbook-present — became "landsraad could not read your repository" the
// moment the repository was remote, and under --allow-partial became exit 0
// with the repository silently absent and a banner blaming the network for a
// YAML problem. The identical dangling runbook: in a local repository has
// always given the right answer.
func TestBuildReportsADanglingRemoteRunbookInsteadOfDroppingTheRepository(t *testing.T) {
	remote := materialize(t, map[string]string{
		// No runbook.md anywhere in this repository.
		"service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge
  description: Edge gateway, whose runbook is not there.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: .
  runbook: runbook.md
`,
	})
	srv := fakeGitHub(t, remote)
	root := remotePlatformRoot(t, srv.Listener.Addr().String(), ".")

	w := openRemoteWorkspace(t, root, srv)
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("the repository was dropped over a dangling runbook: %+v", got)
	}
	if _, ok := w.Sources().Get("edge-gateway"); !ok {
		t.Fatal("edge-gateway is not in the catalog; a dangling spec.runbook must not cost the repository")
	}

	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", LastEdit: noLastEdit(),
	})
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	if files != nil {
		t.Error("Build produced files from a catalog with errors")
	}
	// The whole of stderr: one diagnostic, against the line in the satellite's
	// service.yaml that is actually wrong, and the refusal.
	want := "error: service.yaml:4 [missing-file]\n" +
		"  spec.runbook points at \"runbook.md\", which does not exist\n" +
		"  hint: paths are relative to the repository root, slash-separated\n" +
		"refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n"
	if got := errOut.String(); got != want {
		t.Errorf("stderr =\n%s\nwant\n%s", got, want)
	}
}

// A runbook that genuinely exists but sits outside the configured paths must
// be listed, fetched and rendered.
//
// `paths: [services/*]` with `runbook: docs/runbooks/edge.md` is an ordinary
// satellite layout, and neither GitLab.Open (which lists only the patterns'
// literal prefixes) nor GitHub's truncated-tree descent (which walks only
// what the patterns reach) covers it. docsDirs expanded spec.docs and nothing
// else, so the runbook was in no listing at all: before the fix every build
// of such a repository failed outright, and with contentSet now skipping what
// it cannot see, it would instead be reported as a file that does not exist.
//
// Served truncated deliberately: under a complete listing FromEntries marks
// every directory and the runbook is found whether docsDirs expanded anything
// or not, so the fixture could not tell the two apart.
func TestBuildFetchesARunbookOutsideTheConfiguredPaths(t *testing.T) {
	remote := materialize(t, map[string]string{
		"services/edge/service.yaml": `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge
  description: Edge gateway, documented out of the way.
  owner: team-platform
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/edge
  runbook: docs/runbooks/edge.md
`,
		"docs/runbooks/edge.md": "# Edge runbook\n\nPage the on-call, then drain the pool.\n",
	})
	srv := fakeGitHubTruncatedTree(t, remote)
	root := remotePlatformRoot(t, srv.Listener.Addr().String(), "services/*")

	w := openRemoteWorkspace(t, root, srv)
	if got := w.Failures(); len(got) != 0 {
		t.Fatalf("openRepos failed for %+v", got)
	}

	var errOut bytes.Buffer
	files, code := Build(os.DirFS(root), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", LastEdit: noLastEdit(),
	})
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	byPath := map[string][]byte{}
	for _, f := range files {
		byPath[f.Path] = f.Data
	}
	page, ok := byPath["entity/service/edge/runbook.html"]
	if !ok {
		t.Fatalf("the runbook outside the configured paths was not rendered; pages: %v", sortedPaths(byPath))
	}
	if !strings.Contains(string(page), "drain the pool") {
		t.Errorf("the rendered runbook does not carry the fetched content:\n%s", page)
	}
}

// sortedPaths names what a build produced, for a failure message.
func sortedPaths(byPath map[string][]byte) []string {
	out := make([]string, 0, len(byPath))
	for p := range byPath {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// The same fixture with the host refusing everything: build must refuse, and
// --allow-partial must render the local half with a banner naming the
// remote (ruling R32).
func TestBuildWithAnUnreachableRemote(t *testing.T) {
	dead := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(dead.Close)
	root := multirepoRoot(t, dead.Listener.Addr().String())

	// sleeps counts every call the fetch client makes to wait between
	// retries. A no-op Sleep would make this test fast for the wrong
	// reason — it could pass with the retry path deleted entirely. Counting
	// keeps the real 3-attempt, 2-sleep-per-Get retry schedule in the
	// assertion while paying none of its wall-clock cost (fetch.Client's
	// Sleep seam, reached through reposOptions.Sleep).
	//
	// The bare sleeps++ below is safe ONLY because the dead host 500s the
	// tree listing, so Open fails before fetchBlobs' worker pool ever
	// starts — Sleep is never called from more than one goroutine here. Do
	// not copy this pattern into a test that reaches blobParallel's
	// concurrent workers without a mutex or an atomic.
	var sleeps int
	open := func() *workspace {
		var c diag.Collector
		return openRepos(context.Background(), reposOptions{
			Root: root, RootFS: os.DirFS(root), Cache: fetch.NopCache{},
			Lookup: func(string) (string, bool) { return "t", true },
			ErrOut: io.Discard, HTTP: dead.Client(),
			Sleep: func(time.Duration) { sleeps++ },
		}, &c)
	}

	var errOut bytes.Buffer
	if _, code := Build(os.DirFS(root), open(), &errOut, BuildOptions{Now: testNow, Version: "t"}); code != exitUsage {
		t.Errorf("exit = %d, want %d", code, exitUsage)
	}

	errOut.Reset()
	files, code := Build(os.DirFS(root), open(), &errOut, BuildOptions{
		Now: testNow, Version: "t", AllowPartial: true,
	})
	if code != exitOK {
		t.Fatalf("--allow-partial exit = %d, stderr:\n%s", code, errOut.String())
	}
	var banners int
	for _, f := range files {
		if strings.Contains(string(f.Data), "edge-gateway could not be read") {
			banners++
		}
	}
	if banners == 0 {
		t.Error("no page names the repository that failed")
	}
	// Two open() calls, each one failed Get against the always-500 host:
	// 3 attempts per Get, and the client sleeps between attempts 1→2 and
	// 2→3 but not after the final one, so 2 sleeps per Get × 2 opens = 4.
	// This is what proves the retry path actually ran rather than being
	// short-circuited by MaxAttempts: 1 or a Sleep that was never wired up.
	if sleeps != 4 {
		t.Errorf("sleeps = %d, want 4 (2 opens × 2 retries each against the always-failing host)", sleeps)
	}
}
