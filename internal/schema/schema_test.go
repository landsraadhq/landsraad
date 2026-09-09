package schema

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

const good = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
`

func mustDefault(t *testing.T) *Validator {
	t.Helper()
	v, err := Default()
	if err != nil {
		t.Fatalf("the embedded schema must compile: %v", err)
	}
	return v
}

func TestValidateAcceptsAGoodFile(t *testing.T) {
	var c diag.Collector
	if !mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(good), &c) {
		t.Fatalf("a valid file must pass: %+v", c.Diagnostics())
	}
}

func TestValidateRejectsUnknownFields(t *testing.T) {
	in := strings.Replace(good, "  language: go\n", "  language: go\n  nonsense: yes\n", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("unevaluatedProperties is false — an unknown field must be rejected, not ignored")
	}
	if !c.HasErrors() {
		t.Fatal("rejection must produce an error diagnostic")
	}
	if c.Diagnostics()[0].Line == 0 {
		t.Error("a schema violation must carry a line number")
	}
	// Without an exact message assertion, a Validate that emitted one
	// hardcoded "invalid" for every violation would pass every rejection
	// test in this file.
	//
	// The raw library message for this case is "false schema" — jargon for
	// "matched a schema that is literally `false`" — so Validate rewrites it
	// into a sentence naming the field, per the standard that "invalid
	// input" is worthless and "expected X, got Y" fixes itself.
	d := c.Diagnostics()[0]
	wantMsg := `at '/spec/nonsense': unknown field 'nonsense'`
	if d.Message != wantMsg {
		t.Errorf("message =\n%s\nwant   =\n%s", d.Message, wantMsg)
	}
	wantHint := "this schema rejects fields it doesn't define, rather than silently ignoring them — remove it, or check for a typo"
	if d.Hint != wantHint {
		t.Errorf("hint =\n%s\nwant =\n%s", d.Hint, wantHint)
	}
}

// The unknown-field rewrite must hold at any depth: the location path is
// what makes the message actionable in a real file, where the culprit is
// rarely at the top level.
func TestValidateRejectsUnknownFieldNestedInArray(t *testing.T) {
	in := strings.Replace(good, "  language: go\n",
		"  language: go\n  links:\n    - title: dashboard\n      url: https://example.com\n      bogus: true\n", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("links[].bogus is not defined by the schema and must be rejected")
	}
	d := c.Diagnostics()[0]
	wantMsg := `at '/spec/links/0/bogus': unknown field 'bogus'`
	if d.Message != wantMsg {
		t.Errorf("message =\n%s\nwant   =\n%s", d.Message, wantMsg)
	}
	wantHint := "this schema rejects fields it doesn't define, rather than silently ignoring them — remove it, or check for a typo"
	if d.Hint != wantHint {
		t.Errorf("hint =\n%s\nwant =\n%s", d.Hint, wantHint)
	}
	const wantLine = 13 // "      bogus: true", the offending key itself
	if d.Line != wantLine {
		t.Errorf("line = %d, want %d", d.Line, wantLine)
	}
}

func TestValidateRejectsBadTier(t *testing.T) {
	in := strings.Replace(good, "tier: 1", "tier: 9", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("tier 9 is not one of 1, 2, 3")
	}
	got := c.Diagnostics()[0].Message
	want := `at '/metadata/tier': value must be one of 1, 2, 3`
	if got != want {
		t.Errorf("message =\n%s\nwant   =\n%s", got, want)
	}
}

func TestValidateRejectsMissingOwner(t *testing.T) {
	in := strings.Replace(good, "  owner: team-payments\n", "", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("owner is required")
	}
	got := c.Diagnostics()[0].Message
	want := `at '/metadata': missing property 'owner'`
	if got != want {
		t.Errorf("message =\n%s\nwant   =\n%s", got, want)
	}
}

// tier is required for things that page, optional for things that do not.
func TestTierIsRequiredOnlyForPageableKinds(t *testing.T) {
	lib := `apiVersion: landsraad/v1
