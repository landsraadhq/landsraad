package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

// SystemMapCap is the entity count above which the whole-system map renders
// as a table instead of a diagram (ruling R19).
//
// A 300-node Mermaid flowchart is a hairball the browser spends tens of
// seconds laying out. Degrading to a table, and saying so on the page, is
// the honest answer; silently shipping the hairball is not.
const SystemMapCap = 60

// Edge is one resolved dependency, both ends ready to link.
//
// fromRef and toRef are the same edge as values. They are unexported —
// templates never see them — and they exist so the diagram builder does not
// have to parse a Ref back out of the string it just printed.
type Edge struct {
	From, To RefLink
	fromRef  catalog.Ref
	toRef    catalog.Ref
}

// MapPage is the whole-system dependency map.
type MapPage struct {
	Page
	Diagram string
	Edges   []Edge
	Capped  bool
	Count   int
	Cap     int
}

// nodeIDs assigns a stable generated id to every ref.
//
// A Mermaid node id may not contain ":", and every landsraad ref does, so
// the id is generated and the ref becomes the label.
func nodeIDs(refs []catalog.Ref) map[catalog.Ref]string {
	sorted := append([]catalog.Ref(nil), refs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].String() < sorted[j].String() })
	out := make(map[catalog.Ref]string, len(sorted))
	for i, r := range sorted {
		out[r] = fmt.Sprintf("n%d", i)
	}
	return out
}

// node renders one declaration.
//
// The label needs no escaping: a ref is kind:name and the JSON Schema
// constrains a name to ^[a-z0-9]([a-z0-9._-]*[a-z0-9])?$, so it can never
// contain a quote. That is a constraint two packages away — widening the
// name pattern without revisiting this turns a diagram label into an
// injection into the Mermaid source.
func node(id string, r catalog.Ref) string {
	return fmt.Sprintf("  %s[\"%s\"]", id, r.String())
}

// neighbourhood is the diagram for one entity: what it depends on, and what
// depends on it. It returns "" when the entity has no edges at all — an
// empty `graph LR` renders as a blank box, which reads as a broken page
// rather than as "no dependencies".
func neighbourhood(in Input, ref catalog.Ref) string {
	deps := in.Graph.DependsOn(ref)
	users := in.Graph.Dependents(ref)
	if len(deps) == 0 && len(users) == 0 {
		return ""
	}

	all := append([]catalog.Ref{ref}, deps...)
	all = append(all, users...)
	// The subject is always n0, so the diagram's focus is identifiable in
	// the source and in a test.
	ids := map[catalog.Ref]string{ref: "n0"}
	next := 1
	for _, r := range append(append([]catalog.Ref{}, deps...), users...) {
		if _, seen := ids[r]; seen {
			continue
		}
		ids[r] = fmt.Sprintf("n%d", next)
		next++
	}

	var b strings.Builder
	b.WriteString("graph LR\n")
	declared := map[string]bool{}
	for _, r := range all {
		if declared[ids[r]] {
			continue
		}
		declared[ids[r]] = true
		b.WriteString(node(ids[r], r))
		b.WriteByte('\n')
	}
	for _, d := range deps {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[ref], ids[d])
	}
	for _, u := range users {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[u], ids[ref])
	}
	return b.String()
}

// systemGraph is the whole-system map: the Mermaid source, the edge list,
// and whether the catalog is too large to draw (ruling R19).
func systemGraph(in Input) (string, []Edge, bool) {
	entities := in.Catalog.Entities()

	var edges []Edge
	var refs []catalog.Ref
	for _, e := range entities {
		from := e.Ref()
		refs = append(refs, from)
		for _, to := range in.Graph.DependsOn(from) {
			edges = append(edges, Edge{
				From:    RefLink{Ref: from.String(), URL: EntityURL(from)},
				To:      RefLink{Ref: to.String(), URL: EntityURL(to)},
				fromRef: from,
				toRef:   to,
			})
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From.Ref != edges[j].From.Ref {
			return edges[i].From.Ref < edges[j].From.Ref
		}
		return edges[i].To.Ref < edges[j].To.Ref
	})

	if len(entities) > SystemMapCap {
		return "", edges, true
	}

	ids := nodeIDs(refs)
	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, r := range refs {
		b.WriteString(node(ids[r], r))
		b.WriteByte('\n')
	}
	for _, e := range edges {
		fmt.Fprintf(&b, "  %s --> %s\n", ids[e.fromRef], ids[e.toRef])
	}
	return b.String(), edges, false
}

// mapPage renders the whole-system map.
func mapPage(in Input, c *diag.Collector) (emit.File, bool) {
	t, err := templateSet("map.html")
	if err != nil {
		c.Add(templateCompileError("map.html", err))
		return emit.File{}, false
	}
	src, edges, capped := systemGraph(in)
	view := MapPage{
		Page:    newPage(in, "map/index.html", "Dependencies", "map"),
		Diagram: src,
		Edges:   edges,
		Capped:  capped,
		Count:   len(in.Catalog.Entities()),
		Cap:     SystemMapCap,
	}
	return renderPage(t, "map/index.html", view, c)
}
