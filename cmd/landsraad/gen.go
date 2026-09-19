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
	// R46: before the one give-up path below. A schema that will not compile
	// is a landsraad bug, and that is no reason to hide what is wrong with
	// teams.yaml.
	teams := loadTeamsFor(fsys, c)

	v := defaultValidator(c)
	if v == nil {
		return nil, nil, nil
	}
	repo := localRepoName(fsys)
	// solo: true. loadCatalogScoped is exclusively the single-repository
	// composition (validate, gen, score — ruling R30), so the "no entities"
	// diagnostic below must be the one error main always produced, not the
	// per-repository warning that exists for the multi-repository case.
	//
	// patternsKnown is threaded into parseRepo rather than being a second
	// give-up path here. Returning on it — which is what the first R46 fix
	// did — suppresses `no-entities` by abandoning the load, and takes every
	// other diagnostic that needs a catalog with it: the schema errors on the
	// files that WERE found, and the generators' ownership errors. validate
	// reports those on the identical filesystem, because its own guard
	// (validate.go's `patternsKnown` condition on one `if`) suppresses only
	// its `no-entities` diagnostic. gen and score therefore disagreed with
	// validate about the same directory, which is what ruling R43 exists to
	// prevent.
	patterns, patternsKnown := patternsFor(fsys, c)
	p := parseRepo(repo, fsys, patterns, true, patternsKnown, v, c)
	return assemble(p, catalog.SingleSource(repo, fsys), scope, teams, c)
}

// parseResult is stage 3's output: the entities, and how many catalog files
// were found to parse them from. The count travels with the entities because
// assemble's "no service.yaml found" is a claim about files, and only the
// thing that looked for them knows (ruling R42). It used to be inferred from
// the entities, which reported files that were found and failed to parse as
// files that were never there.
type parseResult struct {
	entities []*catalog.Entity
	found    int
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
//
// patternsKnown is patternsFor's second return: false in exactly one case,
// a repos.yaml that is present and did not parse, where patterns is a guess
// standing in for a file nobody could read. What that guess did or did not
// find is not a fact about the user's layout, so the zero-found diagnostic
// below is skipped — and only that diagnostic. Everything else this function
// reports is about files it actually found and read, and repos-parse is no
// reason to withhold those (ruling R46).
//
// The multi-repository callers in repos.go pass true unconditionally, and
// the spec's R47 is why: a repos.yaml that did not parse now yields no
// repositories at all, so ParseAll has nothing to iterate and openRepos'
// phase-1 loop never runs. R47's "no flag need be threaded anywhere" was a
// claim about that path, and it still holds there; this parameter exists for
// loadCatalogScoped, which reads the same unparseable file and then keeps
// going against the fallback globs.
func parseRepo(name string, fsys fs.FS, patterns []string, solo, patternsKnown bool, v *schema.Validator, c *diag.Collector) parseResult {
	found, err := discover.Find(fsys, patterns)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "repos.yaml", Line: 1,
			Check:   "discover",
			Message: fmt.Sprintf("cannot search for %s files in %s: %v", discover.Filename, repoLabel(name), err),
		})
		return parseResult{}
	}
	if len(found) == 0 {
		if !patternsKnown {
			// repos.yaml is present but did not parse, so patterns is a
			// guess: either diagnostic below would be a second diagnostic
			// for the one cause repos-parse already carries, hinting at a
			// file that is sitting right there. Nothing was found, so there
			// is nothing else to report from here either.
			return parseResult{}
		}
		if solo {
			// The single-repository message: one repository, one
			// diagnostic, and the diagnostic that gates the build carries
			// the paths that were actually searched. Matches main's
			// original wording exactly — this is a refactor, not a
			// behaviour change, for validate/gen/score.
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "repos.yaml", Line: 1,
				Check: "no-entities",
				Message: fmt.Sprintf("no %s found under any configured path (%s)",
					discover.Filename, strings.Join(patterns, ", ")),
				Hint: "add a repos.yaml listing the paths your services live under",
			})
			return parseResult{}
		}
		// A warning per repository, where a single-repository run gets one
		// error instead (above). With several repositories, "no service.yaml
		// anywhere" and "the third repository's paths are wrong" are
		// different problems, and the second is the apps/-instead-of-services/
		// bug the strict repos.yaml decoding already exists to catch.
		// assemble still errors when the WHOLE catalog is empty.
		c.Add(diag.Diagnostic{
			Severity: diag.SevWarn, File: "repos.yaml", Line: 1,
			Check: "no-entities",
			Message: fmt.Sprintf("no %s found in %s under any configured path (%s)",
				discover.Filename, repoLabel(name), strings.Join(patterns, ", ")),
			Hint: "check this repository's `paths:` in repos.yaml",
		})
		return parseResult{}
	}
	files := discover.Load(fsys, found, c)
	for _, f := range files {
		// name, not "": schema.Validator.Validate takes a repo and the
		// single-repository caller had nothing to give it. A schema error in
		// the third repository must say which repository.
		v.Validate(name, f.Path, f.Data, c)
	}
	return parseResult{entities: catalog.ParseAll(name, files, c), found: len(found)}
}

