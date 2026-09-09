package config

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// This file turns yaml.v3 decode failures into diagnostics written in the
// vocabulary of the file the user is editing, rather than Go's.
//
// yaml.v3 reports a rejected key as "field pagerDuty not found in type
// config.Team" and a mis-shaped value as "cannot unmarshal !!str `alice` into
// []string". Both name Go types. The person reading them is editing YAML and
// has never heard of config.Team; the message tells them the tool's
// implementation language and not what to fix. Error message quality is this
// product's whole thesis, so these get translated.

// unknownFieldRE matches one line of a yaml.v3 TypeError for a key rejected by
// a KnownFields decoder:
// "line 3: field pagerDuty not found in type config.Team".
var unknownFieldRE = regexp.MustCompile(`^line (\d+): field (.+) not found in type \S+$`)

// typeMismatchRE matches one line of a yaml.v3 TypeError for a value of the
// wrong shape: "line 3: cannot unmarshal !!str `alice` into []string".
// yaml.v3 omits the quoted value for !!seq and !!map, and truncates a value
// longer than ten characters to seven plus an ellipsis.
var typeMismatchRE = regexp.MustCompile("^line (\\d+): cannot unmarshal (\\S+)(?: `(.*)`)? into (\\S+)$")

// yamlLineRE pulls the line out of a yaml.v3 error string.
var yamlLineRE = regexp.MustCompile(`line (\d+):`)

// yamlDiagnostics turns a decode failure into diagnostics a user can act on.
// It always returns at least one.
//
// noun names the file in prose ("teams file"), used only by the fallback for
// syntax errors, whose wording is the library's but which name a YAML
// construct rather than a Go type and so pass through unchanged.
//
// yaml.v3 collects every type error into a single TypeError, so each becomes
// its own diagnostic: one run reports everything that is wrong with the file.
func yamlDiagnostics(path, check, noun, hint string, err error) []diag.Diagnostic {
	var typeErr *yaml.TypeError
	if errors.As(err, &typeErr) && len(typeErr.Errors) > 0 {
		out := make([]diag.Diagnostic, 0, len(typeErr.Errors))
		for _, e := range typeErr.Errors {
			d := diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check: check, Hint: hint,
			}
			switch {
			case unknownFieldRE.MatchString(e):
				m := unknownFieldRE.FindStringSubmatch(e)
				d.Line = atoiOr(m[1], 1)
				d.Message = fmt.Sprintf("unknown key %q in %s", m[2], path)
			case typeMismatchRE.MatchString(e):
				m := typeMismatchRE.FindStringSubmatch(e)
				d.Line = atoiOr(m[1], 1)
				d.Message = fmt.Sprintf("expected %s, found %s", goTypeShape(m[4]), yamlShape(m[2]))
				if m[3] != "" {
					d.Message += fmt.Sprintf(" (%q)", m[3])
				}
			default:
				d.Line = lineFromYAMLError(errors.New(e))
				d.Message = fmt.Sprintf("cannot parse %s: %s", noun, e)
			}
			out = append(out, d)
		}
		return out
	}
	return []diag.Diagnostic{{
		Severity: diag.SevError, File: path, Line: lineFromYAMLError(err),
		Check:   check,
		Message: fmt.Sprintf("cannot parse %s: %v", noun, err),
		Hint:    hint,
	}}
}

// yamlShape renders a YAML short tag as the thing a person editing the file
// would call it. An unrecognised tag is passed through: it is the user's own
// YAML, not a Go type, so showing it is helpful rather than leaky.
func yamlShape(tag string) string {
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

// goTypeShape renders the Go type from a yaml.v3 TypeError as the YAML shape
// that type expects.
func goTypeShape(t string) string {
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		_, plural := goTypeNoun(elem)
		return "a list of " + plural
	}
	singular, _ := goTypeNoun(t)
	return "a " + singular
}

// goTypeNoun names a Go type in singular and plural, as a person editing YAML
// would say it. It never returns a Go type name: an unrecognised type degrades
// to "value", which is vague but true, where "[]*config.Team" is precise and
// useless. Adding a struct to a config file means adding it here.
func goTypeNoun(t string) (singular, plural string) {
	switch strings.TrimPrefix(t, "*") {
	case "string":
		return "string", "strings"
	case "int", "int64", "float64":
		return "number", "numbers"
	case "bool":
		return "true/false value", "true/false values"
	case "config.Team":
		return "team entry", "team entries"
	case "config.Repo":
		return "repository entry", "repository entries"
	}
	return "value", "values"
}

// lineFromYAMLError pulls a line out of a yaml.v3 error string, defaulting to
// the first line when it names none.
func lineFromYAMLError(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
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
