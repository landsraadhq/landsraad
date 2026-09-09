package config

import (
	"fmt"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/yamlerr"
)

// configNouns is this package's vocabulary for internal/yamlerr: the Go types
// reachable in a decode error, named as someone editing teams.yaml or
// repos.yaml would name them.
//
// TestNounsCoverEveryFieldType walks the two config structs and fails if a
// field's type is missing here, so adding a field cannot quietly degrade a
// message to "a value".
var configNouns = yamlerr.Nouns{
	"config.Team":          {Singular: "team entry", Plural: "team entries"},
	"config.Repo":          {Singular: "repository entry", Plural: "repository entries"},
	"config.CheckStandard": {Singular: "check standard", Plural: "check standards"},
	"config.standardsSpec": {Singular: "standards spec", Plural: "standards specs"},
}

// yamlDiagnostics turns a decode failure into diagnostics a user can act on.
// It always returns at least one.
//
// noun names the file in prose ("teams file"), used only for problems that had
// no translation — syntax errors, whose wording is the library's but which
// name a YAML construct rather than a Go type.
func yamlDiagnostics(path, check, noun, hint string, err error) []diag.Diagnostic {
	problems := yamlerr.Problems(path, configNouns, err)
	out := make([]diag.Diagnostic, 0, len(problems))
	for _, p := range problems {
		message := p.Message
		if !p.Translated {
			message = fmt.Sprintf("cannot parse %s: %s", noun, p.Message)
		}
		out = append(out, diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: p.Line,
			Check: check, Message: message, Hint: hint,
		})
	}
	return out
}
