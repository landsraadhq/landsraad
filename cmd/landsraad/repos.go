package main

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/schema"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// blobParallel is how many blobs are fetched at once per repository. Eight
// is enough to hide latency and small enough that a build does not look
// like an attack to a host's abuse detection.
const blobParallel = 8

// repoFailure is one repository that could not be read. Ruling R32 decides
// what build does with these; this file only collects them.
type repoFailure struct {
	Name string
	URL  string
	Line int
	Err  error
}

// reposOptions is everything openRepos needs from the command layer.
type reposOptions struct {
	// Root is the directory the command is running in, and RootFS reads it.
	// Both, because the local repository is an os.DirFS over Root while
	// teams.yaml and standards.yaml are read through RootFS (ruling R34).
	Root   string
	RootFS fs.FS
	Cache  fetch.Cache
	Lookup func(string) (string, bool) // os.LookupEnv, injected for tests
	ErrOut io.Writer
	// HTTP is the transport the adapters use. Injected, and nil in
	// production, so Task 15's end-to-end test can point the whole command
	// at an httptest TLS server: repos.yaml requires https, and a test that
	// had to relax that rule would stop testing the rule.
	HTTP *http.Client
}

// workspace is every repository a build reads, opened and fetched.
//
// It keeps the patterns beside the filesystems because parsing happens more
// than once: serve --watch re-parses on every file change, against a fresh
// os.DirFS for the local repository and the same already-fetched values for
// the remotes (ruling R33). Re-parsing is cheap and re-fetching is not.
type workspace struct {
	sources  catalog.Sources
	patterns map[string][]string
	// local is the local repository's name, empty when every entry is
	// remote — a platform repository that holds only configuration.
	local    string
	failures []repoFailure
	// fetchers is kept for exactly one caller: docs-fresh asks a remote
	// repository when a path was last changed, and that is a question about
	// a ref which no tree listing answers and no blob cache can hold
	// (ruling R35). Nothing else touches a Fetcher after openRepos returns.
	fetchers map[string]fetch.Fetcher
}

func (w *workspace) Sources() catalog.Sources { return w.sources }
func (w *workspace) Failures() []repoFailure  { return w.failures }

// FetcherFor returns the adapter behind a repository, if it has one. The
// local repository has none: its history comes from git.
func (w *workspace) FetcherFor(name string) (fetch.Fetcher, bool) {
	f, ok := w.fetchers[name]
	return f, ok
}

// ParseAll runs stage 1 and stage 3 over every repository.
//
// Sorted by Sources.Names so entity order — and therefore which entity wins
// a name collision, and every diagnostic's order — does not depend on map
// iteration.
func (w *workspace) ParseAll(v *schema.Validator, c *diag.Collector) []*catalog.Entity {
	// solo, computed once from the same source count assemble will see
	// (assemble receives this same workspace's Sources()): a build reading
	// exactly one repository must get the single rich "no entities" error
	// parseRepo produces when solo, not the per-repository warning that
	// exists to distinguish repositories in a multi-repository build.
	solo := len(w.sources) <= 1
	var out []*catalog.Entity
	for _, name := range w.sources.Names() {
		fsys, ok := w.sources.Get(name)
		if !ok {
			continue
		}
		out = append(out, parseRepo(name, fsys, w.patterns[name], solo, v, c)...)
	}
	return out
}

// WithLocal returns a copy reading the local repository from fsys, leaving
// the receiver and every fetched filesystem alone.
func (w *workspace) WithLocal(fsys fs.FS) *workspace {
	if w.local == "" {
		return w
	}
	return &workspace{
		sources:  w.sources.With(w.local, fsys),
		patterns: w.patterns, local: w.local, failures: w.failures,
		fetchers: w.fetchers,
	}
}

