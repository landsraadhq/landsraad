// Package scorecard measures entities against the team's standard.
//
// Spec D3 splits checks in two. Hermetic checks are computed in-binary and
// must run offline in under a second with no Docker daemon and no network
// egress. Expensive checks — a build, a scanner, an HTTP probe — are reported
// *into* the tool via .landsraad/checks/*.yaml, written by the CI jobs that
// already know the answer. The extension point is YAML, not a plugin API.
//
// Nothing here touches the filesystem directly, reads the clock, or reaches
// the network: everything outside a check comes in through Env.
package scorecard

import (
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

// Status is the outcome of one check for one entity.
//
// Six values, not two, because the difference between them is what makes a
// scorecard trustworthy. "Not reported" and "stale" are specifically not
// failures of the service — they are failures of the evidence — and rendering
// them as fail would send owners hunting for a problem in the wrong place.
type Status string

const (
	StatusPass Status = "pass"
	StatusFail Status = "fail"
	// StatusError: the check could not reach a verdict — an unreadable file, a
	// results file that does not parse. Distinct from fail: the service may be
	// perfectly fine and the tool cannot tell.
	StatusError Status = "error"
	// StatusNotReported: an external check with no result at all (spec §9,
	// "distinct from both pass and fail").
	StatusNotReported Status = "not-reported"
	// StatusStale: an external result older than staleAfterDays. An image scan
	// from March is not evidence about today (spec §6).
	StatusStale Status = "stale"
	// StatusExempt: waived by spec.exemptions with a stated reason.
	StatusExempt Status = "exempt"
)

// Passed reports whether this status counts towards the numerator.
//
// Only pass does. In particular not-reported and stale do not, while remaining
// applicable (ruling R3): if they were excluded from the denominator instead,
// deleting a CI job would raise a team's score, which is the one incentive
// this product must never create.
func (s Status) Passed() bool { return s == StatusPass }

// Result is one check's verdict for one entity.
type Result struct {
	Check  string
	Status Status
	// Detail is what the scorecard shows next to the verdict. "fail" is
	// useless; "spec.runbook is unset" fixes itself. It is the difference
	// between a scorecard people act on and one they ignore.
	Detail string
	// URL points at the evidence, for ingested results that carry one.
	URL string
}

// LastEditFunc reports when a path in a given repository was last changed,
// and whether that is known at all.
//
// It is injected because the answer comes from git, and nothing under
// internal/ may shell out or touch os. In the local repository cmd/ supplies
// it from `git log`; for a repository fetched over a host API there is no git
// history and it costs one API call per service (spec §9, "known cost of
// D5"). A fetcher that cannot answer returns false, and docs-fresh reports
// not-reported rather than inventing a date.
//
// repo arrived with Plan 4. Without it, "services/api" names a different
// directory in every repository in the catalog, and the local git history
// would be asked about paths that only exist on a host somewhere.
type LastEditFunc func(repo, path string) (time.Time, bool)

// Env is everything a check needs from outside itself.
//
// Now is a value rather than a call to time.Now() so scoring is a pure
// function of its inputs: a check that reads the clock has tests that fail at
// midnight and a result that cannot be reproduced from a commit.
type Env struct {
	// Sources replaced a single fs.FS in Plan 4. A merged catalog holds
	// entities from several repositories, so a check reads through
	// Sources.For(e) and never through an ambient filesystem (ruling R23).
	Sources        catalog.Sources
	Now            time.Time
	MaxDocsAgeDays int
	LastEdit       LastEditFunc
}

// Check is one measurable property, with a stable id.
//
// The id is a contract: it appears in standards.yaml, in .landsraad/checks
// files written by CI in other people's repositories, and in every row of
// scorecard-history.csv. Renaming one silently rewrites history.
type Check struct {
	ID  string
	Run func(e *catalog.Entity, env Env) Result
}
