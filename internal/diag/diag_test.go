package diag

import "testing"

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
