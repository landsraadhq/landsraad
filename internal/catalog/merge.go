package catalog

import (
	"fmt"
	"sort"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Catalog is the merged set of entities from every source repo.
//
// Both fields are unexported so NewCatalog is the only way to build one. A
// keyed composite literal used to compile from any package in the module and
// produce a catalog whose byRef index was nil — in which no entity could find
// itself, so Resolve fabricated dangling-ref diagnostics for entities that
// were present and reported zero cycles for a genuine self-loop. A plausible
// empty answer where a crash would be better; now it does not compile.
type Catalog struct {
	entities []*Entity
	byRef    map[Ref]*Entity
}

// Entities returns the merged entities, sorted by source repo then path.
//
// The slice is the catalog's own: callers read it, and the compiler does not
// stop them writing to it. Nothing in this codebase does, and returning a copy
// on every call to defend against a caller that does not exist is the
// over-abstraction half of the composition rule.
func (c *Catalog) Entities() []*Entity { return c.entities }

// NewCatalog merges entities into one catalog, reporting a collision when two
// entities share a kind and a name. Names are flat and globally unique, so a
// collision is a hard error rather than something to resolve silently. The
// first entity encountered wins the index slot so later stages have something
// to resolve against; the diagnostic is what stops the build.
func NewCatalog(entities []*Entity, c *diag.Collector) *Catalog {
	// Sort by file path so which entity "wins" a collision is deterministic
	// and does not depend on filesystem walk order.
	sorted := make([]*Entity, len(entities))
	copy(sorted, entities)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].SourceRepo != sorted[j].SourceRepo {
			return sorted[i].SourceRepo < sorted[j].SourceRepo
		}
		return sorted[i].SourcePath < sorted[j].SourcePath
	})

	cat := &Catalog{
		entities: sorted,
		byRef:    make(map[Ref]*Entity, len(sorted)),
	}
	for _, e := range sorted {
		ref := e.Ref()
		if prev, exists := cat.byRef[ref]; exists {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError,
				Repo:     e.SourceRepo,
				File:     e.SourcePath,
				Line:     e.NameLine,
				Entity:   e.Metadata.Name,
				Check:    "duplicate-name",
				Message: fmt.Sprintf(
					"duplicate entity name %q: already defined as %s in %s (line %d), redefined in %s",
					e.Metadata.Name, ref, prev.Location(), prev.NameLine, e.Location()),
				Hint: "names must be unique across the merged catalog; rename one of them",
			})
			continue
		}
		cat.byRef[ref] = e
	}
	return cat
}

// Lookup finds an entity by reference.
func (c *Catalog) Lookup(r Ref) (*Entity, bool) {
	e, ok := c.byRef[r]
	return e, ok
}
