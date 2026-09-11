package main

import (
	"errors"
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
	"github.com/landsraadhq/landsraad/internal/scorecard"
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

	// repo names this repository in every diagnostic below: schema
	// violations, check-results violations, and — a few lines down —
	// parsed-entity provenance. Computed once, here, so the schema-validate
	// loop and validateCheckResults do not hardcode "" the way the parse
	// step below always named repo correctly.
	repo := localRepoName(fsys)

	// 2. schema — structural validation, the precise messages
	validator, err := schema.Default()
	if err != nil {
		fmt.Fprintf(errOut, "error: %v\n", err)
		return exitUsage
	}
	for _, file := range files {
		validator.Validate(repo, file.Path, file.Data, &c)
	}
	validateCheckResults(fsys, repo, &c)

	// 3. parse and merge — pure, no IO
	cat := catalog.NewCatalog(catalog.ParseAll(repo, files, &c), &c)

	// 4. resolve — LocalOnly: this repo cannot see entities defined elsewhere
	g := cat.Resolve(catalog.LocalOnly, &c)
	reportCycles(cat, g, &c)

	// 5. semantic checks
	checkOwners(fsys, cat, &c)
	catalog.CheckFiles(catalog.SingleSource(repo, fsys), cat, &c)

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

// validateCheckResults structurally validates every .landsraad/checks/*.yaml.
//
// Spec §7.1 excluded this while the shape was unspecified, and said so: "until
// Plan 2 ships, a malformed check-results file is first caught by the platform
// build, not by the PR that introduced it." Plan 2 shipped the schema, so the
// PR catches it now.
//
// Structure only. Resolving entities, applying precedence and ageing results
// need the merged catalog and a clock, which would make validate neither
// hermetic nor offline — and being both is what lets it run in every service
// repo's PR CI with no tokens and no network.
//
// repo is this repository's name, for provenance on any schema diagnostic —
// the same reason the caller now threads it through the service.yaml
// validation loop above, rather than hardcoding "".
func validateCheckResults(fsys fs.FS, repo string, c *diag.Collector) {
	entries, err := fs.ReadDir(fsys, scorecard.ChecksDir)
	if errors.Is(err, fs.ErrNotExist) {
		// No directory is not a problem: most repositories report no external
		// results. Only this answer means that; a directory that exists and
		// cannot be listed is reported below, not validated as empty.
		return
	}
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: scorecard.ChecksDir, Line: 1,
			Check:   "checks-unreadable",
			Message: scorecard.Unreadable(scorecard.ChecksDir, err),
		})
		return
	}
	v, err := schema.New(scorecard.CheckResultsSchema)
	if err != nil {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: scorecard.ChecksDir, Line: 1,
			Check:   "checks-schema",
			Message: fmt.Sprintf("cannot compile the check-results schema: %v", err),
		})
		return
	}
	for _, e := range entries {
		if e.IsDir() || !scorecard.IsCheckResultsFile(e.Name()) {
			continue
		}
		path := scorecard.ChecksDir + "/" + e.Name()
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: 1,
				Check:   "checks-unreadable",
				Message: scorecard.Unreadable(path, err),
			})
			continue
		}
		v.Validate(repo, path, data, c)
	}
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
	patterns, why := r.LocalPatterns()
	// A file that failed to parse has already produced a loud error, and its
	// fallback to defaults is a consequence of that error, not a separate
	// thing to report: two diagnostics for one cause is noise.
	if r.Loaded() {
		switch why {
		case config.LocalDefaulted:
			// Present, parsed, and lists no paths. Same degraded mode as an
			// absent file, and it used to be the half of it nobody was told
			// about.
			c.Add(defaultPatternsNote("repos.yaml names no paths"))
		case config.LocalAssumedFirst:
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn, File: "repos.yaml", Line: 1,
				Check: "repos-local-assumed",
				Message: fmt.Sprintf(
					"no entry in repos.yaml is marked local: true, so the first (%s) is assumed to be this repository",
					r.Repos[0].Identity()),
				Hint: "add `local: true` to the entry for the repository you are standing in",
			})
		}
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
//
// The logic moved to diag.Plural once a third package needed it. This stays
// as a name, not a copy: eleven call sites in this package read better
// unqualified.
func plural(n int, one, many string) string { return diag.Plural(n, one, many) }
