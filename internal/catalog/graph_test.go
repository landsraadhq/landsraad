package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func entDeps(name string, kind Kind, deps ...string) *Entity {
	e := ent("monorepo", "services/"+name+"/service.yaml", name, kind, 4)
	e.Spec.DependsOn = deps
	return e
}

func TestResolveReportsDanglingRefs(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:nowhere"),
	}, &c)
	cat.Resolve(FullCatalog, &c)

	if !c.HasErrors() {
		t.Fatal("a dangling reference must be an error when resolving the full catalog")
	}
	got := c.Diagnostics()[0]
	if want := "service:a dependsOn service:nowhere, which is not in the catalog"; got.Message != want {
		t.Errorf("dangling-ref message\n got: %s\nwant: %s", got.Message, want)
	}
	if want := "check the spelling, or add the missing entity"; got.Hint != want {
		t.Errorf("dangling-ref hint\n got: %s\nwant: %s", got.Hint, want)
	}
	if got.File != "services/a/service.yaml" {
		t.Errorf("dangling-ref File = %q, want %q", got.File, "services/a/service.yaml")
	}
	if got.Line != 4 {
		t.Errorf("dangling-ref Line = %d, want 4", got.Line)
	}
}

func TestResolveToleratesDanglingRefsWhenLocal(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:in-another-repo"),
	}, &c)
	cat.Resolve(LocalOnly, &c)

	if c.HasErrors() {
		t.Errorf("`landsraad validate` runs on one repo and cannot see the others; "+
			"cross-repo refs must not be errors there. Got: %+v", c.Diagnostics())
	}
}

func TestResolveReportsMalformedRefs(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{entDeps("a", KindService, "noprefix")}, &c)
	cat.Resolve(LocalOnly, &c)

	if !c.HasErrors() {
		t.Fatal("a malformed reference is an error even in local mode — it can never resolve")
	}
	got := c.Diagnostics()[0]
	wantMessage := `dependsOn: reference "noprefix" has no kind prefix, want the form kind:name (for example service:ledger-api)`
	if got.Message != wantMessage {
		t.Errorf("malformed-ref message\n got: %s\nwant: %s", got.Message, wantMessage)
	}
	wantHint := "references look like service:ledger-api or topic:payments.events"
	if got.Hint != wantHint {
		t.Errorf("malformed-ref hint\n got: %s\nwant: %s", got.Hint, wantHint)
	}
	if got.File != "services/a/service.yaml" {
		t.Errorf("malformed-ref File = %q, want %q", got.File, "services/a/service.yaml")
	}
	if got.Line != 4 {
		t.Errorf("malformed-ref Line = %d, want 4", got.Line)
	}
}

func TestCyclesDetectsASimpleCycle(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b"),
		entDeps("b", KindService, "service:a"),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 {
		t.Fatalf("a <-> b is one cycle, got %d: %v", len(cycles), cycles)
	}
	// Assert the members, not just the count: a stub that returns any
	// non-empty slice would satisfy a length check.
	members := map[string]bool{}
	for _, r := range cycles[0] {
		members[r.String()] = true
	}
	if len(members) != 2 || !members["service:a"] || !members["service:b"] {
		t.Errorf("cycle members = %v, want service:a and service:b", cycles[0])
	}
}

func TestResolveValidatesProvidesApis(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/a/service.yaml", "a", KindService, 4)
	e.Spec.ProvidesApis = []string{"api:typo"}
	cat := NewCatalog([]*Entity{e}, &c)
	cat.Resolve(FullCatalog, &c)

	if !c.HasErrors() {
		t.Fatal("a providesApis typo must be caught now, not silently accepted " +
			"until a later release starts resolving the field")
	}
	got := c.Diagnostics()[0]
	if got.Message != "service:a providesApis api:typo, which is not in the catalog" {
		t.Errorf("message = %q", got.Message)
	}
	if want := "check the spelling, or add the missing entity"; got.Hint != want {
		t.Errorf("providesApis dangling-ref hint\n got: %s\nwant: %s", got.Hint, want)
	}
	if got.File != "services/a/service.yaml" {
		t.Errorf("providesApis dangling-ref File = %q, want %q", got.File, "services/a/service.yaml")
	}
	if got.Line != 4 {
		t.Errorf("providesApis dangling-ref Line = %d, want 4", got.Line)
	}
}

// providesApis is validated but must NOT become a dependency edge.
func TestProvidesApisIsNotADependencyEdge(t *testing.T) {
	var c diag.Collector
	svc := ent("monorepo", "services/a/service.yaml", "a", KindService, 4)
	svc.Spec.ProvidesApis = []string{"api:billing"}
	api := ent("monorepo", "apis/billing/service.yaml", "billing", KindAPI, 4)
	cat := NewCatalog([]*Entity{svc, api}, &c)
	g := cat.Resolve(FullCatalog, &c)

	if c.HasErrors() {
		t.Fatalf("both entities exist: %+v", c.Diagnostics())
	}
	if got := g.DependsOn(Ref{Kind: KindService, Name: "a"}); len(got) != 0 {
		t.Errorf("providesApis must not create a dependency edge, got %v", got)
	}
}

func TestCyclesDetectsASelfDependency(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{entDeps("a", KindService, "service:a")}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 1 || cycles[0][0].String() != "service:a" {
		t.Errorf("a service depending on itself is a cycle, got %v", cycles)
	}
}

func TestCyclesDetectsALongerChain(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b"),
		entDeps("b", KindService, "service:c"),
		entDeps("c", KindService, "service:a"),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	cycles := g.Cycles()
	if len(cycles) != 1 || len(cycles[0]) != 3 {
		t.Errorf("a -> b -> c -> a is one 3-node cycle, got %v", cycles)
	}
}

// DependsOn is the forward-edge accessor the portal's dependency graph needs.
func TestDependsOnReturnsForwardEdges(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b", "service:c"),
		entDeps("b", KindService),
		entDeps("c", KindService),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	got := g.DependsOn(Ref{Kind: KindService, Name: "a"})
	if len(got) != 2 || got[0].String() != "service:b" || got[1].String() != "service:c" {
		t.Errorf("DependsOn must return sorted forward edges, got %v", got)
	}
}

func TestCyclesIgnoresADiamond(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "service:b", "service:c"),
		entDeps("b", KindService, "service:d"),
		entDeps("c", KindService, "service:d"),
		entDeps("d", KindService),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	if cycles := g.Cycles(); len(cycles) != 0 {
		t.Errorf("a diamond is not a cycle, got %v", cycles)
	}
}

func TestDependentsReturnsReverseEdges(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		entDeps("a", KindService, "topic:t"),
		entDeps("b", KindService, "topic:t"),
		ent("monorepo", "topics/t/service.yaml", "t", KindTopic, 4),
	}, &c)
	g := cat.Resolve(FullCatalog, &c)

	got := g.Dependents(Ref{Kind: KindTopic, Name: "t"})
	if len(got) != 2 {
		t.Fatalf("topic:t has two consumers, got %d: %v", len(got), got)
	}
	if got[0].String() != "service:a" || got[1].String() != "service:b" {
		t.Errorf("Dependents must be sorted, got %v", got)
	}
}
