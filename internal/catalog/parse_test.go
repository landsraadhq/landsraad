package catalog

import (
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

const validYAML = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/payments-worker
  dependsOn:
    - topic:payments.events
`

func TestParseFileReadsFields(t *testing.T) {
	var c diag.Collector
	e, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(validYAML), &c)
	if !ok {
		t.Fatalf("ParseFile failed, diagnostics: %+v", c.Diagnostics())
	}
	if e.Metadata.Name != "payments-worker" {
		t.Errorf("Name = %q, want %q", e.Metadata.Name, "payments-worker")
	}
	if e.Kind != KindService {
		t.Errorf("Kind = %q, want %q", e.Kind, KindService)
	}
	if e.Metadata.Tier != 1 {
		t.Errorf("Tier = %d, want 1", e.Metadata.Tier)
	}
	if len(e.Spec.DependsOn) != 1 || e.Spec.DependsOn[0] != "topic:payments.events" {
		t.Errorf("DependsOn = %v, want [topic:payments.events]", e.Spec.DependsOn)
	}
}

func TestParseFileAttachesProvenance(t *testing.T) {
	var c diag.Collector
	e, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(validYAML), &c)
	if !ok {
		t.Fatal("ParseFile failed")
	}
	if e.SourceRepo != "monorepo" {
		t.Errorf("SourceRepo = %q, want %q", e.SourceRepo, "monorepo")
	}
	if e.SourcePath != "services/payments-worker/service.yaml" {
		t.Errorf("SourcePath = %q", e.SourcePath)
	}
	// metadata.name is on line 4 of validYAML (1-indexed).
	if e.NameLine != 4 {
		t.Errorf("NameLine = %d, want 4 — line numbers are the point of this task", e.NameLine)
	}
}

func TestParseFileReportsMalformedYAML(t *testing.T) {
	var c diag.Collector
	_, ok := ParseFile("monorepo", "broken/service.yaml", []byte("kind: Service\n  bad: indent\n"), &c)
	if ok {
		t.Fatal("malformed YAML must not parse successfully")
	}
	if !c.HasErrors() {
		t.Fatal("malformed YAML must produce an error diagnostic")
	}
	got := c.Diagnostics()[0]
	if got.File != "broken/service.yaml" {
		t.Errorf("File = %q, want %q", got.File, "broken/service.yaml")
	}
	if got.Line == 0 {
		t.Error("a YAML syntax error must carry a line number")
	}
}
