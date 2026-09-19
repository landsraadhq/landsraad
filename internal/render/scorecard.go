package render

import (
	"io/fs"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// scorecardTiers are the tiers the matrix has columns for.
//
// config.Standards can answer Severity(check, tier) but cannot enumerate
// tiers — the YAML is a map and a team may configure any subset. Spec §6's
// matrix uses 1-3 throughout and the JSON Schema constrains metadata.tier to
// them, so those are the columns, named once here.
//
// An array rather than a slice for the reason catalog.allKinds documents:
// tests in a package share a process, so one test mutating a package-level
// slice without a t.Cleanup poisons every test after it and the failure
// surfaces somewhere else entirely. The array is half of that; the other half
// is that every read handing the value out copies it, because `[...]int` stops
// neither `scorecardTiers[0] = 9` nor a `[:]` slice reaching the same backing
// store. See the assignment in scorecardPage.
var scorecardTiers = [...]int{1, 2, 3}

// CheckRow is one row of the standards matrix.
type CheckRow struct {
	Check string
	// External marks a check whose result is reported in by CI rather than
	// computed in-binary (spec D3). A team needs to know which of their
	// gaps they can close by editing YAML and which need a CI job.
	External   bool
	Severities []string
}

// TeamScoreRow is one team's aggregate.
type TeamScoreRow struct {
	Team string
	URL  string
	// Score is nil when the team has nothing applicable: every check on
	// every entity it owns is exempt, or standards.yaml grades that tier
	// below warn on everything. That is nothing demonstrated, not zero
	// passed, and rendering it as 0% would be indistinguishable from a team
	// that failed every check (Plan 2, ruling R1; see also TeamView.Score
	// and CatalogRow.Score, both *float64 for the same reason).
	Score      *float64
	Passed     int
	Applicable int
}

// ScorecardPage is the standards table with the weekly trend (spec §10).
type ScorecardPage struct {
	Page
	// Overall is nil when the catalog-wide Applicable sum is 0 — no tiered
	// entities yet, or every check on every entity exempted. Scorecard.Score
	// returns 0 for that case for the same reason TeamScore.Score does, and
	// rendering it as 0% would be the identical ambiguity ruling R1 forbids
	// for a single entity or team, one level up at the whole catalog.
	Overall *float64
	Teams   []TeamScoreRow
	Checks  []CheckRow
	Tiers   []int
	Trend   Trend
	// HasHistory distinguishes "no scorecard-history.csv at all" from "a
	// file with only a header": the first needs the CI job set up, the
	// second is simply waiting for its second run.
	HasHistory bool
	// HistoryUnreadable is the third answer: the file is there and could not
	// be read. Telling that reader to set up the CI job would be advice about
	// a problem they do not have.
	HistoryUnreadable bool
}

func scorecardPage(web fs.FS, in Input, c *diag.Collector) (emit.File, bool) {
	t, err := templateSet(web, "scorecard.html")
	if err != nil {
		c.Add(templateCompileError("scorecard.html", err))
		return emit.File{}, false
	}

	view := ScorecardPage{
		Page: newPage(in, "scorecard/index.html", "Scorecard", "scorecard"),
		// A copy, not scorecardTiers[:]. The slice a [:] hands out shares the
		// package array's backing store, so a holder of view.Tiers can write
		// straight through to what this package believes the columns are —
		// which is the reachability the array shape exists to remove
		// (catalog.AllKinds() copies for the same reason, and says so). One
		// copy at the one site that needs a slice; the range at the bottom of
		// this function reads the array by value and needs nothing.
		Tiers:             append([]int(nil), scorecardTiers[:]...),
		HasHistory:        in.History != nil,
		HistoryUnreadable: in.HistoryUnreadable,
	}
	if in.Scorecard != nil {
		total := 0
		for _, e := range in.Scorecard.Entities {
			total += e.Applicable
		}
		if total > 0 {
			overall := in.Scorecard.Score()
			view.Overall = &overall
		}
	}

	slugs := teamSlugMap(in)
	if in.Scorecard != nil {
		for _, ts := range in.Scorecard.Teams() {
			row := TeamScoreRow{
				Team: ts.Team, Passed: ts.Passed, Applicable: ts.Applicable,
			}
			if ts.Applicable > 0 {
				score := ts.Score()
				row.Score = &score
			}
			if slug, ok := slugs[ts.Team]; ok {
				row.URL = TeamURL(slug)
			}
			view.Teams = append(view.Teams, row)
		}
	}

	for _, check := range in.Standards.Checks() {
		row := CheckRow{Check: check, External: in.Standards.IsExternal(check)}
		for _, tier := range scorecardTiers {
			row.Severities = append(row.Severities, string(in.Standards.Severity(check, tier)))
		}
		view.Checks = append(view.Checks, row)
	}

	view.Trend = trend(parseHistory(in.History, c))
	return renderPage(t, "scorecard/index.html", view, c)
}
