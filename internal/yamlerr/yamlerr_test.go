package yamlerr

import (
	"errors"
	"testing"

	"gopkg.in/yaml.v3"
)

var testNouns = Nouns{"pkg.Thing": {Singular: "thing", Plural: "things"}}

func typeError(lines ...string) error { return &yaml.TypeError{Errors: lines} }

func TestProblemsTranslatesEachShape(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		line int
		want string
	}{
		{"named struct", "line 2: cannot unmarshal !!str `oops` into pkg.Thing", 2,
			`expected a thing, found a string ("oops")`},
		{"list of named structs", "line 1: cannot unmarshal !!map into []pkg.Thing", 1,
			"expected a list of things, found a mapping"},
		{"list of strings", "line 3: cannot unmarshal !!str `a` into []string", 3,
			`expected a list of strings, found a string ("a")`},
		{"number", "line 4: cannot unmarshal !!bool `true` into int", 4,
			`expected a number, found a true/false value ("true")`},
		{"map", "line 5: cannot unmarshal !!seq into map[string]string", 5,
			"expected a mapping, found a list"},
		{"unknown key", "line 6: field pagerDuty not found in type pkg.Thing", 6,
			`unknown key "pagerDuty" in f.yaml`},
		// A type with no noun degrades rather than leaking. Known() is what
		// keeps this branch unreachable in practice; see the callers'
		// TestNounsCoverEveryFieldType.
		{"unnamed type", "line 7: cannot unmarshal !!str `x` into pkg.Other", 7,
			`expected a value, found a string ("x")`},
		// yaml.v3 truncates a long value to seven characters plus an ellipsis.
		// Quoting that back states something false about the user's file.
		{"truncated value", "line 8: cannot unmarshal !!str `abcdefg...` into int", 8,
			"expected a number, found a string"},
		// An unrecognised tag is the user's own YAML, not a Go type, so it is
		// shown rather than hidden.
		{"custom tag", "line 9: cannot unmarshal !Custom `v` into int", 9,
			`expected a number, found a !Custom value ("v")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Problems("f.yaml", testNouns, typeError(tc.in))
			if len(got) != 1 {
				t.Fatalf("got %d problems, want 1: %+v", len(got), got)
			}
			if !got[0].Translated {
				t.Error("Translated = false, want true")
			}
			if got[0].Line != tc.line {
				t.Errorf("Line = %d, want %d", got[0].Line, tc.line)
			}
			if got[0].Message != tc.want {
				t.Errorf("Message\n got: %s\nwant: %s", got[0].Message, tc.want)
			}
		})
	}
}

// Every rejected key in one run, rather than one per rerun.
func TestProblemsReturnsOnePerError(t *testing.T) {
	got := Problems("f.yaml", testNouns, typeError(
		"line 1: field a not found in type pkg.Thing",
		"line 2: field b not found in type pkg.Thing"))
	if len(got) != 2 {
		t.Fatalf("got %d problems, want 2: %+v", len(got), got)
	}
}

// A syntax error names a YAML construct, not a Go type, so it passes through
// untranslated for the caller to phrase.
func TestProblemsPassesThroughUntranslatableErrors(t *testing.T) {
	err := errors.New("yaml: line 4: mapping values are not allowed in this context")
	got := Problems("f.yaml", testNouns, err)
	if len(got) != 1 {
		t.Fatalf("got %d problems, want 1", len(got))
	}
	if got[0].Translated {
		t.Error("Translated = true, want false — nothing was translated")
	}
	if got[0].Line != 4 {
		t.Errorf("Line = %d, want 4", got[0].Line)
	}
	if got[0].Message != err.Error() {
		t.Errorf("Message\n got: %s\nwant: %s", got[0].Message, err.Error())
	}
}

// An error naming no line still gets one: no diagnostic is ever emitted
// without a location.
func TestLineDefaultsToOne(t *testing.T) {
	if got := Line(errors.New("something went wrong")); got != 1 {
		t.Errorf("Line = %d, want 1", got)
	}
}

func TestKnownDistinguishesNamedFromUnnamed(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"pkg.Thing", true}, {"[]pkg.Thing", true}, {"*pkg.Thing", true},
		{"string", true}, {"[]string", true}, {"int", true}, {"bool", true},
		{"map[string]string", true},
		{"pkg.Other", false}, {"[]pkg.Other", false},
	} {
		if got := Known(tc.in, testNouns); got != tc.want {
			t.Errorf("Known(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
