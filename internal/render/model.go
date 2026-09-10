// Package render turns a validated, scored catalog into a static portal.
//
// Site is a pure function: it takes the catalog, the resolved graph, the
// teams, the scorecard and an fs.FS to read documentation from, and returns
// every byte of the site as []emit.File. Nothing here touches the
// filesystem, reads the clock, or reaches the network — cmd/ owns the one
// loop that writes what this returns (spec §3.1).
//
// That is also what makes `serve --watch` simple: the site is a value, so a
// rebuild is a function call and the preview server holds the result in
// memory rather than watching its own output directory.
package render

import (
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// The pinned Mermaid bundle (ruling R13). Version, URL and hash travel
// together: an SRI hash that does not match its URL blocks the script with a
// console error and no diagram, which is the worst of both outcomes.
const (
	DefaultMermaidSrc       = "https://cdn.jsdelivr.net/npm/mermaid@11.17.2/dist/mermaid.min.js"
	DefaultMermaidIntegrity = "sha384-EOXBFmc3gx5mb+vn0vPvvGqACToJD24hhacX5Yx+8NUUQrHIle/Qi5Bg9o3zKwW2"
	// LocalMermaidPath is the sentinel Src for a copy served from the site
	// itself. newPage rewrites it per page; the file is emitted by Site.
	LocalMermaidPath = "assets/mermaid.min.js"
)

// Mermaid says where the diagram renderer comes from.
type Mermaid struct {
	Src string
	// Integrity is the SRI hash, set only for a remote Src. A local file
	// served from the same origin as the page needs none, and an integrity
	// attribute on a file the user supplied would block their own override.
	Integrity string
	// Data is the bundle's bytes when Src is LocalMermaidPath. cmd/ reads
	// the file the user named; this package only places it, because nothing
	// under internal/ touches the filesystem outside an injected fs.FS.
	Data []byte
}

// Input is everything Site needs. It is a struct rather than nine parameters
// because the renderer genuinely consumes all of it, and because cmd/
// building this value is the explicit composition spec §3.1 asks for.
type Input struct {
	Catalog   *catalog.Catalog
	Graph     *catalog.Graph
	Teams     *config.Teams
	Scorecard *scorecard.Scorecard
	Standards *config.Standards
	// History is scorecard-history.csv, or nil when the repository has none.
	// nil and empty are different: no file means no trend was ever recorded,
	// an empty file means the header is there and no run has appended yet.
	History []byte
	// FS is the repository, for reading docs/ and runbooks.
	FS          fs.FS
	Mermaid     Mermaid
	GeneratedAt time.Time
	Version     string
	// Notice is a degraded-mode banner stamped into every page. Spec §12:
	// a portal quietly missing three services is worse than no portal, so
	// the degradation must be visible in the artifact and not only in a log.
	Notice string
}

// Page is the header every template receives.
type Page struct {
	Title string
	// Root is the relative path back to the site root, "" at the top and
	// "../../../" for an entity page (ruling R12).
	Root        string
	Nav         string
	GeneratedAt string
	Version     string
	Mermaid     Mermaid
	Notice      string
}

// CatalogRow is one entity in the catalog table.
type CatalogRow struct {
	Ref         string
	Name        string
	Kind        string
	Description string
	Owner       string
	// OwnerURL is empty when the owner resolves to no team. A link to a page
	// that was never generated is a 404 the portal created for itself.
	OwnerURL  string
	Tier      int
	Lifecycle string
	Tags      []string
	URL       string
	// Score is nil for an entity that was not scored at all. Plan 2's ruling
	// R1 excludes untiered entities, and rendering "not scored" as 0% would
	// brand every library in the catalog for a check nobody ran.
	Score *float64
}

// CatalogPage is the index.
type CatalogPage struct {
	Page
	Rows []CatalogRow
	// The distinct values present, for the filter controls. Rendered
	// server-side so the filters work before catalog.js loads and so an
	// empty catalog shows empty filters rather than stale ones.
	Kinds []string
	Teams []string
	Tiers []int
	Tags  []string
}

// newPage fills in the header for one output path.
func newPage(in Input, outputPath, title, nav string) Page {
	root := rootRel(outputPath)
	m := in.Mermaid
	if m.Src == LocalMermaidPath {
		// A site-relative asset means something different at every depth.
		m.Src = root + LocalMermaidPath
		m.Integrity = ""
	}
	return Page{
		Title: title,
		Root:  root,
		Nav:   nav,
		// Minute precision: a portal rebuilt on every merge would otherwise
		// show a diff in every footer for no reason a reader cares about.
		GeneratedAt: in.GeneratedAt.UTC().Format("2006-01-02 15:04 MST"),
		Version:     in.Version,
		Mermaid:     m,
		Notice:      in.Notice,
	}
}

// scoresByRef indexes the scorecard so a row lookup is not a linear scan per
// entity.
func scoresByRef(sc *scorecard.Scorecard) map[catalog.Ref]float64 {
	out := map[catalog.Ref]float64{}
	if sc == nil {
		return out
	}
	for _, e := range sc.Entities {
		out[e.Ref] = e.Score()
	}
	return out
}

// catalogRows builds the index table, sorted by ref so the page is stable
// between runs.
func catalogRows(in Input) []CatalogRow {
	scores := scoresByRef(in.Scorecard)
	// TeamSlugs is the single source of truth for name -> slug: it is what
	// drops a losing name on a collision. Computing slugs inline here with
	// bare Slug calls would let a losing team's name silently share the
	// winning team's slug, pointing an entity at a page that was actually
	// rendered with a different team's data.
	//
	// The collector is throwaway: the same collision is already reported
	// once, against the real collector, wherever site.go calls TeamSlugs to
	// build the team pages — reporting it again here would double-report.
	var throwaway diag.Collector
	slugs := TeamSlugs(in.Teams, &throwaway)

	var rows []CatalogRow
	for _, e := range in.Catalog.Entities() {
		ref := e.Ref()
		row := CatalogRow{
			Ref:         ref.String(),
			Name:        e.Metadata.Name,
			Kind:        string(e.Kind),
			Description: e.Metadata.Description,
			Owner:       e.Metadata.Owner,
			Tier:        e.Metadata.Tier,
			Lifecycle:   e.Metadata.Lifecycle,
			Tags:        e.Metadata.Tags,
			URL:         EntityURL(ref),
		}
		if slug, ok := slugs[e.Metadata.Owner]; ok {
			row.OwnerURL = TeamURL(slug)
		}
		if s, ok := scores[ref]; ok {
			score := s
			row.Score = &score
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Ref < rows[j].Ref })
	return rows
}

// distinct returns the sorted unique non-empty values, for a filter control.
func distinct(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// catalogPage assembles the index.
func catalogPage(in Input) CatalogPage {
	rows := catalogRows(in)
	var kinds, teams, tags []string
	tierSeen := map[int]bool{}
	var tiers []int
	for _, r := range rows {
		kinds = append(kinds, r.Kind)
		teams = append(teams, r.Owner)
		tags = append(tags, r.Tags...)
		if r.Tier != 0 && !tierSeen[r.Tier] {
			tierSeen[r.Tier] = true
			tiers = append(tiers, r.Tier)
		}
	}
	sort.Ints(tiers)
	return CatalogPage{
		Page:  newPage(in, "index.html", "Catalog", "catalog"),
		Rows:  rows,
		Kinds: distinct(kinds),
		Teams: distinct(teams),
		Tiers: tiers,
		Tags:  distinct(tags),
	}
}

// lower is a template helper; kinds are title-cased in the data and
// lowercase in URLs and CSS classes.
func lower(s string) string { return strings.ToLower(s) }
