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
	"sync"
	"time"

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
	// Line is where the entry that named this repository sits in
	// repos.yaml. reportFetchFailures prints it, so a failure points at the
	// entry to fix rather than at the top of the file (ruling R38).
	Line int
	// Kind is the host adapter this repository would have used --
	// "github", "gitlab", or "" when config.Repo.HostKind could not tell.
	// It is config.Repo.HostKind's own answer, captured at failure time: a
	// pure function of the repos.yaml entry, honouring an explicit host:
	// key, available whether or not an adapter was ever constructed. It is
	// carried here rather than re-derived from URL later, because a
	// substring match on the URL ("contains gitlab") is wrong for a
	// self-hosted GitLab at a URL that does not say so.
	Kind string
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
	// Sleep is how a fetcher's client waits between retries. Nil in
	// production, which fetch.NewClient already turns into time.Sleep — this
	// field exists so a test against an always-failing fake host can run its
	// retries in milliseconds instead of the real 1s/2s/4s backoff schedule,
	// without weakening the retry path itself (fetch.ClientOptions.Sleep is
	// the seam; this just reaches it from cmd/).
	//
	// Reachable from blobParallel's concurrent workers: a caller that counts
	// calls with a bare counter, as TestBuildWithAnUnreachableRemote does, is
	// safe only when it can prove Sleep is never called from more than one
	// goroutine at a time. A Sleep counted from a genuinely parallel context
	// needs a mutex or an atomic.
	Sleep func(time.Duration)
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
	// lines is where each repository's entry sits in repos.yaml, so a
	// diagnostic about a repository points at the entry that named it
	// (ruling R38).
	lines map[string]int
	// edits is where that one caller puts the errors it cannot return.
	// Shared by pointer across WithLocal, because serve --watch builds from
	// a per-rebuild copy while the LastEditFunc it scores with was built
	// once, at startup, against this original.
	edits *lastEditLog
}

func (w *workspace) Sources() catalog.Sources { return w.sources }
func (w *workspace) Failures() []repoFailure  { return w.failures }

// RecordLastEditFailure notes that a host could not answer docs-fresh.
func (w *workspace) RecordLastEditFailure(repo string, err error) { w.edits.record(repo, err) }

// TakeLastEditFailures returns what the scoring pass recorded, and empties
// the log so the next build reports its own failures rather than every
// failure since startup.
func (w *workspace) TakeLastEditFailures() []repoEditFailure {
	out := w.edits.take()
	for i := range out {
		out[i].Line = w.lines[out[i].Repo]
	}
	return out
}

// repoEditFailure is one repository whose host could not say when a path was
// last changed.
type repoEditFailure struct {
	Repo string
	// Line is the repository's entry in repos.yaml, filled in by
	// TakeLastEditFailures from the workspace that knows it.
	Line int
	Err  error
}

// lastEditLog is the errors docs-fresh cannot return.
//
// scorecard.LastEditFunc answers (time.Time, bool) and has no error channel:
// false means "this host has no answer". An expired token, a spent rate
// limit and a host 500 all arrived as an error that multiLastEdit discarded,
// so every entity in every remote repository rendered docs-fresh:
// not-reported — byte-identical to a host that genuinely has no answer — and
// build exited 0 with a portal carrying no banner. That is exactly the
// silent degradation reportFetchFailures exists to prevent for phases 1 and
// 2, and it was visible in neither stderr nor the artifact.
//
// It lives on the workspace because that is the value multiLastEdit already
// captures, and because a host failure is a fact about a repository the
// workspace owns. Build drains it after Score and before render.Input.
//
// Behind a mutex. Score calls LastEditFunc from one goroutine today, and
// nothing in LastEditFunc's signature says it must.
type lastEditLog struct {
	mu   sync.Mutex
	errs map[string]error
}

func newLastEditLog() *lastEditLog { return &lastEditLog{errs: map[string]error{}} }

