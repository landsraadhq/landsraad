package scorecard

import (
	"fmt"
	"sort"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// EntityScore is one entity measured against the standard.
type EntityScore struct {
	Ref     catalog.Ref
	Tier    int
	Owner   string
	Results []Result
	// Passed and Applicable are the fraction spec §9 defines. Applicable
	// counts required and warn checks that are not exempt; info and skip are
	// reported but not counted (ruling R2).
	Passed     int
	Applicable int
}

// Score is passed over applicable, or 0 when nothing is applicable.
//
// Zero rather than one for the empty case: an entity nothing was asked of has
// not demonstrated anything, and rendering it as 100% would make "exempt
// everything" the cheapest way to a perfect scorecard.
func (s EntityScore) Score() float64 {
	if s.Applicable == 0 {
		return 0
	}
	return float64(s.Passed) / float64(s.Applicable)
}

// Fails returns the results that gate the build at the given severity: checks
// whose severity is at least gate and whose status is not pass.
//
// Exempt does not gate (ruling R4), and neither does not-applicable (ruling
// R53) — the same claim about a kind rather than a date. These two exclusions
// must agree with Score's denominator above, and for one commit they did not:
// an API entity scored 100% while the gate still failed it on image-scanned,
// which is a scorecard and a build disagreeing about the same check.
func (s EntityScore) Fails(gate config.Severity, std *config.Standards) []Result {
	var out []Result
	for _, r := range s.Results {
		if r.Status.Passed() || r.Status == StatusExempt || r.Status == StatusNotApplicable {
			continue
		}
		if atLeast(std.Severity(r.Check, s.Tier), gate) {
			out = append(out, r)
		}
	}
	return out
}

// atLeast orders the severities required > warn > info > skip.
func atLeast(have, gate config.Severity) bool {
	return rank(have) >= rank(gate) && rank(have) > 0
}

func rank(s config.Severity) int {
	switch s {
	case config.SevRequired:
		return 3
	case config.SevWarn:
		return 2
	case config.SevInfo:
		return 1
	}
	return 0
}

// TeamScore aggregates every entity a team owns.
type TeamScore struct {
	Team       string
	Passed     int
	Applicable int
}

func (t TeamScore) Score() float64 {
	if t.Applicable == 0 {
		return 0
	}
	return float64(t.Passed) / float64(t.Applicable)
}

// Scorecard is the whole run.
type Scorecard struct {
	Entities []EntityScore
}

// Teams aggregates by owner, sorted by team name so the rendered table does
// not reshuffle between runs.
func (s *Scorecard) Teams() []TeamScore {
	byTeam := map[string]*TeamScore{}
	for _, e := range s.Entities {
		t, ok := byTeam[e.Owner]
		if !ok {
			t = &TeamScore{Team: e.Owner}
			byTeam[e.Owner] = t
		}
		t.Passed += e.Passed
		t.Applicable += e.Applicable
	}
	out := make([]TeamScore, 0, len(byTeam))
	for _, t := range byTeam {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Team < out[j].Team })
	return out
}

// Score is the whole-catalog total.
func (s *Scorecard) Score() float64 {
	passed, applicable := 0, 0
	for _, e := range s.Entities {
		passed += e.Passed
		applicable += e.Applicable
	}
	if applicable == 0 {
		return 0
	}
	return float64(passed) / float64(applicable)
}

