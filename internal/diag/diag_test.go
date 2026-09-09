package diag

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestCollectorSortsDeterministically(t *testing.T) {
	var c Collector
	c.Add(Diagnostic{File: "b.yaml", Line: 1, Check: "z", Message: "second"})
	c.Add(Diagnostic{File: "a.yaml", Line: 9, Check: "a", Message: "first"})
	c.Add(Diagnostic{File: "a.yaml", Line: 2, Check: "a", Message: "zeroth"})

	got := c.Diagnostics()
	want := []string{"zeroth", "first", "second"}
	if len(got) != len(want) {
		t.Fatalf("got %d diagnostics, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Message != want[i] {
			t.Errorf("position %d: got %q, want %q", i, got[i].Message, want[i])
		}
	}
}

func TestHasErrorsOnlyCountsErrors(t *testing.T) {
	var c Collector
	c.Add(Diagnostic{Severity: SevWarn, File: "a.yaml", Line: 1, Message: "just a warning"})
	if c.HasErrors() {
		t.Error("a warning alone must not report HasErrors")
	}
	c.Add(Diagnostic{Severity: SevError, File: "a.yaml", Line: 2, Message: "a real error"})
	if !c.HasErrors() {
		t.Error("an error must report HasErrors")
	}
}

func TestSeverityString(t *testing.T) {
	cases := map[Severity]string{SevInfo: "info", SevWarn: "warn", SevError: "error"}
	for sev, want := range cases {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}

func TestDiagnosticsAreDeterministicWithIdenticalKeys(t *testing.T) {
	d1 := Diagnostic{File: "a.yaml", Line: 1, Check: "check", Message: "aaa first"}
	d2 := Diagnostic{File: "a.yaml", Line: 1, Check: "check", Message: "zzz second"}

	var c1 Collector
	c1.Add(d1)
	c1.Add(d2)
	result1 := c1.Diagnostics()

	var c2 Collector
	c2.Add(d2)
	c2.Add(d1)
	result2 := c2.Diagnostics()

	if diff := cmp.Diff(result1, result2); diff != "" {
		t.Errorf("adding in different order produced different results (-want +got):\n%s", diff)
	}
}
