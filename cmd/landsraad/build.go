package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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
		for _, d := range c.Diagnostics() {
			fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
		}
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}

	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(fsys, cat, std.StaleAfterDays(), opts.Now, &c)
	sc := scorecard.Score(cat, std, reported, scorecard.Env{
		FS:             fsys,
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}, &c)

	history, _ := fs.ReadFile(fsys, scorecard.HistoryPath)

	in := render.Input{
		Catalog: cat, Graph: g, Teams: teams,
		Scorecard: sc, Standards: std, History: history,
		FS: fsys, Mermaid: opts.Mermaid,
		GeneratedAt: opts.Now, Version: opts.Version,
		Notice: partialNotice(fsys, errOut),
	}
	files := render.Site(in, &c)

	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}
	if c.HasErrors() {
		fmt.Fprintf(errOut, "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n")
		return nil, exitValidation
	}
	fmt.Fprintf(errOut, "ok: %s rendered\n", plural(len(files), "file", "files"))
	return files, exitOK
}

// partialNotice reports the repositories repos.yaml names that this build
// could not read, because fetching them is Plan 4 (ruling R22).
//
// It returns the banner text and writes the warning. Silently rendering a
// portal that covers one repository of three is precisely spec §12's "worse
// than rendering nothing".
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
	var missing []string
	for _, repo := range r.Repos[1:] {
		missing = append(missing, path.Base(strings.TrimSuffix(repo.URL, "/")))
	}
	// plural() prepends the count, which reads wrong here ("1 edge-gateway
	// is missing"). The subject is a list of names, so the verb agrees with
	// how many names there are and the count appears once, earlier.
	subject, verb := strings.Join(missing, ", "), "are"
	if len(missing) == 1 {
		verb = "is"
	}
	fmt.Fprintf(errOut, "warn: repos.yaml lists %d repositories and this build read only the local one; %s %s missing from the portal\n",
		len(r.Repos), subject, verb)
	return fmt.Sprintf("This portal read only the local repository. %s %s not included; fetching remote repositories is not implemented yet.",
		subject, verb)
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
