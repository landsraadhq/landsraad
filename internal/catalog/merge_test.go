package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func ent(repo, path, name string, kind Kind, line int) *Entity {
	e := &Entity{Kind: kind}
	e.Metadata.Name = name
	e.SourceRepo = repo
	e.SourcePath = path
	e.NameLine = line
	return e
}

func TestNewCatalogIndexesByRef(t *testing.T) {
	var c diag.Collector
	cat := NewCatalog([]*Entity{
		ent("monorepo", "services/a/service.yaml", "a", KindService, 4),
		ent("monorepo", "topics/t/service.yaml", "payments.events", KindTopic, 4),
	}, &c)

	if c.HasErrors() {
		t.Fatalf("unexpected errors: %+v", c.Diagnostics())
	}
	if _, ok := cat.Lookup(Ref{Kind: KindService, Name: "a"}); !ok {
		t.Error("service:a must be found")
	}
	if _, ok := cat.Lookup(Ref{Kind: KindTopic, Name: "payments.events"}); !ok {
		t.Error("topic:payments.events must be found")
	}
	if _, ok := cat.Lookup(Ref{Kind: KindService, Name: "missing"}); ok {
		t.Error("service:missing must not be found")
	}
}

func TestNewCatalogRejectsDuplicateNames(t *testing.T) {
	var c diag.Collector
	NewCatalog([]*Entity{
		ent("monorepo", "services/api/service.yaml", "api", KindService, 4),
		ent("edge-gateway", "service.yaml", "api", KindService, 4),
	}, &c)

	if !c.HasErrors() {
		t.Fatal("a duplicate name must be an error")
	}
	// Spec §14: assert the EXACT string. Error message quality is the product,
	// so a wording regression must fail a test rather than slip through a
	// substring check.
	// NewCatalog sorts by (repo, path) so the winner of a collision is
	// deterministic and does not depend on filesystem walk order.
	// "edge-gateway" < "monorepo", so edge-gateway holds the index slot.
	got := c.Diagnostics()[0]
	wantMessage := `duplicate entity name "api": already defined as service:api in ` +
		`edge-gateway:service.yaml (line 4), redefined in ` +
		`monorepo:services/api/service.yaml`
	if got.Message != wantMessage {
		t.Errorf("collision message\n got: %s\nwant: %s", got.Message, wantMessage)
	}
	wantHint := "names must be unique across the merged catalog; rename one of them"
	if got.Hint != wantHint {
		t.Errorf("collision hint\n got: %s\nwant: %s", got.Hint, wantHint)
	}
	if got.File == "" {
		t.Error("collision diagnostic must carry a File")
	}
	if got.Line == 0 {
		t.Error("collision diagnostic must carry a nonzero Line")
	}
}

func TestNewCatalogAllowsSameNameDifferentKind(t *testing.T) {
	var c diag.Collector
	NewCatalog([]*Entity{
		ent("monorepo", "services/orders/service.yaml", "orders", KindService, 4),
		ent("monorepo", "topics/orders/service.yaml", "orders", KindTopic, 4),
	}, &c)

	if c.HasErrors() {
		t.Errorf("service:orders and topic:orders are distinct refs and must coexist: %+v", c.Diagnostics())
	}
}
