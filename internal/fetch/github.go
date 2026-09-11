package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// GitHub fetches a repository over the GitHub REST API (decision D5).
type GitHub struct {
	repo     Repo
	c        *Client
	cache    Cache
	parallel int

	ref string // resolved on first use

	mu    sync.Mutex
	edits map[string]edit   // memoized LastEdit answers (ruling R35)
	shas  map[string]string // subtree shas the truncated-tree descent (R28) has learned, keyed by path; guarded alongside ref and edits for the same reason both of those are
}

type edit struct {
	t  time.Time
	ok bool
}

func NewGitHub(r Repo, c *Client, cache Cache, parallel int) *GitHub {
	return &GitHub{repo: r, c: c, cache: cache, parallel: parallel, ref: r.Ref, edits: map[string]edit{}, shas: map[string]string{}}
}

// GitHubBaseURL is api.github.com for the public host and /api/v3 for GitHub
// Enterprise, which is where a self-hosted instance puts the same API.
func GitHubBaseURL(r Repo) string {
	if u, err := url.Parse(r.URL); err == nil && u.Hostname() == "github.com" {
		return "https://api.github.com"
	}
	return r.Host + "/api/v3"
}

func (g *GitHub) base() string {
	return "/repos/" + url.PathEscape(g.repo.Owner) + "/" + url.PathEscape(g.repo.Slug)
}

// resolveRef asks the host for its default branch when repos.yaml named no
// ref. Assuming "main" would fetch nothing from every repository that still
// uses "master" and report it as a repository that does not exist. Returns
// the resolved ref and any error.
func (g *GitHub) resolveRef(ctx context.Context) (string, error) {
	g.mu.Lock()
	if g.ref != "" {
		ref := g.ref
		g.mu.Unlock()
		return ref, nil
	}
	g.mu.Unlock()

	body, _, err := g.c.Get(ctx, g.base(), nil, "")
	if err != nil {
		return "", err
	}
	var payload struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("cannot read the repository description: %w", err)
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

func (g *GitHub) Open(ctx context.Context, patterns []string) (*FS, error) {
	ref, err := g.resolveRef(ctx)
	if err != nil {
		return nil, err
	}
	body, _, err := g.c.Get(ctx, g.base()+"/git/trees/"+url.PathEscape(ref),
		url.Values{"recursive": {"1"}}, "")
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("cannot read the tree listing: %w", err)
	}
	if payload.Truncated {
		// 100,000 entries or 7 MB. Ruling R28: list what the patterns can
		// reach rather than refusing a repository for being large.
		return g.walk(ctx, patterns)
	}
	var entries []Entry
	for _, e := range payload.Tree {
		switch e.Type {
		case "blob":
			entries = append(entries, Entry{Path: e.Path, SHA: e.SHA, Size: e.Size})
		case "tree":
			entries = append(entries, Entry{Path: e.Path, Dir: true})
			// "commit" is a submodule: a pointer to another repository, with no
			// content on this side. Listing it as a file would give CheckFiles a
			// path that exists and can never be read.
		}
	}
	return FromEntries(entries), nil
}

// Expand lists directories Open did not cover, which under a complete
// listing is none of them.
//
// Free on the fast path, which is what lets cmd/ call it unconditionally —
// regardless of host, ref, or whether this particular listing happened to
// truncate — rather than branching on any of those. "Unconditionally" is
// about which of those it does not need to know; it is not a claim about
// how many goroutines may call it. Expand mutates f (via AddDir), and *FS's
// own contract requires a single goroutine at a time — see *FS's doc
// comment — so a caller expanding several directories at once must still
// serialize those calls itself. A path whose parent was never listed reads
// as ErrNotListed rather than as a missing file, and this is what turns the
// former into the latter honestly: after Expand, absent means absent.
//
// dir can be a directory no pattern's walk ever reached — spec.docs naming
// a path outside every configured glob, which is exactly the layout this
// method exists to serve. walk() always lists the repository root, so
// every top-level directory already has a sha in g.shas; dir is therefore
// reachable by listing its ancestors in root-to-parent order first, each an
// ordinary listDir and each a no-op if already listed. If an ancestor
// listing does not contain the next segment, that segment's sha is never
// recorded, listDir silently declines to list anything further, and dir
// reads back as ErrNotExist (its parent was listed) or ErrNotListed (it was
// not) — never as an error out of Expand.
func (g *GitHub) Expand(ctx context.Context, f *FS, dirs []string) error {
	for _, d := range dirs {
		for _, ancestor := range ancestorsOf(d) {
			if err := g.listDir(ctx, f, ancestor); err != nil {
				return err
			}
		}
		if err := g.expandRecursive(ctx, f, d); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitHub) Fetch(ctx context.Context, f *FS, paths []string) error {
	return fetchBlobs(ctx, g.repo.Name, f, paths, g.parallel, g.cache,
		func(ctx context.Context, sha string) ([]byte, error) {
			body, _, err := g.c.Get(ctx, g.base()+"/git/blobs/"+url.PathEscape(sha),
				nil, "application/vnd.github.raw+json")
			return body, err
		})
}

func (g *GitHub) LastEdit(ctx context.Context, p string) (time.Time, bool, error) {
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
	body, _, err := g.c.Get(ctx, g.base()+"/commits", url.Values{
		"path": {p}, "per_page": {strconv.Itoa(1)}, "sha": {ref},
	}, "")
	if err != nil {
		return time.Time{}, false, err
	}
	var payload []struct {
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return time.Time{}, false, fmt.Errorf("cannot read the commit list for %s: %w", p, err)
	}
	res := edit{}
	if len(payload) > 0 {
		res = edit{t: payload[0].Commit.Committer.Date.UTC(), ok: true}
	}
	g.mu.Lock()
	g.edits[p] = res
	g.mu.Unlock()
	return res.t, res.ok, nil
}

var _ Fetcher = (*GitHub)(nil)
