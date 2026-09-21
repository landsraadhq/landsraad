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
	expectedMsg := "expected a number, found a string"
	if got.Message != expectedMsg {
		t.Errorf("Message = %q, want %q", got.Message, expectedMsg)
	}
}

// A second YAML document is the shape a Kubernetes user reaches for first:
// the Service and the Topic it publishes in one file. yaml.Unmarshal decodes
// document one and returns no error for the rest, so every entity after the
// first was validated by nothing at all while the run reported a clean pass.
func TestParseFileRejectsASecondDocument(t *testing.T) {
	// validYAML is 12 lines, so the `---` separator lands on line 13.
	in := validYAML + `---
apiVersion: landsraad/v1
kind: Topic
metadata:
  name: NOT_A_LEGAL_NAME
  owner: no-such-team
  lifecycle: production
  bogusField: x
`
	var c diag.Collector
	_, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(in), &c)
	if ok {
		t.Fatal("a multi-document file must not parse successfully: everything after the first document would go unvalidated")
	}
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("want exactly 1 diagnostic, got %d: %+v", len(c.Diagnostics()), c.Diagnostics())
	}
	got := c.Diagnostics()[0]
	if want := "services/payments-worker/service.yaml"; got.File != want {
		t.Errorf("File\n got: %s\nwant: %s", got.File, want)
	}
	if got.Line != 13 {
		t.Errorf("Line = %d, want 13 — the `---` that starts the second document", got.Line)
	}
	if want := "yaml-documents"; got.Check != want {
		t.Errorf("Check\n got: %s\nwant: %s", got.Check, want)
	}
	if want := "service.yaml must contain exactly one document, found 2"; got.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", got.Message, want)
	}
	if want := "landsraad reads one entity per file: move the document after `---` into its own service.yaml"; got.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", got.Hint, want)
	}
}

// The same file with a second document that is not even valid YAML: yaml.v3
// parses the first document lazily, so this too returned no error at all.
func TestParseFileRejectsASecondDocumentThatIsBrokenYAML(t *testing.T) {
	in := validYAML + "---\nfoo: bar: baz\n" // `---` on line 13, the broken mapping on 14
	var c diag.Collector
	_, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(in), &c)
	if ok {
		t.Fatal("a file whose second document is unparseable must not parse successfully")
	}
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("want exactly 1 diagnostic, got %d: %+v", len(c.Diagnostics()), c.Diagnostics())
	}
	got := c.Diagnostics()[0]
	if got.Line != 14 {
		t.Errorf("Line = %d, want 14 — where the parser gave up inside the second document", got.Line)
	}
	if want := "service.yaml must contain exactly one document, found 2"; got.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", got.Message, want)
	}
}

// A leading `---` opens the first document rather than adding one, and a
// trailing `---` closes it. Neither is a second entity, so neither may be
// rejected — over-correcting here would break files that are perfectly fine.
func TestParseFileAcceptsDocumentMarkers(t *testing.T) {
	for name, in := range map[string]string{
		"leading":  "---\n" + validYAML,
		"trailing": validYAML + "---\n",
		"both":     "---\n" + validYAML + "---\n",
	} {
		t.Run(name, func(t *testing.T) {
			var c diag.Collector
			e, ok := ParseFile("monorepo", "services/payments-worker/service.yaml", []byte(in), &c)
			if !ok {
				t.Fatalf("ParseFile failed, diagnostics: %+v", c.Diagnostics())
			}
			if e.Metadata.Name != "payments-worker" {
				t.Errorf("Name = %q, want %q", e.Metadata.Name, "payments-worker")
			}
			if c.Len() != 0 {
				t.Errorf("want no diagnostics, got %+v", c.Diagnostics())
			}
		})
	}
}

// Ruling R52's comment claims the NameLine fallback "should be unreachable"
// for a parsed entity. A YAML alias reaches it: seqItemLines requires a
// SequenceNode, an alias is an AliasNode, and the decoder resolves the alias
// so Spec.DependsOn is populated while its lines are not.
//
// The degradation was harmless — it fell back to the pre-R52 line — but the
// comment's whole purpose is that the fallback "cannot quietly become
// load-bearing", and it was load-bearing here. Following the alias is four
// lines and removes the exception rather than documenting it.
const aliasedRefsYAML = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: a
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
  path: services/a
  providesApis: &shared
    - api:one
  dependsOn: *shared
`

func TestParseFileFollowsAYAMLAliasForReferenceLines(t *testing.T) {
	var c diag.Collector
	e, ok := ParseFile("monorepo", "services/a/service.yaml", []byte(aliasedRefsYAML), &c)
	if !ok {
		t.Fatalf("fixture must parse: %+v", c.Diagnostics())
	}
	// "- api:one" is line 12; both fields resolve to that same sequence.
	if got := e.refLines[FieldProvidesApis]; len(got) != 1 || got[0] != 12 {
		t.Errorf("providesApis lines = %v, want [12]", got)
	}
	if got := e.refLines[FieldDependsOn]; len(got) != 1 || got[0] != 12 {
		t.Errorf("dependsOn lines = %v, want [12] — an alias is still a sequence", got)
	}
	if got := e.RefLine(FieldDependsOn, 0); got != 12 {
		t.Errorf("RefLine(dependsOn, 0) = %d, want 12, not the NameLine fallback", got)
	}
}
