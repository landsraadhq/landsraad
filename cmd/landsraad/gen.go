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

// loadCatalogScoped is the single-repository composition: stages 1, 3, 4
// and 5 against one filesystem. validate, gen and score all end here, and
// ruling R30 keeps them there.
func loadCatalogScoped(fsys fs.FS, scope catalog.Scope, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	v := defaultValidator(c)
	if v == nil {
		return nil, nil, nil
	}
	repo := localRepoName(fsys)
	// solo: true. loadCatalogScoped is exclusively the single-repository
	// composition (validate, gen, score — ruling R30), so the "no entities"
	// diagnostic below must be the one error main always produced, not the
	// per-repository warning that exists for the multi-repository case.
	entities := parseRepo(repo, fsys, patternsFor(fsys, c), true, v, c)
	return assemble(entities, catalog.SingleSource(repo, fsys), scope, fsys, c)
}

// parseRepo runs stages 1 and 3 for one repository: find the catalog files,
// read them, validate their shape, parse them into entities.
//
// It stops there — deliberately. Under ruling R25 cmd/ has to fetch a
// repository's *content* before stages 4 onwards can run, and it cannot
// know which files to fetch until the entities are parsed. That is the
// whole reason this is a separate function from assemble.
//
// solo says whether this is the only repository in the whole operation —
// true for every loadCatalogScoped caller, and computed from the source
// count by workspace.ParseAll for the multi-repository path. It governs
// only the zero-found diagnostic below: a single-repository run must
// produce the one rich error main always did, carrying the searched paths,
// rather than the per-repository warning that exists so a multi-repository
// build can tell "no service.yaml anywhere" apart from "the third
// repository's paths are wrong". assemble mirrors this decision from the
// source count it already has, so the two never disagree about whether a
// run was solo.
func parseRepo(name string, fsys fs.FS, patterns []string, solo bool, v *schema.Validator, c *diag.Collector) []*catalog.Entity {
	found, err := discover.Find(fsys, patterns)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, Repo: name, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files: %v", discover.Filename, err),
		})
		return nil
	}
	if len(found) == 0 {
		if solo {
			// The single-repository message: one repository, one
			// diagnostic, and the diagnostic that gates the build carries
			// the paths that were actually searched. Matches main's
			// original wording exactly — this is a refactor, not a
			// behaviour change, for validate/gen/score.
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, Repo: name, File: "repos.yaml", Line: 1,
				Check: "no-entities",
				Message: fmt.Sprintf("no %s found under any configured path (%s)",
					discover.Filename, strings.Join(patterns, ", ")),
				Hint: "add a repos.yaml listing the paths your services live under",
			})
			return nil
		}
		// A warning per repository, where a single-repository run gets one
		// error instead (above). With several repositories, "no service.yaml
		// anywhere" and "the third repository's paths are wrong" are
		// different problems, and the second is the apps/-instead-of-services/
		// bug the strict repos.yaml decoding already exists to catch.
		// assemble still errors when the WHOLE catalog is empty.
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, Repo: name, File: "repos.yaml", Line: 1,
			Check: "no-entities",
			Message: fmt.Sprintf("no %s found in %s under any configured path (%s)",
				discover.Filename, repoLabel(name), strings.Join(patterns, ", ")),
			Hint: "check this repository's `paths:` in repos.yaml",
		})
		return nil
	}
	files := discover.Load(fsys, found, c)
	for _, f := range files {
		// name, not "": schema.Validator.Validate takes a repo and the
		// single-repository caller had nothing to give it. A schema error in
		// the third repository must say which repository.
		v.Validate(name, f.Path, f.Data, c)
	}
	return catalog.ParseAll(name, files, c)
}

// assemble runs stages 4 and 5 over every repository's entities at once, and
// loads the configuration that lives in the repository the command is
// standing in (ruling R34).
func assemble(entities []*catalog.Entity, src catalog.Sources, scope catalog.Scope, cfg fs.FS, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	if len(entities) == 0 {
		// len(src) > 1 mirrors parseRepo's solo flag exactly, from the same
		// source of truth every caller already has (SingleSource for a
		// single repository; workspace.Sources() for the fetched-workspace
		// path). Below that count, a solo parseRepo call has already
		// reported the one rich error a single-repository run produces —
		// adding a second, thinner "no entities" diagnostic here would be
		// the duplicate-with-lost-detail regression a single-repository run
		// must never show. Above it, no per-repository warning can say
		// whether the WHOLE catalog is empty, which is what this reports.
		if len(src) > 1 {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "repos.yaml", Line: 1,
				Check:   "no-entities",
				Message: fmt.Sprintf("no %s found in any configured repository", discover.Filename),
				Hint:    "add a repos.yaml listing the paths your services live under",
			})
		}
		return nil, nil, nil
	}
	cat := catalog.NewCatalog(entities, c)
	catalog.CheckFiles(src, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)

	teamsData, err := fs.ReadFile(cfg, "teams.yaml")
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

// repoLabel names a repository in a message, or says "this repository" when
// there is no name — which is the ordinary case for a checkout with no
// repos.yaml, where "no service.yaml found in  under any path" would read
// as a bug.
func repoLabel(name string) string {
	if name == "" {
		return "this repository"
	}
	return name
}

// defaultValidator compiles the embedded schema once per command.
func defaultValidator(c *diag.Collector) *schema.Validator {
	v, err := schema.Default()
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "schema", Line: 1,
			Check:   "schema-compile",
			Message: fmt.Sprintf("cannot compile the embedded schema: %v", err),
		})
		return nil
	}
	return v
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
