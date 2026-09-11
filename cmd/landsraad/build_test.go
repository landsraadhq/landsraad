package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
	"github.com/landsraadhq/landsraad/internal/fetch"
	"github.com/landsraadhq/landsraad/internal/render"
	"github.com/landsraadhq/landsraad/internal/scorecard"
)

var buildNow = time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

func buildOpts() BuildOptions {
	return BuildOptions{
		Now:      buildNow,
		LastEdit: noLastEdit(),
		Version:  "v0.3.0-test",
		Mermaid:  render.Mermaid{Src: render.DefaultMermaidSrc, Integrity: render.DefaultMermaidIntegrity},
	}
}

// buildFS is a minimal, valid single-repo catalog.
func buildFS() fstest.MapFS {
	return fstest.MapFS{
		"repos.yaml": {Data: []byte(
			"repos:\n  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")},
		"teams.yaml": {Data: []byte(
			"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")},
		"services/ledger-api/service.yaml": {Data: []byte(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
				"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
				"  path: services/ledger-api\n")},
	}
}

// goodFixtureFS is a valid single-repository catalog: the fixture every
// other test in this file already builds via buildFS. Named goodFixtureFS
// because that is what the Task 13 brief's workspace-shaped tests call it.
func goodFixtureFS(t *testing.T) fs.FS {
	t.Helper()
	return buildFS()
}

// buildWorkspace wraps a single filesystem as a one-repository workspace,
// the way openRepos would if fsys's repos.yaml named only the entry it is
// standing in. It exists so the tests that predate Task 12's workspace type
// do not each have to construct one by hand.
func buildWorkspace(t *testing.T, fsys fs.FS) *workspace {
	t.Helper()
	var c diag.Collector
	name := localRepoName(fsys)
	return &workspace{
		sources:  catalog.Sources{name: fsys},
		patterns: map[string][]string{name: patternsFor(fsys, &c)},
		local:    name,
	}
}

func buildSiteMap(t *testing.T, fsys fs.FS, opts BuildOptions) (map[string][]byte, int, string) {
	t.Helper()
	var errOut bytes.Buffer
	files, code := Build(fsys, buildWorkspace(t, fsys), &errOut, opts)
	out := map[string][]byte{}
	for _, f := range files {
		out[f.Path] = f.Data
	}
	return out, code, errOut.String()
}

func TestBuildRendersACleanCatalog(t *testing.T) {
	files, code, errOut := buildSiteMap(t, buildFS(), buildOpts())
	if code != exitOK {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", code, exitOK, errOut)
	}
	if _, ok := files["index.html"]; !ok {
		t.Error("no index.html")
	}
	if !strings.Contains(errOut, "ok: ") {
		t.Errorf("expected an ok summary on stderr, got:\n%s", errOut)
	}
}

// A portal generated from a broken catalog publishes the broken state as if
// it were the truth. gen already refuses for the same reason.
func TestBuildRefusesABrokenCatalog(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	if len(files) != 0 {
		t.Errorf("a refused build must render nothing, got %d files", len(files))
	}
	want := "refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n"
	if !strings.HasSuffix(errOut, want) {
		t.Errorf("stderr:\n%s\nmust end with:\n%s", errOut, want)
	}
}

// Spec §7.1: under build, a dangling reference is a hard failure. validate
// records it and moves on, because the target may live in another repo.
func TestBuildFailsOnADanglingReference(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n  dependsOn: [topic:nowhere]\n")}

	_, code, _ := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Errorf("exit = %d, want %d for a dangling ref under FullCatalog", code, exitValidation)
	}
}

func TestBuildPassesTheFrozenClockThrough(t *testing.T) {
	files, _, _ := buildSiteMap(t, buildFS(), buildOpts())
	if !strings.Contains(string(files["index.html"]), "2026-09-09 12:00 UTC") {
		t.Error("the page footer must carry the injected build time, not time.Now()")
	}
}

