package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
)

func TestParseAllIsPureAndOrdered(t *testing.T) {
	var c diag.Collector
	files := []discover.File{
		{Path: "services/b/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: b\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
		{Path: "services/a/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: a\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
	}
	got := ParseAll("monorepo", files, &c)
	if c.HasErrors() {
		t.Fatalf("valid files must parse: %+v", c.Diagnostics())
	}
	if len(got) != 2 {
		t.Fatalf("got %d entities, want 2", len(got))
	}
	// ParseAll preserves input order; sorting is NewCatalog's job.
	if got[0].Metadata.Name != "b" || got[1].Metadata.Name != "a" {
		t.Errorf("ParseAll must preserve input order, got %q then %q",
			got[0].Metadata.Name, got[1].Metadata.Name)
	}
	if got[0].SourcePath != "services/b/service.yaml" {
		t.Errorf("provenance not attached: %q", got[0].SourcePath)
	}
	if got[0].SourceRepo != "monorepo" {
		t.Errorf("repo provenance not attached: %q", got[0].SourceRepo)
	}
	if want := "monorepo:services/b/service.yaml"; got[0].Location() != want {
		t.Errorf("Location() = %q, want %q", got[0].Location(), want)
	}
}

func TestParseAllSkipsBrokenFilesAndKeepsGoing(t *testing.T) {
	var c diag.Collector
	files := []discover.File{
		{Path: "broken/service.yaml", Data: []byte("kind: Service\n  bad: indent\n")},
		{Path: "services/a/service.yaml", Data: []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: a\n  owner: t\n  tier: 1\n  lifecycle: production\n")},
	}
	got := ParseAll("monorepo", files, &c)
	if len(got) != 1 {
		t.Fatalf("the good file must still be parsed, got %d entities", len(got))
	}
	if !c.HasErrors() {
		t.Error("the broken file must still be reported")
	}
}
