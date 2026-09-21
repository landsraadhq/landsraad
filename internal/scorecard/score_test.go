package scorecard

import (
	"testing"
	"testing/fstest"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// stdOf builds a Standards from YAML, failing the test if it does not load.
func stdOf(t *testing.T, yaml string) *config.Standards {
	t.Helper()
	var c diag.Collector
	s := config.LoadStandards("standards.yaml", []byte(yaml), &c)
	if c.HasErrors() {
		t.Fatalf("fixture standards must load: %+v", c.Diagnostics())
	}
	return s
}

const ownerOnly = `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set: { tiers: {1: required, 2: required, 3: required} }
`

func TestScoreIsPassedOverApplicable(t *testing.T) {
	e := svc("api") // tier 1, owner set
	cat := catalogOf(t, e)
	var c diag.Collector

	sc := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if len(sc.Entities) != 1 {
		t.Fatalf("got %d entity scores, want 1", len(sc.Entities))
	}
	es := sc.Entities[0]
	if es.Applicable != 1 || es.Passed != 1 {
		t.Errorf("Passed/Applicable = %d/%d, want 1/1", es.Passed, es.Applicable)
	}
	if got := es.Score(); got != 1.0 {
		t.Errorf("Score() = %v, want 1.0", got)
	}
}

// Ruling R1: an entity with no tier is not scored at all.
func TestScoreSkipsEntitiesWithNoTier(t *testing.T) {
	lib := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindLibrary}
	lib.Metadata.Name = "kafkaclient"
	lib.Metadata.Owner = "team-payments"
	lib.SourcePath = "libs/kafkaclient/service.yaml"
	lib.NameLine = 4
	cat := catalogOf(t, svc("api"), lib)
	var c diag.Collector

	sc := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if len(sc.Entities) != 1 {
		t.Fatalf("got %d entity scores, want 1 — a Library has no tier and is not scored", len(sc.Entities))
	}
	if sc.Entities[0].Ref.Kind != catalog.KindService {
		t.Errorf("scored %s, want the Service", sc.Entities[0].Ref)
	}
}

// Ruling R2: info is reported and does not move the number.
func TestScoreExcludesInfoChecksFromTheDenominator(t *testing.T) {
	e := svc("api")
	e.Metadata.Tier = 3
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:   { tiers: {3: required} }
    slo-defined: { tiers: {3: info} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	es := sc.Entities[0]
	if es.Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — the info check does not count", es.Applicable)
	}
	// It is still reported: a check nobody can see is a check nobody fixes.
	if len(es.Results) != 2 {
		t.Errorf("got %d results, want 2 — info checks are reported, just not counted", len(es.Results))
	}
}

// Ruling R3, and the most important test in this file. If not-reported were
// excluded from the denominator, deleting a CI job would raise the score.
func TestNotReportedCountsAgainstTheScore(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:     { tiers: {1: required} }
    image-scanned: { source: external, tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c) // nothing reported

	es := sc.Entities[0]
	if es.Applicable != 2 {
		t.Errorf("Applicable = %d, want 2 — an unreported external check stays applicable", es.Applicable)
	}
	if es.Passed != 1 {
		t.Errorf("Passed = %d, want 1", es.Passed)
	}
	if got := es.Score(); got != 0.5 {
		t.Errorf("Score() = %v, want 0.5; deleting the CI job must not raise the score", got)
	}
	var found bool
	for _, r := range es.Results {
		if r.Check == "image-scanned" {
			found = true
			if r.Status != StatusNotReported {
				t.Errorf("Status = %q, want not-reported", r.Status)
			}
			if r.Detail != "no result reported in .landsraad/checks" {
				t.Errorf("Detail = %q", r.Detail)
			}
		}
	}
	if !found {
		t.Error("an external check with no result must still appear in the scorecard")
	}
}

// Ruling R4: an exemption removes a check from the denominator.
func TestAnExemptionRemovesACheckFromTheDenominator(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "runbook-present", Reason: "nightly backfill, no on-call path", Until: "2027-01-01"},
	}
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	es := sc.Entities[0]
	if es.Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — the exempt check is not counted", es.Applicable)
	}
	if got := es.Score(); got != 1.0 {
		t.Errorf("Score() = %v, want 1.0", got)
	}
	for _, r := range es.Results {
		if r.Check == "runbook-present" {
			if r.Status != StatusExempt {
				t.Errorf("Status = %q, want exempt", r.Status)
			}
			if r.Detail != "exempt until 2027-01-01: nightly backfill, no on-call path" {
				t.Errorf("Detail = %q", r.Detail)
			}
		}
	}
}

