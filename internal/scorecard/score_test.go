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

func hasWarn(ds []diag.Diagnostic) bool {
	for _, d := range ds {
		if d.Severity == diag.SevWarn {
			return true
		}
	}
	return false
}
