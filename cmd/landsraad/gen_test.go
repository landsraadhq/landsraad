package main

import (
	"bytes"
	"encoding/json"
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

// Ruling R46's other half, and the half the first fix left open: an
// unparseable repos.yaml must suppress `no-entities` and NOTHING ELSE. The
// give-up path it replaced returned before parseRepo and assemble, so every
// diagnostic that needs a catalog — the schema errors on the files that WERE
// found, the generators' ownership errors — went with it, and gen disagreed
// with validate about the same directory (ruling R43).
//
// Two fixtures, because the two halves cannot both be live in one. With a
// service.yaml inside the fallback globs, files are found and `no-entities`
// is structurally out of play, so the schema error is the live assertion.
// With it outside them, nothing is found and `no-entities` is exactly what a
// guessed pattern set would wrongly report.
func TestGenSuppressesOnlyNoEntitiesWhenReposYAMLFailedToParse(t *testing.T) {
	const brokenRepos = "kind: Service\n  bad: indent\n"
	// A tier the schema rejects and a field it does not define: two
	// diagnostics that can only exist if the files were found, read and
	// validated after repos.yaml failed to parse.
	badService := []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
		"  owner: team-payments\n  tier: 9\n  lifecycle: production\n  nonsense: true\n")

	cases := []struct {
		name  string
		fsys  fstest.MapFS
		wants []string
	}{
		{
			// services/* is one of config.DefaultPatterns(), so the guessed
			// patterns do find this file.
			name: "diagnostics that need a catalog survive",
			fsys: fstest.MapFS{
				"repos.yaml":                {Data: []byte(brokenRepos)},
				"teams.yaml":                genFS()["teams.yaml"],
				"services/api/service.yaml": {Data: badService},
			},
			wants: []string{
				"at '/metadata/tier': value must be one of 1, 2, 3",
				"at '/metadata/nonsense': unknown field 'nonsense'",
			},
		},
		{
			// svc/* is under no default glob, so the fallback finds nothing —
			// precisely when the spurious no-entities appeared.
			name: "no-entities stays suppressed",
			fsys: fstest.MapFS{
				"repos.yaml":           {Data: []byte(brokenRepos)},
				"teams.yaml":           genFS()["teams.yaml"],
				"svc/api/service.yaml": {Data: badService},
			},
			wants: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := Gen(tc.fsys, t.TempDir(), &out, &errOut, diag.JSON{}, false)

			if code != exitValidation {
				t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitValidation, errOut.String())
			}
			var ds []diag.Diagnostic
			if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
				t.Fatalf("stdout must be valid JSON: %v", err)
			}
			var parse, entities *diag.Diagnostic
			byMessage := map[string]bool{}
			for i := range ds {
				byMessage[ds[i].Message] = true
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
			for _, w := range tc.wants {
				if !byMessage[w] {
					t.Errorf("suppressing no-entities must not suppress this too (R46/R43); missing:\n  %s\ngot: %+v", w, ds)
				}
			}
		})
	}
}

// diagCollectorForTest hands out a collector whose diagnostics the test does
// not care about: artifacts() reports through it, and these cases assert on
// exit codes and file content instead.
type diagCollectorForTest struct{ c diag.Collector }

func (d *diagCollectorForTest) collector() *diag.Collector { return &d.c }
