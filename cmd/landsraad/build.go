package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/render"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// BuildOptions is everything build needs that is not a filesystem.
type BuildOptions struct {
	Mermaid  render.Mermaid
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	Version  string
	Force    bool
	// AllowPartial renders a portal from the repositories that could be
	// read, stamping a banner naming the ones that could not (ruling R32).
	AllowPartial bool
}

// Build renders the portal, returning the files and an exit code.
//
// Two filesystems, and they are different things (ruling R34). root is the
// repository the command is standing in: teams.yaml, standards.yaml and
// scorecard-history.csv live there and nowhere else. w is every repository
// in the catalog, already fetched.
//
// It writes nothing and it fetches nothing. writeSite is the disk half;
// openRepos is the network half, and it ran before this was called — which
// is what lets serve --watch rebuild on every keystroke without touching a
// host API (ruling R33).
func Build(root fs.FS, w *workspace, errOut io.Writer, opts BuildOptions) ([]emit.File, int) {
	var c diag.Collector

	if code := reportFetchFailures(w.Failures(), opts.AllowPartial, errOut); code != exitOK {
		return nil, code
	}

	v := defaultValidator(&c)
	if v == nil {
		reportDiagnostics(errOut, c.Diagnostics())
		return nil, exitValidation
	}

	// FullCatalog, not LocalOnly: build claims to render the whole catalog,
	// so a dangling reference is a hard failure (spec §7.1).
	cat, g, teams := assemble(w.ParseAll(v, &c), w.Sources(), catalog.FullCatalog, root, &c)
	if cat == nil || c.HasErrors() {
		reportDiagnostics(errOut, c.Diagnostics())
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}

	std := standardsFor(root, errOut)
	reported := scorecard.Ingest(w.Sources(), cat, std.StaleAfterDays(), opts.Now, &c)
	sc := scorecard.Score(cat, std, reported, scorecard.Env{
		Sources:        w.Sources(),
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}, &c)

	// nil History and unreadable History are different answers, and the
	// scorecard page renders them differently. Discarding the error collapsed
	// a permission problem, an EISDIR and a truncated read into "no file",
	// which renders as "No history yet. Run `landsraad score --history` in CI
	// to start recording one" — telling somebody to set up a job they already
	// set up, about a file that is sitting right there.
	history, historyErr := fs.ReadFile(root, scorecard.HistoryPath)
	if historyErr != nil && !errors.Is(historyErr, fs.ErrNotExist) {
		history = nil
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, File: scorecard.HistoryPath, Line: 1,
			Check:   "history-unreadable",
			Message: fmt.Sprintf("cannot read %s: %v", scorecard.HistoryPath, historyErr),
			Hint: "the portal is built without a trend; fix the file's permissions or " +
				"delete it and let `landsraad score --history` write a fresh one",
		})
	}

	in := render.Input{
		Catalog: cat, Graph: g, Teams: teams,
		Scorecard: sc, Standards: std, History: history,
		HistoryUnreadable: historyErr != nil && !errors.Is(historyErr, fs.ErrNotExist),
		Sources:           w.Sources(), Mermaid: opts.Mermaid,
		GeneratedAt: opts.Now, Version: opts.Version,
		Notice: partialBanner(w.Failures()),
	}
	files := render.Site(in, &c)

	reportDiagnostics(errOut, c.Diagnostics())
	if c.HasErrors() {
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}
	fmt.Fprintf(errOut, "ok: %s rendered\n", plural(len(files), "file", "files"))
	return files, exitOK
}

// reportFetchFailures decides what a failed repository costs.
//
// Spec §12: a fetch failure is a hard failure, because rendering a portal
// quietly missing three services is worse than rendering nothing.
// --allow-partial is the stated exception, and it pays for itself with a
// banner in the artifact rather than a line in a log nobody reads.
//
// exitUsage, not exitValidation: nobody's YAML is wrong. Exit 2 would send
// a service owner to look at a file that is fine.
func reportFetchFailures(fails []repoFailure, allowPartial bool, errOut io.Writer) int {
	if len(fails) == 0 {
		return exitOK
	}
	sorted := make([]repoFailure, len(fails))
	copy(sorted, fails)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	level := "error"
	if allowPartial {
		level = "warn"
	}
	for _, f := range sorted {
		fmt.Fprintf(errOut, "%s: %s\n", level, failureMessage(f))
	}
	if allowPartial {
		return exitOK
	}
	fmt.Fprintf(errOut,
		"refusing to build a portal that is missing %s; pass --allow-partial to build one anyway, with a banner saying so\n",
		plural(len(fails), "repository", "repositories"))
	return exitUsage
}

