package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestEntityPageIsWrittenAtItsRefURL(t *testing.T) {
	var c diag.Collector
	files := siteMap(Site(twoEntities(t), &c))
	for _, want := range []string{
		"entity/service/ledger-api/index.html",
		"entity/topic/payments-events/index.html",
	} {
		if _, ok := files[want]; !ok {
			t.Errorf("missing %s; got %v", want, keys(files))
		}
	}
}

func TestEntityPageShowsOwnerTierAndLifecycle(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	for _, want := range []string{"ledger-api", "team-payments", "production"} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing %q:\n%s", want, page)
		}
	}
	if !strings.Contains(page, `href="../../../team/team-payments/"`) {
		t.Errorf("the owner must link to the team page:\n%s", page)
	}
}

// Ruling R20. The badge is inert until sub-project E ships, and that is the
// point: E must require no portal change.
func TestEntityPageCarriesADegradedRuntimeBadge(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, `data-ref="service:ledger-api"`) {
		t.Errorf("the runtime badge must name its ref so runtime.json can key on it:\n%s", page)
	}
	if !strings.Contains(page, "runtime unknown") {
		t.Errorf("the badge's server-rendered text must be the degraded one:\n%s", page)
	}
}

// scorecard.Status has six values, not two. "not reported" and "stale" are
// failures of the evidence, not of the service; rendering them as fail sends
// the owner hunting for a problem in the wrong place.
func TestEntityScorecardDistinguishesTheSixStatuses(t *testing.T) {
	var c diag.Collector
	// runbook-present fails (no spec.runbook), and every external check is
	// not-reported because there is no .landsraad/checks directory.
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, `class="status status-fail"`) {
		t.Errorf("a failing hermetic check must render as fail:\n%s", page)
	}
	if !strings.Contains(page, `class="status status-not-reported"`) {
		t.Errorf("an external check with no result must render as not-reported:\n%s", page)
	}
	if strings.Contains(page, "status-pass\">not") {
		t.Error("not-reported must never be rendered as a pass")
	}
}

// "fail" is useless; "spec.runbook is unset" fixes itself. Plan 2 put the
// sentence in Result.Detail precisely so the portal could show it.
func TestEntityScorecardShowsTheDetailNotJustTheVerdict(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, "spec.runbook") {
		t.Errorf("the check detail must appear, not only the status:\n%s", page)
	}
}

func TestEntityPageRendersItsLinks(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.Links = []catalog.Link{{Title: "Dashboard", URL: "https://grafana/d/api", Type: "dashboard"}}
	e.Spec.Oncall = "https://pagerduty/schedules/PAY"
	e.Spec.RepoURL = "https://github.com/org/monorepo/tree/main/services/api"

	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, e), &c))["entity/service/api/index.html"])
	for _, want := range []string{
		`href="https://grafana/d/api"`, "Dashboard",
		`href="https://pagerduty/schedules/PAY"`,
		`href="https://github.com/org/monorepo/tree/main/services/api"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing %q:\n%s", want, page)
		}
	}
}

func TestEntityPageRendersSLOs(t *testing.T) {
	e := ent("api", catalog.KindService, "team-payments", 1)
	e.Spec.SLO = []catalog.SLO{{Name: "settle-latency-p99", Target: "500ms", Window: "30d"}}

	var c diag.Collector
	page := string(siteMap(Site(input(t, nil, e), &c))["entity/service/api/index.html"])
	for _, want := range []string{"settle-latency-p99", "500ms", "30d"} {
		if !strings.Contains(page, want) {
			t.Errorf("entity page is missing SLO field %q:\n%s", want, page)
		}
	}
}

// An untiered entity is not scored (Plan 2, ruling R1). Its page shows no
// scorecard section rather than an empty one implying zero.
func TestAnUntieredEntityPageHasNoScorecardSection(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["entity/topic/payments-events/index.html"])
	if strings.Contains(page, `id="scorecard"`) {
		t.Errorf("an entity that was never scored must not show a scorecard:\n%s", page)
	}
	if !strings.Contains(page, "not scored") {
		t.Errorf("it must say so instead:\n%s", page)
	}
}

func TestEntityPageIsGolden(t *testing.T) {
	e := ent("ledger-api", catalog.KindService, "team-payments", 1)
	e.Spec.Runbook = "services/ledger-api/runbook.md"
	e.Spec.Links = []catalog.Link{{Title: "Dashboard", URL: "https://grafana/d/led", Type: "dashboard"}}
	files := fstest.MapFS{
		"services/ledger-api/runbook.md": {Data: []byte("# Runbook\n\nRestart carefully.\n")},
	}
	var c diag.Collector
	got := siteMap(Site(input(t, files, e), &c))["entity/service/ledger-api/index.html"]
	golden(t, "entity-service-ledger-api.html", got)
}