// unreadableFS makes one path fail to read while every other path behaves.
//
// fstest.MapFS implements ReadFile directly, and it never checks permission
// bits, so a MapFile with Mode: 0 reads back cleanly — the failure path a test
// wanted would simply not run. Overriding ReadFile is the only way to get a
// real error out of it. (internal/render's docs_test.go carries the same
// wrapper and the same note, for the same reason.)
type unreadableFS struct {
	fstest.MapFS
	path string
}

func (u unreadableFS) ReadFile(name string) ([]byte, error) {
	if name == u.path {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return u.MapFS.ReadFile(name)
}

// A history file that exists and cannot be read is a third answer, distinct
// from no history file at all. Discarding the error rendered "No history yet.
// Run `landsraad score --history` in CI to start recording one" — advice about
// a job the reader has already set up, for a file sitting right there.
func TestBuildReportsAnUnreadableHistoryFileRatherThanCallingItAbsent(t *testing.T) {
	base := buildFS()
	base[scorecard.HistoryPath] = &fstest.MapFile{Data: []byte("date,ref,tier,owner,passed,applicable,score\n")}
	fsys := unreadableFS{MapFS: base, path: scorecard.HistoryPath}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitOK {
		t.Fatalf("an unreadable history must not fail the build, got exit %d:\n%s", code, errOut)
	}

	wantDiag := "warn: " + scorecard.HistoryPath + ":1 [history-unreadable]\n" +
		"  cannot read " + scorecard.HistoryPath + ": open " + scorecard.HistoryPath + ": permission denied\n" +
		"  hint: the portal is built without a trend; fix the file's permissions or " +
		"delete it and let `landsraad score --history` write a fresh one\n"
	if !strings.Contains(errOut, wantDiag) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantDiag)
	}

	// Spec §12: the degraded state is visible in the ARTIFACT, not only a log.
	page := string(files["scorecard/index.html"])
	wantOnPage := "scorecard-history.csv is present but could not be read, so no trend is shown."
	if !strings.Contains(page, wantOnPage) {
		t.Errorf("the scorecard page does not say the history was unreadable:\n%s", page)
	}
	if strings.Contains(page, "No history yet") {
		t.Error("the page tells the reader to set up a CI job for a file that is already there")
	}
}

// A history file that is genuinely absent keeps the original advice: that
// reader really does need to set the CI job up.
func TestBuildStillSaysNoHistoryWhenThereIsNoFile(t *testing.T) {
	files, code, errOut := buildSiteMap(t, buildFS(), buildOpts())
	if code != exitOK {
		t.Fatalf("exit = %d:\n%s", code, errOut)
	}
	page := string(files["scorecard/index.html"])
	if !strings.Contains(page, "No history yet") {
		t.Errorf("an absent history file must still ask for the CI job:\n%s", page)
	}
	if strings.Contains(errOut, "history-unreadable") {
		t.Errorf("an absent file is not an error to report:\n%s", errOut)
	}
}

// The plan's Global Constraints: every diagnostic carries a file and a line
// and says what to do. build printed "%s: %s" — severity and message — and
// dropped the file, the line, the check name and the hint, which is most of
// what a reader needs and all of what Plan 3's diagnostics were written to
// carry.
func TestBuildPrintsWholeDiagnosticsNotJustTheirMessages(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n")}

	_, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitValidation {
		t.Fatalf("exit = %d, want %d", code, exitValidation)
	}
	// The whole rendering, asserted exactly: the file, the line, the check
	// name and the hint were all thrown away by the old "%s: %s" line, and a
	// substring check for any one of them would pass against a version that
	// still dropped the rest.
	want := "error: services/ledger-api/service.yaml:4 [unknown-owner]\n" +
		"  owner \"team-ghost\" is not defined in teams.yaml\n" +
		"  hint: known teams: [team-payments]\n" +
		"refusing to build a portal from a catalog with errors; it would publish the broken state as if it were the truth\n"
	if errOut != want {
		t.Errorf("stderr =\n%s\nwant\n%s", errOut, want)
	}
}

