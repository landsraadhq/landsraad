package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestTeamPageListsMembersAndContacts(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	for _, want := range []string{"alice", "bob", "#payments", "PAY"} {
		if !strings.Contains(page, want) {
			t.Errorf("team page is missing %q:\n%s", want, page)
		}
	}
}

func TestTeamPageListsWhatItOwns(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	if !strings.Contains(page, `href="../../entity/service/ledger-api/"`) {
		t.Errorf("owned entities must link to their pages:\n%s", page)
	}
	if !strings.Contains(page, "payments-events") {
		t.Errorf("every owned entity must appear, including untiered ones:\n%s", page)
	}
}

func TestTeamPageShowsTheAggregateScore(t *testing.T) {
	var c diag.Collector
	page := string(siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
	if !strings.Contains(page, "aggregate") {
		t.Errorf("the team's aggregate score must be labelled:\n%s", page)
	}
}

// A team that owns nothing still gets a page. Omitting it would turn every
// link to that team into a 404, and make "owns nothing" indistinguishable
// from "does not exist" — the distinction teams.yaml exists to record.
func TestATeamThatOwnsNothingStillGetsAPage(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	// teamsFromYAML adds a second team with no entities.
	in = withTeams(t, in, testTeamsYAML+
		"  - name: team-platform\n    members: [carol]\n    slack: \"#plat\"\n    pagerduty: PLT\n")

	var c diag.Collector
	files := siteMap(Site(in, &c))
	page, ok := files["team/team-platform/index.html"]
	if !ok {
		t.Fatalf("a team with no entities must still have a page; got %v", keys(files))
	}
	if !strings.Contains(string(page), "owns nothing in this catalog") {
		t.Errorf("and it must say so:\n%s", page)
	}
}

func TestTeamPageIsGolden(t *testing.T) {
	var c diag.Collector
	golden(t, "team-team-payments.html", siteMap(Site(twoEntities(t), &c))["team/team-payments/index.html"])
}

func TestTeamPageShowsTheDegradedNoticeBanner(t *testing.T) {
	in := twoEntities(t)
	in.Notice = "This portal is degraded."
	var c diag.Collector
	page := string(siteMap(Site(in, &c))["team/team-payments/index.html"])
	if !strings.Contains(page, "This portal is degraded.") {
		t.Errorf("the degraded notice must appear on the team page:\n%s", page)
	}
}