kind: Library
metadata:
  name: kafkaclient
  owner: team-payments
  lifecycle: production
`
	var c diag.Collector
	if !mustDefault(t).Validate("", "libs/kafkaclient/service.yaml", []byte(lib), &c) {
		t.Errorf("a Library needs no tier: %+v", c.Diagnostics())
	}

	svc := strings.Replace(good, "  tier: 1\n", "", 1)
	var c2 diag.Collector
	if mustDefault(t).Validate("", "a/service.yaml", []byte(svc), &c2) {
		t.Fatal("a Service without a tier must be rejected")
	}
	got := c2.Diagnostics()[0].Message
	want := `at '/metadata': missing property 'tier'`
	if got != want {
		t.Errorf("message =\n%s\nwant   =\n%s", got, want)
	}
}

func TestValidateAcceptsAnnotationsAndAliases(t *testing.T) {
	in := strings.Replace(good, "  tier: 1\n",
		"  tier: 1\n  aliases: [payments-svc]\n  annotations:\n    grafana-folder: abc123\n", 1)
	var c diag.Collector
	if !mustDefault(t).Validate("", "a/service.yaml", []byte(in), &c) {
		t.Errorf("annotations and aliases must be accepted: %+v", c.Diagnostics())
	}
}

func TestNameMustNotEndInASeparator(t *testing.T) {
	wants := map[string]string{
		"payments.": `at '/metadata/name': 'payments.' does not match pattern '^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$'`,
		"foo-":      `at '/metadata/name': 'foo-' does not match pattern '^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$'`,
		"bar_":      `at '/metadata/name': 'bar_' does not match pattern '^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$'`,
	}
	for _, bad := range []string{"payments.", "foo-", "bar_"} {
		in := strings.Replace(good, "name: payments-worker", "name: "+bad, 1)
		var c diag.Collector
		if mustDefault(t).Validate("", "a/service.yaml", []byte(in), &c) {
			t.Errorf("%q ends in a separator and must be rejected (Backstage requires "+
				"names to end alphanumeric; loosening later is free, tightening is not)", bad)
			continue
		}
		got := c.Diagnostics()[0].Message
		if want := wants[bad]; got != want {
			t.Errorf("%q: message =\n%s\nwant   =\n%s", bad, got, want)
		}
	}
}

func TestValidateRejectsWrongAPIVersion(t *testing.T) {
	in := strings.Replace(good, "landsraad/v1", "platform/v1", 1)
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("only landsraad/v1 is accepted")
	}
	got := c.Diagnostics()[0].Message
	want := `at '/apiVersion': value must be 'landsraad/v1'`
	if got != want {
		t.Errorf("message =\n%s\nwant   =\n%s", got, want)
	}
}

// A Validator is a value, so a caller can supply its own schema. This is the
// flexibility requirement in spec §3.1, asserted rather than assumed.
func TestNewAcceptsACallerSuppliedSchema(t *testing.T) {
	v, err := New([]byte(`{"type":"object","required":["kind"]}`))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var c diag.Collector
	if !v.Validate("", "a.yaml", []byte("kind: Anything\n"), &c) {
		t.Errorf("the caller's schema must be the one applied: %+v", c.Diagnostics())
	}
}

func TestNewRejectsBrokenSchema(t *testing.T) {
	if _, err := New([]byte("{not json")); err == nil {
		t.Fatal("a schema that is not JSON must return an error, not panic")
	}
}

// `false` is a legal JSON Schema that rejects everything. Its violation has
// an empty instance location, which the unknown-field rewrite below indexed
// unguarded: an exported API that panics on legal input must not ship.
func TestValidateOnAFalseSchemaReportsRatherThanPanics(t *testing.T) {
	v, err := New([]byte("false"))
	if err != nil {
		t.Fatalf("`false` is a valid JSON Schema: %v", err)
	}
	var c diag.Collector
	if v.Validate("", "a.yaml", []byte(good), &c) {
		t.Fatal("a `false` schema rejects every instance")
	}
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("want exactly 1 diagnostic, got %d: %+v", len(c.Diagnostics()), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if want := "at '': false schema"; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if d.Line != 1 {
		t.Errorf("Line = %d, want 1 — a whole-document violation still needs a line", d.Line)
	}
}

// yaml.v3 resolves an unquoted 2027-01-01 into a time.Time, which is not a
// JSON type: the validator reported "invalid jsonType time.Time" — library
// internals, and unactionable — for the spec's own documented example.
func TestValidateAcceptsAnUnquotedDate(t *testing.T) {
	in := good + `  exemptions:
    - check: has-runbook
      reason: pre-existing service, scheduled for Q1
      until: 2027-01-01
