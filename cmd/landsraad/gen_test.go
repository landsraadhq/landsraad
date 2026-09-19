package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// genFS is a minimal repository: one team, one service, and the repos.yaml
// that points at it.
func genFS() fstest.MapFS {
	return fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"services/api/service.yaml": {Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)},
	}
}

func TestGenWritesNothingToStdoutWhenClean(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Gen(genFS(), t.TempDir(), &out, &errOut, diagText(), false)

	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	// Stream contract (spec §12): the human summary is stderr, and gen's
	// payload is files on disk, not stdout.
	if out.Len() != 0 {
		t.Errorf("stdout must be empty, got %q", out.String())
	}
	if !strings.Contains(errOut.String(), "3 artifacts") {
		t.Errorf("stderr must summarise what was generated, got %q", errOut.String())
	}
}

// --check against a repository with no generated files must fail: the
// artifacts have never been committed, so ownership routing does not exist.
func TestGenCheckFailsWhenArtifactsAreMissing(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Gen(genFS(), "", &out, &errOut, diagText(), true)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if !strings.Contains(errOut.String(), "CODEOWNERS") {
		t.Errorf("the failure must name the stale file, got %q", errOut.String())
	}
}

// --check against a repository holding exactly what gen would produce passes.
// This is the CI gate: it is what makes metadata rot break something visible.
func TestGenCheckPassesWhenArtifactsAreCurrent(t *testing.T) {
	fsys := genFS()
	var c diagCollectorForTest
	for _, f := range artifacts(fsys, c.collector()) {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data}
	}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), true)

	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
}

// A hand-edited artifact is the case --check exists for.
func TestGenCheckFailsOnAHandEditedArtifact(t *testing.T) {
	fsys := genFS()
	var c diagCollectorForTest
	for _, f := range artifacts(fsys, c.collector()) {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data}
	}
	fsys["CODEOWNERS"] = &fstest.MapFile{Data: []byte("services/api/ @someone-else\n")}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), true)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if !strings.Contains(errOut.String(), "out of date") {
		t.Errorf("stderr must say the file is out of date, got %q", errOut.String())
	}
}

// Generating from a catalog with validation errors would encode the broken
// state into the artifact, and --check would then pass against it forever.
func TestGenRefusesToGenerateFromABrokenCatalog(t *testing.T) {
	fsys := genFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-does-not-exist
  tier: 2
  lifecycle: production
spec:
  path: services/api
`)}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if strings.Contains(errOut.String(), "artifacts") {
		t.Errorf("nothing must be reported as generated, got %q", errOut.String())
	}
}

// Fix round 2 (coordinator review): a single-repository run whose patterns
// match nothing must produce EXACTLY the one diagnostic main always did —
// not that error plus the per-repository warning that exists for telling
// several repositories apart. Pinned as the full rendered text, the same
// way the coordinator's own main-vs-branch comparison was done, so the
// regression (confirmed to reproduce before this fix, by temporarily
// reverting it and diffing this exact test's output against a `main`
// worktree) cannot come back silently.
func TestGenOnZeroMatchRepositoryEmitsExactlyOneDiagnostic(t *testing.T) {
	fsys := genFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [nope/*]\n")}

	var out, errOut bytes.Buffer
	code := Gen(fsys, "", &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	want := "error: repos.yaml:1 [no-entities]\n" +
		"  no service.yaml found under any configured path (nope/*)\n" +
		"  hint: add a repos.yaml listing the paths your services live under\n"
	if out.String() != want {
		t.Errorf("stdout\n got:\n%s\nwant:\n%s", out.String(), want)
	}
}

// Ruling R46: teams.yaml is a different file from repos.yaml, so a
// repos-parse error must not hide missing-teams. service.yaml sits INSIDE
// the fallback globs deliberately, so no-entities is never in play and the
// only thing under test is whether teams.yaml was read at all.
func TestGenReportsTeamsProblemsWhenReposYAMLFailedToParse(t *testing.T) {
	fsys := fstest.MapFS{
		"repos.yaml":                {Data: []byte("kind: Service\n  bad: indent\n")},
		"services/api/service.yaml": genFS()["services/api/service.yaml"],
	}
	var out, errOut bytes.Buffer

	code := Gen(fsys, t.TempDir(), &out, &errOut, diagText(), false)

	if code != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
	}
	// Gen's error branch writes diagnostics to stdout (f.Write(out, ...)) and
	// reserves stderr for the "refusing to generate..." summary — the same
	// split TestGenOnZeroMatchRepositoryEmitsExactlyOneDiagnostic pins for the
	// sibling zero-match case.
	got := out.String()
	for _, want := range []string{
		"cannot parse repos file: yaml: line 2: mapping values are not allowed in this context",
		"teams.yaml not found at the repository root, so no owner can be resolved",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout must report both files' problems in one run (R46); missing:\n  %s\ngot:\n%s", want, got)
		}
	}
}

// diagCollectorForTest hands out a collector whose diagnostics the test does
// not care about: artifacts() reports through it, and these cases assert on
// exit codes and file content instead.
type diagCollectorForTest struct{ c diag.Collector }

func (d *diagCollectorForTest) collector() *diag.Collector { return &d.c }