// Ruling R4's other half: an expired exemption waives nothing and says so. An
// expired waiver that keeps waiving is a permanent lie in the dataset.
func TestAnExpiredExemptionDoesNotWaive(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "runbook-present", Reason: "temporary", Until: "2026-01-01"},
	}
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if sc.Entities[0].Applicable != 1 {
		t.Errorf("Applicable = %d, want 1 — an expired exemption waives nothing", sc.Entities[0].Applicable)
	}
	if !hasWarn(c.Diagnostics()) {
		t.Fatal("an expired exemption must warn")
	}
	d := c.Diagnostics()[0]
	if d.Check != "exemption-expired" {
		t.Errorf("Check = %q, want %q", d.Check, "exemption-expired")
	}
	want := `exemption for runbook-present on service:api expired on 2026-01-01 and no longer waives anything`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "renew it with a new `until`, or fix the check and remove the exemption"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// An exemption with no `until` never expires, which is allowed — some
// waivers are permanent facts about a service — but it must still carry a
// reason, and the schema already requires one.
func TestAnExemptionWithNoUntilNeverExpires(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{{Check: "runbook-present", Reason: "no on-call path"}}
	cat := catalogOf(t, e)
	std := stdOf(t, "apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    runbook-present: { tiers: {1: required} }\n")
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if sc.Entities[0].Applicable != 0 {
		t.Errorf("Applicable = %d, want 0", sc.Entities[0].Applicable)
	}
	for _, r := range sc.Entities[0].Results {
		if r.Detail != "exempt: no on-call path" {
			t.Errorf("Detail = %q", r.Detail)
		}
	}
}

// An exemption naming a check that does not exist is a typo that silently
// waives nothing. Say so — the author believes they are covered.
func TestAnExemptionForAnUnknownCheckIsReported(t *testing.T) {
	e := svc("api")
	e.Spec.Exemptions = []catalog.Exemption{{Check: "runbook-presnt", Reason: "typo"}}
	cat := catalogOf(t, e)
	var c diag.Collector

	Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c)

	if !hasWarn(c.Diagnostics()) {
		t.Fatal("an exemption for an unknown check must be reported")
	}
	d := c.Diagnostics()[0]
	if d.Check != "exemption-unknown-check" {
		t.Errorf("Check = %q, want %q", d.Check, "exemption-unknown-check")
	}
	want := `exemption on service:api names check "runbook-presnt", which is not in standards.yaml, so it waives nothing`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

// An entity with every check exempt or skipped has no score. Zero is the
// honest rendering: it has passed nothing because nothing was asked.
func TestScoreIsZeroWhenNothingIsApplicable(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, "apiVersion: landsraad/v1\nkind: Standards\nspec:\n  checks:\n    owner-set: { tiers: {1: skip} }\n")
	var c diag.Collector

	sc := Score(cat, std, nil, env(nil), &c)

	if got := sc.Entities[0].Score(); got != 0 {
		t.Errorf("Score() = %v, want 0 for an entity with nothing applicable", got)
	}
	if sc.Entities[0].Applicable != 0 {
		t.Errorf("Applicable = %d, want 0", sc.Entities[0].Applicable)
	}
}

// Results are sorted by check id so the JSON payload, the rendered table and
// the history CSV are stable between runs.
func TestResultsAreSortedByCheckID(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "r.md"
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    slo-defined:     { tiers: {1: warn} }
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
`)
	var c diag.Collector

	sc := Score(cat, std, nil, env(fstest.MapFS{}), &c)

	got := sc.Entities[0].Results
	want := []string{"owner-set", "runbook-present", "slo-defined"}
	for i, id := range want {
		if got[i].Check != id {
			t.Errorf("Results[%d].Check = %q, want %q", i, got[i].Check, id)
		}
	}
}

// Fails is what the gate reads: only checks at or above the gate severity that
// did not pass.
func TestFailsHonoursTheGate(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:       { tiers: {1: required} }
    runbook-present: { tiers: {1: required} }
    slo-defined:     { tiers: {1: warn} }
`)
	var c diag.Collector

	es := Score(cat, std, nil, env(fstest.MapFS{}), &c).Entities[0]

	// runbook-present fails (unset) and slo-defined fails (none). owner-set passes.
	if got := len(es.Fails(config.SevRequired, std)); got != 1 {
		t.Errorf("Fails(required) = %d, want 1 — only runbook-present gates", got)
	}
	if got := len(es.Fails(config.SevWarn, std)); got != 2 {
		t.Errorf("Fails(warn) = %d, want 2 — a ratcheting team gates on both", got)
	}
}

