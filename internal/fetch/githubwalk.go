package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"strings"
)

// ghEntry is one row of a non-recursive tree listing. GitHub reports paths
// relative to the tree being listed, not to the repository root, so every
// caller joins them onto the directory it asked about.
type ghEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// listTree fetches one tree by sha, without recursing.
func (g *GitHub) listTree(ctx context.Context, sha string) ([]ghEntry, error) {
	body, _, err := g.c.Get(ctx, g.base()+"/git/trees/"+url.PathEscape(sha), nil, "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tree []ghEntry `json:"tree"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("cannot read the tree listing: %w", err)
	}
	return payload.Tree, nil
}

// record adds one listing to f and returns the subtree shas it found, keyed
// by full path. It touches only f and its own arguments — never g.shas — so
// callers decide when the shared map is safe to update.
func (g *GitHub) record(f *FS, dir string, rows []ghEntry) map[string]string {
	subtrees := map[string]string{}
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		full := r.Path
		if dir != "." {
			full = path.Join(dir, r.Path)
		}
		switch r.Type {
		case "blob":
			entries = append(entries, Entry{Path: full, SHA: r.SHA, Size: r.Size})
		case "tree":
			entries = append(entries, Entry{Path: full, Dir: true})
			subtrees[full] = r.SHA
			// "commit" is a submodule; see Open.
		}
	}
	f.AddDir(dir, entries)
	return subtrees
}

// shaAt reports the subtree sha the descent has learned for dir, if any.
// Guarded by g.mu alongside ref and edits: a descent that fills in
// spec.docs later (Expand) can run against the same *GitHub as an Open for
// a different repository phase, and this is the only field a previous
// review found unguarded (ref) — this one does not repeat that mistake.
func (g *GitHub) shaAt(dir string) (string, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	sha, ok := g.shas[dir]
	return sha, ok
}

// recordSHAs merges newly discovered subtree shas into g.shas.
func (g *GitHub) recordSHAs(m map[string]string) {
	if len(m) == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for p, sha := range m {
		g.shas[p] = sha
	}
}

// childrenSHAs returns the known subtree shas whose parent is dir, keyed by
// full path. A snapshot copy, taken under lock, so callers can range over it
// without holding g.mu for the rest of their work.
//
// This is safe against a leading wildcard segment (a pattern like
// "*/service.yaml") only because "." is never a key in g.shas — see walk.
// path.Dir(".") == "." in Go's path package, so a "." key would appear as
// its own child here whenever dir == ".", which fs.Glob would never do for
// "*" and which would silently diverge the two code paths.
func (g *GitHub) childrenSHAs(dir string) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	for child, sha := range g.shas {
		if path.Dir(child) == dir {
			out[child] = sha
		}
	}
	return out
}

// walk lists only what patterns can reach.
//
// The alternative, when a repository is too large for one recursive listing,
// is to give up — and the repositories large enough to truncate are exactly
// the monorepos this tool exists for. Walking costs roughly 2 + entities
// requests, which is worse than one and enormously better than nothing.
func (g *GitHub) walk(ctx context.Context, patterns []string) (*FS, error) {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return nil, err
	}

	f := NewFS()
	rows, err := g.listTree(ctx, ref)
	if err != nil {
		return nil, err
	}
	// record(f, ".", rows) returns subtree shas keyed by the *rows'* paths —
	// "services", "vendor" and so on — never by "." itself. shas must never
	// gain a "." key (see childrenSHAs), so this is deliberately not
	// `shas := map[string]string{".": ref}` the way a naive seed would read;
	// nothing here inserts one.
	g.recordSHAs(g.record(f, ".", rows))

	for _, pattern := range patterns {
		dirs, err := g.expandPattern(ctx, f, pattern)
		if err != nil {
			return nil, err
		}
		// Each matched directory must itself be listed: that is where
		// discover.Find looks for service.yaml.
		for _, d := range dirs {
			if err := g.listDir(ctx, f, d); err != nil {
				return nil, err
			}
		}
	}
	return f, nil
}

// expandPattern resolves one repos.yaml glob to the directories it matches,
// listing intermediate directories as it descends.
//
// It handles the shapes fs.Glob accepts in a repos.yaml path: literal
// segments and a segment containing a wildcard. It deliberately does not
// implement "**" — neither fs.Glob nor discover.Find supports it either, so
// accepting it here would make the fallback path match things the fast path
// does not.
func (g *GitHub) expandPattern(ctx context.Context, f *FS, pattern string) ([]string, error) {
	if pattern == "." || pattern == "" {
		return []string{"."}, nil
	}
	current := []string{"."}
	for _, seg := range strings.Split(pattern, "/") {
		var next []string
		for _, dir := range current {
			if err := g.listDir(ctx, f, dir); err != nil {
				return nil, err
			}
			if !strings.ContainsAny(seg, "*?[") {
				candidate := join(dir, seg)
				if _, ok := g.shaAt(candidate); ok {
					next = append(next, candidate)
				}
				continue
			}
			for child := range g.childrenSHAs(dir) {
				if ok, err := path.Match(seg, path.Base(child)); err == nil && ok {
					next = append(next, child)
				}
			}
		}
		current = next
		if len(current) == 0 {
			// A pattern matching nothing is not an error: a repository may
			// legitimately have no services under a configured path. Compare
			// discover.Find, which makes the same distinction.
			return nil, nil
		}
	}
	return current, nil
}

// listDir lists dir if it has not been listed already, recording any
// subtrees it finds.
func (g *GitHub) listDir(ctx context.Context, f *FS, dir string) error {
	if f.Listed(dir) {
		return nil
	}
	sha, ok := g.shaAt(dir)
	if !ok {
		// Nothing on the way here saw this directory, so it is not there.
		// f already answers Stat correctly for it; there is nothing to list.
		return nil
	}
	rows, err := g.listTree(ctx, sha)
	if err != nil {
		return err
	}
	g.recordSHAs(g.record(f, dir, rows))
	return nil
}

// expandRecursive lists dir and everything beneath it. Used by Expand for a
// spec.docs directory, whose subdirectories each hold pages.
func (g *GitHub) expandRecursive(ctx context.Context, f *FS, dir string) error {
	if err := g.listDir(ctx, f, dir); err != nil {
		return err
	}
	for _, e := range f.Entries() {
		if !e.Dir || path.Dir(e.Path) != dir {
			continue
		}
		if err := g.expandRecursive(ctx, f, e.Path); err != nil {
			return err
		}
	}
	return nil
}

func join(dir, seg string) string {
	if dir == "." {
		return seg
	}
	return dir + "/" + seg
}

// ancestorsOf returns dir's proper ancestors, in root-to-parent order,
// excluding "." (walk() always lists it, so listDir on it is never useful)
// and dir itself (the caller lists dir on its own account). For a
// top-level dir this is empty: walk()'s initial root listing already gave
// it a sha, so nothing needs listing first.
func ancestorsOf(dir string) []string {
	var chain []string
	for d := path.Dir(dir); d != "."; d = path.Dir(d) {
		chain = append(chain, d)
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}
