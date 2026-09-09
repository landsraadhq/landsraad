package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/discover"
	"github.com/landsraadhq/landsraad/internal/schema"
)

// Validate is the hermetic pipeline: an explicit composition of the typed
// stages, in the only order the types permit. Compare spec §7 — this function
// is that table, and nothing more.
//
// It takes an fs.FS rather than a path, so the same pipeline runs against a
// local checkout, a fetched remote repo, or a test fixture in memory.
func Validate(fsys fs.FS, out, errOut io.Writer, f diag.Formatter) int {
	var c diag.Collector

	// 1. discover — which files are we looking at
	patterns := patternsFor(fsys, &c)
	paths, err := discover.Find(fsys, patterns)
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
	// Matching nothing at all is the single most likely way a first run goes
	// wrong: a team whose code lives under apps/* would otherwise get a green
	// check forever on a repo the tool never looked at.
	if len(paths) == 0 {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError,
			File:     "repos.yaml",
			Line:     1,
			Check:    "no-entities",
			Message: fmt.Sprintf("no %s found under any configured path (%s)",
				discover.Filename, strings.Join(patterns, ", ")),
			Hint: "add a repos.yaml listing the paths your services live under",
		})
	}
	files := discover.Load(fsys, paths, &c)

	// 2. schema — structural validation, the precise messages
	validator, err := schema.Default()
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
	for _, file := range files {
		validator.Validate("", file.Path, file.Data, &c)
	}

	// 3. parse and merge — pure, no IO
	cat := catalog.NewCatalog(catalog.ParseAll(localRepoName(fsys), files, &c), &c)

	// 4. resolve — LocalOnly: this repo cannot see entities defined elsewhere
	g := cat.Resolve(catalog.LocalOnly, &c)
	reportCycles(cat, g, &c)

	// 5. semantic checks
	checkOwners(fsys, cat, &c)
	catalog.CheckFiles(fsys, cat, &c)

	// 6. report
	// out carries ONLY the selected format's payload, so `--format json` stays
	// pipeable to jq and the GitLab report stays a valid artifact. Everything
	// human goes to errOut.
	if err := f.Write(out, c.Diagnostics()); err != nil {
		fmt.Fprintf(errOut, "error: cannot write output: %v\n", err)
		return exitUsage
	}
	if c.HasErrors() {
		return exitValidation
	}
	fmt.Fprintf(errOut, "ok: %s validated, no problems found\n", plural(len(cat.Entities()), "entity", "entities"))
	return exitOK
}

// localRepoName names the repo being validated, for provenance in diagnostics.
// It is empty when repos.yaml is absent, and Entity.Location() renders that
// case without a dangling prefix.
func localRepoName(fsys fs.FS) string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		return ""
	}
	var discard diag.Collector
	r := config.LoadRepos("repos.yaml", data, &discard)
	if len(r.Repos) == 0 {
		return ""
	}
	return path.Base(strings.TrimSuffix(r.Repos[0].URL, "/"))
}

// patternsFor reads repos.yaml if present, falling back to the conventional
// layout. A repo without repos.yaml still works out of the box.
func patternsFor(fsys fs.FS, c *diag.Collector) []string {
	data, err := fs.ReadFile(fsys, "repos.yaml")
	if err != nil {
		// Not an error — but not silent either. Spec §12: degraded mode must
		// be visible in the artifact, not only in a log. Compare checkOwners,
		// which errors loudly for a missing teams.yaml.
		c.Add(defaultPatternsNote("no repos.yaml found"))
		return config.DefaultPatterns()
	}
	r := config.LoadRepos("repos.yaml", data, c)
	patterns, defaulted := r.LocalPatterns()
	// A file that failed to parse has already produced a loud error, and its
	// fallback to defaults is a consequence of that error, not a separate
	// thing to report: two diagnostics for one cause is noise.
	if defaulted && r.Loaded() {
		// Present, parsed, and lists no paths. Same degraded mode as an absent
		// file, and it used to be the half of it nobody was told about.
		c.Add(defaultPatternsNote("repos.yaml lists no paths"))
	}
	return patterns
}

// defaultPatternsNote is the one place the conventional-layout fallback is
// announced, so the two ways of reaching it cannot drift apart.
func defaultPatternsNote(reason string) diag.Diagnostic {
	return diag.Diagnostic{
		Severity: diag.SevInfo,
		File:     "repos.yaml",
		Line:     1,
		Check:    "default-patterns",
		Message: fmt.Sprintf("%s; using default paths (%s)",
			reason, strings.Join(config.DefaultPatterns(), ", ")),
		Hint: "add repos.yaml if your services live elsewhere",
	}
}

func checkOwners(fsys fs.FS, cat *catalog.Catalog, c *diag.Collector) {
	data, err := fs.ReadFile(fsys, "teams.yaml")
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: "teams.yaml", Line: 1,
			Check:   "missing-teams",
			Message: "teams.yaml not found at the repository root",
			Hint:    "every entity's owner must resolve to a team defined there",
		})
		return
	}
	config.LoadTeams("teams.yaml", data, c).ValidateOwners(cat, c)
}

func reportCycles(cat *catalog.Catalog, g *catalog.Graph, c *diag.Collector) {
	for _, cyc := range g.Cycles() {
		d := diag.Diagnostic{
			Severity: diag.SevError, Line: 1,
			Check:   "dependency-cycle",
			Message: fmt.Sprintf("dependency cycle: %s", joinRefs(cyc)),
			Hint:    "break the loop, or model one direction as a shared library",
		}
		if e, ok := cat.Lookup(cyc[0]); ok {
			d.File, d.Line, d.Entity = e.SourcePath, e.NameLine, e.Metadata.Name
		}
		c.Add(d)
	}
}

func joinRefs(rs []catalog.Ref) string {
	if len(rs) == 0 {
		return ""
	}
	out := ""
	for _, r := range rs {
		out += r.String() + " -> "
	}
	return out + rs[0].String()
}

func newValidateCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:   "validate [root]",
		Short: "Truthsayer — validate this repository's catalog files",
		Long: "Truthsayer detects metadata that lies about reality.\n\n" +
			"It runs offline against a single repository: no network access, no " +
			"tokens, and no resolution of references to entities in other " +
			"repositories — the platform build does that.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root := "."
			if len(args) == 1 {
				root = args[0]
			}
			resolved, err := findRoot(root)
			if err != nil {
				return err
			}
			name := format
			if name == "auto" {
				name = "text"
				if os.Getenv("GITHUB_ACTIONS") == "true" {
					name = "github"
				} else if os.Getenv("GITLAB_CI") == "true" {
					name = "gitlab"
				}
			}
			f, ok := diag.Lookup(name)
			if !ok {
				return fmt.Errorf("unknown format %q, want one of %v", name, diag.FormatNames())
			}
			cmd.SilenceUsage = true
			// os.DirFS is the single place this program touches os for reading.
			if code := Validate(os.DirFS(resolved), cmd.OutOrStdout(), cmd.ErrOrStderr(), f); code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "auto",
		"output format: auto, "+strings.Join(diag.FormatNames(), ", "))
	return cmd
}

// plural renders a count with the right noun. "1 entities validated" is the
// most-read line the tool prints, in a project whose thesis is that message
// quality is the product.
func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
