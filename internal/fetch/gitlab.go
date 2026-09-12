package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// GitLab fetches a repository over the GitLab REST API (decision D5).
type GitLab struct {
	repo     Repo
	c        *Client
	cache    Cache
	parallel int

	// mu guards BOTH ref and edits, for the reason recorded on GitHub: a
	// caller expanding one directory can run against the same *GitLab as an
	// LastEdit for a different path, and ref is resolved lazily by either.
	mu    sync.Mutex
	ref   string
	edits map[string]edit
}

func NewGitLab(r Repo, c *Client, cache Cache, parallel int) *GitLab {
	return &GitLab{repo: r, c: c, cache: cache, parallel: parallel, ref: r.Ref, edits: map[string]edit{}}
}

// GitLabBaseURL is /api/v4 on whichever host the repository lives on. Unlike
// GitHub, gitlab.com is not a special case: the public instance serves the
// same path as a self-hosted one.
func GitLabBaseURL(r Repo) string { return r.Host + "/api/v4" }

// project is the id GitLab addresses a repository by: the whole path,
// percent-encoded as a single segment. Groups nest, so this is not the
// two-segment shape GitHub uses.
func (g *GitLab) project() string {
	return "/projects/" + url.PathEscape(g.repo.Owner+"/"+g.repo.Slug)
}

// resolveRef asks the host for its default branch when repos.yaml named no
// ref, and returns the resolved ref. Every access to g.ref goes through
// here and is guarded by g.mu — see the field's comment and the same
// discipline on GitHub.resolveRef, which a previous review found unguarded.
func (g *GitLab) resolveRef(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.ref != "" {
		ref := g.ref
		g.mu.Unlock()
		return ref, nil
	}
	g.mu.Unlock()

	body, _, err := g.c.Get(ctx, g.project(), nil, "")
	if err != nil {
		return "", err
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("cannot read the project description: %w", err)
	}
	if payload.DefaultBranch == "" {
		return "", errors.New("the host reported no default branch; set `ref:` in repos.yaml")
	}

	g.mu.Lock()
	g.ref = payload.DefaultBranch
	ref := g.ref
	g.mu.Unlock()
	return ref, nil
}

// glRow is one row of a tree listing. `id` is the blob sha — the same value
// GitHub calls `sha`, and the key ruling R27's cache is built on. There is
// no size field; see this task's preamble.
type glRow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	Path string `json:"path"`
}

// list lists dir, following every page: its immediate children, or with
// recursive set everything beneath it.
//
// per_page is set to 100 because GitLab's default is 20, and a listing that
// stops after twenty files is a portal missing services with nothing to
// notice it by. X-Next-Page is empty on the last page, which is the
// documented end condition.
//
// A successful listing marks dir listed even when it returns no rows, and a
// recursive one marks every directory it names, as FromEntries does for a
// complete listing: the response described their whole contents.
func (g *GitLab) list(ctx context.Context, f *FS, ref, dir string, recursive bool) error {
	// Normalised once, here, so the rest of this function only ever asks
	// "is dir '.'?" — never "is dir '' or \".\"?" — and byDir's seed key and
	// the query's path parameter cannot disagree about what an empty dir
	// means.
	if dir == "" {
		dir = "."
	}
	page := 1
	for {
		q := url.Values{
			"ref":      {ref},
			"per_page": {"100"},
			"page":     {strconv.Itoa(page)},
		}
		if recursive {
			q.Set("recursive", "true")
		}
		if dir != "." {
			q.Set("path", dir)
		}
		body, header, err := g.c.Get(ctx, g.project()+"/repository/tree", q, "")
		if err != nil {
			return err
		}
		var rows []glRow
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("cannot read the tree listing: %w", err)
		}
		byDir := map[string][]Entry{dir: nil}
		for _, r := range rows {
			// A non-recursive listing only proves what it enumerated one
			// level down from dir. The API's documented contract is that
			// every row here is an immediate child, but trusting a row that
			// violates it would mark some deeper, intermediate directory
			// listed when nothing here actually enumerated it — turning "I
			// cannot say" into a false "it does not exist" (ruling R45).
			if !recursive && path.Dir(r.Path) != dir {
				continue
			}
			switch r.Type {
			case "blob":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, SHA: r.ID})
			case "tree":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, Dir: true})
				if _, seen := byDir[r.Path]; recursive && !seen {
					byDir[r.Path] = nil
				}
				// "commit" is a submodule; see GitHub.Open.
			}
		}
		for d, entries := range byDir {
			f.AddDir(d, entries)
		}
		next := header.Get("X-Next-Page")
		if next == "" {
			return nil
		}
		n, err := strconv.Atoi(next)
		if err != nil {
			return fmt.Errorf("the host returned an unreadable X-Next-Page %q", next)
		}
		page = n
	}
}

// Open lists what the patterns can reach, proving each step with a listing
// (ruling R45).
//
// GitLab has no truncation flag — it simply paginates — so a hundred-thousand
// file monorepo would be a thousand requests for a listing of which
// landsraad reads a handful of directories. Bounding by prefix is the same
// economy ruling R28 buys on GitHub, taken on the cheap path rather than as
// a fallback.
//
// Every prefix is reached from a real listing of the root. Before R45 the
// root was only marked listed, by NewFS, so every root-level path read as
// fs.ErrNotExist, and a runbook sitting in the repository was reported
// missing. Prefixes go shallowest first, so a recursive listing of services
// covers services/api before anything asks for it on its own.
func (g *GitLab) Open(ctx context.Context, patterns []string) (*FS, error) {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return nil, err
	}
	f := NewFS()
	prefixes := literalPrefixes(patterns)
	if prefixes[0] == "." {
		// literalPrefixes returns "." alone. One recursive listing of the
		// whole repository lists the root along with everything else.
		if err := g.list(ctx, f, ref, ".", true); err != nil {
			return nil, err
		}
		return f, nil
	}
	if err := g.list(ctx, f, ref, ".", false); err != nil {
		return nil, err
	}
	sort.SliceStable(prefixes, func(i, j int) bool {
		return strings.Count(prefixes[i], "/") < strings.Count(prefixes[j], "/")
	})
	for _, p := range prefixes {
		if err := g.reach(ctx, f, ref, p); err != nil {
			return nil, err
		}
	}
	return f, nil
}