func TestTeamScoresAggregate(t *testing.T) {
	a, b := svc("a"), svc("b")
	b.Metadata.Owner = "team-sre"
	cat := catalogOf(t, a, b)
	var c diag.Collector

	teams := Score(cat, stdOf(t, ownerOnly), nil, env(nil), &c).Teams()

	if len(teams) != 2 {
		t.Fatalf("got %d teams, want 2: %+v", len(teams), teams)
	}
	// Sorted by name, so the rendered table does not reshuffle between runs.
	if teams[0].Team != "team-payments" || teams[1].Team != "team-sre" {
		t.Errorf("teams must be sorted by name, got %+v", teams)
	}
}

// standards.yaml naming a check id this binary does not implement — a typo,
// or a check id from a different version of landsraad — must warn rather
// than silently score fewer checks than the team believes it configured.
func TestScoreWarnsOnAnUnknownHermeticCheck(t *testing.T) {
	e := svc("api")
	cat := catalogOf(t, e)
	std := stdOf(t, `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    made-up-check: { tiers: {1: required} }
`)
	var c diag.Collector

	Score(cat, std, nil, env(nil), &c)

	if !hasWarn(c.Diagnostics()) {
		t.Fatal("an unknown, non-external check must warn")
	}
	d := c.Diagnostics()[0]
	if d.Check != "standards-unknown-check" {
		t.Errorf("Check = %q, want %q", d.Check, "standards-unknown-check")
	}
	want := `check "made-up-check" is not implemented by this version of landsraad and is not marked ` + "`source: external`"
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
}

func hasWarn(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.SevWarn {
			return true
		}
	}
	return false
}

// Ruling R53. Severity was keyed on tier alone, so every API entity was
// REQUIRED to pass image-scanned, runbook-present and otel-present. A proto
// contract directory has no container image and no runtime; the monorepo run
// that found this scored all 11 of its API entities at 11%, 1 of 9.
//
// Exemptions could express it and should not have to: an exemption is
// time-bounded by design and R4's second half exists to make it stop waiving
// when the date passes. "An API is not a deployable" never expires, and
// encoding it as ~4 exemption blocks per entity uses that machinery against
// its purpose.
//
// A check that cannot apply leaves the denominator, exactly as an in-force
// exemption does. A score of 1/9 where two of the nine cannot apply is a lie
// in the denominator.
const scanAppliesToDeployables = `apiVersion: landsraad/v1
kind: Standards
spec:
  checks:
    owner-set:     { tiers: {1: required, 2: required, 3: required} }
    image-scanned: { source: external, appliesTo: [Service, Worker],
                     tiers: {1: required, 2: required, 3: warn} }
`

func TestScoreMarksACheckNotApplicableToTheKind(t *testing.T) {
	api := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindAPI}
	api.Metadata.Name = "payments"
	api.Metadata.Owner = "team-payments"
	api.Metadata.Tier = 1
	api.SourcePath = "apis/payments/service.yaml"
	api.NameLine = 4
	cat := catalogOf(t, api)
	var c diag.Collector

	sc := Score(cat, stdOf(t, scanAppliesToDeployables), nil, env(nil), &c)

	if len(sc.Entities) != 1 {
		t.Fatalf("got %d entity scores, want 1", len(sc.Entities))
	}
	es := sc.Entities[0]
	if es.Applicable != 1 || es.Passed != 1 {
		t.Errorf("Passed/Applicable = %d/%d, want 1/1 — a check that cannot apply must leave the denominator",
			es.Passed, es.Applicable)
	}
	var got Result
	for _, r := range es.Results {
		if r.Check == "image-scanned" {
			got = r
		}
	}
	if got.Status != StatusNotApplicable {
		t.Errorf("image-scanned Status = %q, want %q", got.Status, StatusNotApplicable)
	}
	if want := "not applicable to kind API"; got.Detail != want {
		t.Errorf("Detail\n got: %s\nwant: %s", got.Detail, want)
	}
}

