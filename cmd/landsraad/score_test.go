package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/config"
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
		LastEdit: func(string) (time.Time, bool) { return time.Time{}, false },
	}
}

// A tier-1 service with no runbook, no SLO and no alerts fails required
// checks, so the gate trips with exit 3 — not 2, which means broken metadata.
func TestScoreExitsThreeWhenARequiredCheckFails(t *testing.T) {
	var out, errOut bytes.Buffer

	code := Score(scoreFS(), &out, &errOut, scoreOpts())

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
	if code := Score(fsys, &out, &errOut, scoreOpts()); code != exitOK {
		t.Fatalf("at tier 3 with --fail-on required, exit = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}

	opts := scoreOpts()
	opts.FailOn = config.SevWarn
	out.Reset()
	errOut.Reset()
	if code := Score(fsys, &out, &errOut, opts); code != exitScorecard {
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
	if code := Score(fsys, &out, &errOut, scoreOpts()); code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
}

// --history writes the trend row. Without it, score is read-only.
func TestScoreWritesHistoryOnlyWhenAsked(t *testing.T) {
	var out, errOut bytes.Buffer
	opts := scoreOpts()
	opts.History = true

	files := scoreHistoryFiles(scoreFS(), &out, &errOut, opts)

	if len(files) != 1 || files[0].Path != scorecard.HistoryPath {
		t.Fatalf("want exactly one history file, got %+v", files)
	}
	if !strings.Contains(string(files[0].Data), "2026-09-09,service:api,1,team-payments,") {
		t.Errorf("history row missing:\n%s", files[0].Data)
	}
}