// openRepos turns repos.yaml into a workspace, fetching every remote
// repository in two phases (ruling R25).
//
// Phase 1 lists each repository and fetches only the service.yaml files the
// configured patterns match. The entities are parsed — and then thrown away,
// because their only job here is to say which files phase 2 must fetch.
// Phase 2 fetches those. Every content request happens inside this function,
// before assemble runs a single stage — docs-fresh is the exception, asking
// a remote repository's host API again during Score (ruling R35).
func openRepos(ctx context.Context, o reposOptions, c *diag.Collector) *workspace {
	v := defaultValidator(c)
	if v == nil {
		return &workspace{sources: catalog.Sources{}, patterns: map[string][]string{}, fetchers: map[string]fetch.Fetcher{}}
	}
	repos := loadReposFile(o.RootFS, c)

	// Built directly as a catalog.Sources rather than as a map that is
	// converted at the end: a conversion would alias this loop's map, and
	// the type carries no constructor to copy it (see catalog/sources.go).
	w := &workspace{
		sources:  catalog.Sources{},
		patterns: map[string][]string{},
		fetchers: map[string]fetch.Fetcher{},
	}
	var failures []repoFailure

	for _, r := range repos {
		name := r.Identity()
		patterns := r.Paths
		if len(patterns) == 0 {
			// Silent defaulting with no diagnostic contradicts patternsFor's
			// identical single-repository case (defaultPatternsNote) and
			// CLAUDE.md's guidance that degraded mode must be visible in the
			// artifact, not only in a log.
			c.Add(repoDefaultPatternsNote(name, r.Line))
			patterns = config.DefaultPatterns()
		}

		fail := func(err error) {
			failures = append(failures, repoFailure{Name: name, URL: r.URL, Line: r.Line, Err: err})
		}

		fsys, fetcher, err := openOne(ctx, r, patterns, o)
		if err != nil {
			fail(err)
			continue
		}
		if r.Local {
			w.local = name
		}

		if fetcher != nil {
			// Phase 1: the catalog files the patterns match, and nothing
			// else. A local repository skips both phases: it is already on
			// disk, and every read below will find it there.
			remote := fsys.(*fetch.FS)
			found, err := discover.Find(fsys, patterns)
			if err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, found); err != nil {
				fail(err)
				continue
			}

			// Phase 2: everything those entities name. These entities are
			// discarded — ParseAll re-derives them once every repository is
			// in the workspace, so that cross-repository references resolve
			// against the merged catalog rather than against one repository
			// at a time. Parsing twice is cheap; fetching twice is not.
			// solo is irrelevant here: scratch is thrown away below, so
			// whichever "no entities" diagnostic parseRepo would have added
			// to it never surfaces. ParseAll makes the real, kept decision
			// once every repository is in the workspace.
			var scratch diag.Collector
			parsed := parseRepo(name, fsys, patterns, false, v, &scratch)

			// Expand before contentSet: a spec.docs directory outside the
			// configured paths may not be listed yet, and contentSet walks
			// it to find the pages.
			if err := fetcher.Expand(ctx, remote, docsDirs(parsed)); err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, contentSet(fsys, parsed)); err != nil {
				fail(err)
				continue
			}
		}

		w.sources[name] = fsys
		w.patterns[name] = patterns
		if fetcher != nil {
			w.fetchers[name] = fetcher
		}
	}
	w.failures = failures
	return w
}

// openOne returns the filesystem for one repository, and the fetcher behind
// it when there is one. The local repository has no fetcher: it is already
// on disk, and re-reading it through a host API would be slower, need a
// token, and show committed state rather than what the user is editing.
func openOne(ctx context.Context, r config.Repo, patterns []string, o reposOptions) (fs.FS, fetch.Fetcher, error) {
	if r.Local {
		return os.DirFS(o.Root), nil, nil
	}
	repo, err := fetch.ParseRepo(r.Identity(), r.URL, r.Ref)
	if err != nil {
		return nil, nil, err
	}
	kind, known := r.HostKind()
	if !known {
		// validateRepos already reported this as a diagnostic; reaching here
		// means the command chose to continue past it.
		return nil, nil, fmt.Errorf("no adapter for %s", r.URL)
	}
	token := tokenFor(r, o.Lookup)
	if token == "" {
		fmt.Fprintf(o.ErrOut,
			"warn: no token found for %s; unauthenticated requests are rate-limited (set %s or %s)\n",
			r.Identity(), tokenVarName(r.Identity()), strings.ToUpper(kind)+"_TOKEN")
	}

	var f fetch.Fetcher
	switch kind {
	case "github":
		f = fetch.NewGitHub(repo, fetch.NewClient(fetch.ClientOptions{
			HTTP:    o.HTTP,
			BaseURL: fetch.GitHubBaseURL(repo), Token: token,
			AuthHeader: "Authorization", AuthPrefix: "Bearer ",
			Headers: map[string]string{
				"Accept":               "application/vnd.github+json",
				"X-GitHub-Api-Version": "2026-03-10",
			},
		}), o.Cache, blobParallel)
	case "gitlab":
		f = fetch.NewGitLab(repo, fetch.NewClient(fetch.ClientOptions{
			HTTP:    o.HTTP,
			BaseURL: fetch.GitLabBaseURL(repo), Token: token,
			AuthHeader: "PRIVATE-TOKEN",
		}), o.Cache, blobParallel)
	}
	fsys, err := f.Open(ctx, patterns)
	if err != nil {
		return nil, nil, err
	}
	return fsys, f, nil
}

