package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/config"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// ScoreOptions is everything the score command needs that is not the
// filesystem. Now and LastEdit are values so the whole command is testable
// without a clock or a git repository.
type ScoreOptions struct {
	Format   diag.Formatter
	FailOn   config.Severity
	Now      time.Time
	LastEdit scorecard.LastEditFunc
	JSON     bool
	History  bool
}

// standardsFor loads standards.yaml, announcing the fallback to spec §6's
// published defaults the way patternsFor announces a missing repos.yaml.
// Degraded mode is visible in the artifact, not only in a log.
func standardsFor(fsys fs.FS, errOut io.Writer) *config.Standards {
	data, err := fs.ReadFile(fsys, "standards.yaml")
	if err != nil {
		fmt.Fprintf(errOut, "no standards.yaml found; scoring against the published defaults\n")
		return config.DefaultStandards()
	}
	var c diag.Collector
	std := config.LoadStandards("standards.yaml", data, &c)
	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}
	if !std.Loaded() {
		fmt.Fprintf(errOut, "standards.yaml did not parse; scoring against the published defaults\n")
		return config.DefaultStandards()
	}
	return std
}

// scoreHistoryFiles runs the pipeline and returns the history file it would
// write. Extracted so the command and its test take the same path.
func scoreHistoryFiles(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) []emit.File {
	sc, _, _, ok := computeScore(fsys, errOut, opts)
	if !ok || !opts.History {
		return nil
	}
	existing, _ := fs.ReadFile(fsys, scorecard.HistoryPath)
	return []emit.File{scorecard.AppendHistory(existing, sc, opts.Now)}
}

// computeScore runs stages 1, 3, 4, 5, 6 and 7. It returns ok=false when the
// catalog has errors: a score computed from a catalog with a dangling ref or a
// duplicate name is a number nobody should act on.
func computeScore(fsys fs.FS, errOut io.Writer, opts ScoreOptions) (*scorecard.Scorecard, *config.Standards, *diag.Collector, bool) {
	var c diag.Collector
	cat, _ := loadCatalog(fsys, &c)
	if cat == nil || c.HasErrors() {
		return nil, nil, &c, false
	}
	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(fsys, cat, std.StaleAfterDays(), opts.Now, &c)
	env := scorecard.Env{
		FS:             fsys,
		Now:            opts.Now,
		MaxDocsAgeDays: std.Param("docs-fresh", "maxAgeDays", 180),
		LastEdit:       opts.LastEdit,
	}
	sc := scorecard.Score(cat, std, reported, env, &c)
	if c.HasErrors() {
		return nil, nil, &c, false
	}
	return sc, std, &c, true
}

// Score measures the catalog against the standard.
//
// Exit 3 when a check at or above --fail-on does not pass. Exit 2 when the
// metadata itself is broken, because those are different problems for
// different people (spec §12).
func Score(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) int {
	sc, std, c, ok := computeScore(fsys, errOut, opts)
	if !ok {
		if err := opts.Format.Write(out, c.Diagnostics()); err != nil {
			fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
			return exitUsage
		}
		fmt.Fprintf(errOut, "refusing to score a catalog with errors; a score computed from broken metadata is a number nobody should act on\n")
		return exitValidation
	}

	if opts.JSON {
		if err := writeScoreJSON(out, sc); err != nil {
			fmt.Fprintf(errOut, "error: cannot write scorecard: %v\n", err)
			return exitUsage
		}
	} else {
		writeScoreText(errOut, sc)
	}

	// Diagnostics from ingest and exemptions go to stderr in text mode and are
	// part of the payload's siblings in JSON mode; either way they are never
	// interleaved with a JSON document on stdout.
	for _, d := range c.Diagnostics() {
		fmt.Fprintf(errOut, "%s: %s\n", d.Severity, d.Message)
	}

	gated := 0
	for _, e := range sc.Entities {
		gated += len(e.Fails(opts.FailOn, std))
	}
	if gated > 0 {
		fmt.Fprintf(errOut, "\n%s failing at or above %q\n",
			plural(gated, "check", "checks"), opts.FailOn)
		return exitScorecard
	}
	fmt.Fprintf(errOut, "\nok: %s meet the standard\n",
		plural(len(sc.Entities), "entity", "entities"))
	return exitOK
}

