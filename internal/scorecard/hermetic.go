package scorecard

import (
	"fmt"
	"io/fs"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

// HermeticChecks returns the checks computed in-binary (spec §9), fresh on
// each call so a caller cannot rewrite what the package believes.
func HermeticChecks() []Check {
	return []Check{
		{ID: "owner-set", Run: ownerSet},
		{ID: "runbook-present", Run: runbookPresent},
		{ID: "alerts-parse", Run: alertsParse},
		{ID: "slo-defined", Run: sloDefined},
		{ID: "docs-fresh", Run: docsFresh},
	}
}

func ownerSet(e *catalog.Entity, _ Env) Result {
	if strings.TrimSpace(e.Metadata.Owner) == "" {
		return Result{Check: "owner-set", Status: StatusFail, Detail: "metadata.owner is unset"}
	}
	return Result{Check: "owner-set", Status: StatusPass, Detail: e.Metadata.Owner}
}

// runbookPresent requires a runbook that exists and says something.
//
// Spec §5.3 words it as "runbook exists and non-empty". A file holding only a
// heading is what a scaffold leaves behind, and counting it as a pass is how a
// scorecard comes to certify a runbook nobody wrote — the rot this product
// exists to make visible, certified by the product.
func runbookPresent(e *catalog.Entity, env Env) Result {
	const id = "runbook-present"
	if e.Spec.Runbook == "" {
		return Result{Check: id, Status: StatusFail, Detail: "spec.runbook is unset"}
	}
	data, err := fs.ReadFile(env.FS, e.Spec.Runbook)
	if err != nil {
		// Never pass for a file that could not be read: that is the
		// exit-0-on-something-unexamined failure inside a single check.
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("cannot read %s", e.Spec.Runbook)}
	}
	if bodyIsEmpty(data) {
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s has a heading and no content", e.Spec.Runbook)}
	}
	return Result{Check: id, Status: StatusPass, Detail: e.Spec.Runbook}
}

// bodyIsEmpty reports whether a Markdown document has no content beyond
// headings, blank lines and HTML comments.
func bodyIsEmpty(data []byte) bool {
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
		case strings.HasPrefix(t, "<!--"):
		default:
			return false
		}
	}
	return true
}

// alertRules is the subset of the Prometheus rules format landsraad reads. It
// is deliberately shallow: this check answers "does this file define alerts",
// not "are these good alerts", and promtool is the right tool for the latter.
type alertRules struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert  string `yaml:"alert"`
			Record string `yaml:"record"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func alertsParse(e *catalog.Entity, env Env) Result {
	const id = "alerts-parse"
	if e.Spec.Alerts == "" {
		return Result{Check: id, Status: StatusFail, Detail: "spec.alerts is unset"}
	}
	data, err := fs.ReadFile(env.FS, e.Spec.Alerts)
	if err != nil {
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("cannot read %s", e.Spec.Alerts)}
	}
	var rules alertRules
	if err := yaml.Unmarshal(data, &rules); err != nil {
		return Result{Check: id, Status: StatusError,
			Detail: fmt.Sprintf("%s does not parse as Prometheus rules", e.Spec.Alerts)}
	}
	if len(rules.Groups) == 0 {
		// Prometheus loads a file with no groups without complaint, and it
		// alerts on nothing.
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s declares no alert groups", e.Spec.Alerts)}
	}
	alerts := 0
	for _, g := range rules.Groups {
		for _, r := range g.Rules {
			if r.Alert != "" {
				alerts++
			}
		}
	}
	if alerts == 0 {
		return Result{Check: id, Status: StatusFail,
			Detail: fmt.Sprintf("%s defines only recording rules, no alerts", e.Spec.Alerts)}
	}
	return Result{Check: id, Status: StatusPass,
		Detail: fmt.Sprintf("%d alert rules", alerts)}
}

func sloDefined(e *catalog.Entity, _ Env) Result {
	const id = "slo-defined"
	if len(e.Spec.SLO) == 0 {
		return Result{Check: id, Status: StatusFail, Detail: "spec.slo is empty"}
	}
	for _, s := range e.Spec.SLO {
		// A name with no target is a label, not an objective.
		if strings.TrimSpace(s.Target) == "" {
			return Result{Check: id, Status: StatusFail,
				Detail: fmt.Sprintf("SLO %q has no target", s.Name)}
		}
	}
	return Result{Check: id, Status: StatusPass,
		Detail: fmt.Sprintf("%d defined", len(e.Spec.SLO))}
}

// docsFresh is implemented in Task 7, which adds the last-edit plumbing.
func docsFresh(_ *catalog.Entity, _ Env) Result {
	return Result{Check: "docs-fresh", Status: StatusNotReported, Detail: "not implemented"}
}
