package config

import (
	_ "embed"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/schema"
)

//go:embed standards.schema.json
var StandardsSchema []byte

// DefaultStandardsYAML is spec §6's matrix, verbatim. It is the document a
// repository is scored against when it has no standards.yaml of its own, and
// it is kept as YAML rather than as a Go literal so that `landsraad init` can
// write it out and a team can start editing from exactly what they were
// already being scored against.
//
//go:embed standards.default.yaml
var DefaultStandardsYAML []byte

// Severity is what one check is worth for one tier.
type Severity string

const (
	// SevRequired fails the build under `score --fail-on required`.
	SevRequired Severity = "required"
	// SevWarn is applicable to the score but gates only under --fail-on warn.
	SevWarn Severity = "warn"
	// SevInfo is reported and does not affect the score (ruling R2).
	SevInfo Severity = "info"
	// SevSkip is not run and not counted.
	SevSkip Severity = "skip"
)

// CheckStandard is one row of the matrix.
type CheckStandard struct {
	// Source is "hermetic" (default) or "external". External checks are not
	// computed in-binary; their results are reported in via
	// .landsraad/checks/*.yaml (spec D3).
	Source string `yaml:"source"`
	// Params are per-check thresholds. Only docs-fresh uses one today.
	Params map[string]int `yaml:"params"`
	// Tiers maps tier to severity. A tier with no entry is SevSkip.
	Tiers map[int]Severity `yaml:"tiers"`
}

// standardsSpec is standards.yaml's `spec:` section. It is a named type,
// rather than inlined anonymously into standardsFile, so a decode error
// naming it can be given a noun in configNouns: an anonymous struct type has
// no name to add there, and TestNounsCoverEveryFieldType catches exactly that.
type standardsSpec struct {
	StaleAfterDays int                      `yaml:"staleAfterDays"`
	Checks         map[string]CheckStandard `yaml:"checks"`
}

type standardsFile struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Spec       standardsSpec `yaml:"spec"`
}

// Standards is the loaded standards.yaml: the tier by severity matrix and the
// thresholds, and nothing else (spec D4). Checks themselves are Go functions
// with stable ids, so there is nothing to debug in YAML.
type Standards struct {
	checks         map[string]CheckStandard
	staleAfterDays int
	loaded         bool
}

// defaultStaleAfterDays is spec §6's value, applied when the document omits it.
const defaultStaleAfterDays = 14

// Loaded reports whether standards.yaml parsed. A Standards that did not parse
// is empty, which scores nothing — callers must not treat that as a clean run.
func (s *Standards) Loaded() bool { return s.loaded }

// Severity returns what check is worth at tier. An unknown check, an
// unconfigured tier, and tier 0 all return SevSkip.
//
// Skip rather than required is the safe default in both directions: defaulting
// to required would fail every entity for a check nobody configured, and teams
// would respond by deleting the check.
//
// Tier 0 means the entity has no tier, which the schema allows for kinds that
// cannot page anyone — a Library, a Topic (spec §12). Ruling R1: those are not
// scored.
func (s *Standards) Severity(check string, tier int) Severity {
	cs, ok := s.checks[check]
	if !ok {
		return SevSkip
	}
	sev, ok := cs.Tiers[tier]
	if !ok {
		return SevSkip
	}
	return sev
}

// Checks returns every configured check id, sorted, so the scorecard's column
// order, the JSON payload and the history CSV header are stable between runs.
func (s *Standards) Checks() []string {
	out := make([]string, 0, len(s.checks))
	for id := range s.checks {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// IsExternal reports whether results for this check are reported in rather
// than computed (spec D3).
func (s *Standards) IsExternal(check string) bool {
	return s.checks[check].Source == "external"
}

// Param returns a per-check threshold, or fallback when it is not configured.
func (s *Standards) Param(check, name string, fallback int) int {
	cs, ok := s.checks[check]
	if !ok {
		return fallback
	}
	v, ok := cs.Params[name]
	if !ok {
		return fallback
	}
	return v
}

// StaleAfterDays is how old an ingested result may be before it renders as
// stale rather than pass.
//
// This is one of two deliberately distinct staleness clocks (spec §6). The
// other is docs-fresh.params.maxAgeDays, which ages out service documentation.
// They answer different questions and are tuned independently.
func (s *Standards) StaleAfterDays() int { return s.staleAfterDays }

// DefaultStandards is the matrix a repository is scored against when it has no
// standards.yaml. Parsing the embedded document rather than building a Go
// literal is what keeps the default and the published schema from drifting.
func DefaultStandards() *Standards {
	var discard diag.Collector
	s := LoadStandards("standards.yaml", DefaultStandardsYAML, &discard)
	if !s.loaded {
		// Unreachable in a working binary: TestDefaultStandardsValidatesAgainstTheSchema
		// fails first. Panicking rather than returning an empty Standards is
		// deliberate — an empty matrix scores nothing and reports a perfect
		// score, which is the failure mode this project exists to prevent.
		panic("embedded default standards do not parse: " + fmt.Sprint(discard.Diagnostics()))
	}
	return s
}

// LoadStandards reads standards.yaml, always returning a usable value.
func LoadStandards(path string, data []byte, c *diag.Collector) *Standards {
	s := &Standards{checks: map[string]CheckStandard{}, staleAfterDays: defaultStaleAfterDays}

	// Schema first, for the precise messages, exactly as catalog files are
	// handled: the schema is the single source of truth for structure.
	v, err := schema.New(StandardsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: 1,
			Check:   "standards-schema",
			Message: fmt.Sprintf("cannot compile the standards schema: %v", err),
		})
		return s
	}
	// Relabel schema violations as "standards-schema" rather than the
	// generic "schema" v.Validate assigns: standards.yaml is the second file
	// this package validates against a JSON Schema, and its diagnostics need
	// their own check id to stay distinguishable from service.yaml's,
	// matching the schema-compile-failure branch above.
	var schemaDiags diag.Collector
	if !v.Validate("", path, data, &schemaDiags) {
		for _, d := range schemaDiags.Diagnostics() {
			d.Check = "standards-schema"
			c.Add(d)
		}
		return s
	}

	var f standardsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		for _, d := range yamlDiagnostics(path, "standards-parse", "standards file", standardsParseHint, err) {
			c.Add(d)
		}
		return s
	}
	s.checks = f.Spec.Checks
	if f.Spec.StaleAfterDays > 0 {
		s.staleAfterDays = f.Spec.StaleAfterDays
	}
	s.loaded = true
	return s
}

const standardsParseHint = "standards.yaml is a `checks:` mapping under `spec:`, each check with a `tiers:` map of tier to required|warn|info|skip"
