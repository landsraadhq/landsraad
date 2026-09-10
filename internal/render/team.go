package render

import (
	"io/fs"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// TeamView is one team's page.
type TeamView struct {
	Page
	Name      string
	Members   []string
	Slack     string
	PagerDuty string
	Owned     []CatalogRow
	// Score is nil when nothing this team owns was scored. Distinct from
	// zero, for the same reason as everywhere else.
	Score      *float64
	Passed     int
	Applicable int
}

// teamScores indexes the scorecard's per-team aggregate.
func teamScores(sc *scorecard.Scorecard) map[string]scorecard.TeamScore {
	out := map[string]scorecard.TeamScore{}
	if sc == nil {
		return out
	}
	for _, t := range sc.Teams() {
		out[t.Team] = t
	}
	return out
}

// teamPages renders one page per team in teams.yaml.
//
// Every team gets one, including a team that owns nothing: the page says so,
// which is a different statement from a 404.
func teamPages(web fs.FS, in Input, c *diag.Collector) []emit.File {
	t, err := templateSet(web, "team.html")
	if err != nil {
		c.Add(templateCompileError("team.html", err))
		return nil
	}
	slugs := teamSlugMap(in)
	aggregates := teamScores(in.Scorecard)
	rows := catalogRows(in)

	owned := map[string][]CatalogRow{}
	for _, r := range rows {
		owned[r.Owner] = append(owned[r.Owner], r)
	}

	var out []emit.File
	for _, name := range in.Teams.Names() {
		slug, ok := slugs[name]
		if !ok {
			// TeamSlugs already reported why; a page with no URL cannot be
			// written and a second diagnostic would be noise.
			continue
		}
		team, _ := in.Teams.Get(name)
		path := TeamPath(slug)
		view := TeamView{
			Page:      newPage(in, path, name, "catalog"),
			Name:      name,
			Members:   team.Members,
			Slack:     team.Slack,
			PagerDuty: team.PagerDuty,
			Owned:     owned[name],
		}
		if agg, ok := aggregates[name]; ok && agg.Applicable > 0 {
			score := agg.Score()
			view.Score = &score
			view.Passed, view.Applicable = agg.Passed, agg.Applicable
		}
		if f, ok := renderPage(t, path, view, c); ok {
			out = append(out, f)
		}
	}
	return out
}
