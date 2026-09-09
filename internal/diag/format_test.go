package diag

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var sample = []Diagnostic{
	{Severity: SevError, File: "services/a/service.yaml", Line: 4, Entity: "a",
		Check: "unknown-owner", Message: `owner "team-payment" is not defined in teams.yaml`,
		Hint: `did you mean "team-payments"?`},
	{Severity: SevWarn, File: "services/b/service.yaml", Line: 9, Entity: "b",
		Check: "missing-file", Message: `spec.runbook points at "nope.md", which does not exist`},
}

func TestTextIncludesFileLineAndHint(t *testing.T) {
	var buf bytes.Buffer
	if err := (Text{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"services/a/service.yaml:4", "error", "unknown-owner",
		`did you mean "team-payments"?`, "services/b/service.yaml:9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q:\n%s", want, out)
		}
	}
}

func TestJSONRoundTripsEveryField(t *testing.T) {
	var buf bytes.Buffer
	if err := (JSON{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var back []Diagnostic
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	// Compare the WHOLE struct. Checking one field lets a formatter silently
	// drop File, Line, Message, Hint and Severity and still pass.
	if diff := cmp.Diff(sample, back); diff != "" {
		t.Errorf("round trip lost data (-want +got):\n%s", diff)
	}
}

// Severity must cross the wire as a name. It is an iota, so if it serialised
// as an integer, inserting a new severity later would silently change the
// meaning of every user's jq filter.
func TestSeverityMarshalsAsAName(t *testing.T) {
	var buf bytes.Buffer
	if err := (JSON{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var raw []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if raw[0]["severity"] != "error" || raw[1]["severity"] != "warn" {
		t.Errorf("severity must serialise as a name, got %v and %v",
			raw[0]["severity"], raw[1]["severity"])
	}
}

func TestGitHubUsesAnnotationSyntax(t *testing.T) {
	var buf bytes.Buffer
	if err := (GitHub{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "::error file=services/a/service.yaml,line=4::") {
		t.Errorf("missing GitHub error annotation:\n%s", out)
	}
	if !strings.Contains(out, "::warning file=services/b/service.yaml,line=9::") {
		t.Errorf("missing GitHub warning annotation:\n%s", out)
	}
}

func TestGitLabEmitsCodeQualityShape(t *testing.T) {
	var buf bytes.Buffer
	if err := (GitLab{}).Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d issues, want 2", len(out))
	}
	if out[0]["severity"] != "major" {
		t.Errorf("an error must map to GitLab severity major, got %v", out[0]["severity"])
	}
	if out[1]["severity"] != "minor" {
		t.Errorf("a warning must map to minor, got %v", out[1]["severity"])
	}
	if out[0]["fingerprint"] == "" || out[0]["fingerprint"] == nil {
		t.Error("GitLab requires a fingerprint per issue")
	}
	loc, _ := out[0]["location"].(map[string]any)
	if loc == nil || loc["path"] != "services/a/service.yaml" {
		t.Errorf("location.path missing or wrong: %v", out[0]["location"])
	}
	// Without this, every issue could be pinned to line 0 and the test would
	// still pass — defeating the point of a line-scoped report.
	lines, _ := loc["lines"].(map[string]any)
	if lines == nil || lines["begin"] != float64(4) {
		t.Errorf("location.lines.begin = %v, want 4", lines["begin"])
	}
	if out[0]["description"] == "" || out[0]["check_name"] != "unknown-owner" {
		t.Errorf("description/check_name not carried: %v", out[0])
	}
	// GitLab's Code Quality shape has no field of its own for a suggested
	// fix, so the hint must be folded into the description — exactly as
	// GitHub.Write already does — or it is silently lost. Assert the exact
	// rendering, not just that a hint appears somewhere.
	wantDesc0 := `owner "team-payment" is not defined in teams.yaml (did you mean "team-payments"?)`
	if out[0]["description"] != wantDesc0 {
		t.Errorf("description\n got: %v\nwant: %s", out[0]["description"], wantDesc0)
	}
	// sample[1] has no Hint: the description must be the bare message, with
	// no trailing " ()" artefact.
	wantDesc1 := `spec.runbook points at "nope.md", which does not exist`
	if out[1]["description"] != wantDesc1 {
		t.Errorf("description\n got: %v\nwant: %s", out[1]["description"], wantDesc1)
	}
}

// The fingerprint identifies the underlying problem, not the tool's current
// wording for how to fix it. Two diagnostics that differ only in Hint (say,
// because teams.yaml gained a closer name match) must fingerprint
// identically, or every such change would churn GitLab's issue tracking for
// a problem that has not actually changed — while the description, which a
// human reads, must still reflect the new hint.
func TestGitLabFingerprintIgnoresHint(t *testing.T) {
	withHint := []Diagnostic{{Severity: SevError, File: "a.yaml", Line: 1,
		Check: "unknown-owner", Message: "owner not defined", Hint: "did you mean X?"}}
	withoutHint := []Diagnostic{{Severity: SevError, File: "a.yaml", Line: 1,
		Check: "unknown-owner", Message: "owner not defined"}}

	var buf1, buf2 bytes.Buffer
	if err := (GitLab{}).Write(&buf1, withHint); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := (GitLab{}).Write(&buf2, withoutHint); err != nil {
		t.Fatalf("Write: %v", err)
	}
	var out1, out2 []map[string]any
	if err := json.Unmarshal(buf1.Bytes(), &out1); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(buf2.Bytes(), &out2); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if out1[0]["fingerprint"] != out2[0]["fingerprint"] {
		t.Errorf("fingerprint must not depend on Hint, got %v and %v",
			out1[0]["fingerprint"], out2[0]["fingerprint"])
	}
	if out1[0]["description"] == out2[0]["description"] {
		t.Error("description must still differ when Hint differs — the hint would otherwise be silently lost")
	}
}

// Every built-in format resolves, and each one's Name matches its key.
func TestFormattersAreConsistent(t *testing.T) {
	for _, name := range []string{"text", "json", "github", "gitlab"} {
		f, ok := Lookup(name)
		if !ok {
			t.Errorf("built-in format %q does not resolve", name)
			continue
		}
		if f.Name() != name {
			t.Errorf("format keyed %q reports Name() = %q", name, f.Name())
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("an unknown name must not resolve")
	}
	if got := FormatNames(); len(got) != 4 || got[0] != "github" {
		t.Errorf("FormatNames must be sorted, got %v", got)
	}
}

// A caller can supply its own Formatter: the interface is the extension
// point, which is why the Registry type was unnecessary.
func TestAnyFormatterSatisfiesTheInterface(t *testing.T) {
	var f Formatter = quietFormat{}
	var buf bytes.Buffer
	if err := f.Write(&buf, sample); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("quiet formatter wrote %q", buf.String())
	}
}

type quietFormat struct{}

func (quietFormat) Name() string                        { return "quiet" }
func (quietFormat) Write(io.Writer, []Diagnostic) error { return nil }
