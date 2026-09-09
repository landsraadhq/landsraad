package catalog

import (
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
)

// ParseAll turns loaded bytes into entities. It is pure: no filesystem, no
// network, no globals — which is what makes it reusable across a local
// checkout, a fetched remote repo and a test fixture without changing.
//
// Files that cannot be parsed are reported and skipped; the rest still parse,
// so one broken file never hides problems in the others.
func ParseAll(repo string, files []discover.File, c *diag.Collector) []*Entity {
	out := make([]*Entity, 0, len(files))
	for _, f := range files {
		if e, ok := ParseFile(repo, f.Path, f.Data, c); ok {
			out = append(out, e)
		}
	}
	return out
}
