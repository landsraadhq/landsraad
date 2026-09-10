// Package diag collects validation problems so a single run can report all
// of them. Nothing in landsraad fails fast: every stage appends here and
// continues, so validating twelve broken services reports twelve problems.
package diag

import (
	"fmt"
	"sort"
)

// Severity ranks a diagnostic. Only SevError affects the exit code.
type Severity int

const (
	SevInfo Severity = iota
	SevWarn
	SevError
)

// MarshalJSON writes the severity name. Severity is an iota, so serialising
// the integer would mean that inserting a new severity silently changes what
// every existing consumer's filter matches.
func (s Severity) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s *Severity) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case `"info"`:
		*s = SevInfo
	case `"warn"`:
		*s = SevWarn
	case `"error"`:
		*s = SevError
	default:
		return fmt.Errorf("unknown severity %s", b)
	}
	return nil
}

func (s Severity) String() string {
	switch s {
	case SevInfo:
		return "info"
	case SevWarn:
		return "warn"
	case SevError:
		return "error"
	default:
		return "unknown"
	}
}

// Diagnostic is one problem found in one place. File and Line are required:
// a diagnostic the reader cannot navigate to is a bug.
type Diagnostic struct {
	Severity Severity `json:"severity"`
	Repo     string   `json:"repo,omitempty"`
	File     string   `json:"file"`
	Line     int      `json:"line"`
	Entity   string   `json:"entity,omitempty"`
	Check    string   `json:"check,omitempty"`
	Message  string   `json:"message"`
	// Hint is an optional suggested fix, e.g. "did you mean 'team-payments'?".
	Hint string `json:"hint,omitempty"`
}

// Collector accumulates diagnostics. The zero value is ready to use.
// It is not safe for concurrent use; the pipeline is single-goroutine.
type Collector struct {
	diags []Diagnostic
}

func (c *Collector) Add(d Diagnostic) { c.diags = append(c.diags, d) }

func (c *Collector) Len() int { return len(c.diags) }

// Diagnostics returns the collected diagnostics sorted deterministically by
// (file, line, check, message, severity, entity, repo, hint). Output order
// is independent of insertion order and filesystem walk order, so tests and
// CI logs remain stable.
func (c *Collector) Diagnostics() []Diagnostic {
	out := make([]Diagnostic, len(c.diags))
	copy(out, c.diags)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		if a.Message != b.Message {
			return a.Message < b.Message
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Hint < b.Hint
	})
	return out
}

func (c *Collector) HasErrors() bool {
	for _, d := range c.diags {
		if d.Severity == SevError {
			return true
		}
	}
	return false
}

// Plural renders a count with the right noun.
//
// It lives here, in the package about message quality, because it had been
// copied verbatim into two others and was about to be copied into a third.
// "1 entities validated" on stdout, "1 entities" in a diagnostic and "edited
// 1 days ago" on every entity page of the portal are one defect with three
// addresses, in a project whose thesis is that the message is the product.
func Plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
