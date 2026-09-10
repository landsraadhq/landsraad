package render

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
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

// The losing side of a team-name collision must not render a page at the
// shared slug. This is the third call site composing teamSlugMap (after
// catalogRows and entityPages), both of which have regression tests for this.
// Without one here, a future refactor inlining a bare Slug call would silently
// clobber a team's page. The assertion must count: a content check alone
// passes even when both teams render (the first write wins in the file).
func TestLosingTeamNameCollisionGetsNoTeamPage(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	var c diag.Collector
	in.Teams = config.LoadTeams("teams.yaml", []byte(
		"teams:\n"+
			"  - name: payments-team\n"+
			"    members: [alice]\n"+
			"  - name: Payments Team\n"+
			"    members: [bob]\n"), &c)
	// config.Teams.Names() sorts, and "Payments Team" (capital P, 0x50)
	// sorts before "payments-team" (0x70), so it claims the slug first;
	// "payments-team" itself is the losing name.

	files := Site(in, &c)

	// Count the files at the collision path. If both teams rendered, there
	// would be two emit.Files with the same path. If the guard removed, there
	// would be an extra at "team//index.html". Do NOT use siteMap, which
	// silently collapses duplicates and hides the very bug we are pinning.
	var collisionPathCount int
	var hasWinner bool
	var hasMalformed bool
	var teamPaths []string

	for _, f := range files {
		if strings.HasPrefix(f.Path, "team/") {
			teamPaths = append(teamPaths, f.Path)
		}
		if f.Path == "team/payments-team/index.html" {
			collisionPathCount++
			if strings.Contains(string(f.Data), "bob") {
				hasWinner = true
			} else if strings.Contains(string(f.Data), "alice") {
				t.Errorf("collision path contains loser's content (alice), not winner's (bob)")
			}
		}
		if f.Path == "team//index.html" {
			hasMalformed = true
		}
	}

	if collisionPathCount != 1 {
		t.Errorf("collision path must exist exactly once, got %d times; all team paths: %v", collisionPathCount, teamPaths)
	}
	if !hasWinner {
		t.Errorf("collision path must contain winner's content (bob)")
	}
	if hasMalformed {
		t.Errorf("malformed path team//index.html must not exist; all team paths: %v", teamPaths)
	}
}

// A team owning only untiered entities must show "not scored" at the
// aggregate level, never "0%". This pins Plan 2's ruling R1 at this call site.
func TestTeamOwningOnlyUntiteredEntitiesShowsNotScoredAggregate(t *testing.T) {
	in := input(t, nil, ent("lib", catalog.KindLibrary, "team-platform", 0))
	// Add team-platform to the teams fixture.
	in = withTeams(t, in, testTeamsYAML+
		"  - name: team-platform\n    members: [carol]\n    slack: \"#plat\"\n    pagerduty: PLT\n")

	var c diag.Collector
	page := string(siteMap(Site(in, &c))["team/team-platform/index.html"])
	if !strings.Contains(page, "nothing this team owns was scored") {
		t.Errorf("an all-untiered team must show 'nothing scored', not 0%%:\n%s", page)
	}
}
