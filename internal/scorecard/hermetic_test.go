package scorecard

import (
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/fetch"
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
		Sources:        catalog.SingleSource("", files),
		Now:            time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
		MaxDocsAgeDays: 180,
		LastEdit:       func(string, string) (time.Time, bool) { return time.Time{}, false },
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
	got1 := run(t, "alerts-parse", e, env(good))
	if got1.Status != StatusPass {
		t.Errorf("Status = %q, want pass", got1.Status)
	}
	// This fixture has exactly one rule, and the assertion above was only ever
	// about Status — so the portal rendered "1 alert rules" on the entity page
	// with the suite green.
	if got1.Detail != "1 alert rule" {
		t.Errorf("Detail = %q, want %q", got1.Detail, "1 alert rule")
	}

	two := fstest.MapFS{"services/api/alerts.yaml": {Data: []byte(
		"groups:\n  - name: api\n    rules:\n      - alert: HighErrorRate\n        expr: rate(errors[5m]) > 0.05\n" +
			"      - alert: Down\n        expr: up == 0\n")}}
	if got := run(t, "alerts-parse", e, env(two)); got.Detail != "2 alert rules" {
		t.Errorf("Detail = %q, want %q", got.Detail, "2 alert rules")
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
	recent.LastEdit = func(string, string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -10), true
	}
	if got := docsFreshFor(t, e, recent); got.Status != StatusPass {
		t.Errorf("Status = %q, want pass for docs edited 10 days ago", got.Status)
	}

	old := base
	old.LastEdit = func(string, string) (time.Time, bool) {
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
		at.LastEdit = func(string, string) (time.Time, bool) {
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
	en.LastEdit = func(string, string) (time.Time, bool) { return base.AddDate(0, 0, -180), true }
	if got := docsFreshFor(t, e, en); got.Status != StatusPass {
		t.Errorf("exactly at the limit must pass, got %q", got.Status)
	}
	en.LastEdit = func(string, string) (time.Time, bool) { return base.AddDate(0, 0, -181), true }
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
	en.LastEdit = func(string, string) (time.Time, bool) { return en.Now, true }

	got := docsFreshFor(t, e, en)
	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail", got.Status)
	}
	// Ruling R51: the message names both spellings. One that named only
	// index.md is what sent 18 services' owners looking for the wrong file.
	if got.Detail != "services/api/docs has no index.md or _index.md" {
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

// runbook-present reads the runbook from the entity's own repository. Two
// entities naming the same relative path in different repositories must get
// different answers.
func TestRunbookPresentReadsTheEntitysOwnRepository(t *testing.T) {
	full := fstest.MapFS{"runbook.md": {Data: []byte("# Runbook\n\nCall the on-call.\n")}}
	stub := fstest.MapFS{"runbook.md": {Data: []byte("# Runbook\n")}}
	src := catalog.Sources{"full-repo": full, "stub-repo": stub}

	good := &catalog.Entity{SourceRepo: "full-repo"}
	good.Spec.Runbook = "runbook.md"
	bad := &catalog.Entity{SourceRepo: "stub-repo"}
	bad.Spec.Runbook = "runbook.md"

	env := Env{Sources: src}
	if got := runbookPresent(good, env); got.Status != StatusPass {
		t.Errorf("full-repo: Status = %v, want %v (Detail %q)", got.Status, StatusPass, got.Detail)
	}
	got := runbookPresent(bad, env)
	if got.Status != StatusFail {
		t.Errorf("stub-repo: Status = %v, want %v", got.Status, StatusFail)
	}
	if got.Detail != "runbook.md has a heading and no content" {
		t.Errorf("Detail = %q", got.Detail)
	}
}

func TestChecksReportAnEntityWithNoFilesystem(t *testing.T) {
	env := Env{Sources: catalog.Sources{"known": fstest.MapFS{}}}
	e := &catalog.Entity{SourceRepo: "ghost"}
	e.Spec.Runbook = "runbook.md"
	e.Spec.Alerts = "alerts.yaml"

	for _, tt := range []struct {
		name string
		run  func(*catalog.Entity, Env) Result
	}{
		{"runbook-present", runbookPresent},
		{"alerts-parse", alertsParse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.run(e, env)
			if got.Status != StatusError {
				t.Errorf("Status = %v, want %v", got.Status, StatusError)
			}
			if got.Detail != `no filesystem for repository "ghost"` {
				t.Errorf("Detail = %q", got.Detail)
			}
		})
	}
}

// docs-fresh asks for a last-edit date per repository. The same path in two
// repositories is two different directories with two different histories.
func TestDocsFreshAsksPerRepository(t *testing.T) {
	// Real prose, not just a heading: ruling R56 makes a heading-only index a
	// fail, and this test's subject is which repository gets asked for a
	// last-edit date, not what counts as content.
	docs := fstest.MapFS{"docs/index.md": {Data: []byte("# Docs\n\nThe overview.\n")}}
	src := catalog.Sources{"fresh": docs, "ancient": docs}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)

	var asked []string
	env := Env{
		Sources:        src,
		Now:            now,
		MaxDocsAgeDays: 180,
		LastEdit: func(repo, path string) (time.Time, bool) {
			asked = append(asked, repo+":"+path)
			if repo == "fresh" {
				return now.AddDate(0, 0, -3), true
			}
			return now.AddDate(0, 0, -400), true
		},
	}

	fresh := &catalog.Entity{SourceRepo: "fresh"}
	fresh.Spec.Docs = "docs"
	ancient := &catalog.Entity{SourceRepo: "ancient"}
	ancient.Spec.Docs = "docs"

	if got := docsFresh(fresh, env); got.Status != StatusPass {
		t.Errorf("fresh: Status = %v, want %v", got.Status, StatusPass)
	}
	if got := docsFresh(ancient, env); got.Status != StatusFail {
		t.Errorf("ancient: Status = %v, want %v", got.Status, StatusFail)
	}
	want := []string{"fresh:docs", "ancient:docs"}
	if diff := cmp.Diff(want, asked); diff != "" {
		t.Errorf("LastEdit calls mismatch (-want +got):\n%s", diff)
	}
}

// A file that is in the repository and whose content was never fetched is a
// landsraad bug, and the detail on the scorecard must say so.
//
// fetch.ErrNotFetched is deliberately not fs.ErrNotExist, because "a
// missing-file diagnostic would send somebody to look for a file that is
// sitting in their repository". Every consumer then dropped the error and
// rendered "cannot read services/api/runbook.md", which sends them exactly
// there — with a red mark on their scorecard for a bug in cmd/.
func TestUnfetchedFilesReadAsALandsraadBugNotAMissingFile(t *testing.T) {
	// A sparse filesystem that listed both files and fetched neither.
	remote := fetch.NewFS()
	remote.AddDir("services/api", []fetch.Entry{
		{Path: "services/api/runbook.md", SHA: "0123456789abcdef0123456789abcdef01234567", Size: 12},
		{Path: "services/api/alerts.yaml", SHA: "89abcdef0123456789abcdef0123456789abcdef", Size: 12},
	})
	env := Env{Sources: catalog.SingleSource("", remote), Now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}

	for _, tt := range []struct{ check, path string }{
		{"runbook-present", "services/api/runbook.md"},
		{"alerts-parse", "services/api/alerts.yaml"},
	} {
		t.Run(tt.check, func(t *testing.T) {
			e := svc("api")
			e.Spec.Runbook = "services/api/runbook.md"
			e.Spec.Alerts = "services/api/alerts.yaml"

			got := run(t, tt.check, e, env)
			if got.Status != StatusError {
				t.Errorf("Status = %q, want %q", got.Status, StatusError)
			}
			want := tt.path + " is in the repository but its content was never fetched; " +
				"this is a landsraad bug, not a problem with your catalog"
			if got.Detail != want {
				t.Errorf("Detail = %q, want %q", got.Detail, want)
			}
		})
	}
}

// Every other read failure carries the error itself. "cannot read X"
// collapsed a permission problem, an EISDIR and a truncated read into one
// sentence that says nothing about any of them.
func TestAnUnreadableRunbookCarriesTheError(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/runbook.md"
	env := Env{
		Sources: catalog.SingleSource("", fstest.MapFS{}),
		Now:     time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
	}
	got := run(t, "runbook-present", e, env)
	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q", got.Status, StatusError)
	}
	want := "cannot read services/api/runbook.md: open services/api/runbook.md: file does not exist"
	if got.Detail != want {
		t.Errorf("Detail = %q, want %q", got.Detail, want)
	}
}

