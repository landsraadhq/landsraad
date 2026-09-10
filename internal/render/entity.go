package render

import (
	"sort"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// RefLink is one edge of the dependency graph, ready to render.
type RefLink struct {
	Ref string
	// URL is empty when the target is not in the catalog. Under FullCatalog
	// that cannot happen — a dangling ref already failed the build — but
	// Resolve is also called at LocalOnly scope, and a link to a page that
	// was never generated is a 404 the portal invented for itself.
	URL string
}

// ResultView is one scorecard row.
type ResultView struct {
	Check  string
	Status string
	// Class is the status with its spaces already gone, for the CSS hook.
	// Doing it here rather than in the template keeps the vocabulary in Go,
	// where a renamed Status is a compile error.
	Class    string
	Detail   string
	URL      string
	Severity string
}

// EntityView is one entity's page.
type EntityView struct {
	Page
	Ref         string
	Name        string
	Kind        string
	Description string
	Owner       string
	OwnerURL    string
	Tier        int
	Lifecycle   string
	Language    string
	Type        string
	RepoURL     string
	Oncall      string
	SourcePath  string
	Tags        []string
	Links       []catalog.Link
	SLO         []catalog.SLO
	Aliases     []string
	Labels      []Pair
	Annotations []Pair
	// Score is nil when the entity was not scored at all (Plan 2, R1).
	Score      *float64
	Passed     int
	Applicable int
	Results    []ResultView
	DependsOn  []RefLink
	Dependents []RefLink
}

// Pair is a sorted key/value, so labels and annotations render in a stable
// order rather than Go's randomised map order.
type Pair struct{ Key, Value string }

func pairs(m map[string]string) []Pair {
	out := make([]Pair, 0, len(m))
	for k, v := range m {
		out = append(out, Pair{k, v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// statusClass turns a Status into a CSS class: "not-reported" already has
// the shape, and every value in the vocabulary is lower-case and hyphenated.
func statusClass(s scorecard.Status) string { return "status-" + string(s) }

// entityScores indexes the scorecard by ref.
func entityScores(sc *scorecard.Scorecard) map[catalog.Ref]scorecard.EntityScore {
	out := map[catalog.Ref]scorecard.EntityScore{}
	if sc == nil {
		return out
	}
	for _, e := range sc.Entities {
		out[e.Ref] = e
	}
	return out
}

// refLinks turns resolved edges into links, in the order Graph returned them
// (already sorted).
func refLinks(in Input, refs []catalog.Ref) []RefLink {
	out := make([]RefLink, 0, len(refs))
	for _, r := range refs {
		l := RefLink{Ref: r.String()}
		if _, ok := in.Catalog.Lookup(r); ok {
			l.URL = EntityURL(r)
		}
		out = append(out, l)
	}
	return out
}

// entityView builds one page's data.
func entityView(in Input, e *catalog.Entity, slugs map[string]string, scores map[catalog.Ref]scorecard.EntityScore) EntityView {
	ref := e.Ref()
	out := EntityView{
		Page:        newPage(in, EntityPath(ref), e.Metadata.Name, "catalog"),
		Ref:         ref.String(),
		Name:        e.Metadata.Name,
		Kind:        string(e.Kind),
		Description: e.Metadata.Description,
		Owner:       e.Metadata.Owner,
		Tier:        e.Metadata.Tier,
		Lifecycle:   e.Metadata.Lifecycle,
		Language:    e.Spec.Language,
		Type:        e.Spec.Type,
		RepoURL:     e.Spec.RepoURL,
		Oncall:      e.Spec.Oncall,
		SourcePath:  e.SourcePath,
		Tags:        e.Metadata.Tags,
		Links:       e.Spec.Links,
		SLO:         e.Spec.SLO,
		Aliases:     e.Metadata.Aliases,
		Labels:      pairs(e.Metadata.Labels),
		Annotations: pairs(e.Metadata.Annotations),
		DependsOn:   refLinks(in, in.Graph.DependsOn(ref)),
		Dependents:  refLinks(in, in.Graph.Dependents(ref)),
	}
	if slug, ok := slugs[e.Metadata.Owner]; ok {
		out.OwnerURL = TeamURL(slug)
	}
	if es, ok := scores[ref]; ok {
		score := es.Score()
		out.Score = &score
		out.Passed, out.Applicable = es.Passed, es.Applicable
		for _, r := range es.Results {
			out.Results = append(out.Results, ResultView{
				Check:    r.Check,
				Status:   string(r.Status),
				Class:    statusClass(r.Status),
				Detail:   r.Detail,
				URL:      r.URL,
				Severity: string(in.Standards.Severity(r.Check, e.Metadata.Tier)),
			})
		}
	}
	return out
}

// entityPages renders one page per entity.
func entityPages(in Input, c *diag.Collector) []emit.File {
	t, err := templateSet("entity.html")
	if err != nil {
		c.Add(templateCompileError("entity.html", err))
		return nil
	}
	slugs := teamSlugMap(in)
	scores := entityScores(in.Scorecard)

	var out []emit.File
	for _, e := range in.Catalog.Entities() {
		view := entityView(in, e, slugs, scores)
		if f, ok := renderPage(t, EntityPath(e.Ref()), view, c); ok {
			out = append(out, f)
		}
	}
	return out
}

// teamSlugMap is the non-reporting slug lookup every page builder shares.
//
// It delegates to TeamSlugs rather than looping over Slug directly: a bare
// Slug call per name has no collision tracking, so the losing side of a
// name collision would silently share the winning name's slug, pointing an
// entity's OwnerURL at a page that is actually rendered with the other
// team's members and on-call data (see model.go's catalogRows for the same
// warning). The collector is throwaway because the real collision
// diagnostic is already reported exactly once, in site.go's TeamSlugs call
// — reporting it again per page would spam it once per entity.
func teamSlugMap(in Input) map[string]string {
	var throwaway diag.Collector
	return TeamSlugs(in.Teams, &throwaway)
}
