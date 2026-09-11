package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
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

// listPath lists everything under prefix, following every page.
//
// per_page is set to 100 because GitLab's default is 20, and a listing that
// stops after twenty files is a portal missing services with nothing to
// notice it by. X-Next-Page is empty on the last page, which is the
// documented end condition.
func (g *GitLab) listPath(ctx context.Context, f *FS, ref, prefix string) error {
	page := 1
	for {
		q := url.Values{
			"ref":       {ref},
			"recursive": {"true"},
			"per_page":  {"100"},
			"page":      {strconv.Itoa(page)},
		}
		if prefix != "." && prefix != "" {
			q.Set("path", prefix)
		}
		body, header, err := g.c.Get(ctx, g.project()+"/repository/tree", q, "")
		if err != nil {
			return err
		}
		var rows []glRow
		if err := json.Unmarshal(body, &rows); err != nil {
			return fmt.Errorf("cannot read the tree listing: %w", err)
		}
		byDir := map[string][]Entry{}
		for _, r := range rows {
			switch r.Type {
			case "blob":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, SHA: r.ID})
			case "tree":
				byDir[path.Dir(r.Path)] = append(byDir[path.Dir(r.Path)], Entry{Path: r.Path, Dir: true})
				// "commit" is a submodule; see GitHub.Open.
			}
		}
		for dir, entries := range byDir {
			f.AddDir(dir, entries)
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

// Open lists under each pattern's literal prefix rather than the whole
// repository.
//
// GitLab has no truncation flag — it simply paginates — so a hundred-thousand
// file monorepo would be a thousand requests for a listing of which
// landsraad reads a handful of directories. Bounding by prefix is the same
// economy ruling R28 buys on GitHub, taken on the cheap path rather than as
// a fallback.
func (g *GitLab) Open(ctx context.Context, patterns []string) (*FS, error) {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return nil, err
	}
	f := NewFS()
	for _, prefix := range literalPrefixes(patterns) {
		if err := g.listPath(ctx, f, ref, prefix); err != nil {
			return nil, err
		}
	}
	return f, nil
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

// Expand lists a directory that no pattern prefix covered — a spec.docs
// somewhere else in the repository.
func (g *GitLab) Expand(ctx context.Context, f *FS, dirs []string) error {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return err
	}
	for _, d := range dirs {
		if f.Listed(d) {
			continue
		}
		if err := g.listPath(ctx, f, ref, d); err != nil {
			if IsNotFound(err) {
				// The directory is not there. f already answers Stat
				// correctly; a 404 for a path the user named is a catalog
				// problem for CheckFiles to report, not a fetch failure.
				continue
			}
			return err
		}
	}
	return nil
}

func (g *GitLab) Fetch(ctx context.Context, f *FS, paths []string) error {
	return fetchBlobs(ctx, g.repo.Name, f, paths, g.parallel, g.cache,
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
