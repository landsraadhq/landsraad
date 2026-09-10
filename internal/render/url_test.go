package render

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

func TestEntityURLIsKeyedOnTheRef(t *testing.T) {
	// service:orders and topic:orders may coexist (spec §12), so the kind is
	// part of the path and not a disambiguating suffix.
	svc := EntityURL(catalog.Ref{Kind: catalog.KindService, Name: "orders"})
	top := EntityURL(catalog.Ref{Kind: catalog.KindTopic, Name: "orders"})
	if svc != "entity/service/orders/" {
		t.Errorf("EntityURL(service:orders) = %q, want %q", svc, "entity/service/orders/")
	}
	if top != "entity/topic/orders/" {
		t.Errorf("EntityURL(topic:orders) = %q, want %q", top, "entity/topic/orders/")
	}
	if svc == top {
		t.Error("two entities sharing a name must not share a URL")
	}
}

func TestEntityPathIsTheDirectoryIndex(t *testing.T) {
	got := EntityPath(catalog.Ref{Kind: catalog.KindWorker, Name: "payments.events"})
	if got != "entity/worker/payments.events/index.html" {
		t.Errorf("EntityPath = %q", got)
	}
}

// A directory-per-page means every URL ends in "/" and needs no rewrite rule
// from whatever static host the team puts this behind (ruling R11).
func TestEveryEntityURLEndsInASlash(t *testing.T) {
	for _, k := range catalog.AllKinds() {
		u := EntityURL(catalog.Ref{Kind: k, Name: "x"})
		if u[len(u)-1] != '/' {
			t.Errorf("EntityURL for %s = %q, want a trailing slash", k, u)
		}
	}
}

func TestRootRelCountsDepth(t *testing.T) {
	cases := []struct{ path, want string }{
		{"index.html", ""},
		{"search-index.json", ""},
		{"scorecard/index.html", "../"},
		{"entity/service/api/index.html", "../../../"},
		{"entity/service/api/docs/runbook.html", "../../../../"},
	}
	for _, c := range cases {
		if got := rootRel(c.path); got != c.want {
			t.Errorf("rootRel(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestSlugMakesALegalPathSegment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"team-payments", "team-payments"},
		{"Payments Team", "payments-team"},
		{"platform/infra", "platform-infra"},
		{"  spaced  out  ", "spaced-out"},
		{"Ünïcødé", "n-c-d"},
	}
	for _, c := range cases {
		got, ok := Slug(c.in)
		if !ok {
			t.Errorf("Slug(%q) reported failure", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("Slug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// No silent fallback: a name with nothing sluggable in it produces no URL,
// and the caller reports it. Inventing "team-1" would put a page on the site
// that nobody can find from the name they know.
func TestSlugReportsFailureRatherThanInventingAName(t *testing.T) {
	for _, in := range []string{"", "###", "   "} {
		if got, ok := Slug(in); ok {
			t.Errorf("Slug(%q) = %q, ok — want a reported failure", in, got)
		}
	}
}

func TestTeamSlugsReportsACollision(t *testing.T) {
	var c diag.Collector
	teams := config.LoadTeams("teams.yaml", []byte(
		"teams:\n"+
			"  - name: payments-team\n"+
			"    members: [alice]\n"+
			"  - name: Payments Team\n"+
			"    members: [bob]\n"), &c)

	slugs := TeamSlugs(teams, &c)

	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	if ds[0].Severity != diag.SevError {
		t.Errorf("Severity = %v, want SevError", ds[0].Severity)
	}
	// config.Teams.Names() sorts, and "Payments Team" (capital P, 0x50)
	// sorts before "payments-team" (0x70) — so it is the first claimant and
	// the one the message names first. The order is arbitrary from the
	// user's point of view but it is DETERMINISTIC, which is what a
	// diagnostic asserted by an exact string needs.
	want := `teams "Payments Team" and "payments-team" both produce the page team/payments-team/`
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
	wantHint := "rename one of them; a team's page URL is derived from its name"
	if ds[0].Hint != wantHint {
		t.Errorf("Hint = %q, want %q", ds[0].Hint, wantHint)
	}
	if ds[0].File != "teams.yaml" {
		t.Errorf("File = %q, want %q", ds[0].File, "teams.yaml")
	}
	// The first claimant keeps the slug so the rest of the render still has
	// somewhere to link; the diagnostic is what stops the build.
	if slugs["Payments Team"] != "payments-team" {
		t.Errorf("first claimant lost its slug: %v", slugs)
	}
	if _, ok := slugs["payments-team"]; ok {
		t.Errorf("the losing team must get no slug: %v", slugs)
	}
}

func TestTeamSlugsReportsAnUnsluggableName(t *testing.T) {
	var c diag.Collector
	teams := config.LoadTeams("teams.yaml", []byte(
		"teams:\n  - name: \"###\"\n    members: [alice]\n"), &c)

	if slugs := TeamSlugs(teams, &c); len(slugs) != 0 {
		t.Errorf("got slugs %v, want none", slugs)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	want := `team "###" has no usable page URL: its name contains no letters or digits`
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
}
