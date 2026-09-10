package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

// historyColumns is the grain Plan 2's ruling R9 fixed:
// date,ref,tier,owner,passed,applicable,score.
const historyColumns = 7

// Sparkline geometry. Small, fixed, and in one place so the template and
// the coordinates cannot disagree.
const (
	trendWidth  = 240
	trendHeight = 48
)

// TrendPoint is one date's whole-catalog score.
type TrendPoint struct {
	Date       string
	Passed     int
	Applicable int
	Score      float64
}

// Trend is the sparkline.
//
// Polyline is a plain coordinate string and the template writes the <svg>
// around it. That is deliberate: it keeps the only template.HTML cast in
// this codebase confined to rendered Markdown, and it needs no charting
// library for a five-point line.
type Trend struct {
	Points        []TrendPoint
	Polyline      string
	Width, Height int
}

// parseHistory reads scorecard-history.csv into one point per date.
//
// This file is written by CI jobs in other people's repositories, and
// AppendHistory deliberately preserves rows it does not understand. So this
// reader is equally tolerant: a malformed row is a warning and a skipped
// point, never a refusal to render the portal.
func parseHistory(data []byte, c *diag.Collector) []TrendPoint {
	type totals struct{ passed, applicable int }
	byDate := map[string]*totals{}

	for i, line := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "date,") {
			continue
		}
		cols := strings.Split(line, ",")
		if len(cols) != historyColumns {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				File:     scorecard.HistoryPath, Line: lineNo,
				Check: "history",
				Message: fmt.Sprintf("%s line %d has %d columns, want %d; skipping it",
					scorecard.HistoryPath, lineNo, len(cols), historyColumns),
				Hint: "the columns are date,ref,tier,owner,passed,applicable,score",
			})
			continue
		}
		passed, errP := strconv.Atoi(strings.TrimSpace(cols[4]))
		applicable, errA := strconv.Atoi(strings.TrimSpace(cols[5]))
		if errP != nil || errA != nil {
			c.Add(diag.Diagnostic{
				Severity: diag.SevWarn,
				File:     scorecard.HistoryPath, Line: lineNo,
				Check: "history",
				Message: fmt.Sprintf("%s line %d has a non-numeric passed or applicable count; skipping it",
					scorecard.HistoryPath, lineNo),
			})
			continue
		}
		date := strings.TrimSpace(cols[0])
		if byDate[date] == nil {
			byDate[date] = &totals{}
		}
		byDate[date].passed += passed
		byDate[date].applicable += applicable
	}

	out := make([]TrendPoint, 0, len(byDate))
	for date, t := range byDate {
		p := TrendPoint{Date: date, Passed: t.passed, Applicable: t.applicable}
		if t.applicable > 0 {
			// passed over applicable across the whole catalog — the same
			// arithmetic Scorecard.Score uses, not a mean of per-entity
			// means, which would weight a one-check library like a
			// twelve-check service.
			p.Score = float64(t.passed) / float64(t.applicable)
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// trend lays the points out as SVG coordinates.
//
// The y axis is always 0–100%, never auto-scaled to the data. An
// auto-scaled axis turns a wobble between 94% and 95% into a dramatic
// mountain range, which is how a chart lies without stating a falsehood.
func trend(points []TrendPoint) Trend {
	out := Trend{Points: points, Width: trendWidth, Height: trendHeight}
	if len(points) < 2 {
		// One point is not a trend. Rendering a single dot as a line chart
		// implies a history that does not exist.
		return out
	}
	step := float64(trendWidth) / float64(len(points)-1)
	coords := make([]string, 0, len(points))
	for i, p := range points {
		x := float64(i) * step
		// SVG y grows downwards, so a higher score is a smaller y.
		y := float64(trendHeight) * (1 - p.Score)
		coords = append(coords, fmt.Sprintf("%.1f,%.1f", x, y))
	}
	out.Polyline = strings.Join(coords, " ")
	return out
}
