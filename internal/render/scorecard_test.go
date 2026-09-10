package render

import (
	"strings"
	"testing"

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

func TestScorecardPageIsGolden(t *testing.T) {
	in := twoEntities(t)
	in.History = []byte(historyCSV)
	var c diag.Collector
	golden(t, "scorecard.html", siteMap(Site(in, &c))["scorecard/index.html"])
}
