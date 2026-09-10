package render

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// testNow is the frozen clock every render test uses. Nothing below cmd/
// reads the clock, so a page's build timestamp is an input — which is what
// makes the golden files stable.
var testNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

// ent builds one entity for a test catalog.
func ent(name string, kind catalog.Kind, owner string, tier int) *catalog.Entity {
	return &catalog.Entity{
		APIVersion: catalog.APIVersion,
		Kind:       kind,
		Metadata: catalog.Metadata{
			Name: name, Owner: owner, Tier: tier,
			Description: "The " + name + " entity.",
			Lifecycle:   "production",
			Tags:        []string{"go"},
		},
		Spec:       catalog.Spec{Path: "services/" + name},
		SourcePath: "services/" + name + "/service.yaml",
		NameLine:   4,
	}
}

const testTeamsYAML = "teams:\n" +
	"  - name: team-payments\n" +
	"    members: [alice, bob]\n" +
	"    slack: \"#payments\"\n" +
	"    pagerduty: PAY\n"

// input builds a complete render.Input from entities, so each test names
// only what it cares about.
func input(t *testing.T, files fstest.MapFS, entities ...*catalog.Entity) Input {
	t.Helper()
	var c diag.Collector
	cat := catalog.NewCatalog(entities, &c)
	g := cat.Resolve(catalog.FullCatalog, &c)
	teams := config.LoadTeams("teams.yaml", []byte(testTeamsYAML), &c)
	std := config.DefaultStandards()
	if files == nil {
		files = fstest.MapFS{}
	}
	env := scorecard.Env{
		Sources: catalog.SingleSource("", files), Now: testNow, MaxDocsAgeDays: 180,
		LastEdit: func(string, string) (time.Time, bool) { return time.Time{}, false },
	}
	sc := scorecard.Score(cat, std, nil, env, &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("fixture is not clean: %+v", ds)
	}
	return Input{
		Catalog: cat, Graph: g, Teams: teams, Scorecard: sc, Standards: std,
		FS: files, GeneratedAt: testNow, Version: "v0.3.0-test",
		Mermaid: Mermaid{Src: DefaultMermaidSrc, Integrity: DefaultMermaidIntegrity},
	}
}

func TestNewPageCarriesTheRelativeRoot(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	p := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if p.Root != "../../../" {
		t.Errorf("Root = %q, want %q", p.Root, "../../../")
	}
	if p.Title != "api" {
		t.Errorf("Title = %q, want %q", p.Title, "api")
	}
	if p.Nav != "catalog" {
		t.Errorf("Nav = %q, want %q", p.Nav, "catalog")
	}
	if p.GeneratedAt != "2026-09-09 12:00 UTC" {
		t.Errorf("GeneratedAt = %q, want %q", p.GeneratedAt, "2026-09-09 12:00 UTC")
	}
}

// A remote Mermaid stays absolute; a local one is rewritten per page, since
// "assets/mermaid.min.js" means something different three levels down.
func TestNewPageResolvesALocalMermaidRelativeToThePage(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	in.Mermaid = Mermaid{Src: LocalMermaidPath}

	deep := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if deep.Mermaid.Src != "../../../assets/mermaid.min.js" {
		t.Errorf("deep Src = %q", deep.Mermaid.Src)
	}
	if deep.Mermaid.Integrity != "" {
		t.Errorf("a local file carries no integrity attribute, got %q", deep.Mermaid.Integrity)
	}

	top := newPage(in, "index.html", "Catalog", "catalog")
	if top.Mermaid.Src != "assets/mermaid.min.js" {
		t.Errorf("top Src = %q", top.Mermaid.Src)
	}
}

func TestNewPageLeavesARemoteMermaidAbsolute(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	p := newPage(in, "entity/service/api/index.html", "api", "catalog")
	if p.Mermaid.Src != DefaultMermaidSrc {
		t.Errorf("Src = %q, want %q", p.Mermaid.Src, DefaultMermaidSrc)
	}
	if !strings.HasPrefix(p.Mermaid.Integrity, "sha384-") {
		t.Errorf("the pinned CDN URL must carry an SRI hash, got %q", p.Mermaid.Integrity)
	}
}

func TestCatalogRowsAreSortedByRef(t *testing.T) {
	in := input(t, nil,
		ent("zebra", catalog.KindService, "team-payments", 1),
		ent("alpha", catalog.KindService, "team-payments", 2),
	)
	rows := catalogRows(in)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Ref != "service:alpha" || rows[1].Ref != "service:zebra" {
		t.Errorf("rows are not sorted by ref: %q, %q", rows[0].Ref, rows[1].Ref)
	}
}

// Plan 2's ruling R1: an entity with no tier is not scored. Rendering that
// as 0% would brand every library in the catalog for a check nobody ran.
func TestAnUntieredEntityHasNoScoreRatherThanZero(t *testing.T) {
	in := input(t, nil,
		ent("api", catalog.KindService, "team-payments", 1),
		ent("shared", catalog.KindLibrary, "team-payments", 0),
	)
	rows := catalogRows(in)
	byRef := map[string]CatalogRow{}
	for _, r := range rows {
		byRef[r.Ref] = r
	}
	if byRef["library:shared"].Score != nil {
		t.Errorf("an untiered entity must have no score, got %v", *byRef["library:shared"].Score)
	}
	if byRef["service:api"].Score == nil {
		t.Error("a tiered entity must have a score")
	}
}

func TestCatalogRowsCarryTheirTeamURL(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	rows := catalogRows(in)
	if rows[0].OwnerURL != "team/team-payments/" {
		t.Errorf("OwnerURL = %q, want %q", rows[0].OwnerURL, "team/team-payments/")
	}
}

// An owner that resolves to no team gets no link. A link to a page that was
// never generated is a 404 the portal itself created.
func TestAnUnknownOwnerGetsNoTeamLink(t *testing.T) {
	in := input(t, nil, ent("api", catalog.KindService, "team-payments", 1))
	in.Catalog.Entities()[0].Metadata.Owner = "team-ghost"
	rows := catalogRows(in)
	if rows[0].OwnerURL != "" {
		t.Errorf("OwnerURL = %q, want empty for an unresolvable owner", rows[0].OwnerURL)
	}
	if rows[0].Owner != "team-ghost" {
		t.Errorf("the owner name is still shown, got %q", rows[0].Owner)
	}
}

// The losing side of a team-name collision must not silently share the
// winning name's slug: that would point the entity's OwnerURL at a page that
// was actually rendered with the other team's members and on-call. No link
// is honest; a link to the wrong team is corruption a reader would not
// notice. This pins the defect directly — a version that only checks the
// winning team still links correctly (TestCatalogRowsCarryTheirTeamURL)
// would pass against the bug this test catches.
func TestALosingTeamNameCollisionGetsNoOwnerLink(t *testing.T) {
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

	rows := catalogRows(in)
	if rows[0].OwnerURL != "" {
		t.Errorf("OwnerURL = %q, want empty for the losing side of a slug collision", rows[0].OwnerURL)
	}
	if rows[0].Owner != "payments-team" {
		t.Errorf("the owner name is still shown, got %q", rows[0].Owner)
	}
}

// withTeams replaces an Input's teams, for tests about teams that own
// nothing.
func withTeams(t *testing.T, in Input, yaml string) Input {
	t.Helper()
	var c diag.Collector
	in.Teams = config.LoadTeams("teams.yaml", []byte(yaml), &c)
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Fatalf("teams fixture is not clean: %+v", ds)
	}
	return in
}
