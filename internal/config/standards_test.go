package config

import (
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

const minimalStandards = `apiVersion: landsraad/v1
kind: Standards
spec:
  staleAfterDays: 7
  checks:
    owner-set: { tiers: {1: required, 2: required, 3: required} }
    runbook-present: { tiers: {1: required, 2: required, 3: warn} }
    docs-fresh: { params: {maxAgeDays: 90}, tiers: {1: warn, 2: warn, 3: info} }
    image-scanned: { source: external, tiers: {1: required, 2: required, 3: warn} }
`

func TestLoadStandardsReadsTheMatrix(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	if c.HasErrors() {
		t.Fatalf("a valid standards.yaml must load: %+v", c.Diagnostics())
	}
	if !s.Loaded() {
		t.Error("Loaded() = false after a clean load")
	}
	if got := s.Severity("runbook-present", 3); got != SevWarn {
		t.Errorf("Severity(runbook-present, 3) = %q, want %q", got, SevWarn)
	}
	if got := s.Severity("owner-set", 1); got != SevRequired {
		t.Errorf("Severity(owner-set, 1) = %q, want %q", got, SevRequired)
	}
	if got := s.StaleAfterDays(); got != 7 {
		t.Errorf("StaleAfterDays() = %d, want 7", got)
	}
	if !s.IsExternal("image-scanned") {
		t.Error("image-scanned declares source: external")
	}
	if s.IsExternal("owner-set") {
		t.Error("owner-set has no source, so it is hermetic")
	}
	if got := s.Param("docs-fresh", "maxAgeDays", 180); got != 90 {
		t.Errorf("Param(docs-fresh, maxAgeDays) = %d, want 90", got)
	}
	if got := s.Param("docs-fresh", "nope", 42); got != 42 {
		t.Errorf("an absent param must fall back, got %d", got)
	}
}

// Checks() is sorted so the scorecard's column order, the JSON output and the
// history CSV header are all stable between runs.
func TestStandardsChecksAreSorted(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	got := s.Checks()
	want := []string{"docs-fresh", "image-scanned", "owner-set", "runbook-present"}
	if len(got) != len(want) {
		t.Fatalf("Checks() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Checks()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// A tier with no row in the matrix is skip, not required. Defaulting the other
// way would fail every entity for a check its team never configured.
func TestSeverityOfAnUnconfiguredTierIsSkip(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: required} }\n"), &c)
	if got := s.Severity("owner-set", 2); got != SevSkip {
		t.Errorf("Severity(owner-set, 2) = %q, want %q", got, SevSkip)
	}
	if got := s.Severity("not-a-check", 1); got != SevSkip {
		t.Errorf("an unknown check is skip, got %q", got)
	}
}

// Ruling R1: an entity with no tier is not scored, so tier 0 has no row.
func TestSeverityOfTierZeroIsSkip(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(minimalStandards), &c)
	if got := s.Severity("owner-set", 0); got != SevSkip {
		t.Errorf("Severity(owner-set, 0) = %q, want %q — a Library is not scored", got, SevSkip)
	}
}

func TestLoadStandardsRejectsAnUnknownSeverity(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: mandatory} }\n"), &c)
	if !c.HasErrors() {
		t.Fatal("an unknown severity must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "standards-schema" {
		t.Errorf("Check = %q, want %q", d.Check, "standards-schema")
	}
}

func TestLoadStandardsRejectsAnUnknownKey(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  stale_after_days: 7\n  checks:\n    owner-set: { tiers: {1: required} }\n"), &c)
	if !c.HasErrors() {
		t.Fatal("an unknown key must be rejected, as it is in teams.yaml and repos.yaml")
	}
}