// contentSet is every file a later stage will read from this repository.
//
// Exactly five things, and the list is not a guess: it is every fs.ReadFile
// call site below internal/, enumerated. spec.runbook (scorecard and
// renderer), spec.alerts (scorecard), every .md under spec.docs (renderer),
// and .landsraad/checks/*.yaml (ingest). Anything a later plan adds must be
// added here too, or it reads as fetch.ErrNotFetched inside a stage.
//
// spec.path is deliberately absent: CheckFiles only stats it, and a stat is
// answered from the tree listing for free (ruling R24).
func contentSet(fsys fs.FS, entities []*catalog.Entity) []string {
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || !fs.ValidPath(p) {
			return
		}
		seen[p] = true
	}
	for _, e := range entities {
		add(e.Spec.Runbook)
		add(e.Spec.Alerts)
		if e.Spec.Docs == "" {
			continue
		}
		// WalkDir over the listing: no content is needed to find the pages,
		// which is the sparse fetcher's whole point.
		fs.WalkDir(fsys, e.Spec.Docs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // reported later, by the renderer, with a path
			}
			if !d.IsDir() && strings.HasSuffix(p, ".md") {
				add(p)
			}
			return nil
		})
	}
	if entries, err := fs.ReadDir(fsys, scorecard.ChecksDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && scorecard.IsCheckResultsFile(e.Name()) {
				add(path.Join(scorecard.ChecksDir, e.Name()))
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// docsDirs is every directory that must be listed before contentSet can walk
// it: each entity's spec.docs, plus the check-results directory.
//
// Separate from contentSet because listing and fetching are different
// requests, and the listing has to happen first — contentSet cannot walk a
// directory the host has not described yet.
func docsDirs(entities []*catalog.Entity) []string {
	seen := map[string]bool{scorecard.ChecksDir: true}
	for _, e := range entities {
		if e.Spec.Docs != "" && fs.ValidPath(e.Spec.Docs) {
			seen[e.Spec.Docs] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// tokenFor resolves a repository's token (ruling R29).
//
// Per-repository first, then the host-wide variable. Never from repos.yaml:
// spec §14.1 puts tokens in CI secrets, and a token committed to a
// repository is a token in everybody's clone forever.
func tokenFor(r config.Repo, look func(string) (string, bool)) string {
	if name := r.Identity(); name != "" {
		if v, ok := look(tokenVarName(name)); ok && v != "" {
			return v
		}
	}
	kind, known := r.HostKind()
	if !known {
		return ""
	}
	if v, ok := look(strings.ToUpper(kind) + "_TOKEN"); ok {
		return v
	}
	return ""
}

// tokenVarName is LANDSRAAD_TOKEN_<NAME>: uppercased, every byte that is not
// a letter or a digit replaced with an underscore. Stated rather than clever,
// because a variable nobody can guess is a variable nobody sets.
func tokenVarName(name string) string {
	var b strings.Builder
	b.WriteString("LANDSRAAD_TOKEN_")
	for _, r := range strings.ToUpper(name) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// repoDefaultPatternsNote is defaultPatternsNote's per-repository twin.
//
// patternsFor's single-repository version can name the one diagnostic it
// might emit after the fact, because there is only ever one repository to
// mean. openRepos runs across every entry in repos.yaml, and each can
// independently name no paths: — a diagnostic that cannot say which one is
// silent about exactly the thing a multi-repository repos.yaml most needs
// pointed out, so this carries Repo and the entry's own line rather than
// reusing defaultPatternsNote's file-level Line 1.
func repoDefaultPatternsNote(name string, line int) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevInfo,
		Repo:     name,
		File:     "repos.yaml",
		Line:     line,
		Check:    "default-patterns",
		Message: fmt.Sprintf("%s names no paths; using default paths (%s)",
			repoLabel(name), strings.Join(config.DefaultPatterns(), ", ")),
		Hint: "add paths: to this entry if its services live elsewhere",
	}
}

// loadReposFile reads repos.yaml, defaulting to a single local repository
// when there is none. A checkout with no repos.yaml still builds.
func loadReposFile(rootFS fs.FS, c *diag.Collector) []config.Repo {
	data, err := fs.ReadFile(rootFS, "repos.yaml")
	if err != nil {
		c.Add(defaultPatternsNote("no repos.yaml found"))
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
	r := config.LoadRepos("repos.yaml", data, c)
	if len(r.Repos) == 0 {
		// Same degraded mode as an absent file — repos.yaml exists but names
		// no repositories at all — and it deserves the same announcement:
		// silent here would mean this specific case is invisible while its
		// sibling three lines up is not.
		c.Add(defaultPatternsNote("repos.yaml lists no repositories"))
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
	// Exactly one entry is read from disk. LocalRepo applies ruling R26's
	// rule; anything it does not choose is fetched.
	out := make([]config.Repo, len(r.Repos))
	copy(out, r.Repos)
	local, ok := r.LocalRepo()
	for i := range out {
		out[i].Local = ok && out[i].URL == local.URL && out[i].Name == local.Name
	}
	return out
}
