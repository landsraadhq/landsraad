package scorecard

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/landsraadhq/landsraad/internal/emit"
)

// HistoryPath is the trend file spec §9 describes, appended by the CI job.
const HistoryPath = "scorecard-history.csv"

// historyHeader is the column contract. Spec D12 names this file as the one
// artifact with no way to notice it broke, which is why the grain is explicit
// and why an unrecognised earlier row is preserved rather than reformatted.
const historyHeader = "date,ref,tier,owner,passed,applicable,score"

// AppendHistory returns the whole history file with this run's rows in place.
//
// It takes the existing bytes rather than a file handle so it stays a pure
// function: cmd/ reads, this decides, cmd/ writes. Ruling R9 fixes the grain
// at one row per (date, ref) per run — a superset of spec §9's "weekly", which
// can be down-sampled later, where a weekly grain would lose data from a
// project that runs CI daily.
//
// A run on a date that already has rows replaces them. CI runs more than once
// a day, and a trend with several points per day per re-run is a trend nobody
// trusts.
func AppendHistory(existing []byte, sc *Scorecard, now time.Time) emit.File {
	date := now.UTC().Format("2006-01-02")

	var kept []string
	for _, line := range strings.Split(string(existing), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line == historyHeader {
			continue
		}
		// Rows for today are replaced by this run's. Everything else survives
		// verbatim, including rows written by a different version with
		// different columns: they are somebody's trend data and this tool did
		// not write them.
		if strings.HasPrefix(line, date+",") {
			continue
		}
		kept = append(kept, line)
	}

	for _, e := range sc.Entities {
		kept = append(kept, fmt.Sprintf("%s,%s,%d,%s,%d,%d,%.3f",
			date, e.Ref, e.Tier, e.Owner, e.Passed, e.Applicable, e.Score()))
	}

	// Sorted by the whole line, which orders by date then ref, so a diff
	// between runs shows only the rows that actually changed.
	sort.Strings(kept)

	var b strings.Builder
	b.WriteString(historyHeader)
	b.WriteString("\n")
	for _, line := range kept {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return emit.File{Path: HistoryPath, Data: []byte(b.String())}
}