// A documentation filename is USER DATA, and it becomes part of a page path:
// docsFor walks every *.md under spec.docs and emits entityDir + "docs/" +
// htmlSuffix(rel), where htmlSuffix is a bare suffix swap. So a file called
// "2024-06-01T09:00-incident.md" produces a site path containing a colon,
// which writeSite's path guard rejects — and rejecting it there aborts the
// whole build, after the output directory has already been half-updated, with
// a message telling the user to file a landsraad bug about their own filename.
//
// The build must survive it. The one page is dropped, by name, with a
// diagnostic that says what to rename.
func TestBuildSurvivesADocumentationFilenameThatCannotBecomeAPagePath(t *testing.T) {
	fsys := buildFS()
	fsys["services/ledger-api/service.yaml"] = &fstest.MapFile{Data: []byte(
		"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n  docs: services/ledger-api/docs\n")}
	fsys["services/ledger-api/docs/2024-06-01T09:00-incident.md"] = &fstest.MapFile{
		Data: []byte("# Incident\n\nWhat happened.\n")}
	fsys["services/ledger-api/docs/rollback.md"] = &fstest.MapFile{
		Data: []byte("# Rollback\n\nHow to roll back.\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitOK {
		t.Fatalf("a filename landsraad cannot publish must not fail the build, got exit %d:\n%s", code, errOut)
	}

	// The sibling document is unaffected: accumulate and skip, never stop.
	if _, ok := files["entity/service/ledger-api/docs/rollback.html"]; !ok {
		t.Errorf("the other documentation page must still be emitted; got %v", keysOf(files))
	}
	if _, ok := files["entity/service/ledger-api/index.html"]; !ok {
		t.Error("the entity page must still be emitted")
	}
	for p := range files {
		if strings.Contains(p, ":") || strings.Contains(p, `\`) {
			t.Errorf("a path writeSite would refuse reached the output: %q", p)
		}
	}

	wantDiag := "warn: services/ledger-api/docs/2024-06-01T09:00-incident.md:1 [docs-filename]\n" +
		"  cannot publish services/ledger-api/docs/2024-06-01T09:00-incident.md: " +
		"its name would make the page path \"entity/service/ledger-api/docs/2024-06-01T09:00-incident.html\", " +
		"which landsraad cannot write\n" +
		"  hint: rename the file without \":\" or \"\\\" — a documentation filename becomes part of its page's URL\n"
	if !strings.Contains(errOut, wantDiag) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantDiag)
	}

	// The user-visible half: writeSite is where the guard lives, so the whole
	// command has to survive, not just Build. This also pins that the guard
	// stays unreachable for user data — if a future change lets such a path out
	// of render again, this fails here rather than on somebody's machine.
	var files2 []emit.File
	for _, p := range keysOf(files) {
		files2 = append(files2, emit.File{Path: p, Data: files[p]})
	}
	if err := writeSite(t.TempDir(), files2, false, io.Discard); err != nil {
		t.Fatalf("writeSite must not abort the build over a documentation filename: %v", err)
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestBuildIsPureAndWritesNothing(t *testing.T) {
	fsys := buildFS()
	before := len(fsys)
	buildSiteMap(t, fsys, buildOpts())
	if len(fsys) != before {
		t.Error("Build must not write into the filesystem it reads")
	}
}

// Spec §12: a fetch failure is a hard failure. A portal quietly missing
// three services is worse than no portal.
func TestBuildRefusesAFailedFetch(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"platform": goodFixtureFS(t)},
		patterns: map[string][]string{"platform": {"services/*"}},
		local:    "platform",
		failures: []repoFailure{{
			Name: "edge-gateway", URL: "https://github.com/org/edge-gateway", Line: 4, Kind: "github",
			Err: &fetch.StatusError{Status: 404, Method: "GET", Endpoint: "/repos/org/edge-gateway", Body: "Not Found", RateRemaining: -1},
		}},
	}
	var errOut bytes.Buffer
	files, code := Build(goodFixtureFS(t), w, &errOut, BuildOptions{Now: testNow, Version: "test"})

	if code != exitUsage {
		t.Errorf("exit code = %d, want %d", code, exitUsage)
	}
	if files != nil {
		t.Error("Build produced files despite a failed fetch")
	}
	want := "error: cannot read edge-gateway: not found. A private repository with no token " +
		"looks exactly like this; check the url and that LANDSRAAD_TOKEN_EDGE_GATEWAY or GITHUB_TOKEN is set\n"
	if got := errOut.String(); !strings.Contains(got, want) {
		t.Errorf("stderr =\n%s\nwant it to contain\n%s", got, want)
	}
	// The refusal trailer, exact and singular: one repository, "repository"
	// not "repositories".
	wantTrailer := "refusing to build a portal that is missing 1 repository; " +
		"pass --allow-partial to build one anyway, with a banner saying so\n"
	if got := errOut.String(); !strings.HasSuffix(got, wantTrailer) {
		t.Errorf("stderr =\n%s\nmust end with\n%s", got, wantTrailer)
	}
}

// --allow-partial downgrades it, and the banner names the repositories that
// actually failed -- which is the whole difference from the scaffold this
// replaces.
func TestBuildAllowPartialNamesTheFailures(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"platform": goodFixtureFS(t)},
		patterns: map[string][]string{"platform": {"services/*"}},
		local:    "platform",
		failures: []repoFailure{
			{Name: "edge-gateway", URL: "https://github.com/org/edge-gateway", Line: 4, Err: errors.New("boom")},
			{Name: "billing", URL: "https://gitlab.com/org/billing", Line: 7, Err: errors.New("boom")},
		},
	}
	var errOut bytes.Buffer
	files, code := Build(goodFixtureFS(t), w, &errOut, BuildOptions{
		Now: testNow, Version: "test", AllowPartial: true,
	})
	if code != exitOK {
		t.Fatalf("exit code = %d, want %d; stderr:\n%s", code, exitOK, errOut.String())
	}
	want := "This portal is incomplete: billing and edge-gateway could not be read, " +
		"so their services are missing from this catalog."
	var found bool
	for _, f := range files {
		if strings.Contains(string(f.Data), want) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no generated page carries the banner %q", want)
	}
	// warn:, not error: -- and no refusal trailer, since --allow-partial
	// downgrades it. Exact and sorted by name, same as the errors above.
	wantWarnings := "warn: cannot read billing: boom\n" +
		"warn: cannot read edge-gateway: boom\n"
	if got := errOut.String(); !strings.Contains(got, wantWarnings) {
		t.Errorf("stderr =\n%s\nmust contain\n%s", got, wantWarnings)
	}
}

// The refusal trailer goes through plural: TestBuildRefusesAFailedFetch
// pins the singular ("1 repository"); this pins the plural two failures
// produce ("2 repositories"). Exercised directly against
// reportFetchFailures, whose whole output is deterministic, rather than
// through Build, so the assertion can be the entire buffer rather than a
// substring.
func TestReportFetchFailuresRefusalTrailerIsPlural(t *testing.T) {
	fails := []repoFailure{
		{Name: "edge-gateway", Err: errors.New("boom")},
		{Name: "billing", Err: errors.New("boom")},
	}
	var errOut bytes.Buffer
	code := reportFetchFailures(fails, false, &errOut)
	if code != exitUsage {
		t.Errorf("code = %d, want %d", code, exitUsage)
	}
	want := "error: cannot read billing: boom\n" +
		"error: cannot read edge-gateway: boom\n" +
		"refusing to build a portal that is missing 2 repositories; " +
		"pass --allow-partial to build one anyway, with a banner saying so\n"
	if got := errOut.String(); got != want {
		t.Errorf("errOut = %q, want %q", got, want)
	}
}

func TestPartialBanner(t *testing.T) {
	for _, tt := range []struct {
		name  string
		fails []repoFailure
		want  string
	}{
		{"none", nil, ""},
		{
			"one",
			[]repoFailure{{Name: "billing"}},
			"This portal is incomplete: billing could not be read, so its services are missing from this catalog.",
		},
		{
			"two, named in sorted order",
			[]repoFailure{{Name: "edge-gateway"}, {Name: "billing"}},
			"This portal is incomplete: billing and edge-gateway could not be read, so their services are missing from this catalog.",
		},
		{
			"three",
			[]repoFailure{{Name: "c"}, {Name: "a"}, {Name: "b"}},
			"This portal is incomplete: a, b and c could not be read, so their services are missing from this catalog.",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := partialBanner(tt.fails); got != tt.want {
				t.Errorf("partialBanner = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFailureMessage(t *testing.T) {
	for _, tt := range []struct {
		name string
		f    repoFailure
		want string
	}{
		{
			"not found says why it might not be",
			repoFailure{Name: "edge", Kind: "github", Err: &fetch.StatusError{Status: 404, RateRemaining: -1}},
			"cannot read edge: not found. A private repository with no token looks exactly like this; " +
				"check the url and that LANDSRAAD_TOKEN_EDGE or GITHUB_TOKEN is set",
		},
		{
			"rejected token",
			repoFailure{Name: "edge", Kind: "github", Err: &fetch.StatusError{Status: 401, RateRemaining: -1}},
			"cannot read edge: the host rejected the token; check that LANDSRAAD_TOKEN_EDGE or GITHUB_TOKEN is current and has read access",
		},
		{
			"rate limited names the reset",
			repoFailure{Name: "edge", Kind: "github", Err: &fetch.StatusError{
				Status: 403, RateRemaining: 0,
				RateReset: time.Date(2026, 9, 10, 15, 4, 5, 0, time.UTC),
			}},
			"cannot read edge: the host's rate limit is spent until 2026-09-10T15:04:05Z; " +
				"an unauthenticated build gets 60 requests an hour, an authenticated one 5000",
		},
		// A self-hosted GitLab's URL need not contain "gitlab" anywhere --
		// e.g. https://git.example.com/org/repo with host: gitlab in
		// repos.yaml. Kind comes from config.Repo.HostKind, which honours
		// that host: key; a substring match on the URL would miss it and
		// send the reader to set GITHUB_TOKEN for a GitLab failure.
		{
			"self-hosted gitlab names GITLAB_TOKEN even though the url doesn't say gitlab",
			repoFailure{Name: "edge", Kind: "gitlab", URL: "https://git.example.com/org/edge",
				Err: &fetch.StatusError{Status: 404, RateRemaining: -1}},
			"cannot read edge: not found. A private repository with no token looks exactly like this; " +
				"check the url and that LANDSRAAD_TOKEN_EDGE or GITLAB_TOKEN is set",
		},
		// HostKind could not tell (no host: key, and the hostname is
		// neither github.com nor gitlab.com). Naming a host-wide variable
		// here would be a guess with the same failure mode this exists to
		// avoid, so the hint names only the per-repository variable.
		{
			"unknown kind names only the per-repository variable",
			repoFailure{Name: "edge", Kind: "", URL: "https://git.example.com/org/edge",
				Err: &fetch.StatusError{Status: 404, RateRemaining: -1}},
			"cannot read edge: not found. A private repository with no token looks exactly like this; " +
				"check the url and that LANDSRAAD_TOKEN_EDGE is set",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := failureMessage(tt.f); got != tt.want {
				t.Errorf("failureMessage = %q, want %q", got, tt.want)
			}
		})
	}
}
