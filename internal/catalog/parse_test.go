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
	if got.Message != "cannot parse YAML: yaml: line 2: mapping values are not allowed in this context" {
		t.Errorf("Message = %q, want %q", got.Message, "cannot parse YAML: yaml: line 2: mapping values are not allowed in this context")
	}
}

func TestParseFileReportsEmptyFile(t *testing.T) {
	var c diag.Collector
	_, ok := ParseFile("monorepo", "empty/service.yaml", []byte(""), &c)
	if ok {
		t.Fatal("empty file must not parse successfully")
	}
	if !c.HasErrors() {
		t.Fatal("empty file must produce an error diagnostic")
	}
	got := c.Diagnostics()[0]
	if got.File != "empty/service.yaml" {
		t.Errorf("File = %q, want %q", got.File, "empty/service.yaml")
	}
	if got.Line == 0 {
		t.Error("an empty file error must carry a line number")
	}
	if got.Message != "file is empty" {
		t.Errorf("Message = %q, want %q", got.Message, "file is empty")
	}
	if got.Hint != "a service.yaml needs at least apiVersion, kind and metadata.name" {
		t.Errorf("Hint = %q, want %q", got.Hint, "a service.yaml needs at least apiVersion, kind and metadata.name")
	}
}

func TestParseFileReportsDecodeError(t *testing.T) {
	var c diag.Collector
	_, ok := ParseFile("monorepo", "bad-type/service.yaml", []byte(`apiVersion: landsraad/v1
kind: Service
metadata:
  name: test
  tier: "not-an-integer"
spec:
  language: go
`), &c)
	if ok {
		t.Fatal("decode error must not parse successfully")
	}
	if !c.HasErrors() {
		t.Fatal("decode error must produce an error diagnostic")
	}
	got := c.Diagnostics()[0]
	if got.File != "bad-type/service.yaml" {
		t.Errorf("File = %q, want %q", got.File, "bad-type/service.yaml")
	}
	if got.Line == 0 {
		t.Error("a decode error must carry a line number")
	}
	expectedMsg := "cannot read as a catalog entity: yaml: unmarshal errors:\n  line 5: cannot unmarshal !!str `not-an-...` into int"
	if got.Message != expectedMsg {
		t.Errorf("Message = %q, want %q", got.Message, expectedMsg)
	}
}
