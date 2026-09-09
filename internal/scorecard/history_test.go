package scorecard

import (
	"strings"
	"testing"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

func scorecardOf(rows ...EntityScore) *Scorecard { return &Scorecard{Entities: rows} }

func row(name string, tier, passed, applicable int) EntityScore {
	return EntityScore{
		Ref:        catalog.Ref{Kind: catalog.KindService, Name: name},
		Tier:       tier,
		Owner:      "team-payments",
		Passed:     passed,
		Applicable: applicable,
	}
}

func TestAppendHistoryWritesAHeaderAndRows(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("api", 1, 3, 4)), now)

	if f.Path != HistoryPath {
		t.Errorf("Path = %q, want %q", f.Path, HistoryPath)
	}
	want := "date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-09,service:api,1,team-payments,3,4,0.750\n"
	if string(f.Data) != want {
		t.Errorf("history\n got:\n%s\nwant:\n%s", f.Data, want)
	}
}

func TestAppendHistoryKeepsEarlierRows(t *testing.T) {
	existing := []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-02,service:api,1,team-payments,2,4,0.500\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	got := string(f.Data)
	if !strings.Contains(got, "2026-09-02,service:api,1,team-payments,2,4,0.500") {
		t.Errorf("the earlier row must survive:\n%s", got)
	}
	if !strings.Contains(got, "2026-09-09,service:api,1,team-payments,3,4,0.750") {
		t.Errorf("the new row must be appended:\n%s", got)
	}
}

// CI runs more than once a day. Re-running on the same date must replace that
// date's rows, not double them — a trend line with two points per day for
// every re-run is a trend line nobody trusts.
func TestAppendHistoryReplacesRowsForTheSameDate(t *testing.T) {
	existing := []byte("date,ref,tier,owner,passed,applicable,score\n" +
		"2026-09-09,service:api,1,team-payments,1,4,0.250\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	got := string(f.Data)
	if strings.Count(got, "2026-09-09,service:api") != 1 {
		t.Errorf("expected exactly one row for today:\n%s", got)
	}
	if !strings.Contains(got, "2026-09-09,service:api,1,team-payments,3,4,0.750") {
		t.Errorf("today's row must be the new one:\n%s", got)
	}
}

// Rows are sorted by date then ref, so a diff of this file between runs shows
// only the rows that actually changed.
func TestAppendHistoryIsSorted(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("z", 1, 1, 1), row("a", 2, 1, 1)), now)

	lines := strings.Split(strings.TrimSpace(string(f.Data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected header plus two rows, got %d lines", len(lines))
	}
	if !strings.HasPrefix(lines[1], "2026-09-09,service:a,") {
		t.Errorf("rows must be sorted by ref, got %q", lines[1])
	}
}

// Score is written to three decimals so a one-check improvement in a
// twelve-check service is visible in the trend rather than rounded away.
func TestScoreIsWrittenToThreeDecimals(t *testing.T) {
	f := AppendHistory(nil, scorecardOf(row("api", 1, 1, 3)), now)
	if !strings.Contains(string(f.Data), ",1,3,0.333\n") {
		t.Errorf("want three decimal places:\n%s", f.Data)
	}
}

// An existing file with a different header is not silently reformatted: the
// columns are a contract with whatever reads the trend, and rewriting them
// would discard data the tool did not write.
func TestAppendHistoryPreservesAnUnknownHeader(t *testing.T) {
	existing := []byte("date,ref,score\n2026-09-02,service:api,0.500\n")

	f := AppendHistory(existing, scorecardOf(row("api", 1, 3, 4)), now)

	if !strings.Contains(string(f.Data), "2026-09-02,service:api,0.500") {
		t.Errorf("unrecognised earlier rows must survive verbatim:\n%s", f.Data)
	}
}

func TestAppendHistoryUsesTheInjectedDate(t *testing.T) {
	other := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	f := AppendHistory(nil, scorecardOf(row("api", 1, 1, 1)), other)
	if !strings.Contains(string(f.Data), "2027-03-01,") {
		t.Errorf("the injected date must be used, got:\n%s", f.Data)
	}
}
