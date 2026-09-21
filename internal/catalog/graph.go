package catalog

import (
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Cycle is a dependency loop, listed in traversal order.
type Cycle []Ref

// Scope says how much of the world the caller can see.
type Scope int

const (
	// FullCatalog means every entity is present, so an unresolvable
	// reference is a real error. The platform build uses this.
	FullCatalog Scope = iota
	// LocalOnly means the caller is validating a single repo and cannot see
	// entities defined elsewhere. `landsraad validate` uses this.
	LocalOnly
)

// Navigator — route-finding over the dependency graph.
//
// Graph is produced BY Resolve and holds the resolved edges. Keeping the edges
// here rather than on Catalog removes the obvious way to get a silent "zero
// cycles" answer: there is no cat.Cycles() to call before resolving.
//
// It is not, however, a compile error, and this comment used to claim it was.
// Graph is an exported struct with a usable zero value, so
// `(&catalog.Graph{}).Cycles()` compiles from anywhere and answers "no
// cycles". That answer is defensible — a graph with no edges genuinely has
// none — so the type stays as it is; the claim is what was wrong. The
// constraint is conventional, enforced by Resolve being the only thing that
// returns a populated Graph, not by the type checker.
type Graph struct {
	order   []*Entity
	edges   map[Ref][]Ref
	reverse map[Ref][]Ref
}

// Resolve walks every reference and returns the resolved graph.
//
// Under LocalOnly an unresolvable reference is recorded and skipped rather
// than reported, because the target may simply live in another repo.
// Malformed references are errors under either scope: they could never
// resolve anywhere.
func (c *Catalog) Resolve(scope Scope, col *diag.Collector) *Graph {
	g := &Graph{
		order:   c.entities,
		edges:   make(map[Ref][]Ref, len(c.entities)),
		reverse: make(map[Ref][]Ref, len(c.entities)),
	}
	for _, e := range c.entities {
		from := e.Ref()
		c.resolveRefs(g, e, from, e.Spec.DependsOn, FieldDependsOn, scope, col)
		c.resolveRefs(g, e, from, e.Spec.ProvidesApis, FieldProvidesApis, scope, col)
	}
	for k := range g.edges {
		sortRefs(g.edges[k])
	}
	for k := range g.reverse {
		sortRefs(g.reverse[k])
	}
	return g
}

// resolveRefs validates one reference list and records its edges.
//
// providesApis goes through the same path as dependsOn: it was previously
// accepted and never resolved, so `providesApis: [api:bling]` validated green
// today and would have become a hard failure the day a later release started
// resolving it — breaking repos that had been green for months.
func (c *Catalog) resolveRefs(g *Graph, e *Entity, from Ref, raws []string, field string, scope Scope, col *diag.Collector) {
	for i, raw := range raws {
		to, err := ParseRef(raw)
		if err != nil {
			col.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.RefLine(field, i),
				Entity:   e.Metadata.Name,
				Check:    "malformed-ref",
				Message:  fmt.Sprintf("%s: %v", field, err),
				Hint:     "references look like service:ledger-api or topic:payments.events",
			})
			continue
		}
		if _, found := c.Lookup(to); !found {
			if scope == FullCatalog {
				col.Add(diag.Diagnostic{
					Severity: diag.SevError,
					Repo:     e.SourceRepo,
					File:     e.SourcePath,
					Line:     e.RefLine(field, i),
					Entity:   e.Metadata.Name,
					Check:    "dangling-ref",
					Message: fmt.Sprintf("%s %s %s, which is not in the catalog",
						from, field, to),
					Hint: "check the spelling, or add the missing entity",
				})
			}
			// Under LocalOnly the target lives in another repo. Record no
			// edge: the platform build resolves it.
			continue
		}
		// providesApis is validated but is not a dependency: recording it as
		// one would put a false edge in the graph the portal renders.
		if field == "dependsOn" {
			g.edges[from] = append(g.edges[from], to)
			g.reverse[to] = append(g.reverse[to], from)
		}
	}
}

func sortRefs(rs []Ref) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].String() < rs[j].String() })
}

// DependsOn returns the resolved outgoing edges, sorted.
func (g *Graph) DependsOn(r Ref) []Ref { return g.edges[r] }

// Dependents returns everything that depends on r, sorted. This is what
// answers "who consumes this topic?" on an entity page.
func (g *Graph) Dependents(r Ref) []Ref { return g.reverse[r] }

// Cycles returns every dependency loop, using a depth-first search with a
// recursion stack. Each cycle is reported once.
func (g *Graph) Cycles() []Cycle {
	const (
		white = 0 // unvisited
		grey  = 1 // on the current path
		black = 2 // finished
	)
	state := make(map[Ref]int, len(g.order))
	var path []Ref
	var found []Cycle
	seen := make(map[string]bool)

	var visit func(Ref)
	visit = func(r Ref) {
		state[r] = grey
		path = append(path, r)
		for _, next := range g.edges[r] {
			switch state[next] {
			case white:
				visit(next)
			case grey:
				// Found a loop: take the path back to where next appears.
				for i, pr := range path {
					if pr == next {
						cyc := append(Cycle{}, path[i:]...)
						if key := cycleKey(cyc); !seen[key] {
							seen[key] = true
							found = append(found, cyc)
						}
						break
					}
				}
			}
		}
		path = path[:len(path)-1]
		state[r] = black
	}

	// Iterate entities in their sorted order so output is deterministic.
	for _, e := range g.order {
		if state[e.Ref()] == white {
			visit(e.Ref())
		}
	}
	return found
}

// cycleKey builds an order-independent identity for a cycle so the same loop
// discovered from two entry points is only reported once.
func cycleKey(cyc Cycle) string {
	parts := make([]string, len(cyc))
	for i, r := range cyc {
		parts[i] = r.String()
	}
	sort.Strings(parts)
	key := ""
	for _, p := range parts {
		key += p + "|"
	}
	return key
}