// DefaultStandards is the matrix in spec §6, verbatim. A repository with no
// standards.yaml scores against the published defaults; scoring against
// nothing would report a perfect score having checked nothing.
func TestDefaultStandardsMatchesTheSpec(t *testing.T) {
	s := DefaultStandards()
	if !s.Loaded() {
		t.Error("the defaults are loaded by definition")
	}
	for _, tc := range []struct {
		check string
		tier  int
		want  Severity
	}{
		{"owner-set", 1, SevRequired},
		{"owner-set", 3, SevRequired},
		{"runbook-present", 3, SevWarn},
		{"alerts-parse", 3, SevInfo},
		{"slo-defined", 2, SevWarn},
		{"docs-fresh", 1, SevWarn},
		{"dashboard-resolves", 1, SevRequired},
		{"image-scanned", 3, SevWarn},
		{"otel-present", 3, SevInfo},
		{"deps-declared", 2, SevRequired},
	} {
		if got := s.Severity(tc.check, tc.tier); got != tc.want {
			t.Errorf("Severity(%s, %d) = %q, want %q", tc.check, tc.tier, got, tc.want)
		}
	}
	if got := s.StaleAfterDays(); got != 14 {
		t.Errorf("StaleAfterDays() = %d, want 14 (spec §6)", got)
	}
	if got := s.Param("docs-fresh", "maxAgeDays", 0); got != 180 {
		t.Errorf("docs-fresh maxAgeDays = %d, want 180 (spec §6)", got)
	}
	for _, ext := range []string{"dashboard-resolves", "image-scanned", "otel-present", "deps-declared"} {
		if !s.IsExternal(ext) {
			t.Errorf("%s is source: external in spec §6", ext)
		}
	}
	for _, herm := range []string{"owner-set", "runbook-present", "alerts-parse", "slo-defined", "docs-fresh"} {
		if s.IsExternal(herm) {
			t.Errorf("%s is hermetic in spec §9", herm)
		}
	}
}

// The embedded default must itself satisfy the published schema. A default
// that would be rejected if a user wrote it is a schema bug either way.
func TestDefaultStandardsValidatesAgainstTheSchema(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", DefaultStandardsYAML, &c)
	if c.HasErrors() {
		t.Errorf("the shipped defaults must validate: %+v", c.Diagnostics())
	}
}

// Ruling R53. A typo in appliesTo must be a load error, not a check that
// silently evaluates against no kind at all. That would be the same
// "reports on fewer checks than the team believes" failure that
// standards-unknown-check exists to prevent, arrived at from the other side.
func TestLoadStandardsRejectsAnUnknownKindInAppliesTo(t *testing.T) {
	var c diag.Collector
	LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { appliesTo: [Srvice], tiers: {1: required} }\n"), &c)
	if !c.HasErrors() {
		t.Fatal("a misspelled kind in appliesTo must be an error")
	}
	d := c.Diagnostics()[0]
	if d.Check != "standards-schema" {
		t.Errorf("Check = %q, want %q", d.Check, "standards-schema")
	}
}

// Ruling R53's additive guarantee, stated as its own test rather than left to
// the other tests passing: a check with no appliesTo applies to every kind, so
// a standards.yaml written before the ruling scores exactly as it did.
func TestAppliesToIsEveryKindWhenAbsent(t *testing.T) {
	var c diag.Collector
	s := LoadStandards("standards.yaml", []byte(
		"apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: required} }\n"), &c)
	if c.HasErrors() {
		t.Fatalf("fixture must load: %+v", c.Diagnostics())
	}
	for _, k := range catalog.AllKinds() {
		if !s.AppliesTo("owner-set", k) {
			t.Errorf("AppliesTo(owner-set, %s) = false, want true — absent appliesTo means every kind", k)
		}
	}
}

// standards.schema.json enumerates the kinds appliesTo accepts, which is a
// third copy of a list Go owns — service.schema.json already holds two.
// internal/schema has TestSchemaKindsMatchGoKinds for its copy; this is the
// same guard for this one.
//
// Without it, adding a kind to catalog.allKinds ships a landsraad that accepts
// the kind everywhere in a catalog and rejects it in standards.yaml, with the
// same wording a genuine typo produces — so the user cannot tell which it is.
func TestStandardsSchemaKindsMatchGoKinds(t *testing.T) {
	raw := string(StandardsSchema)
	for _, k := range catalog.AllKinds() {
		if !strings.Contains(raw, `"`+string(k)+`"`) {
			t.Errorf("kind %q exists in Go but not in standards.schema.json", k)
		}
	}
}