// ErrNotListed is the same kind of fact as ErrNotFetched: a gap in what
// landsraad asked the host for, never evidence about the repository.
// Unreadable used to word only ErrNotFetched as a landsraad bug. This one
// fell through to "cannot read X: directory was never listed", which reads
// as a problem with the user's files.
func TestUnlistedFilesReadAsALandsraadBugNotAMissingFile(t *testing.T) {
	// The root listing saw services/, and nothing ever listed inside it.
	remote := fetch.NewFS()
	remote.AddDir(".", []fetch.Entry{{Path: "services", Dir: true}})
	env := Env{Sources: catalog.SingleSource("", remote), Now: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)}

	e := svc("api")
	e.Spec.Runbook = "services/api/runbook.md"
	got := run(t, "runbook-present", e, env)

	if got.Status != StatusError {
		t.Errorf("Status = %q, want %q", got.Status, StatusError)
	}
	want := "services/api/runbook.md was never listed, so landsraad cannot read it; " +
		"this is a landsraad bug, not a problem with your catalog"
	if got.Detail != want {
		t.Errorf("Detail = %q, want %q", got.Detail, want)
	}
}

// Ruling R51. docsFresh joined a literal "index.md" and returned fail on the
// Stat before env.LastEdit was ever reached, so the freshness machinery below
// it never ran for a Hugo docs tree. In the monorepo that found this, 1 file
// is named index.md and 53 are named _index.md: all 18 documented services
// reported "has no index.md", and one of them was genuinely 391 days stale
// behind that false answer.
func TestDocsFreshAcceptsAHugoUnderscoreIndex(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	files := fstest.MapFS{"services/api/docs/_index.md": {Data: []byte("# Docs\n\nreal content\n")}}

	base := env(files)
	recent := base
	recent.LastEdit = func(string, string) (time.Time, bool) {
		return base.Now.AddDate(0, 0, -10), true
	}

	got := docsFreshFor(t, e, recent)
	if got.Status != StatusPass {
		t.Errorf("Status = %q (%s), want pass — _index.md is a documentation index", got.Status, got.Detail)
	}
}

