package fetch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Repo identifies one repository to an adapter.
//
// Owner and Slug are split out because both hosts address a repository by
// its path and neither accepts the browser URL: GitHub wants
// /repos/{owner}/{slug} and GitLab wants the whole path percent-encoded as
// one id. Deriving them once, here, is what keeps that difference inside
// each adapter's URL builder instead of in cmd/.
type Repo struct {
	Name  string // the identity from repos.yaml; Entity.SourceRepo
	URL   string
	Ref   string // empty means "ask the host for its default branch"
	Host  string // scheme://hostname, for building the API base
	Owner string // "org", or "group/sub" on GitLab
	Slug  string // "monorepo"
}

// ParseRepo splits a repository URL.
//
// GitLab groups nest, so everything before the last path segment is the
// owner. GitHub's owner never nests, and treating its two-segment path the
// same way gives the same answer — so there is one rule here rather than a
// branch on which host it is.
func ParseRepo(name, rawURL, ref string) (Repo, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return Repo{}, fmt.Errorf("cannot parse repository url %q: %w", rawURL, err)
	}
	p := strings.Trim(u.Path, "/")
	p = strings.TrimSuffix(p, ".git")
	owner, slug, found := cutLast(p, "/")
	if !found || owner == "" || slug == "" {
		return Repo{}, fmt.Errorf(
			"repository url %q has no owner and name; expected https://%s/<owner>/<repo>", rawURL, u.Hostname())
	}
	return Repo{
		Name: name, URL: rawURL, Ref: ref,
		Host:  u.Scheme + "://" + u.Host,
		Owner: owner, Slug: slug,
	}, nil
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

// Fetcher is one repository on one host.
//
// Four methods rather than one Get, because cmd/ drives fetching in phases
// and each phase asks a different question (ruling R25). Two implementations
// exist — github and gitlab — which is what earns this the interface it has;
// a third, generic-git fetcher is what decision D5 says stays additive.
type Fetcher interface {
	// Open lists the repository and returns a filesystem holding its
	// metadata and no content. patterns bound the work when the host cannot
	// return a whole listing (ruling R28); an adapter that always can may
	// ignore them.
	Open(ctx context.Context, patterns []string) (*FS, error)

	// Expand lists directories Open may not have covered. A no-op when the
	// listing was already complete.
	Expand(ctx context.Context, f *FS, dirs []string) error

	// Fetch loads the content of paths into f. Paths not in f's listing are
	// an error: the caller computed the set, and asking for something that
	// is not there means the caller is wrong.
	Fetch(ctx context.Context, f *FS, paths []string) error

	// LastEdit reports when a path was last changed. ok is false when the
	// host has no answer, and docs-fresh then reports not-reported rather
	// than inventing a date (ruling R35).
	LastEdit(ctx context.Context, path string) (t time.Time, ok bool, err error)
}

// Cache is a content-addressed blob store, keyed on the git blob SHA the
// tree listing returns (ruling R27).
//
// An interface here and an implementation in cmd/ because a cache reads and
// writes real files, and nothing under internal/ may import os. A miss is
// never an error: a cache that cannot answer is a slow build, not a broken
// one, so Get has no error return at all.
type Cache interface {
	Get(sha string) ([]byte, bool)
	Put(sha string, data []byte) error
}

// NopCache is the --no-cache implementation and the default in tests.
type NopCache struct{}

func (NopCache) Get(string) ([]byte, bool) { return nil, false }
func (NopCache) Put(string, []byte) error  { return nil }
