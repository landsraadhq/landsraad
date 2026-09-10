package scorecard

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
)

func svc(name string) *catalog.Entity {
	e := &catalog.Entity{APIVersion: catalog.APIVersion, Kind: catalog.KindService}
	e.Metadata.Name = name
	e.Metadata.Owner = "team-payments"
	e.Metadata.Tier = 1
	e.SourcePath = "services/" + name + "/service.yaml"
	e.NameLine = 4
	return e
}

func env(files fstest.MapFS) Env {
	return Env{
		FS:             files,
		Now:            time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		MaxDocsAgeDays: 180,
		LastEdit:       func(string) (time.Time, bool) { return time.Time{}, false },
	}
}

// run finds a check by id and runs it, failing the test when the id is not
// registered — a typo in a check id would otherwise silently test nothing.
func run(t *testing.T, id string, e *catalog.Entity, en Env) Result {
	t.Helper()
	for _, c := range HermeticChecks() {
		if c.ID == id {
			return c.Run(e, en)
		}
	}
	t.Fatalf("no hermetic check with id %q; have %v", id, checkIDs())
	return Result{}
}

func checkIDs() []string {
	var out []string
	for _, c := range HermeticChecks() {
		out = append(out, c.ID)
	}
	return out
}

func TestHermeticChecksHaveTheIDsTheSpecNames(t *testing.T) {
	want := map[string]bool{
		"owner-set": false, "runbook-present": false,
		"alerts-parse": false, "slo-defined": false, "docs-fresh": false,
	}
	for _, c := range HermeticChecks() {
		if _, known := want[c.ID]; !known {
			t.Errorf("unexpected hermetic check %q — spec §9 names five", c.ID)
			continue
		}
		want[c.ID] = true
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("spec §9 names %q as hermetic, but it is not registered", id)
		}
	}
}

// A check id is a stable contract: it appears in standards.yaml, in
// .landsraad/checks/*.yaml written by other people's CI, and in the history
// CSV. Renaming one silently rewrites history.
func TestHermeticChecksReturnsAFreshSlice(t *testing.T) {
	first := HermeticChecks()
	first[0].ID = "clobbered"
	if HermeticChecks()[0].ID == "clobbered" {
		t.Error("HermeticChecks() handed out state a caller could rewrite")
	}
}

func TestOwnerSet(t *testing.T) {
	e := svc("api")
	if got := run(t, "owner-set", e, env(nil)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}
	e.Metadata.Owner = ""
	got := run(t, "owner-set", e, env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "metadata.owner is unset" {
		t.Errorf("Detail = %q, want %q", got.Detail, "metadata.owner is unset")
	}
}

func TestRunbookPresentRequiresANonEmptyFile(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/docs/runbook.md"

	files := fstest.MapFS{"services/api/docs/runbook.md": {Data: []byte("# Runbook\n\nRestart it.\n")}}
	if got := run(t, "runbook-present", e, env(files)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// Spec §5.3 says "runbook exists and non-empty". A file holding only a
	// heading is the shape a scaffold leaves behind, and treating it as a pass
	// is how a scorecard comes to certify a runbook nobody wrote.
	empty := fstest.MapFS{"services/api/docs/runbook.md": {Data: []byte("# Runbook\n")}}
	got := run(t, "runbook-present", e, env(empty))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for a heading-only runbook", got.Status)
	}
	if got.Detail != "services/api/docs/runbook.md has a heading and no content" {
		t.Errorf("Detail = %q", got.Detail)
	}

	e.Spec.Runbook = ""
	if got := run(t, "runbook-present", e, env(files)); got.Detail != "spec.runbook is unset" {
		t.Errorf("Detail = %q, want %q", got.Detail, "spec.runbook is unset")
	}
}

func TestAlertsParse(t *testing.T) {
	e := svc("api")
	e.Spec.Alerts = "services/api/alerts.yaml"

	good := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte(
		"groups:\n  - name: api\n    rules:\n      - alert: HighErrorRate\n        expr: rate(errors[5m]) > 0.05\n")}}
	if got := run(t, "alerts-parse", e, env(good)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// A rules file that parses as YAML but declares no groups is a file that
	// alerts on nothing. Prometheus loads it without complaint.
	emptyGroups := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte("groups: []\n")}}
	got := run(t, "alerts-parse", e, env(emptyGroups))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for zero groups", got.Status)
	}
	if got.Detail != "services/api/alerts.yaml declares no alert groups" {
		t.Errorf("Detail = %q", got.Detail)
	}

	broken := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte("groups:\n  - name: api\n   rules: []\n")}}
	if got := run(t, "alerts-parse", e, env(broken)); got.Status != StatusError {
		t.Errorf("Status = %q, want error for unparseable YAML", got.Status)
	}
}

