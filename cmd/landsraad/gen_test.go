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

// diagCollectorForTest hands out a collector whose diagnostics the test does
// not care about: artifacts() reports through it, and these cases assert on
// exit codes and file content instead.
type diagCollectorForTest struct{ c diag.Collector }

func (d *diagCollectorForTest) collector() *diag.Collector { return &d.c }