// loadTeamsFor reads teams.yaml and reports its own mistakes.
//
// Called before every give-up path in the load composition, because
// teams.yaml is a different file from repos.yaml and from the embedded
// schema, and a failure to read either of those is no reason to withhold a
// fact about this one. R42 established that for the empty-catalog return;
// ruling R46 makes it hold for all of them.
//
// assemble takes the result rather than the filesystem, so it can no longer
// read teams.yaml itself and therefore cannot run against an unread one.
// That is weaker than ordering-as-a-compile-error, and the difference
// matters: *config.Teams's zero value is valid and load-bearing — assemble
// reads a nil teams as "already reported" and returns all-nil — so
// assemble(p, src, scope, nil, c) compiles and skips this function entirely
// (TestAssembleWithNilTeamsReturnsAllNil pins that behaviour). The type stops
// a caller passing a filesystem; it does not stop a caller passing a literal
// nil. Which is why no give-up path in a composition may return above the
// call to this function, and why that is held by behaviour tests rather than
// by the compiler.
func loadTeamsFor(cfg fs.FS, c *diag.Collector) *config.Teams {
	data, err := fs.ReadFile(cfg, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			// validate's id and message for the same absent file (ruling R43).
			// The hint is this command's own: gen, score and build run only in
			// the platform repository, where --satellite is not an answer.
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root, so no owner can be resolved",
			Hint:    "run `landsraad init` to create one",
		})
		return nil
	}
	return config.LoadTeams("teams.yaml", data, c)
}

// assemble runs stages 4 and 5 over every repository's entities at once,
// against the configuration the command is standing in (ruling R34). teams is
// loadTeamsFor's result, taken as a value rather than read here, so no caller
// can reach stage 4 without teams.yaml's own diagnostics already collected
// (ruling R46).
func assemble(p parseResult, src catalog.Sources, scope catalog.Scope, teams *config.Teams, c *diag.Collector) (*catalog.Catalog, *catalog.Graph, *config.Teams) {
	if len(p.entities) == 0 {
		// len(src) > 1 mirrors parseRepo's solo flag exactly, from the same
		// source of truth every caller already has (SingleSource for a
		// single repository; workspace.Sources() for the fetched-workspace
		// path). Below that count, a solo parseRepo call has already
		// reported the one rich error a single-repository run produces —
		// adding a second, thinner "no entities" diagnostic here would be
		// the duplicate-with-lost-detail regression a single-repository run
		// must never show. Above it, no per-repository warning can say
		// whether the WHOLE catalog is empty, which is what this reports.
		//
		// p.found == 0 because the message is a claim about files. When
		// every file that was found failed to parse, the parse errors have
		// already said why the catalog is empty (ruling R42).
		if len(src) > 1 && p.found == 0 {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: "repos.yaml", Line: 1,
				Check:   "no-entities",
				Message: fmt.Sprintf("no %s found in any configured repository", discover.Filename),
				Hint:    "add a repos.yaml listing the paths your services live under",
			})
		}
		return nil, nil, nil
	}
	cat := catalog.NewCatalog(p.entities, c)
	catalog.CheckFiles(src, cat, c)
	g := cat.Resolve(scope, c)
	reportCycles(cat, g, c)
	if teams == nil {
		return nil, nil, nil
	}
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
