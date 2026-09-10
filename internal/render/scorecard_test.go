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
	// The exact markup its sibling at TestScorecardPageOverallIsNotScoredRather
	// ThanZero asserts. A bare Contains(page, "0%") also matched "50%", "100%"
	// and "80%": it passed only because this fixture's percentages happen not
	// to end in a zero, so a change to DefaultStandards would have broken it
	// with a message about a thing it was never testing.
	want := `<span class="none">not scored</span>`
	if !strings.Contains(page, want) {
		t.Errorf("a team with zero applicable checks must render %s:\n%s", want, page)
	}
	if strings.Contains(page, `<span class="mono">0%</span>`) {
		t.Errorf("it must never read 0%%, indistinguishable from failing everything:\n%s", page)
	}
}

// The same R1 ambiguity as the team row, one field over: a catalog with no
// tiered entities yet has nothing applicable catalog-wide, and
// Scorecard.Score() returns 0 for "nothing counted" the same way
// TeamScore.Score() does. The page must not show "Catalog total 0%" above a
// "By team" section that correctly says nothing was scored.
func TestScorecardPageOverallIsNotScoredRatherThanZero(t *testing.T) {
	in := input(t, nil, ent("shared", catalog.KindLibrary, "team-payments", 0))
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	want := `<p class="count">Catalog total <span class="none">not scored</span></p>`
	if !strings.Contains(page, want) {
		t.Errorf("an empty scorecard's total must read not scored, not 0%%:\n%s", page)
	}
}

// The same R1 ambiguity again, in the trend table this time: a recorded
// date where nothing was applicable catalog-wide (every check on every
// entity exempted that run) leaves TrendPoint.Score at its zero value.
// TrendPoint.Score stays a plain float64 — it is a contract other tasks
// consume by name — so this is guarded from the template side using the
// Applicable int that is already there, rather than by changing the
// struct.
func TestScorecardPageTrendPointWithNoApplicableChecksIsNotScored(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-01,service:api,1,team-payments,0,0,0.000\n" +
		"2026-09-08,service:api,1,team-payments,4,4,1.000\n")
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["scorecard/index.html"])
	want := `<td class="mono">2026-09-01</td>
  <td><span class="none">not scored</span></td>`
	if !strings.Contains(page, want) {
		t.Errorf("the 2026-09-01 trend row must read not scored, not 0%%:\n%s", page)
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
	if !strings.Contains(page, "No runs have been recorded yet") {
		t.Errorf("zero runs must say zero, not collapse into the one-run wording:\n%s", page)
	}
	if strings.Contains(page, "Only one run has been recorded so far") {
		t.Errorf("zero recorded runs must not be reported as one:\n%s", page)
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
	if strings.Contains(page, "No runs have been recorded yet") {
		t.Errorf("one recorded run must not be reported as zero:\n%s", page)
	}
}

func TestScorecardPageIsGolden(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	golden(t, "scorecard.html", siteMap(Site(in, &c))["scorecard/index.html"])
}