`
	var c diag.Collector
	if !mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatalf("an unquoted date is the spec's own example and must validate: %+v", c.Diagnostics())
	}
}

// The same resolution happens anywhere in the file, including inside the
// free-form string maps.
func TestValidateAcceptsAnUnquotedDateInAnnotations(t *testing.T) {
	in := strings.Replace(good, "  lifecycle: production\n",
		"  lifecycle: production\n  annotations:\n    created: 2027-01-01\n", 1)
	var c diag.Collector
	if !mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatalf("annotations are strings; a date-like value must not leak a Go type: %+v", c.Diagnostics())
	}
}

// "format": "date" is annotation-only in JSON Schema 2020-12 unless the
// validator is told to assert it. It was not, so the schema advertised a
// constraint the tool did not apply.
func TestValidateRejectsAnUntilThatIsNotADate(t *testing.T) {
	in := good + `  exemptions:
    - check: has-runbook
      reason: pre-existing service, scheduled for Q1
      until: banana
`
	var c diag.Collector
	if mustDefault(t).Validate("monorepo", "a/service.yaml", []byte(in), &c) {
		t.Fatal("`until: banana` is not a date and must be rejected")
	}
	if len(c.Diagnostics()) != 1 {
		t.Fatalf("want exactly 1 diagnostic, got %d: %+v", len(c.Diagnostics()), c.Diagnostics())
	}
	d := c.Diagnostics()[0]
	if want := "at '/spec/exemptions/0/until': 'banana' is not a valid date"; d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	if want := "dates are written YYYY-MM-DD, for example 2027-01-01"; d.Hint != want {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, want)
	}
	const wantLine = 13 // "      until: banana"
	if d.Line != wantLine {
		t.Errorf("Line = %d, want %d", d.Line, wantLine)
	}
}

// A violation nested inside an array, several levels below the document
// root, must still report the line of the offending entry — not line 1 and
// not the line of some unrelated sibling.
func TestValidateReportsLineOfNestedViolation(t *testing.T) {
	in := `apiVersion: landsraad/v1
kind: Service
metadata:
  name: payments-worker
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  language: go
  slo:
    - name: latency
      target: p99<200ms
    - name: availability
`
	var c diag.Collector
	if mustDefault(t).Validate("", "a/service.yaml", []byte(in), &c) {
		t.Fatal("an slo entry without a target must be rejected")
	}
	d := c.Diagnostics()[0]
	const wantLine = 13 // "- name: availability", the start of the incomplete entry
	if d.Line != wantLine {
		t.Errorf("line = %d, want %d", d.Line, wantLine)
	}
	want := `at '/spec/slo/1': missing property 'target'`
	if d.Message != want {
		t.Errorf("message =\n%s\nwant   =\n%s", d.Message, want)
	}
}

// The schema and the Go Kind constants must never drift apart.
func TestSchemaKindsMatchGoKinds(t *testing.T) {
	raw := string(Raw)
	for _, k := range catalog.AllKinds {
		if !strings.Contains(raw, `"`+string(k)+`"`) {
			t.Errorf("kind %q exists in Go but not in service.schema.json", k)
		}
	}
}
