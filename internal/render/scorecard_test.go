package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestScorecardPageShowsTheStandardsMatrix(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	for _, want := range []string{"owner-set", "runbook-present", "image-scanned", "required", "warn"} {
		if !strings.Contains(page, want) {
			t.Errorf("the standards matrix is missing %q:\n%s", want, page)
		}
	}
}

// D3 splits checks in two, and a team needs to know which of their gaps they
// can close by editing YAML and which need a CI job.
func TestScorecardPageMarksExternalChecks(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if !strings.Contains(page, "reported by CI") {
		t.Errorf("external checks must be labelled as such:\n%s", page)
	}
}

func TestScorecardPageListsTeams(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if !strings.Contains(page, `href="../team/team-payments/"`) {
		t.Errorf("each team must link to its page:\n%s", page)
	}
}

func TestScorecardPageRendersTheTrend(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if !strings.Contains(page, "<polyline") {
		t.Errorf("the trend sparkline is missing:\n%s", page)
	}
	if !strings.Contains(page, "2026-09-08") {
		t.Errorf("the trend's dates must be readable as text too:\n%s", page)
	}
}

// nil History and an empty one are different: no file means no trend was
// ever recorded, an empty file means CI has not appended yet.
func TestScorecardPageWithoutHistorySaysHow(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["scorecard/index.html"])
	if strings.Contains(page, "<polyline") {
		t.Error("no history must mean no chart")
	}
	if !strings.Contains(page, "landsraad score --history") {
		t.Errorf("and the page must say how to start one:\n%s", page)
	}
}

// Spec §12: a portal quietly missing three services is worse than no
// portal, so the degraded-mode banner must reach every page type. Tasks 7,
// 8 and 10 each pinned this for their own page; this is the scorecard's.
func TestScorecardPageShowsTheDegradedNoticeBanner(t *testing.T) {
	in := twoEntities(t)
	in.Notice = "degraded: could not fetch team-payments from GitHub"
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if !strings.Contains(page, `<div class="notice" role="status">degraded: could not fetch team-payments from GitHub</div>`) {
		t.Errorf("the degraded-mode notice must render on the scorecard page:\n%s", page)
	}
}

// A team can own a tiered, scored entity and still have zero applicable
// checks — every check exempted, or a standards.yaml that grades that tier
// below warn on everything. That team's aggregate is nothing demonstrated,
// not zero passed: rendering "0%" would be indistinguishable from a team
// that failed every check, exactly the ambiguity Plan 2's R1 exists to
// prevent (see TeamView.Score and CatalogRow.Score, both *float64 for the
// same reason).
func TestScorecardPageShowsNotScoredForATeamWithNoApplicableChecks(t *testing.T) {
	exempt := ent("no-op", catalog.KindService, "team-zero", 1)
	for _, check := range config.DefaultStandards().Checks() {
		exempt.Spec.Exemptions = append(exempt.Spec.Exemptions, catalog.Exemption{
			Check: check, Reason: "test fixture: nothing applicable",
		})
	}
	in := input(t, nil, ent("ledger-api", catalog.KindService, "team-payments", 1), exempt)

	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if !strings.Contains(page, "not scored") {
		t.Errorf("a team with zero applicable checks must read 'not scored':\n%s", page)
	}
	if strings.Contains(page, "0%") {
		t.Errorf("it must never read 0%%, indistinguishable from failing everything:\n%s", page)
	}
}

// A file with only a header is a materially different fact from "no
// history file at all" (spec §12: distinct states must not collapse) — CI
// is wired up but has not appended its first run. Pinned here so the
// zero-point case cannot silently start rendering a chart, or the wrong
// message, in a future refactor.
func TestScorecardPageWithHeaderOnlyHistoryShowsNoChart(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte("date,ref,tier,owner,passed,applicable,score\n")
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if strings.Contains(page, "<polyline") {
		t.Error("zero points is nothing to chart")
	}
	if !strings.Contains(page, "Only one run has been recorded so far") {
		t.Errorf("the page must still say why there is no chart:\n%s", page)
	}
}

// A polyline needs two points (history.go's trend): one recorded run is not
// a trend, and drawing it as a line would imply a history that does not
// exist. Pinned at the page level, not just in trend()'s own unit test, so
// a future refactor of the template's branching can't silently change it.
func TestScorecardPageWithExactlyOneHistoryPointShowsNoChart(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-01,service:api,1,team-payments,3,4,0.750\n")
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	if strings.Contains(page, "<polyline") {
		t.Error("one point is not a trend; there must be no chart")
	}
	if !strings.Contains(page, "Only one run has been recorded so far") {
		t.Errorf("the page must say why there is no chart:\n%s", page)
	}
}

func TestScorecardPageIsGolden(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	golden(t, "scorecard.html", siteMap(Site(in, &c))["scorecard/index.html"])
}
