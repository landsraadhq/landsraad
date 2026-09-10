package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

var testNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func scoreFS() fstest.MapFS {
	return fstest.MapFS{
		"teams.yaml": {Data: []byte("teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"repos.yaml": {Data: []byte("repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"services/api/service.yaml": {Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: services/api
`)},
	}
}

func scoreOpts() ScoreOptions {
	return ScoreOptions{
		Format:   diagText(),
		FailOn:   config.SevRequired,
		Now:      testNow,
		LastEdit: func(string, string) (time.Time, bool) { return time.Time{}, false },
	}
}

// A tier-1 service with no runbook, no SLO and no alerts fails required
// checks, so the gate trips with exit 3 — not 2, which means broken metadata.
func TestScoreExitsThreeWhenARequiredCheckFails(t *testing.T) {
	var out, errOut bytes.Buffer

	_, code := Score(scoreFS(), &out, &errOut, scoreOpts())

	if code != exitScorecard {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitScorecard, errOut.String())
	}
	if code == exitValidation {
		t.Error("a failing check is not a validation error; those are different problems for different people")
	}
}

// The metadata is fine, so validation must not be what fails.
func TestScoreDoesNotReportValidationErrorsForACleanCatalog(t *testing.T) {
	var out, errOut bytes.Buffer
	Score(scoreFS(), &out, &errOut, scoreOpts())
	if strings.Contains(errOut.String(), "refusing") {
		t.Errorf("the catalog is valid; stderr should not refuse:\n%s", errOut.String())
	}
}

// Stream contract (spec §12): --format json puts only JSON on stdout. Plan 1
// established this because a JSON array followed by "ok: ..." parses nowhere.
func TestScoreJSONOutputIsParseable(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.JSON = true

	Score(scoreFS(), &out, &errOut, opts)

	var payload struct {
		Entities []struct {
			Ref        string  `json:"ref"`
			Tier       int     `json:"tier"`
			Owner      string  `json:"owner"`
			Score      float64 `json:"score"`
			Passed     int     `json:"passed"`
			Applicable int     `json:"applicable"`
			Results    []struct {
				Check  string `json:"check"`
				Status string `json:"status"`
				Detail string `json:"detail"`
			} `json:"results"`
		} `json:"entities"`
		Teams []struct {
			Team  string  `json:"team"`
			Score float64 `json:"score"`
		} `json:"teams"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("stdout must be parseable JSON: %v\n%s", err, out.String())
	}
	if len(payload.Entities) != 1 {
		t.Fatalf("got %d entities, want 1", len(payload.Entities))
	}
	if payload.Entities[0].Ref != "service:api" {
		t.Errorf("ref = %q, want service:api", payload.Entities[0].Ref)
	}
}

// --fail-on warn is the ratchet: a team that has cleared the required checks
// opts into gating on warn too.
func TestScoreFailOnWarnGatesMore(t *testing.T) {
	fsys := scoreFS()
	// A tier-3 service: runbook-present is warn, not required.
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-payments
  tier: 3
  lifecycle: production
spec:
  path: services/api
  slo:
    - { name: availability, target: "99.9%", window: 30d }
  alerts: services/api/alerts.yaml
  docs: services/api/docs
`)}
	fsys["services/api/alerts.yaml"] = &fstest.MapFile{Data: []byte(
		"groups:\n  - name: api\n    rules:\n      - alert: Down\n        expr: up == 0\n")}
	fsys["services/api/docs/index.md"] = &fstest.MapFile{Data: []byte("# Docs\n\ncontent\n")}

	var out, errOut bytes.Buffer
	if _, code := Score(fsys, &out, &errOut, scoreOpts()); code != exitOK {
		t.Fatalf("at tier 3 with --fail-on required, exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}

	opts := scoreOpts()
	opts.FailOn = config.SevWarn
	out.Reset()
	errOut.Reset()
	if _, code := Score(fsys, &out, &errOut, opts); code != exitScorecard {
		t.Fatalf("--fail-on warn must gate on runbook-present, exit = %d, want %d", code, exitScorecard)
	}
}

// A repository with no standards.yaml is scored against spec §6's defaults,
// and told so. Scoring against nothing would report a perfect score having
// checked nothing.
func TestScoreAnnouncesTheDefaultStandards(t *testing.T) {
	var out, errOut bytes.Buffer
	Score(scoreFS(), &out, &errOut, scoreOpts())
	if !strings.Contains(errOut.String(), "no standards.yaml found") {
		t.Errorf("the default matrix must be announced, got:\n%s", errOut.String())
	}
}

// Broken metadata is exit 2, and scoring does not happen: a score computed
// from a catalog with a dangling ref is a number nobody should act on.
func TestScoreExitsTwoOnABrokenCatalog(t *testing.T) {
	fsys := scoreFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-nope
  tier: 1
  lifecycle: production
spec:
  path: services/api
`)}

	var out, errOut bytes.Buffer
	if _, code := Score(fsys, &out, &errOut, scoreOpts()); code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
}

// An error raised during scoring — not only during loadCatalog — must also
// gate the exit code. Two producers reporting the same (entity, check) at the
// same instant is scorecard.Ingest's checks-tie error (spec §6: "a tie is an
// error rather than a coin flip"); it used to be printed to stderr but leave
// Score() reporting exitOK, because computeScore only checked HasErrors()
// once, before Ingest ran.
func TestScoreExitsTwoWhenIngestReportsATie(t *testing.T) {
	fsys := scoreFS()
	fsys[".landsraad/checks/a.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/a\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: pass }\n")}
	fsys[".landsraad/checks/b.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: CheckResults\nproducer: ci/b\ngeneratedAt: 2026-09-08T00:00:00Z\nresults:\n  - { entity: service:api, check: image-scanned, status: fail }\n")}

	var out, errOut bytes.Buffer
	if _, code := Score(fsys, &out, &errOut, scoreOpts()); code != exitValidation {
		t.Fatalf("exit = %d, want %d (a checks-tie error must gate the exit code); stderr:\n%s", code, exitValidation, errOut.String())
	}
}

