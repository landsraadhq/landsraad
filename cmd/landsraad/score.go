package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
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
	reportDiagnostics(errOut, c.Diagnostics(), false)
	if !std.Loaded() {
		fmt.Fprintf(errOut, "standards.yaml did not parse; scoring against the published defaults\n")
		return config.DefaultStandards()
	}
	return std
}

// historyRow returns the history file this run appends, and whether the file
// already on disk could be read.
//
// Discarding this error was destructive, not merely lossy: AppendHistory
// given no existing bytes produces a fresh file with one row, and the caller
// writes it straight over the real one. A history file that exists and cannot
// be read is therefore the case in which every recorded run is silently
// replaced by today's. Absent is the only readable-as-empty answer.
func historyRow(fsys fs.FS, errOut io.Writer, sc *scorecard.Scorecard, now time.Time) ([]emit.File, bool) {
	existing, err := fs.ReadFile(fsys, scorecard.HistoryPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(errOut, "error: cannot read %s: %v\n", scorecard.HistoryPath, err)
		fmt.Fprintf(errOut, "  not appending this run: writing a fresh file would replace the history already recorded there\n")
		return nil, false
	}
	return []emit.File{scorecard.AppendHistory(existing, sc, now)}, true
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
	src := catalog.SingleSource(localRepoName(fsys), fsys)
	std := standardsFor(fsys, errOut)
	reported := scorecard.Ingest(src, cat, std.StaleAfterDays(), opts.Now, &c)
	env := scorecard.Env{
		Sources:        src,
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

// Score measures the catalog against the standard, returning the files to
// write and an exit code.
//
// It writes nothing, the way Build does not (build.go): the caller owns the
// one loop that touches the disk. Folding --history in here rather than
// running the pipeline a second time beside it is also what stops the two
// from disagreeing — and stops the whole scoring run happening twice, which
// printed every stderr line standardsFor emits twice with it.
//
// Exit 3 when a check at or above --fail-on does not pass. Exit 2 when the
// metadata itself is broken, because those are different problems for
// different people (spec §12).
func Score(fsys fs.FS, out, errOut io.Writer, opts ScoreOptions) ([]emit.File, int) {
	sc, std, c, ok := computeScore(fsys, errOut, opts)
	if !ok {
		if err := opts.Format.Write(out, c.Diagnostics()); err != nil {
			fmt.Fprintf(errOut, "error: cannot write diagnostics: %v\n", err)
			return nil, exitUsage
		}
		fmt.Fprintf(errOut, "refusing to score a catalog with errors; a score computed from broken metadata is a number nobody should act on\n")
		return nil, exitValidation
	}

	if opts.JSON {
		if err := writeScoreJSON(out, sc); err != nil {
			fmt.Fprintf(errOut, "error: cannot write scorecard: %v\n", err)
			return nil, exitUsage
		}
	} else {
		writeScoreText(errOut, sc)
	}

	// Diagnostics from ingest and exemptions go to stderr in text mode and are
	// part of the payload's siblings in JSON mode; either way they are never
	// interleaved with a JSON document on stdout.
	reportDiagnostics(errOut, c.Diagnostics(), false)

	gated := 0
	for _, e := range sc.Entities {
		gated += len(e.Fails(opts.FailOn, std))
	}
	if gated > 0 {
		fmt.Fprintf(errOut, "\n%s failing at or above %q\n",
			plural(gated, "check", "checks"), opts.FailOn)
	} else {
		fmt.Fprintf(errOut, "\nok: %s meet the standard\n",
			plural(len(sc.Entities), "entity", "entities"))
	}

	// Last, so the reason for the exit code is the last thing on stderr. The
	// "ok:" line above is about the scores and stays true — they were computed
	// and they are clean; it is the trend that was not recorded.
	files, historyOK := []emit.File(nil), true
	if opts.History {
		files, historyOK = historyRow(fsys, errOut, sc, opts.Now)
	}

	// A history file that could not be read preempts the gate, exactly as an
	// unwritable scorecard already does above. Exit 3 is a claim about the
	// services — "your service does not meet the standard" — and a CI job that
	// treats it as the expected gate result would swallow the fact that
	// --history did nothing at all. Exit 1 is spec §12's "landsraad could not
	// run", fixed by whoever ran it, which is precisely who fixes a file this
	// process cannot read. Exit 0 was the wrong answer either way: the run
	// printed "error:" and then reported itself clean.
	if !historyOK {
		return files, exitUsage
	}
	if gated > 0 {
		return files, exitScorecard
	}
	return files, exitOK
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
			// This list is what a reader scans for things to fix, so it holds
			// only results that are one of them. A pass is not, and neither
			// is a check the entity's kind excludes (ruling R53) — that one
			// stays visible on the portal, which is a surface with a
			// different job.
			if r.Status.Passed() || r.Status == scorecard.StatusNotApplicable {
				continue
			}
			// 14, because "not-applicable" is 14 characters and a status that
			// touches its neighbour is the column silently failing.
			fmt.Fprintf(w, "  %-16s %-14s %s\n", r.Check, r.Status, r.Detail)
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
			"because those are different problems for different people; exit 1 when " +
			"--history was asked for and the history already recorded could not be read " +
			"— that run is refused rather than allowed to replace it.",
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
			cmd.SilenceUsage = true
			files, code := Score(os.DirFS(resolved), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
			// Written before the exit, not after it: a run whose gate tripped
			// still belongs in the trend — a history that records only the
			// good days is not a trend.
			for _, f := range files {
				full := filepath.Join(resolved, filepath.FromSlash(f.Path))
				if err := os.WriteFile(full, f.Data, 0o644); err != nil {
					return err
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "  wrote %s\n", f.Path)
			}
			if code != exitOK {
				os.Exit(code)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&format, "format", "text", "text or json")
	cmd.Flags().StringVar(&failOn, "fail-on", "required", "gate on required, or additionally on warn")
	cmd.Flags().BoolVar(&history, "history", false,
		"append this run to "+scorecard.HistoryPath+" (exit 1 if that file exists and cannot be read)")
	return cmd
}