// record keeps the FIRST error per repository. A repository whose token died
// fails for every path in every entity it holds; saying so once is the
// point, and a hundred identical lines would bury the one that matters.
//
// A nil receiver records nothing, and that is a real state rather than a
// swallowed failure: a workspace with no fetchers — every test literal in
// this package, and openRepos' early return — has no remote host that could
// fail, because multiLastEdit returns before it ever reaches one.
func (l *lastEditLog) record(repo string, err error) {
	if l == nil || err == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, seen := l.errs[repo]; !seen {
		l.errs[repo] = err
	}
}

// take drains the log, sorted by repository so a banner and a diagnostic
// read the same on every run.
func (l *lastEditLog) take() []repoEditFailure {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]repoEditFailure, 0, len(l.errs))
	for repo, err := range l.errs {
		out = append(out, repoEditFailure{Repo: repo, Err: err})
	}
	clear(l.errs)
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

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
func (w *workspace) ParseAll(v *schema.Validator, c *diag.Collector) parseResult {
	// solo, computed once from the same source count assemble will see
	// (assemble receives this same workspace's Sources()): a build reading
	// exactly one repository must get the single rich "no entities" error
	// parseRepo produces when solo, not the per-repository warning that
	// exists to distinguish repositories in a multi-repository build.
	solo := len(w.sources) <= 1
	var out parseResult
	for _, name := range w.sources.Names() {
		fsys, ok := w.sources.Get(name)
		if !ok {
			continue
		}
		// patternsKnown: true. R47 makes it sound rather than lucky — a
		// repos.yaml that did not parse yields no repositories, so this loop
		// does not run at all on that path and w.patterns holds only patterns
		// a file that parsed asked for (or, for an entry naming none,
		// defaults openRepos announced).
		p := parseRepo(name, fsys, w.patterns[name], solo, true, v, c)
		out.entities = append(out.entities, p.entities...)
		out.found += p.found
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
		fetchers: w.fetchers, lines: w.lines,
		// Shared, not copied: the LastEditFunc scoring this rebuild was
		// built from the original workspace at startup and writes there.
		edits: w.edits,
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
		return &workspace{sources: catalog.Sources{}, patterns: map[string][]string{}, fetchers: map[string]fetch.Fetcher{}, edits: newLastEditLog()}
	}
	repos := loadReposFile(o.RootFS, c)

	// Built directly as a catalog.Sources rather than as a map that is
	// converted at the end: a conversion would alias this loop's map, and
	// the type carries no constructor to copy it (see catalog/sources.go).
	w := &workspace{
		sources:  catalog.Sources{},
		patterns: map[string][]string{},
		fetchers: map[string]fetch.Fetcher{},
		lines:    map[string]int{},
		edits:    newLastEditLog(),
	}
	var failures []repoFailure

	for _, r := range repos {
		name := r.Identity()
		w.lines[name] = r.Line
		patterns := r.Paths
		if len(patterns) == 0 {
			// Silent defaulting with no diagnostic contradicts patternsFor's
			// identical single-repository case (defaultPatternsNote) and
			// CLAUDE.md's guidance that degraded mode must be visible in the
			// artifact, not only in a log. The one silent case is an entry
			// whose every pattern was rejected: repos-path already said so,
			// and the default paths are that diagnostic's consequence.
			if !r.PathsRejected() {
				c.Add(repoDefaultPatternsNote(name, r.Line))
			}
			patterns = config.DefaultPatterns()
		}

		kind, known := r.HostKind()
		if !known {
			// HostKind's "cannot tell" answer for an explicit host: typo is
			// (r.Host, false) -- the typo itself, not empty. Carrying that
			// into a repoFailure would name a hint variable
			// (strings.ToUpper(kind)+"_TOKEN" in failureMessage) built from
			// a token that does not exist. This is inert today --
			// validateRepos already raises repos-host as a SevError for
			// every case HostKind cannot resolve, and the caller refuses on
			// that before Build ever renders a failure message -- but the
			// safety belongs here, not in another package's diagnostic.
			kind = ""
		}
		fail := func(err error) {
			failures = append(failures, repoFailure{Name: name, Line: r.Line, Kind: kind, Err: err})
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
			// solo and patternsKnown are both irrelevant here: scratch is
			// thrown away below, so whichever "no entities" diagnostic
			// parseRepo would have added to it never surfaces. ParseAll
			// makes the real, kept decision once every repository is in the
			// workspace. patternsKnown is nonetheless true rather than
			// false, because true is what it is: under R47 this loop is
			// unreachable for a repos.yaml that did not parse.
			var scratch diag.Collector
			parsed := parseRepo(name, fsys, patterns, false, true, v, &scratch).entities

			// expand lists every directory these entities name — spec.docs,
			// and the directories holding spec.runbook and spec.alerts —
			// and contentSet accepts nothing else, so the listing always
			// comes first. See expanded.
			x, err := expand(ctx, fetcher, remote, parsed)
			if err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, contentSet(x)); err != nil {
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
	repo, err := fetch.ParseRepo(r.URL, r.Ref)
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
			Sleep: o.Sleep,
		}), o.Cache, blobParallel)
	case "gitlab":
		f = fetch.NewGitLab(repo, fetch.NewClient(fetch.ClientOptions{
			HTTP:    o.HTTP,
			BaseURL: fetch.GitLabBaseURL(repo), Token: token,
			AuthHeader: "PRIVATE-TOKEN",
			Sleep:      o.Sleep,
		}), o.Cache, blobParallel)
	}
	fsys, err := f.Open(ctx, patterns)
	if err != nil {
		return nil, nil, err
	}
	return fsys, f, nil
}

