// Package yamlerr turns gopkg.in/yaml.v3 decode failures into text written in
// the vocabulary of the file the user is editing, rather than Go's.
//
// yaml.v3 reports a rejected key as "field pagerDuty not found in type
// config.Team" and a mis-shaped value as "cannot unmarshal !!str `alice` into
// []string". Both name Go types. The reader is editing YAML and has never
// heard of config.Team; the message names the tool's implementation language
// instead of what to fix. Error message quality is this product's thesis, so
// these get translated.
//
// This package knows nothing about diagnostics or about any particular file:
// callers supply the vocabulary for their own types and phrase the result.
package yamlerr

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Noun is what a person editing YAML calls one of a Go type's values.
type Noun struct{ Singular, Plural string }

// Nouns maps a Go type, spelled as yaml.v3 prints it ("config.Repo",
// "[]catalog.Link" is derived from "catalog.Link"), to that vocabulary.
// Go's own primitives are built in and need no entry.
type Nouns map[string]Noun

// Problem is one thing wrong with a file: where, and what to say about it.
//
// Translated is false when the library's error was not a shape a translation
// exists for — a syntax error, say, whose wording is yaml.v3's but which names
// a YAML construct rather than a Go type and so is safe to show. Message then
// holds that raw text, and the caller wraps it in its own phrasing.
type Problem struct {
	Line       int
	Message    string
	Translated bool
}

// unknownFieldRE matches one line of a TypeError for a key rejected by a
// KnownFields decoder: "line 3: field pagerDuty not found in type config.Team".
var unknownFieldRE = regexp.MustCompile(`^line (\d+): field (.+) not found in type \S+$`)

// typeMismatchRE matches one line of a TypeError for a value of the wrong
// shape: "line 3: cannot unmarshal !!str `alice` into []string". yaml.v3 omits
// the quoted value for !!seq and !!map, and truncates a value longer than ten
// characters to seven plus an ellipsis.
var typeMismatchRE = regexp.MustCompile("^line (\\d+): cannot unmarshal (\\S+)(?: `(.*)`)? into (\\S+)$")

// lineRE pulls the line number out of a yaml.v3 error string.
var lineRE = regexp.MustCompile(`line (\d+):`)

// Problems translates a decode failure into one entry per thing wrong with the
// file. It always returns at least one.
//
// file appears in the unknown-key message, which has to name the file it is
// talking about. yaml.v3 collects every type error into a single TypeError, so
// each becomes its own Problem: one run reports everything that is wrong.
func Problems(file string, nouns Nouns, err error) []Problem {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		out := make([]Problem, 0, len(typeErr.Errors))
		for _, e := range typeErr.Errors {
			out = append(out, problem(file, nouns, e))
		}
		return out
	}
	return []Problem{{Line: Line(err), Message: err.Error()}}
}

func problem(file string, nouns Nouns, e string) Problem {
	if m := unknownFieldRE.FindStringSubmatch(e); m != nil {
		return Problem{
			Line:       atoiOr(m[1], 1),
			Message:    fmt.Sprintf("unknown key %q in %s", m[2], file),
			Translated: true,
		}
	}
	if m := typeMismatchRE.FindStringSubmatch(e); m != nil {
		msg := fmt.Sprintf("expected %s, found %s", shapeOf(m[4], nouns), tagShape(m[2]))
		// Quote the offending value only when it is the whole value. yaml.v3
		// truncates anything over ten characters to seven plus an ellipsis, and
		// quoting "not-an-..." to a user who wrote "not-an-integer" states
		// something false about their file. The line number already locates it.
		if m[3] != "" && !truncated(m[3]) {
			msg += fmt.Sprintf(" (%q)", m[3])
		}
		return Problem{Line: atoiOr(m[1], 1), Message: msg, Translated: true}
	}
	return Problem{Line: Line(errors.New(e)), Message: e}
}

// truncated reports whether yaml.v3 shortened a value for display: it cuts to
// seven characters plus "...", so exactly ten ending in an ellipsis.
func truncated(value string) bool {
	return len(value) == 10 && strings.HasSuffix(value, "...")
}

// tagShape renders a YAML short tag as the thing a person editing the file
// would call it. An unrecognised tag passes through: it is the user's own
// YAML, not a Go type, so showing it helps rather than leaks.
func tagShape(tag string) string {
	switch tag {
	case "!!str":
		return "a string"
	case "!!int", "!!float":
		return "a number"
	case "!!bool":
		return "a true/false value"
	case "!!seq":
		return "a list"
	case "!!map":
		return "a mapping"
	case "!!null":
		return "an empty value"
	case "!!timestamp":
		return "a date"
	}
	return "a " + tag + " value"
}

// shapeOf renders a Go type as the YAML shape that type expects.
func shapeOf(t string, nouns Nouns) string {
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "a list of " + nounFor(elem, nouns).Plural
	}
	return "a " + nounFor(t, nouns).Singular
}

// nounFor names a Go type, consulting the caller's vocabulary before falling
// back to Go's primitives. Known reports whether either had an entry; see
// Known, which is what lets a caller test that its vocabulary is complete.
func nounFor(t string, nouns Nouns) Noun {
	t = strings.TrimPrefix(t, "*")
	if n, ok := nouns[t]; ok {
		return n
	}
	if strings.HasPrefix(t, "map[") {
		return Noun{"mapping", "mappings"}
	}
	switch t {
	case "string":
		return Noun{"string", "strings"}
	case "int", "int8", "int16", "int32", "int64", "uint", "uint64", "float32", "float64":
		return Noun{"number", "numbers"}
	case "bool":
		return Noun{"true/false value", "true/false values"}
	}
	return Noun{"value", "values"}
}

// Known reports whether a Go type has a noun, from the caller's vocabulary or
// from Go's primitives. A type without one still produces a message — "a
// value" — but a vaguer one than the file deserves, and nothing at runtime
// would say so. Callers assert over their own structs with this, so adding a
// field of an unnamed type fails a test instead of quietly degrading the
// message. See TestNounsCoverEveryFieldType in the calling packages.
func Known(t string, nouns Nouns) bool {
	t = strings.TrimPrefix(strings.TrimPrefix(t, "[]"), "*")
	if _, ok := nouns[t]; ok {
		return true
	}
	return nounFor(t, nouns) != Noun{"value", "values"}
}

// Line pulls the line out of a yaml.v3 error string, defaulting to the first
// line when it names none.
func Line(err error) int {
	if m := lineRE.FindStringSubmatch(err.Error()); m != nil {
		return atoiOr(m[1], 1)
	}
	return 1
}

func atoiOr(s string, fallback int) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return fallback
}