func writeScoreJSON(w io.Writer, sc *scorecard.Scorecard) error {
	type result struct {
		Check  string `json:"check"`
		Status string `json:"status"`
		Detail string `json:"detail,omitempty"`
		URL    string `json:"url,omitempty"`
	}
	type entity struct {
		Ref        string   `json:"ref"`
		Tier       int      `json:"tier"`
		Owner      string   `json:"owner"`
		Score      float64  `json:"score"`
		Passed     int      `json:"passed"`
		Applicable int      `json:"applicable"`
		Results    []result `json:"results"`
	}
	type team struct {
		Team       string  `json:"team"`
		Score      float64 `json:"score"`
		Passed     int     `json:"passed"`
		Applicable int     `json:"applicable"`
	}
	payload := struct {
		Entities []entity `json:"entities"`
		Teams    []team   `json:"teams"`
		Score    float64  `json:"score"`
	}{Score: sc.Score()}

	for _, e := range sc.Entities {
		ent := entity{
			Ref: e.Ref.String(), Tier: e.Tier, Owner: e.Owner,
			Score: e.Score(), Passed: e.Passed, Applicable: e.Applicable,
		}
		for _, r := range e.Results {
			ent.Results = append(ent.Results, result{
				Check: r.Check, Status: string(r.Status), Detail: r.Detail, URL: r.URL,
			})
		}
		payload.Entities = append(payload.Entities, ent)
	}
	for _, t := range sc.Teams() {
		payload.Teams = append(payload.Teams, team{
			Team: t.Team, Score: t.Score(), Passed: t.Passed, Applicable: t.Applicable,
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// writeScoreText renders to stderr: the scorecard's human form is a summary,
// and stdout is reserved for the selected format's payload (spec §12).
func writeScoreText(w io.Writer, sc *scorecard.Scorecard) {
	for _, e := range sc.Entities {
		fmt.Fprintf(w, "\n%s  tier %d  %s  %.0f%% (%d/%d)\n",
			e.Ref, e.Tier, e.Owner, e.Score()*100, e.Passed, e.Applicable)
		for _, r := range e.Results {
			if r.Status.Passed() {
				continue
			}
			fmt.Fprintf(w, "  %-16s %-13s %s\n", r.Check, r.Status, r.Detail)
		}
	}
	fmt.Fprintf(w, "\nby team:\n")
	for _, t := range sc.Teams() {
		fmt.Fprintf(w, "  %-20s %.0f%% (%d/%d)\n", t.Team, t.Score()*100, t.Passed, t.Applicable)
	}
}

func newScoreCmd() *cobra.Command {
	var (
		format  string
		failOn  string
		history bool
	)
	cmd := &cobra.Command{
		Use:   "score [root]",
		Short: "Landsraad Council — measure services against the team standard",
		Long: "Score every entity against standards.yaml. Exit 3 when a check at or " +
			"above --fail-on does not pass; exit 2 when the metadata itself is broken, " +
			"because those are different problems for different people.",
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
			gate := config.Severity(failOn)
			if gate != config.SevRequired && gate != config.SevWarn {
				return fmt.Errorf("--fail-on must be required or warn, got %q", failOn)
			}
			var scoreFormatter diag.Formatter
			switch format {
			case "text":
				scoreFormatter = diagText()
			case "json":
				scoreFormatter = diag.JSON{}
			default:
				return fmt.Errorf("--format must be text or json, got %q", format)
			}
			opts := ScoreOptions{
				Format:   scoreFormatter,
				FailOn:   gate,
				Now:      time.Now().UTC(),
				LastEdit: gitLastEdit(resolved),
				JSON:     format == "json",
				History:  history,
			}
			fsys := os.DirFS(resolved)

			if history {
				for _, f := range scoreHistoryFiles(fsys, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts) {
					full := filepath.Join(resolved, filepath.FromSlash(f.Path))
					if err := os.WriteFile(full, f.Data, 0o644); err != nil {
						return err
					}
					fmt.Fprintf(cmd.ErrOrStderr(), "  wrote %s\n", f.Path)
				}
			}
			cmd.SilenceUsage = true
			if code := Score(fsys, cmd.OutOrStdout(), cmd.ErrOrStderr(), opts); code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or json")
	cmd.Flags().StringVar(&failOn, "fail-on", "required", "gate on required, or additionally on warn")
	cmd.Flags().BoolVar(&history, "history", false, "append this run to "+scorecard.HistoryPath)
	return cmd
}