// expanded is one repository's filesystem after every directory its
// entities name has been listed: the only state in which contentSet's
// answer is complete.
//
// The order used to be held by a comment at the call site. Out of order,
// contentSet cannot see a runbook, an alerts file or a docs page outside
// the configured paths, so none is fetched, and the stage that reads it
// reports a landsraad bug (fetch.ErrNotFetched) instead of the page. expand
// is the only function that builds one, and contentSet takes nothing else.
// A struct literal in this package can still bypass it — the tests do, to
// hand contentSet a listing directly — so this is a signpost for the next
// reader, not a proof.
type expanded struct {
	fsys     fs.FS
	entities []*catalog.Entity
}

// expand lists every directory docsDirs names, and returns the one input
// contentSet accepts.
func expand(ctx context.Context, f fetch.Fetcher, remote *fetch.FS, entities []*catalog.Entity) (expanded, error) {
	if err := f.Expand(ctx, remote, docsDirs(entities)); err != nil {
		return expanded{}, err
	}
	return expanded{fsys: remote, entities: entities}, nil
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
//
// Every path is checked against the listing before it is asked for, which is
// what keeps a user's YAML mistake a YAML mistake. fetchBlobs rejects a path
// it cannot find an Entry for -- correctly, since that means the planner is
// wrong -- and openRepos turns any fetch error into a repoFailure, which
// drops the whole repository. So a spec.runbook naming a file that is not
// there used to cost the entire repository: "landsraad could not read
// edge-gateway" instead of "your runbook is missing", and under
// --allow-partial an exit 0 with the repository silently absent. Skipping it
// here leaves CheckFiles to report missing-file against the line in
// service.yaml that is actually wrong, and runbook-present to fail the way
// the scorecard is for. A fetch is not the right place to decide a user's
// YAML is wrong.
//
// The check is a Stat, which for *fetch.FS is answered from the tree listing
// and costs no request (ruling R24). It is also why docsDirs must expand a
// runbook's and an alerts file's directory: a file that genuinely exists but
// sits outside the configured patterns has to be LISTED before this can tell
// it apart from one that is not there at all.
func contentSet(x expanded) []string {
	fsys, entities := x.fsys, x.entities
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || !fs.ValidPath(p) {
			return
		}
		if _, err := fs.Stat(fsys, p); err != nil {
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

// docsDirs is every directory that must be listed before contentSet can see
// it: each entity's spec.docs, the directory holding its spec.runbook and
// the one holding its spec.alerts, plus the check-results directory.
//
// Separate from contentSet because listing and fetching are different
// requests, and the listing has to happen first — contentSet cannot walk a
// directory the host has not described yet, and since contentSet now skips
// anything absent from the listing, it cannot see a file in one either.
//
// The runbook's and the alerts file's directories are here because neither
// is reachable from the configured patterns in an ordinary satellite layout:
// `paths: [services/*]` with `runbook: docs/runbooks/api.md` puts the
// runbook outside every prefix GitLab.Open lists and outside every directory
// GitHub's truncated-tree descent walks. Without this the file is simply not
// in the listing, and landsraad reports a runbook that is sitting in the
// repository as missing.
//
// "." is deliberately never returned, because it never needs to be: both
// adapters list the root at Open (ruling R45). GitHub's complete listing
// covers it and walk() records it; GitLab lists it before any prefix. A
// root-level runbook.md is therefore already in the listing. Asking GitHub
// to expand "." would recursively list the entire repository, the one thing
// ruling R28's descent exists to avoid.
func docsDirs(entities []*catalog.Entity) []string {
	seen := map[string]bool{scorecard.ChecksDir: true}
	addDir := func(d string) {
		if d == "" || d == "." || !fs.ValidPath(d) {
			return
		}
		seen[d] = true
	}
	parentOf := func(p string) string {
		if p == "" || !fs.ValidPath(p) {
			return ""
		}
		return path.Dir(p)
	}
	for _, e := range entities {
		addDir(e.Spec.Docs)
		addDir(parentOf(e.Spec.Runbook))
		addDir(parentOf(e.Spec.Alerts))
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

// tokenVarName is LANDSRAAD_TOKEN_<NAME>: uppercased, every character that
// is not an ASCII letter or digit replaced with one underscore. A character,
// not a byte: "café" is LANDSRAAD_TOKEN_CAF_. Stated rather than clever,
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
// pointed out, so this names the repository in its message and carries the
// entry's own line rather than reusing defaultPatternsNote's file-level Line
// 1. Repo stays empty: the file is repos.yaml, in the repository the command
// is standing in (ruling R41).
func repoDefaultPatternsNote(name string, line int) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevInfo,
		File:     "repos.yaml",
		Line:     line,
		Check:    "default-patterns",
		Message: fmt.Sprintf("%s names no paths; using default paths (%s)",
			repoLabel(name), strings.Join(config.DefaultPatterns(), ", ")),
		Hint: "add paths: to this entry if its services live elsewhere",
	}
}

// loadReposFile reads repos.yaml and returns one of three things.
//
// The file is absent, or present and names no repositories: a single local
// repository carrying the default patterns, announced as default-patterns. A
// checkout with no repos.yaml still builds.
//
// The file is present and did not parse: no repositories at all (ruling R47).
// Not one synthesised local entry, because repos-parse already carries the
// cause and defaulting would present a guess as configuration.
//
// Otherwise: what the file says.
func loadReposFile(rootFS fs.FS, c *diag.Collector) []config.Repo {
	data, err := fs.ReadFile(rootFS, "repos.yaml")
	if err != nil {
		c.Add(defaultPatternsNote("no repos.yaml found"))
		return []config.Repo{{Local: true, Paths: config.DefaultPatterns(), Line: 1}}
	}
	r := config.LoadRepos("repos.yaml", data, c)
	if !r.Loaded() {
		// The file exists and did not parse. repos-parse already carries the
		// cause, and defaulting here would present a guess as configuration —
		// the same rule patternsFor applies to default-patterns (ruling R47).
		// Returning no repositories is also what makes the multi-repository
		// path structurally unable to reach parseRepo's solo no-entities off
		// a guessed pattern set: there is nothing for ParseAll to iterate.
		return nil
	}
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