// opts.Format must hold on the broken-catalog error path too, not only on
// the success path (which opts.JSON controls). Score() writes diagnostics
// through opts.Format.Write when the catalog has errors; if that were
// hardcoded to the text formatter, `score --format json` would emit plain
// text on stdout for a broken catalog, breaking every consumer expecting
// only JSON there.
func TestScoreErrorPathHonoursTheJSONFormatter(t *testing.T) {
	fsys := scoreFS()
	fsys["services/api/service.yaml"] = &fstest.MapFile{Data: []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  description: The API.
  owner: team-nope
  tier: 1
  lifecycle: production
spec:
  path: services/api
`)}

	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.Format = diag.JSON{}

	if _, code := Score(fsys, &out, &errOut, opts); code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	var ds []diag.Diagnostic
	if err := json.Unmarshal(out.Bytes(), &ds); err != nil {
		t.Fatalf("stdout must be valid JSON diagnostics on the error path: %v\n%s", err, out.String())
	}
	if len(ds) == 0 {
		t.Fatal("expected at least one diagnostic for the unknown owner")
	}
}

// --history writes the trend row. Without it, score is read-only — and this
// test asserts BOTH halves: the previous version set History=true and checked
// only that a file came back, so it would have passed just as happily against
// a Score that appended to the history on every run whether or not it was
// asked to.
func TestScoreWritesHistoryOnlyWhenAsked(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.History = true

	files, _ := Score(scoreFS(), &out, &errOut, opts)

	if len(files) != 1 || files[0].Path != scorecard.HistoryPath {
		t.Fatalf("want exactly one history file, got %+v", files)
	}
	if !strings.Contains(string(files[0].Data), "2026-09-09,service:api,1,team-payments,") {
		t.Errorf("history row missing:\n%s", files[0].Data)
	}

	out.Reset()
	errOut.Reset()
	if files, _ := Score(scoreFS(), &out, &errOut, scoreOpts()); len(files) != 0 {
		t.Errorf("score without --history must write nothing, got %+v", files)
	}
}

// Scoring must happen exactly once per run. --history used to run the whole
// pipeline a second time beside Score, which printed every line standardsFor
// writes directly to stderr twice — the fixture has no standards.yaml, so a
// real `score --history` announced the default matrix two times.
func TestScoreWithHistoryScoresOnlyOnce(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.History = true

	Score(scoreFS(), &out, &errOut, opts)

	const announcement = "no standards.yaml found; scoring against the published defaults\n"
	if n := strings.Count(errOut.String(), announcement); n != 1 {
		t.Errorf("the default matrix was announced %d times, want 1; stderr:\n%s", n, errOut.String())
	}
}

// The same discarded-error shape as build's history read, one command over,
// and destructive rather than merely misleading: AppendHistory given no
// existing bytes produces a fresh file with one row, and newScoreCmd writes it
// straight over the real one. A history file that is present and unreadable
// was therefore the case in which every run ever recorded was silently
// replaced by today's.
func TestScoreRefusesToReplaceAHistoryFileItCannotRead(t *testing.T) {
	base := scoreFS()
	base[scorecard.HistoryPath] = &fstest.MapFile{Data: []byte(
		"date,ref,tier,owner,passed,applicable,score\n2026-01-01,service:api,1,team-payments,5,7,0.71\n")}
	fsys := unreadableFS{MapFS: base, path: scorecard.HistoryPath}

	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.History = true

	files, code := Score(fsys, &out, &errOut, opts)

	if len(files) != 0 {
		t.Fatalf("nothing may be written over a history file that could not be read, got %+v", files)
	}
	want := "error: cannot read " + scorecard.HistoryPath + ": open " + scorecard.HistoryPath + ": permission denied\n" +
		"  not appending this run: writing a fresh file would replace the history already recorded there\n"
	if !strings.Contains(errOut.String(), want) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut.String(), want)
	}
	// Refusing and then reporting success is the shape of the bug this
	// guard was added to prevent, one level up: a CI job running
	// `landsraad score --history` to record the trend went green forever
	// while recording nothing, because the run printed "error:" and exited
	// 0. Spec §12 reserves 0 for "clean", and 1 for a config error whoever
	// ran it must fix — which is exactly a file this process cannot read.
	if code != exitUsage {
		t.Errorf("exit = %d, want %d; a run that printed \"error:\" must not also report itself clean", code, exitUsage)
	}
	// And it preempts the gate rather than being masked by it: this fixture
	// is a tier-1 service failing required checks, so exit 3 is what a
	// history-blind Score would have returned here.
	if code == exitScorecard {
		t.Errorf("the gate must not mask a --history that did nothing")
	}
}
