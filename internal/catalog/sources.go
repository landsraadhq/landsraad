package catalog

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"

	"github.com/landsraadhq/landsraad/internal/diag"
)

// Sources maps a repository's name to the filesystem holding its files.
//
// It exists because a merged Catalog is two-dimensional and an fs.FS is
// one-dimensional. Every stage below cmd/ already took an fs.FS — which is
// what lets this plan add a filesystem implementation without teaching any
// stage what a repository is — but one filesystem per *stage* is not one
// filesystem per *entity*, and once entities from three repositories share
// a Catalog, "read e.Spec.Runbook" has three possible answers. Ruling R23.
//
// A named map rather than a struct wrapping one, and the reason is a
// decision this codebase already took. Catalog.Entities returns the
// catalog's own slice, arguing that "returning a copy on every call to
// defend against a caller that does not exist is the over-abstraction half
// of the composition rule". The only thing a struct would buy here over
// this is preventing a write by a caller that does not exist — the same
// defence, declined one file over.
//
// What the named type earns, and it is the whole reason this is not a bare
// map[string]fs.FS: For encodes that the key is e.SourceRepo, once, instead
// of at the five call sites that would each otherwise write
// m[e.SourceRepo]. Five consumers of one invariant is what a method is for.
//
// It also earns nil-safety for free — a read from a nil map is a zero value,
// not a panic — which matters because a check calls s.For(e) deep inside
// Score. A caller that built an Env without a Sources gets a loud, specific
// "no filesystem for repository" against every entity rather than a stack
// trace pointing at a scorecard check for a mistake made in cmd/.
//
// Unlike Catalog, there is no keyed literal here that looks constructed and
// answers wrongly: an empty Sources is visibly empty. That is why it needs
// no constructor and has none — build one with a literal.
type Sources map[string]fs.FS

// SingleSource is the one-repository case: validate, gen and score.
//
// name may be empty, and routinely is — localRepoName returns "" for a
// repository with no repos.yaml, and every entity it parses then carries an
// empty SourceRepo. That must resolve rather than miss.
func SingleSource(name string, fsys fs.FS) Sources { return Sources{name: fsys} }

// For returns the filesystem holding this entity's files.
//
// ok is false only when cmd/ built a Sources that does not cover a
// repository it parsed entities from, which is a bug in this program rather
// than in anybody's catalog. Callers report MissingSourceDiagnostic and
// carry on with the next entity: accumulate, never fail fast.
func (s Sources) For(e *Entity) (fs.FS, bool) { return s.Get(e.SourceRepo) }

// Get returns a filesystem by repository name.
func (s Sources) Get(name string) (fs.FS, bool) {
	fsys, ok := s[name]
	return fsys, ok
}

// Names returns every repository name, sorted.
func (s Sources) Names() []string { return slices.Sorted(maps.Keys(s)) }

// With returns a copy with name bound to fsys, leaving the receiver alone.
//
// A copy rather than a mutation because serve --watch rebuilds on every file
// change and must swap only the local filesystem while reusing the remotes
// fetched at startup (ruling R33). A mutating setter would make the
// rebuild's Sources shared state between the watcher's goroutine and the
// server's, for no gain.
func (s Sources) With(name string, fsys fs.FS) Sources {
	next := maps.Clone(s)
	if next == nil {
		next = Sources{}
	}
	next[name] = fsys
	return next
}

// MissingSourceDiagnostic reports an entity whose repository has no
// filesystem, in the caller's own check namespace.
//
// check is a parameter because the same bug surfaces at five different
// stages and each one already has a name the user has seen in other
// diagnostics; inventing a sixth check id here would make the failure look
// unrelated to the stage it stopped.
func MissingSourceDiagnostic(e *Entity, check string) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevError,
		Repo:     e.SourceRepo,
		File:     e.SourcePath,
		Line:     e.NameLine,
		Entity:   e.Metadata.Name,
		Check:    check,
		Message:  fmt.Sprintf("no filesystem for repository %q, which defines %s", e.SourceRepo, e.Ref()),
		Hint:     "this is a landsraad bug, not a problem with your catalog: cmd/ must put every repository it parsed into catalog.Sources",
	}
}
