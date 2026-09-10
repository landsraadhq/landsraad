package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/generate"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// artifacts is stage 8 (EMIT) for the generated-ownership half of the spec:
// the pure part, returning what should be on disk without touching it.
//
// Exported artifacts are ordered as CODEOWNERS, routing, Slack map — the order
// spec §11 lists them, which is also the order of decreasing blast radius.
func artifacts(fsys fs.FS, c *diag.Collector) []emit.File {
	cat, teams := loadCatalog(fsys, c)
	if cat == nil {
		return nil
	}
	return []emit.File{
		generate.CODEOWNERS(cat, teams, c),
		generate.AlertRoutes(cat, teams, c),
		generate.SlackMap(cat, teams, c),
	}
}

// loadCatalog runs stages 1, 3, 4 and 5 at LocalOnly scope — what gen and
// score need, and what validate uses.
func loadCatalog(fsys fs.FS, c *diag.Collector) (*catalog.Catalog, *config.Teams) {
	cat, _, teams := loadCatalogScoped(fsys, catalog.LocalOnly, c)
	return cat, teams
}

// loadCatalogScoped is the same composition at a caller-chosen scope, also
// returning the resolved graph.
//
// build needs both: spec §7.1 makes a dangling reference a hard failure
// under build and a recorded-and-skipped one under validate, because a
// service repo's CI cannot see entities defined elsewhere. And the portal's
// dependency pages are drawn from the Graph, which is a value produced BY
// Resolve rather than state on Catalog (spec §3.1).
func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	paths := patternsFor(fsys, c)
	found, err := discover.Find(fsys, paths)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
		return nil, nil, nil
	}
	// Matching nothing at all must be an error here too: gen and score both
	// call loadCatalog, and without this check an empty-matching repos.yaml
	// produces a valid-looking empty catalog that gen writes as empty
	// artifacts and gen --check certifies as up to date forever. Compare
	// Validate's identical check in validate.go.
	if len(found) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			File:     "repos.yaml",
			Line:     1,
			Check:    "no-entities",
			Message: fmt.Sprintf("no %s found under any configured path (%s)",
				discover.Filename, strings.Join(paths, ", ")),
			Hint: "add a repos.yaml listing the paths your services live under",
		})
	}
	files := discover.Load(fsys, found, c)

	validator, err := schema.Default()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "schema", Line: 1,
			Check:   "schema-compile",
			Message: fmt.Sprintf("cannot compile the embedded schema: %v", err),
		})
		return nil, nil, nil
	}
	for _, f := range files {
		validator.Validate("", f.Path, f.Data, c)
	}

	cat := catalog.NewCatalog(catalog.ParseAll(localRepoName(fsys), files, c), c)
	catalog.CheckFiles(fsys, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)

	teamsData, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "teams-missing",
			Message: "teams.yaml not found, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
		return nil, nil, nil
	}
	teams := config.LoadTeams("teams.yaml", teamsData, c)
	teams.ValidateOwners(cat, c)
	return cat, g, teams
}

// Gen writes the derived artifacts, or under check reports which are stale.
//
// It refuses to generate from a catalog with errors. An artifact derived from
// a broken catalog encodes the broken state, and `--check` then passes against
// it forever — metadata rot that the anti-rot mechanism certifies as fine.
func Gen(fsys fs.FS, root string, out, errOut io.Writer, f diag.Formatter, check bool) int {
	var c diag.Collector
	files := artifacts(fsys, &c)

	if c.HasErrors() {
		if err := f.Write(out, c.Diagnostics()); err != nil {
			fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "refusing to generate from a catalog with errors; fix them and rerun\n")
		return exitValidation
	}
	if err := f.Write(out, c.Diagnostics()); err != nil {
		fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
		return exitUsage
	}

	if check {
		diffs := emit.Diff(files, fsys)
		if len(diffs) == 0 {
			fmt.Fprintf(errOut, "ok: %s up to date\n", plural(len(files), "artifact", "artifacts"))
			return exitOK
		}
		for _, d := range diffs {
			fmt.Fprintf(errOut, "stale: %s is %s\n", d.Path, d.Kind)
		}
		fmt.Fprintf(errOut, "\nrun `landsraad gen` and commit the result\n")
		return exitValidation
	}

	// The one write loop. Spec §3.1: only the command layer touches the
	// filesystem, and every generator above returned values.
	//
	// root travels beside fsys rather than being recovered from it: an fs.FS
	// does not know where it came from, and a package-level variable holding
	// the answer would be exactly the hidden global the project forbids one
	// directory down.
	for _, file := range files {
		full := filepath.Join(root, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return exitUsage
		}
		if err := os.WriteFile(full, file.Data, 0o644); err != nil {
			fmt.Fprintf(errOut, "error: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "  wrote %s\n", file.Path)
	}
	fmt.Fprintf(errOut, "ok: %s generated\n", plural(len(files), "artifact", "artifacts"))
	return exitOK
}

func newGenCmd() *cobra.Command {
	var check bool
	cmd := &cobra.Command{
		Use:   "gen [root]",
		Short: "Bene Gesserit — regenerate CODEOWNERS, alert routing and the Slack map",
		Long: "Derive the ownership artifacts from the catalog. These files are " +
			"never hand-edited: `--check` regenerates them in memory and fails when " +
			"what is committed differs, which is what makes metadata rot break " +
			"something visible.",
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
			cmd.SilenceUsage = true
			if code := Gen(os.DirFS(resolved), resolved, cmd.OutOrStdout(), cmd.ErrOrStderr(), diagText(), check); code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&check, "check", false, "fail if the committed artifacts differ from what would be generated")
	return cmd
}
