package catalog

import (
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/diag"
)

func repoFS() fstest.MapFS {
	return fstest.MapFS{
		"services/payments-worker/docs/runbook.md": {Data: []byte("# runbook\n")},
		"services/payments-worker/docs/index.md":   {Data: []byte("# docs\n")},
		"services/payments-worker/alerts.yaml":     {Data: []byte("groups: []\n")},
	}
}

func TestCheckFilesAcceptsExistingPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/runbook.md"
	e.Spec.Docs = "services/payments-worker/docs"
	e.Spec.Alerts = "services/payments-worker/alerts.yaml"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if c.HasErrors() {
		t.Errorf("existing paths must pass: %+v", c.Diagnostics())
	}
}

func TestCheckFilesReportsMissingRunbook(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Runbook = "services/payments-worker/docs/nope.md"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if !c.HasErrors() {
		t.Fatal("a runbook path that does not exist must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	if d.Line != 4 {
		t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
	}
	want := `spec.runbook points at "services/payments-worker/docs/nope.md", which does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "paths are relative to the repository root, slash-separated"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestCheckFilesReportsMissingAlerts(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Alerts = "services/payments-worker/alerts-nope.yaml"
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if !c.HasErrors() {
		t.Fatal("an alerts path that does not exist must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	if d.Line != 4 {
		t.Errorf("the diagnostic must point at the entity's line, got %d", d.Line)
	}
	want := `spec.alerts points at "services/payments-worker/alerts-nope.yaml", which does not exist`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "paths are relative to the repository root, slash-separated"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

func TestCheckFilesRejectsFileWhereDirectoryExpected(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/payments-worker/service.yaml", "payments-worker", KindService, 4)
	e.Spec.Docs = "services/payments-worker/docs/index.md" // a file, not a dir
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if !c.HasErrors() {
		t.Fatal("spec.docs must be a directory")
	}
	d := c.Diagnostics()[0]
	if d.Check != "missing-file" {
		t.Errorf("Check = %q, want %q", d.Check, "missing-file")
	}
	want := `spec.docs points at "services/payments-worker/docs/index.md", which is a file but must be a directory`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Hint != "" {
		t.Errorf("Hint = %q, want empty", d.Hint)
	}
}

func TestCheckFilesIgnoresEmptyPaths(t *testing.T) {
	var c diag.Collector
	e := ent("monorepo", "services/ledger-api/service.yaml", "ledger-api", KindService, 4)
	cat := NewCatalog([]*Entity{e}, &c)

	CheckFiles(repoFS(), cat, &c)

	if c.HasErrors() {
		t.Errorf("an unset optional path is not a missing file: %+v", c.Diagnostics())
	}
}