// failureMessage says what went wrong and what to do about it.
//
// The 404 case is the one that earns its length. Both hosts return 404 for a
// private repository with no token — identical to a repository that is not
// there — so a bare "not found" tells somebody their repository does not
// exist while they are looking at it in a browser tab.
//
// f.Kind is config.Repo.HostKind's own answer (set in openRepos), not a
// substring match on the URL: a self-hosted GitLab's URL rarely contains
// "gitlab", and a wrong hint sends the person fixing the failure to set the
// wrong variable. When HostKind could not tell (f.Kind == ""), the message
// names only the per-repository variable -- inventing a host-wide one would
// be a guess with the same failure mode this exists to avoid.
func failureMessage(f repoFailure) string {
	vars := tokenVarName(f.Name)
	if f.Kind != "" {
		vars += " or " + strings.ToUpper(f.Kind) + "_TOKEN"
	}
	switch {
	case fetch.IsNotFound(f.Err):
		return fmt.Sprintf("cannot read %s: not found. A private repository with no token looks "+
			"exactly like this; check the url and that %s is set", f.Name, vars)
	case fetch.IsUnauthorized(f.Err):
		return fmt.Sprintf("cannot read %s: the host rejected the token; check that %s is current "+
			"and has read access", f.Name, vars)
	case fetch.IsRateLimited(f.Err):
		var se *fetch.StatusError
		errors.As(f.Err, &se)
		return fmt.Sprintf("cannot read %s: the host's rate limit is spent until %s; an "+
			"unauthenticated build gets 60 requests an hour, an authenticated one 5000",
			f.Name, se.RateReset.Format(time.RFC3339))
	default:
		return fmt.Sprintf("cannot read %s: %v", f.Name, f.Err)
	}
}

