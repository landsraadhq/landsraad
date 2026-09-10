package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/render"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// BuildOptions is everything build needs that is not the filesystem. Now,
// LastEdit and Version are values so the whole command is reproducible from
// its inputs and testable without a clock or a git repository.
// The output directory is deliberately absent: Build produces values and
// has no opinion about where they land. writeSite takes it instead.
type BuildOptions struct {
	Mermaid  render.Mermaid
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	Version  string
	Force    bool
}

// Build renders the portal, returning the files and an exit code.
//
// It writes nothing. writeSite is the disk half and lives beside it in cmd/,
// which is what lets the entire build be tested against an fstest.MapFS —
// and what lets `serve --watch` rebuild in memory without ever touching the
// output directory.
func Build(fsys fs.FS, errOut io.Writer, opts BuildOptions) ([]emit.File, int) {
	var c diag.Collector

	// FullCatalog, not LocalOnly: build claims to render the whole catalog,
	// so a dangling reference is a hard failure (spec §7.1).
	cat, g, teams := loadCatalogScoped(fsys, catalog.FullCatalog, &c)
	if cat == nil || c.HasErrors() {
		reportDiagnostics(errOut, c.Diagnostics())
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}

	src := catalog.SingleSource(localRepoName(fsys), fsys)
	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(src, cat, std.StaleAfterDays(), opts.Now, &c)
	sc := scorecard.Score(cat, std, reported, scorecard.Env{
		Sources:        src,
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
	history, historyErr := fs.ReadFile(fsys, scorecard.HistoryPath)
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
		FS:                fsys, Mermaid: opts.Mermaid,
		GeneratedAt: opts.Now, Version: opts.Version,
		Notice: partialNotice(fsys, errOut),
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

// partialNotice reports that this build covers fewer repositories than
// repos.yaml names, because fetching the others is Plan 4 (ruling R22).
//
// It returns the banner text and writes the warning. Silently rendering a
// portal that covers one repository of three is precisely spec §12's "worse
// than rendering nothing".
//
// It counts rather than names. The previous version listed r.Repos[1:] as the
// missing ones, which is right only if the entry you are standing in happens
// to be written first. config.LoadRepos does not verify that — LocalPatterns
// takes r.Repos[0].Paths "by convention" — so with the local repository listed
// third the banner stamped into EVERY page named the wrong two repositories as
// missing and quietly omitted the two that actually were. R22's point is that
// the omission is visible AND accurate; a count is both, whichever entry is
// local. Naming them correctly would mean teaching the loader which entry it
// is standing in, which changes what repos.yaml means to everyone who already
// has one — a one-way door, and Plan 4's to open.
func partialNotice(fsys fs.FS, errOut io.Writer) string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		return ""
	}
	var discard diag.Collector
	r := config.LoadRepos("repos.yaml", data, &discard)
	if len(r.Repos) < 2 {
		return ""
	}
	missing := len(r.Repos) - 1
	verb := "were"
	if missing == 1 {
		verb = "was"
	}
	fmt.Fprintf(errOut, "warn: repos.yaml lists %s and this build read only the local one; %s %s not read\n",
		plural(len(r.Repos), "repository", "repositories"),
		plural(missing, "repository", "repositories"), verb)
	return fmt.Sprintf("This portal covers only the repository this build ran in. "+
		"%s of the %d in repos.yaml %s not read; fetching remote repositories is not implemented yet.",
		plural(missing, "repository", "repositories"), len(r.Repos), verb)
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

func newBuildCmd() *cobra.Command {
	var (
		out        string
		mermaidSrc string
		force      bool
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
			opts := BuildOptions{
				Mermaid: mermaid,
				Now:     time.Now().UTC(), LastEdit: gitLastEdit(resolved),
				Version: version(), Force: force,
			}
			files, code := Build(os.DirFS(resolved), cmd.ErrOrStderr(), opts)
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
	return cmd
}
