package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/landsraadhq/landsraad/internal/diag"
)

const historyCSV = "date,ref,tier,owner,passed,applicable,score\n" +
	"2026-09-01,service:api,1,team-payments,3,4,0.750\n" +
	"2026-09-01,service:web,1,team-payments,1,4,0.250\n" +
	"2026-09-08,service:api,1,team-payments,4,4,1.000\n" +
	"2026-09-08,service:web,1,team-payments,2,4,0.500\n"

// One point per date, aggregating every entity — the same arithmetic the
// scorecard's own total uses: passed over applicable, not a mean of means.
func TestParseHistoryAggregatesByDate(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte(historyCSV), &c)
	if len(points) != 2 {
		t.Fatalf("got %d points, want 2: %+v", len(points), points)
	}
	if points[0].Date != "2026-09-01" || points[1].Date != "2026-09-08" {
		t.Errorf("points are not in date order: %+v", points)
	}
	if points[0].Passed != 4 || points[0].Applicable != 8 {
		t.Errorf("point 0 = %+v, want 4/8", points[0])
	}
	if points[1].Score != 0.75 {
		t.Errorf("point 1 score = %v, want 0.75", points[1].Score)
	}
}

// A portal that refuses to render because one CSV line is malformed is
// worse than a portal with one missing point.
func TestParseHistorySkipsAMalformedRow(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte(historyCSV+"2026-09-15,broken\n"), &c)
	if len(points) != 2 {
		t.Errorf("got %d points, want 2 — the malformed row must be skipped: %+v", len(points), points)
	}
	ds := c.Diagnostics()
	if len(ds) != 1 {
		t.Fatalf("got %d diagnostics, want 1: %+v", len(ds), ds)
	}
	if ds[0].Severity != diag.SevWarn {
		t.Errorf("Severity = %v, want SevWarn: a bad row is not a build failure", ds[0].Severity)
	}
	want := "scorecard-history.csv line 6 has 2 columns, want 7; skipping it"
	if ds[0].Message != want {
		t.Errorf("Message = %q, want %q", ds[0].Message, want)
	}
	if ds[0].Line != 6 {
		t.Errorf("Line = %d, want 6", ds[0].Line)
	}
}

func TestParseHistoryOnAHeaderOnlyFile(t *testing.T) {
	var c diag.Collector
	points := parseHistory([]byte("date,ref,tier,owner,passed,applicable,score\n"), &c)
	if len(points) != 0 {
		t.Errorf("got %d points, want none", len(points))
	}
	if ds := c.Diagnostics(); len(ds) != 0 {
		t.Errorf("a file with only a header is normal, not a problem: %+v", ds)
	}
}

func TestTrendBuildsAPolyline(t *testing.T) {
	var c diag.Collector
	tr := trend(parseHistory([]byte(historyCSV), &c))
	if tr.Polyline == "" {
		t.Fatal("no polyline")
	}
	coords := strings.Fields(tr.Polyline)
	if len(coords) != 2 {
		t.Fatalf("got %d coordinates, want 2: %q", len(coords), tr.Polyline)
	}
	// Higher score, higher on the chart: SVG y grows downwards, so the
	// later, better point must have the SMALLER y.
	var y0, y1 float64
	if _, err := fmt.Sscanf(coords[0], "%f,%f", new(float64), &y0); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Sscanf(coords[1], "%f,%f", new(float64), &y1); err != nil {
		t.Fatal(err)
	}
	if y1 >= y0 {
		t.Errorf("an improving score must rise: y went %v -> %v", y0, y1)
	}
}

// A single run is not a trend, and two points from one date is not either.
func TestTrendNeedsTwoPoints(t *testing.T) {
	var c diag.Collector
	one := parseHistory([]byte("date,ref,tier,owner,passed,applicable,score\n2026-09-01,service:api,1,t,3,4,0.750\n"), &c)
	if tr := trend(one); tr.Polyline != "" {
		t.Errorf("one point is not a trend, got %q", tr.Polyline)
	}
}