// partialBanner is the degraded-mode notice stamped into every page.
//
// It NAMES the repositories. partialNotice, the scaffold this replaces,
// could only count them: with no fetcher it did not know which entry was
// local, and the version that guessed named the wrong two in every page of
// a generated site. Ruling R22 said a real fetcher would name the ones that
// actually failed. This is that -- partialNotice was deleted in Plan 4
// Task 13 (ruling R32). A build with no fetch failures renders an empty
// Notice: see TestPartialBanner's "none" case.
func partialBanner(fails []repoFailure) string {
	if len(fails) == 0 {
		return ""
	}
	names := make([]string, 0, len(fails))
	for _, f := range fails {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	possessive := "their"
	if len(names) == 1 {
		possessive = "its"
	}
	return fmt.Sprintf("This portal is incomplete: %s could not be read, so %s services are "+
		"missing from this catalog.", englishList(names), possessive)
}

// englishList renders "a", "a and b", "a, b and c". A banner is read by a
// person, and "a, b" for two items reads as a truncated list.
func englishList(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

// singleRepoWorkspace adapts one filesystem into a workspace with no
// remotes, for a caller that predates openRepos.
//
// serve.go's rebuild loop is the only caller. It fetches nothing today --
// `serve` has no repos.yaml wiring yet -- so this only has to reproduce what
// loadCatalogScoped used to do for a single repository. Task 14 replaces it
// with a *workspace built once by openRepos at startup and reused across
// rebuilds via WithLocal (ruling R33); this bridge exists only so Build's
// signature change does not leave serve.go uncompilable in between.
func singleRepoWorkspace(fsys fs.FS, c *diag.Collector) *workspace {
	name := localRepoName(fsys)
	return &workspace{
		sources:  catalog.Sources{name: fsys},
		patterns: map[string][]string{name: patternsFor(fsys, c)},
		local:    name,
	}
}

// mermaidFor resolves --mermaid-src (ruling R13).
func mermaidFor(src string) (render.Mermaid, error) {
	switch {
	case src == "":
		return render.Mermaid{
			Src:       render.DefaultMermaidSrc,
			Integrity: render.DefaultMermaidIntegrity,
		}, nil
	case src == "none":
		// No diagram renderer at all. The pages still emit their Mermaid
		// source; assets/mermaid.js marks it unavailable rather than
		// leaving a blank space (spec §12).
		return render.Mermaid{}, nil
	case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "http://"):
		// A user-supplied URL carries no integrity hash: we do not know it,
		// and inventing one would block the very file they asked for.
		return render.Mermaid{Src: src}, nil
	default:
		data, err := os.ReadFile(src)
		if err != nil {
			return render.Mermaid{}, fmt.Errorf("cannot read the Mermaid bundle %s: %w", src, err)
		}
		return render.Mermaid{Src: render.LocalMermaidPath, Data: data}, nil
	}
}

// cacheFor is the blob cache, or nothing when --no-cache is set.
func cacheFor(root string, noCache bool) fetch.Cache {
	if noCache {
		return fetch.NopCache{}
	}
	return newBlobCache(filepath.Join(root, ".landsraad", "cache", "blobs"))
}

// multiLastEdit answers docs-fresh for every repository: git history for the
// local one, the host's commit API for the rest (ruling R35).
//
// A repository whose adapter has no answer returns false rather than a date,
// and docs-fresh then reports not-reported. Inventing one would give every
// fetched service full marks for freshness.
//
// ctx is threaded through rather than captured from context.Background(): a
// request to a host API belongs to the build that asked for it, and Ctrl-C
// during Score must be able to cancel it rather than waiting out a slow host.
func multiLastEdit(ctx context.Context, root string, w *workspace) scorecard.LastEditFunc {
	local := gitLastEdit(root)
	return func(repo, p string) (time.Time, bool) {
		if repo == w.local {
			return local(repo, p)
		}
		f, ok := w.FetcherFor(repo)
		if !ok {
			return time.Time{}, false
		}
		t, known, err := f.LastEdit(ctx, p)
		if err != nil {
			return time.Time{}, false
		}
		return t, known
	}
}

func newBuildCmd() *cobra.Command {
	var (
		out          string
		mermaidSrc   string
		force        bool
		allowPartial bool
		noCache      bool
	)
	cmd := &cobra.Command{
		Use:   "build [root]",
		Short: "Render the static portal",
		Long: "Render the catalog, the scorecard and every entity's documentation " +
			"into a static site.\n\n" +
			"Diagrams load Mermaid from a pinned CDN URL with an integrity hash. " +
			"On a host with no outbound network, pass --mermaid-src with a path to " +
			"a local mermaid.min.js and it is copied into the site, or --mermaid-src " +
			"none to leave the diagrams unrendered.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			start := "."
			if len(args) == 1 {
				start = args[0]
			}
			resolved, err := findRoot(start)
			if err != nil {
				return err
			}
			mermaid, err := mermaidFor(mermaidSrc)
			if err != nil {
				return err
			}
			cmd.SilenceUsage = true
			var c diag.Collector
			w := openRepos(cmd.Context(), reposOptions{
				Root: resolved, RootFS: os.DirFS(resolved),
				Cache:  cacheFor(resolved, noCache),
				Lookup: os.LookupEnv,
				ErrOut: cmd.ErrOrStderr(),
			}, &c)
			reportDiagnostics(cmd.ErrOrStderr(), c.Diagnostics())
			if c.HasErrors() {
				os.Exit(exitUsage)
			}
			opts := BuildOptions{
				Mermaid: mermaid,
				Now:     time.Now().UTC(), LastEdit: multiLastEdit(cmd.Context(), resolved, w),
				Version: version(), Force: force, AllowPartial: allowPartial,
			}
			files, code := Build(os.DirFS(resolved), w, cmd.ErrOrStderr(), opts)
			if code != exitOK {
				os.Exit(code)
			}
			if err := writeSite(out, files, force, cmd.ErrOrStderr()); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "ok: portal written to %s\n", out)
			return nil
		},
	}
	cmd.Flags().StringVarP(&out, "out", "o", "dist", "output directory")
	cmd.Flags().StringVar(&mermaidSrc, "mermaid-src", "",
		"Mermaid bundle: a URL, a path to a local file, or \"none\"")
	cmd.Flags().BoolVar(&force, "force", false,
		"write into a non-empty directory landsraad did not create")
	cmd.Flags().BoolVar(&allowPartial, "allow-partial", false,
		"render a portal from the repositories that could be read, with a banner naming the ones that could not")
	cmd.Flags().BoolVar(&noCache, "no-cache", false,
		"ignore the fetched-blob cache under .landsraad/cache")
	return cmd
}