// Score is pipeline stage 7: hermetic checks plus ingested results, resolved
// through standards.yaml.
//
// Entities are scored in catalog order, which NewCatalog already sorted by
// source repo and path, so the output is stable.
func Score(cat *catalog.Catalog, std *config.Standards, reported map[catalog.Ref]map[string]Reported, env Env, c *diag.Collector) *Scorecard {
	hermetic := map[string]Check{}
	for _, ch := range HermeticChecks() {
		hermetic[ch.ID] = ch
	}

	sc := &Scorecard{}
	for _, e := range cat.Entities() {
		// Ruling R1: tier is required only for kinds that can page someone
		// (spec §12), and an entity without one is not scored. Standards
		// returns skip for tier 0, so this is belt and braces — but an entity
		// with no applicable checks would otherwise appear in the scorecard
		// and in the history CSV as a permanent 0%.
		if e.Metadata.Tier == 0 {
			continue
		}

		es := EntityScore{Ref: e.Ref(), Tier: e.Metadata.Tier, Owner: e.Metadata.Owner}
		exempt := exemptions(e, std, env.Now, c)

		for _, id := range std.Checks() {
			sev := std.Severity(id, e.Metadata.Tier)
			if sev == config.SevSkip {
				continue
			}

			var r Result
			switch {
			case !std.AppliesTo(id, e.Kind):
				// Ruling R53. Ordered before the exemption because a check
				// that cannot apply needs no waiver — and an exemption for
				// one would carry an expiry somebody has to keep renewing
				// for a condition that never expires.
				r = Result{Check: id, Status: StatusNotApplicable,
					Detail: fmt.Sprintf("not applicable to kind %s", e.Kind)}
			case exempt[id] != "":
				r = Result{Check: id, Status: StatusExempt, Detail: exempt[id]}
			case std.IsExternal(id):
				r = externalResult(id, e.Ref(), reported)
			default:
				ch, ok := hermetic[id]
				if !ok {
					// standards.yaml names a hermetic check the binary does
					// not implement. Silently skipping it would make the
					// scorecard report on fewer checks than the team believes.
					c.Add(diag.Diagnostic{
						Severity: diag.SevWarn, File: "standards.yaml", Line: 1,
						Check:   "standards-unknown-check",
						Message: fmt.Sprintf("check %q is not implemented by this version of landsraad and is not marked `source: external`", id),
						Hint:    "check the spelling, add `source: external`, or upgrade landsraad",
					})
					continue
				}
				r = ch.Run(e, env)
			}

			es.Results = append(es.Results, r)

			// Ruling R2: only required and warn are applicable.
			// Ruling R3: not-reported and stale are not passes, and are NOT
			// removed from the denominator — otherwise deleting a CI job
			// raises the score, which is the one incentive this product must
			// never create.
			// Ruling R4: exempt is removed from the denominator.
			// Ruling R53: so is a check that cannot apply to this kind. A
			// score of 1/9 where two of the nine are meaningless for the kind
			// is a lie in the denominator, not a low score.
			if r.Status == StatusExempt || r.Status == StatusNotApplicable ||
				rank(sev) < rank(config.SevWarn) {
				continue
			}
			es.Applicable++
			if r.Status.Passed() {
				es.Passed++
			}
		}

		sort.Slice(es.Results, func(i, j int) bool { return es.Results[i].Check < es.Results[j].Check })
		sc.Entities = append(sc.Entities, es)
	}
	return sc
}

// externalResult looks up an ingested result, rendering its absence as
// not-reported rather than as a failure of the service (spec §9: "distinct
// from both pass and fail").
func externalResult(id string, ref catalog.Ref, reported map[catalog.Ref]map[string]Reported) Result {
	rep, ok := reported[ref][id]
	if !ok {
		return Result{
			Check:  id,
			Status: StatusNotReported,
			Detail: "no result reported in " + ChecksDir,
		}
	}
	return rep.Result
}

// exemptions returns check id to rendered detail for every exemption that is
// currently in force, reporting the ones that are not.
//
// Spec §12: exemptions exist so nobody has to lie. Without them the only lever
// for a tier-1 nightly backfill with no runbook is to misstate its tier, which
// corrupts the dataset the whole product rests on. That makes an exemption
// that silently does nothing — expired, or naming a check that does not
// exist — worse than no exemption at all: the author believes they are
// covered.
func exemptions(e *catalog.Entity, std *config.Standards, now time.Time, c *diag.Collector) map[string]string {
	known := map[string]bool{}
	for _, id := range std.Checks() {
		known[id] = true
	}

	out := map[string]string{}
	for _, x := range e.Spec.Exemptions {
		if !known[x.Check] {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "exemption-unknown-check",
				Message: fmt.Sprintf("exemption on %s names check %q, which is not in standards.yaml, so it waives nothing",
					e.Ref(), x.Check),
				Hint: "check the spelling against the checks listed in standards.yaml",
			})
			continue
		}
		if x.Until != "" {
			// The schema asserts format: date on `until`, so this parses.
			until, err := time.Parse("2006-01-02", x.Until)
			if err == nil && until.Before(now) {
				c.Add(diag.Diagnostic{
					Severity: diag.SevWarn,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.NameLine,
					Entity:   e.Metadata.Name,
					Check:    "exemption-expired",
					Message: fmt.Sprintf("exemption for %s on %s expired on %s and no longer waives anything",
						x.Check, e.Ref(), x.Until),
					Hint: "renew it with a new `until`, or fix the check and remove the exemption",
				})
				continue
			}
			out[x.Check] = fmt.Sprintf("exempt until %s: %s", x.Until, x.Reason)
			continue
		}
		out[x.Check] = "exempt: " + x.Reason
	}
	return out
}