// reach makes dir and everything beneath it known, proving each step the
// only way this adapter accepts: a listing of the parent that shows it.
// Ancestors are listed one level at a time, root to parent, each skipped if
// already listed — the discipline GitHub's truncated-tree descent follows —
// and dir itself is listed recursively unless everything beneath it is
// already known.
//
// A step its parent's listing does not show is absent. reach stops there,
// with no request, and f answers fs.ErrNotExist beneath it because the
// parent is listed. Open lists the root before anything calls this.
func (g *GitLab) reach(ctx context.Context, f *FS, ref, dir string) error {
	for _, a := range ancestorsOf(dir) {
		shown, err := showsDir(f, a)
		if err != nil || !shown {
			return err
		}
		if !f.Listed(a) {
			if err := g.list(ctx, f, ref, a, false); err != nil {
				return err
			}
		}
	}
	shown, err := showsDir(f, dir)
	if err != nil || !shown || covered(f, dir) {
		return err
	}
	return g.list(ctx, f, ref, dir, true)
}

// showsDir reports whether dir's parent, which reach has already listed,
// shows dir as a directory. fs.ErrNotExist is a plain no. Any other answer
// means the parent was not listed after all: a bug in reach, not a fact
// about the repository, so it is returned as an error.
func showsDir(f *FS, dir string) (bool, error) {
	info, err := fs.Stat(f, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.IsDir(), nil
}

// covered reports whether dir and every directory beneath it are listed,
// which is what a recursive listing of dir establishes. Listed(dir) alone no
// longer says that: reach lists ancestors one level deep, so a directory can
// be listed with nothing beneath it known.
func covered(f *FS, dir string) bool {
	if !f.Listed(dir) {
		return false
	}
	prefix := dir + "/"
	if dir == "." {
		prefix = ""
	}
	for _, e := range f.Entries() {
		if e.Dir && strings.HasPrefix(e.Path, prefix) && !f.Listed(e.Path) {
			return false
		}
	}
	return true
}

// literalPrefixes reduces glob patterns to the deepest directory that must
// be listed for each, deduplicated. "services/*" needs "services";
// "*/api" and "." both need the root, and a root listing subsumes every
// other prefix, so it is returned alone.
func literalPrefixes(patterns []string) []string {
	if len(patterns) == 0 {
		return []string{"."}
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		prefix := "."
		var kept []string
		for _, seg := range strings.Split(p, "/") {
			if seg == "." || seg == "" || strings.ContainsAny(seg, "*?[") {
				break
			}
			kept = append(kept, seg)
		}
		if len(kept) > 0 {
			prefix = strings.Join(kept, "/")
		}
		if prefix == "." {
			return []string{"."}
		}
		if !seen[prefix] {
			seen[prefix] = true
			out = append(out, prefix)
		}
	}
	return out
}

// Expand lists a directory no pattern prefix covered — a spec.docs, or the
// directory of a runbook or an alerts file — reaching it the way Open
// reaches a prefix.
//
// A directory no listing shows costs no request and reads as absent: its
// parent's listing is the proof. That replaced treating a 404 as absence.
// GitLab answers 404 for a missing path only from 17.7 (200 and [] before
// that), and also when Gitaly is down, so a 404 could turn an outage into
// "your runbook is missing". Now a 404 can only come back for a directory
// some listing showed, which means the listing is wrong, and it fails the
// repository like any other error.
func (g *GitLab) Expand(ctx context.Context, f *FS, dirs []string) error {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if err := g.reach(ctx, f, ref, d); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitLab) Fetch(ctx context.Context, f *FS, paths []string) error {
	return fetchBlobs(ctx, f, paths, g.parallel, g.cache,
		func(ctx context.Context, sha string) ([]byte, error) {
			body, _, err := g.c.Get(ctx,
				g.project()+"/repository/blobs/"+url.PathEscape(sha)+"/raw", nil, "")
			return body, err
		})
}

func (g *GitLab) LastEdit(ctx context.Context, p string) (time.Time, bool, error) {
	g.mu.Lock()
	hit, seen := g.edits[p]
	g.mu.Unlock()
	if seen {
		return hit.t, hit.ok, nil
	}
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	body, _, err := g.c.Get(ctx, g.project()+"/repository/commits", url.Values{
		"path": {p}, "ref_name": {ref}, "per_page": {"1"},
	}, "")
	if err != nil {
		return time.Time{}, false, err
	}
	var payload []struct {
		CommittedDate time.Time `json:"committed_date"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return time.Time{}, false, fmt.Errorf("cannot read the commit list for %s: %w", p, err)
	}
	res := edit{}
	if len(payload) > 0 {
		// GitLab returns an explicit offset; UTC normalises it so two
		// repositories in two timezones sort against each other correctly.
		res = edit{t: payload[0].CommittedDate.UTC(), ok: true}
	}
	g.mu.Lock()
	g.edits[p] = res
	g.mu.Unlock()
	return res.t, res.ok, nil
}

var _ Fetcher = (*GitLab)(nil)
