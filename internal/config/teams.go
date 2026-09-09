// Package config loads the repository-level YAML files: teams.yaml and
// repos.yaml.
package config

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
)

// Team is one owning team. It is the source for CODEOWNERS and alert routing.
type Team struct {
	Name      string   `yaml:"name"`
	Members   []string `yaml:"members"`
	Slack     string   `yaml:"slack"`
	PagerDuty string   `yaml:"pagerduty"`
}

// Teams is the loaded teams.yaml.
type Teams struct {
	byName map[string]*Team
}

type teamsFile struct {
	Teams []*Team `yaml:"teams"`
}

// LoadTeams reads teams.yaml. It always returns a usable (possibly empty)
// Teams so callers need no nil checks; problems are reported as diagnostics.
func LoadTeams(path string, data []byte, c *diag.Collector) *Teams {
	t := &Teams{byName: map[string]*Team{}}

	// KnownFields(true) rejects unknown keys. teams.yaml is the source for
	// alert routing, so a silently-ignored `pagerDuty:` typo would make
	// routing rot invisibly — the exact inverse of this product's thesis.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f teamsFile
	if err := dec.Decode(&f); err != nil && err != io.EOF {
		c.Add(diag.Diagnostic{
			Severity: diag.SevError, File: path, Line: lineFromYAMLError(err),
			Check:   "teams-parse",
			Message: fmt.Sprintf("cannot parse teams file: %v", err),
			Hint:    "teams.yaml is a list under `teams:` with name, members, slack and pagerduty",
		})
		return t
	}

	lines := teamLines(data, len(f.Teams))
	for i, team := range f.Teams {
		line := lines[i]
		if team.Name == "" {
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: line,
				Check: "teams-parse", Message: "a team entry has no name",
				Hint: "every team needs a name; it is what service.yaml owner fields refer to",
			})
			continue
		}
		if prev, dup := t.byName[team.Name]; dup {
			_ = prev
			c.Add(diag.Diagnostic{
				Severity: diag.SevError, File: path, Line: line,
				Check:   "teams-duplicate",
				Message: fmt.Sprintf("team %q is defined twice", team.Name),
				Hint:    "merge the two entries",
			})
			continue
		}
		t.byName[team.Name] = team
	}
	return t
}

// teamLines returns the 1-indexed line of each entry under `teams:`, so a
// duplicate or nameless team points at itself rather than at line 1. The
// yaml.Node technique is the same one catalog.ParseFile uses.
func teamLines(data []byte, n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = 1
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil || len(root.Content) == 0 {
		return out
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		if doc.Content[i].Value != "teams" {
			continue
		}
		seq := doc.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return out
		}
		for j := 0; j < len(seq.Content) && j < n; j++ {
			out[j] = seq.Content[j].Line
		}
	}
	return out
}

// lineFromYAMLError pulls a line out of a yaml.v3 error string.
func lineFromYAMLError(err error) int {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		if n, convErr := strconv.Atoi(m[1]); convErr == nil {
			return n
		}
	}
	return 1
}

var yamlLineRE = regexp.MustCompile(`line (\d+):`)

func (t *Teams) Get(name string) (*Team, bool) {
	team, ok := t.byName[name]
	return team, ok
}

// Names returns every team name, sorted.
func (t *Teams) Names() []string {
	out := make([]string, 0, len(t.byName))
	for n := range t.byName {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ValidateOwners checks that every entity's owner exists, suggesting the
// closest real team when the owner looks like a typo.
func (t *Teams) ValidateOwners(cat *catalog.Catalog, c *diag.Collector) {
	for _, e := range cat.Entities {
		if _, ok := t.Get(e.Metadata.Owner); ok {
			continue
		}
		d := diag.Diagnostic{
			Severity: diag.SevError,
			Repo:     e.SourceRepo,
			File:     e.SourcePath,
			Line:     e.NameLine,
			Entity:   e.Metadata.Name,
			Check:    "unknown-owner",
			Message:  fmt.Sprintf("owner %q is not defined in teams.yaml", e.Metadata.Owner),
		}
		if best, ok := closest(e.Metadata.Owner, t.Names()); ok {
			d.Hint = fmt.Sprintf("did you mean %q?", best)
		} else {
			d.Hint = fmt.Sprintf("known teams: %v", t.Names())
		}
		c.Add(d)
	}
}

// closest returns the nearest candidate within an edit distance of 3, which
// catches typos without inventing wild suggestions.
func closest(s string, candidates []string) (string, bool) {
	best, bestDist := "", 4
	for _, cand := range candidates {
		if d := levenshtein(s, cand); d < bestDist {
			best, bestDist = cand, d
		}
	}
	return best, best != ""
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}
