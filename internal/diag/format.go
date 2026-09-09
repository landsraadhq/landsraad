package diag

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"sort"
	"strings"
)

// Formatter renders diagnostics for one destination. Adding a destination is
// a new type implementing this interface — never an edit to a switch
// statement (spec §3.1).
type Formatter interface {
	Name() string
	Write(w io.Writer, ds []Diagnostic) error
}

// Formatters returns the built-in formats by name. A new format is a new type
// implementing Formatter plus one line here — no switch to edit, and no init()
// self-registration, which would be package-level mutable state.
//
// This was a Registry type with a constructor and a Names method. It was
// deleted: there was never a second registry, its only consumer was a test
// that invented its own subject to justify it, and the interface alone gives
// every property the extra type claimed.
func Formatters() map[string]Formatter {
	return map[string]Formatter{
		"text":   Text{},
		"json":   JSON{},
		"github": GitHub{},
		"gitlab": GitLab{},
	}
}

// Lookup finds a formatter by name.
func Lookup(name string) (Formatter, bool) {
	f, ok := Formatters()[name]
	return f, ok
}

// FormatNames returns the built-in names, sorted, for help text.
func FormatNames() []string {
	out := make([]string, 0, 4)
	for n := range Formatters() {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Text renders diagnostics for a human terminal.
type Text struct{}

func (Text) Name() string { return "text" }

func (Text) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		if _, err := fmt.Fprintf(w, "%s: %s:%d", d.Severity, d.File, d.Line); err != nil {
			return err
		}
		if d.Check != "" {
			if _, err := fmt.Fprintf(w, " [%s]", d.Check); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n  %s\n", d.Message); err != nil {
			return err
		}
		if d.Hint != "" {
			if _, err := fmt.Fprintf(w, "  hint: %s\n", d.Hint); err != nil {
				return err
			}
		}
	}
	return nil
}

// JSON renders diagnostics as an array for machine consumption.
type JSON struct{}

func (JSON) Name() string { return "json" }

func (JSON) Write(w io.Writer, ds []Diagnostic) error {
	if ds == nil {
		ds = []Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ds)
}

// GitHub renders GitHub Actions workflow commands, so problems appear as
// inline annotations on the pull request.
type GitHub struct{}

func (GitHub) Name() string { return "github" }

func (GitHub) Write(w io.Writer, ds []Diagnostic) error {
	for _, d := range ds {
		level := "notice"
		switch d.Severity {
		case SevError:
			level = "error"
		case SevWarn:
			level = "warning"
		}
		msg := d.Message
		if d.Hint != "" {
			msg += " (" + d.Hint + ")"
		}
		// A newline would terminate the workflow command early.
		msg = strings.ReplaceAll(msg, "\n", " ")
		if _, err := fmt.Fprintf(w, "::%s file=%s,line=%d::%s\n", level, d.File, d.Line, msg); err != nil {
			return err
		}
	}
	return nil
}

// GitLab renders GitLab Code Quality report JSON.
type GitLab struct{}

func (GitLab) Name() string { return "gitlab" }

func (GitLab) Write(w io.Writer, ds []Diagnostic) error {
	type qlines struct {
		Begin int `json:"begin"`
	}
	type qlocation struct {
		Path  string `json:"path"`
		Lines qlines `json:"lines"`
	}
	type qissue struct {
		Description string    `json:"description"`
		CheckName   string    `json:"check_name"`
		Fingerprint string    `json:"fingerprint"`
		Severity    string    `json:"severity"`
		Location    qlocation `json:"location"`
	}

	out := make([]qissue, 0, len(ds))
	for _, d := range ds {
		severity := "info"
		switch d.Severity {
		case SevError:
			severity = "major"
		case SevWarn:
			severity = "minor"
		}
		// GitLab dedupes by fingerprint across pipeline runs, so it must be
		// stable for the same problem in the same place.
		h := fnv.New64a()
		fmt.Fprintf(h, "%s:%d:%s:%s", d.File, d.Line, d.Check, d.Message)
		out = append(out, qissue{
			Description: d.Message,
			CheckName:   d.Check,
			Fingerprint: hex.EncodeToString(h.Sum(nil)),
			Severity:    severity,
			Location:    qlocation{Path: d.File, Lines: qlines{Begin: d.Line}},
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
