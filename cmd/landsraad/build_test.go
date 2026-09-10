package main

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

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

func buildSiteMap(t *testing.T, fsys fs.FS, opts BuildOptions) (map[string][]byte, int, string) {
	t.Helper()
	var errOut bytes.Buffer
	files, code := Build(fsys, &errOut, opts)
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

// Ruling R22. Rendering a portal that quietly omits two of three repos is
// exactly what spec §12 forbids. Plan 4 replaces this with real fetching.
func TestBuildWarnsWhenReposYAMLListsRepositoriesItCannotRead(t *testing.T) {
	fsys := buildFS()
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(
		"repos:\n" +
			"  - url: https://github.com/org/monorepo\n    paths: [services/*]\n" +
			"  - url: https://github.com/org/edge-gateway\n    paths: [.]\n")}

	files, code, errOut := buildSiteMap(t, fsys, buildOpts())
	if code != exitOK {
		t.Fatalf("a partial build still succeeds, got exit %d:\n%s", code, errOut)
	}
	wantLine := "warn: repos.yaml lists 2 repositories and this build read only the local one; " +
		"1 repository was not read\n"
	if !strings.Contains(errOut, wantLine) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantLine)
	}
	// Degraded mode must be visible in the ARTIFACT, not only in a log.
	index := string(files["index.html"])
	wantBanner := "This portal covers only the repository this build ran in. " +
		"1 repository of the 2 in repos.yaml was not read; fetching remote repositories is not implemented yet."
	if !strings.Contains(index, wantBanner) {
		t.Errorf("the generated page carries no banner:\n%s", index)
	}
}

// R22's point is that the omission is visible AND ACCURATE. repos.yaml's first
// entry is the local repository only by convention — config.LoadRepos does not
// check it — so the notice must not name repositories as missing on the
// strength of a position it cannot verify. With the local repository written
// last, the old wording named the two that were actually read and omitted the
// two that were not, on every page of the portal.
func TestBuildsPartialNoticeNamesNoRepositoryItCannotIdentify(t *testing.T) {
	fsys := buildFS()
	// The local repository is written LAST. Every entry carries the same paths
	// so the catalog still loads whichever one LocalPatterns happens to take —
	// this test is about what the notice claims, not about which globs ran.
	fsys["repos.yaml"] = &fstest.MapFile{Data: []byte(
		"repos:\n" +
			"  - url: https://github.com/org/edge-gateway\n    paths: [services/*]\n" +
			"  - url: https://github.com/org/data-platform\n    paths: [services/*]\n" +
			"  - url: https://github.com/org/monorepo\n    paths: [services/*]\n")}

	files, _, errOut := buildSiteMap(t, fsys, buildOpts())
	wantLine := "warn: repos.yaml lists 3 repositories and this build read only the local one; " +
		"2 repositories were not read\n"
	if !strings.Contains(errOut, wantLine) {
		t.Errorf("stderr:\n%s\nmust contain:\n%s", errOut, wantLine)
	}
	index := string(files["index.html"])
	wantBanner := "This portal covers only the repository this build ran in. " +
		"2 repositories of the 3 in repos.yaml were not read; fetching remote repositories is not implemented yet."
	if !strings.Contains(index, wantBanner) {
		t.Errorf("banner missing or reworded:\n%s", index)
	}
	for _, name := range []string{"edge-gateway", "data-platform", "monorepo"} {
		if strings.Contains(index, name) {
			t.Errorf("the notice names %q, which it has no way to know is missing:\n%s", name, index)
		}
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

func TestBuildIsPureAndWritesNothing(t *testing.T) {
	fsys := buildFS()
	before := len(fsys)
	buildSiteMap(t, fsys, buildOpts())
	if len(fsys) != before {
		t.Error("Build must not write into the filesystem it reads")
	}
}
