package render

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
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

// The losing side of a team-name collision must not get an owner link on
// the entity page either — mirrors TestALosingTeamNameCollisionGetsNoOwnerLink
// for catalogRows. A version that only checks the winning team's entity page
// links correctly would pass against the teamSlugMap bug this pins: a bare
// per-name Slug lookup with no collision tracking would let the losing name
// share the winner's slug, linking the owner to the wrong team's page.
func TestEntityPageGivesTheLosingTeamNameCollisionNoOwnerLink(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	var c diag.Collector
	in.Teams = config.LoadTeams("teams.yaml", []byte(
		"teams:\n"+
			"  - name: payments-team\n"+
			"    members: [alice]\n"+
			"  - name: Payments Team\n"+
			"    members: [bob]\n"), &c)
	// config.Teams.Names() sorts, and "Payments Team" (capital P, 0x50)
	// sorts before "payments-team" (0x70), so it claims the slug
	// "payments-team" first; "payments-team" itself is the losing name.
	in.Catalog.Entities()[0].Metadata.Owner = "payments-team"

	page := string(siteMap(Site(in, &c))["entity/service/api/index.html"])
	if strings.Contains(page, `href="../../../team/payments-team/"`) {
		t.Errorf("the losing side of a slug collision must not link to the winning team's page:\n%s", page)
	}
	if !strings.Contains(page, "<dd>payments-team</dd>") {
		t.Errorf("the owner name must still render:\n%s", page)
	}
}

// Spec §12: the degraded-mode banner must appear on every page. The entity
// page inherits it through newPage/Page the same way the catalog page does,
// but nothing pinned that with a committed test until now.
func TestEntityPageShowsTheDegradedNoticeBanner(t *testing.T) {
	in := twoEntities(t)
	in.Notice = "degraded: could not fetch team-payments from GitHub"
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["entity/service/ledger-api/index.html"])
	if !strings.Contains(page, `<div class="notice" role="status">degraded: could not fetch team-payments from GitHub</div>`) {
		t.Errorf("the degraded-mode notice must render on the entity page:\n%s", page)
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