// Ruling R53's second half, and the half the first implementation left open.
// Score's denominator excluded a not-applicable check while Fails — the gate
// --fail-on consults — did not, so an API entity scored 100% and the build
// still failed on image-scanned. Fails already excludes StatusExempt for
// exactly this reason (ruling R4); not-applicable is the same claim, made
// about a kind instead of a date.
//
// Found by running the binary, not by a unit test: the score line said
// 100% (1/1) and the line under it said "2 checks failing at or above
// required" in the same output.
func TestFailsIgnoresACheckThatCannotApplyToTheKind(t *testing.T) {
	api := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindAPI}
	api.Metadata.Name = "payments"
	api.Metadata.Owner = "team-payments"
	api.Metadata.Tier = 1
	api.SourcePath = "apis/payments/service.yaml"
	api.NameLine = 4
	cat := catalogOf(t, api)
	var c diag.Collector
	std := stdOf(t, scanAppliesToDeployables)

	sc := Score(cat, std, nil, env(nil), &c)

	fails := sc.Entities[0].Fails(config.SevRequired, std)
	for _, f := range fails {
		if f.Check == "image-scanned" {
			t.Errorf("image-scanned cannot apply to an API and must not gate the build; got %+v", f)
		}
	}
	if len(fails) != 0 {
		t.Errorf("want no gating failures, got %d: %+v", len(fails), fails)
	}
}

// apiEntity is an API at tier 1 — the kind R53 exists for, and the kind
// image-scanned cannot apply to.
func apiEntity() *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindAPI}
	e.Metadata.Name = "payments"
	e.Metadata.Owner = "team-payments"
	e.Metadata.Tier = 1
	e.SourcePath = "apis/payments/service.yaml"
	e.NameLine = 4
	return e
}

// R53 created a third way for an exemption to waive nothing, and exemptions()
// knew only two. Its own doc comment enumerates "expired, or naming a check
// that does not exist" and says such an exemption is "worse than no exemption
// at all: the author believes they are covered". An exemption on a check the
// entity's kind excludes is exactly that third case.
//
// It matters on the migration path this ruling creates: a team narrowing a
// check with appliesTo will naturally leave the old exemption blocks in place,
// and nothing told them the blocks are now inert.
func TestExemptionOnACheckTheKindExcludesIsReported(t *testing.T) {
	e := apiEntity()
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "image-scanned", Until: "2099-01-01", Reason: "scanner rollout"},
	}
	var c diag.Collector

	Score(catalogOf(t, e), stdOf(t, scanAppliesToDeployables), nil, env(nil), &c)

	diags := c.Diagnostics()
	if len(diags) != 1 {
		t.Fatalf("want exactly 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Check != "exemption-not-applicable" {
		t.Errorf("Check = %q, want %q", d.Check, "exemption-not-applicable")
	}
	if d.Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want warn", d.Severity)
	}
	want := `exemption on api:payments names check "image-scanned", which does not apply to kind API, so it waives nothing`
	if d.Message != want {
		t.Errorf("Message\n got: %s\nwant: %s", d.Message, want)
	}
	wantHint := "remove the exemption; appliesTo in standards.yaml already excludes this kind"
	if d.Hint != wantHint {
		t.Errorf("Hint\n got: %s\nwant: %s", d.Hint, wantHint)
	}
}

// And the nag it replaces: an EXPIRED exemption on a check the kind excludes
// must not tell the team to renew a waiver for a check that can never apply.
func TestAnExpiredExemptionIsNotReportedWhenTheKindExcludesTheCheck(t *testing.T) {
	e := apiEntity()
	e.Spec.Exemptions = []catalog.Exemption{
		{Check: "image-scanned", Until: "2020-01-01", Reason: "scanner rollout"},
	}
	var c diag.Collector

	Score(catalogOf(t, e), stdOf(t, scanAppliesToDeployables), nil, env(nil), &c)

	for _, d := range c.Diagnostics() {
		if d.Check == "exemption-expired" {
			t.Errorf("renewing a waiver for a check that cannot apply is not advice: %+v", d)
		}
	}
}