// Ruling R56. A Hugo section stub is front matter and nothing else — it exists
// to give a section a title and a weight, and the prose lives in a sibling
// document. docsFresh only Stat'd the index, so a stub counted as
// documentation and the entity went on to be scored for freshness on a file
// with nothing in it.
//
// runbook-present has refused exactly this since it was written: "A file
// holding only a heading is what a scaffold leaves behind, and counting it as
// a pass is how a scorecard comes to certify a runbook nobody wrote — the rot
// this product exists to make visible, certified by the product." The docs
// index gets the same rule.
//
// In the monorepo that prompted R51, all 18 application _index.md files are
// front-matter only: zero body characters, eighteen times.
func TestDocsFreshRejectsAFrontMatterOnlyIndex(t *testing.T) {
	e := svc("api")
	e.Spec.Docs = "services/api/docs"
	stub := "---\ntitle: \"API\"\nweight: 10020\ndraft: false\n---\n"
	en := env(fstest.MapFS{"services/api/docs/_index.md": {Data: []byte(stub)}})
	en.LastEdit = func(string, string) (time.Time, bool) { return en.Now, true }

	got := docsFreshFor(t, e, en)

	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail — a section stub is not documentation", got.Status)
	}
	want := "services/api/docs/_index.md has no content beyond its front matter and headings"
	if got.Detail != want {
		t.Errorf("Detail\n got: %s\nwant: %s", got.Detail, want)
	}
}

// The same defect one file over, and the reason R56 moved the emptiness test
// into internal/mdtext rather than fixing docsFresh alone. bodyIsEmpty read
// every front-matter line as content, so a runbook that was front matter and
// nothing else passed runbook-present.
func TestRunbookPresentRejectsAFrontMatterOnlyRunbook(t *testing.T) {
	e := svc("api")
	e.Spec.Runbook = "services/api/runbook.md"
	stub := "---\ntitle: \"Runbook\"\ndraft: true\n---\n"
	en := env(fstest.MapFS{"services/api/runbook.md": {Data: []byte(stub)}})

	got := run(t, "runbook-present", e, en)

	if got.Status != StatusFail {
		t.Errorf("Status = %q, want fail — front matter is not a runbook", got.Status)
	}
}