func TestSLODefined(t *testing.T) {
	e := svc("api")
	if got := run(t, "slo-defined", e, env(nil)); got.Status != StatusFail {
		t.Errorf("Status = %q, want fail with no SLO", got.Status)
	}

	e.Spec.SLO = []catalog.SLO{{Name: "availability", Target: "99.9%", Window: "30d"}}
	if got := run(t, "slo-defined", e, env(nil)); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got.Status)
	}

	// An SLO with no target is a name, not an objective.
	e.Spec.SLO = []catalog.SLO{{Name: "availability"}}
	got := run(t, "slo-defined", e, env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != `SLO "availability" has no target` {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// A check must never report pass for a file it could not read. That is the
// exit-0-on-something-unexamined failure, inside a single check.
func TestChecksReportErrorRatherThanPassOnAnUnreadableFile(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/docs/runbook.md"
	e.Spec.Alerts = "services/api/alerts.yaml"

	for _, id := range []string{"runbook-present", "alerts-parse"} {
		got := run(t, id, e, env(fstest.MapFS{}))
		if got.Status == StatusPass {
			t.Errorf("%s returned pass for a file that does not exist", id)
		}
	}
}

func TestDocsFreshUsesTheInjectedLastEditDate(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nreal content\n")}}

	base := env(files)
	recent := base
	recent.LastEdit = func(string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -10), true
	}
	if got := docsFreshFor(t, e, recent); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass for docs edited 10 days ago", got.Status)
	}

	old := base
	old.LastEdit = func(string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -365), true
	}
	got := docsFreshFor(t, e, old)
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail for docs edited 365 days ago", got.Status)
	}
	if got.Detail != "services/api/docs last edited 365 days ago, limit is 180 days" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// The portal prints this detail on every entity page, so its grammar is not a
// detail. Nothing pinned the counts where "%d days ago" reads wrong: at one
// day it said "edited 1 days ago", and at zero "edited 0 days ago". Both were
// visible in a real build; neither was covered.
func TestDocsFreshDetailReadsCorrectlyAtEveryCount(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nreal content\n")}}
	base := env(files)

	for _, tc := range []struct {
		daysOld int
		want    string
	}{
		{0, "edited today"},
		{1, "edited 1 day ago"},
		{2, "edited 2 days ago"},
		{10, "edited 10 days ago"},
	} {
		at := base
		at.LastEdit = func(string) (time.Time, bool) {
			return base.Now.AddDate(0, 0, -tc.daysOld), true
		}
		got := docsFreshFor(t, e, at)
		if got.Status != StatusPass {
			t.Fatalf("%d days old: Status = %q, want pass", tc.daysOld, got.Status)
		}
		if got.Detail != tc.want {
			t.Errorf("%d days old: Detail = %q, want %q", tc.daysOld, got.Detail, tc.want)
		}
	}
}

// The boundary: exactly maxAgeDays old is still fresh. An off-by-one here
// flips a whole tier of services on the day the threshold changes.
func TestDocsFreshBoundaryIsInclusive(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nc\n")}}

	en := env(files)
	base := en.Now
	en.LastEdit = func(string) (time.Time, bool) { return base.AddDate(0, 0, -180), true }
	if got := docsFreshFor(t, e, en); got.Status != StatusPass {
		t.Errorf("exactly at the limit must pass, got %q", got.Status)
	}
	en.LastEdit = func(string) (time.Time, bool) { return base.AddDate(0, 0, -181), true }
	if got := docsFreshFor(t, e, en); got.Status != StatusFail {
		t.Errorf("one day past the limit must fail, got %q", got.Status)
	}
}

// A repository fetched over a host API has no git history. Reporting pass
// would silently give every such service full marks for documentation
// freshness — the failure this project keeps finding in new corners.
func TestDocsFreshReportsNotReportedWhenTheDateIsUnknown(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/index.md": {Data: []byte("# Docs\n\nc\n")}}

	got := docsFreshFor(t, e, env(files))
	if got.Status != StatusNotReported {
		t.Errorf("Status = %q, want not-reported when no last-edit date is available", got.Status)
	}
	if got.Detail != "no last-edit date available for services/api/docs" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

// Spec §5.3 pairs freshness with an index: "Docs index and last edit < 180
// days". Docs with no index page is a directory, not documentation.
func TestDocsFreshRequiresAnIndex(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	en := env(fstest.MapFS{"services/api/docs/other.md": {Data: []byte("x\n")}})
	en.LastEdit = func(string) (time.Time, bool) { return en.Now, true }

	got := docsFreshFor(t, e, en)
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "services/api/docs has no index.md" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func TestDocsFreshFailsWhenDocsAreUnset(t *testing.T) {
	got := docsFreshFor(t, svc("api"), env(nil))
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	if got.Detail != "spec.docs is unset" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func docsFreshFor(t *testing.T, e *catalog.Entity, en Env) Result {
	t.Helper()
	return run(t, "docs-fresh", e, en)
}
