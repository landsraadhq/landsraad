package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"

	"github.com/landsraadhq/landsraad/internal/catalog"
	"github.com/landsraadhq/landsraad/internal/diag"
	"github.com/landsraadhq/landsraad/internal/emit"
)

func testServer() *siteServer {
	s := &siteServer{}
	s.set([]emit.File{
		{Path: "index.html", Data: []byte("<h1>catalog</h1>")},
		{Path: "entity/service/api/index.html", Data: []byte("<h1>api</h1>")},
		{Path: "assets/style.css", Data: []byte("body{}")},
		{Path: "search-index.json", Data: []byte("[]")},
	})
	return s
}

func get(t *testing.T, s *siteServer, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestServeMapsRootToTheCatalogIndex(t *testing.T) {
	rec := get(t, testServer(), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>catalog</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// Ruling R11's directory-per-page URLs must resolve without a rewrite rule.
func TestServeMapsADirectoryURLToItsIndex(t *testing.T) {
	rec := get(t, testServer(), "/entity/service/api/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "<h1>api</h1>" {
		t.Errorf("body = %q", rec.Body.String())
	}
}

func TestServeSetsContentTypes(t *testing.T) {
	cases := map[string]string{
		"/assets/style.css":  "text/css",
		"/search-index.json": "application/json",
		"/":                  "text/html",
	}
	s := testServer()
	for path, want := range cases {
		got := get(t, s, path).Header().Get("Content-Type")
		if len(got) < len(want) || got[:len(want)] != want {
			t.Errorf("%s Content-Type = %q, want a %q", path, got, want)
		}
	}
}

func TestServeReturns404ForAnUnknownPath(t *testing.T) {
	if code := get(t, testServer(), "/nope/").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", code)
	}
}

// A path escape must not reach outside the in-memory map. It cannot — there
// is no filesystem behind this — but the mapping must still not resolve.
func TestServeRejectsTraversal(t *testing.T) {
	if code := get(t, testServer(), "/../../etc/passwd").Code; code == http.StatusOK {
		t.Error("a traversal path must not resolve")
	}
}

func TestServeSwapsTheSiteOnRebuild(t *testing.T) {
	s := testServer()
	s.set([]emit.File{{Path: "index.html", Data: []byte("<h1>rebuilt</h1>")}})
	if body := get(t, s, "/").Body.String(); body != "<h1>rebuilt</h1>" {
		t.Errorf("body = %q, want the rebuilt page", body)
	}
	// The previous site's pages are gone, not merged.
	if code := get(t, s, "/entity/service/api/").Code; code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 after a rebuild that dropped the page", code)
	}
}

// fsnotify is NOT recursive. Without re-adding on Create, a service added
// while the server runs is invisible until restart — the failure that looks
// like "the watcher just doesn't work sometimes".
func TestWatchDirsPicksUpADirectoryCreatedLater(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "services", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := watchDirs(root, w); err != nil {
		t.Fatalf("watchDirs: %v", err)
	}

	events := make(chan fsnotify.Event, 64)
	go func() {
		for e := range w.Events {
			// This is what the serve loop does with a new directory.
			if e.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(e.Name); err == nil && info.IsDir() {
					_ = watchDirs(e.Name, w)
				}
			}
			events <- e
		}
	}()

	if err := os.MkdirAll(filepath.Join(root, "services", "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, 2*time.Second) // the directory creation itself

	if err := os.WriteFile(filepath.Join(root, "services", "new", "service.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, 2*time.Second) // the write inside it
}

// .git produces thousands of events on an ordinary checkout. Watching it
// means a rebuild storm on every git command.
func TestWatchDirsSkipsDotGit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := watchDirs(root, w); err != nil {
		t.Fatal(err)
	}
	for _, watched := range w.WatchList() {
		if filepath.Base(watched) == ".git" || filepath.Base(filepath.Dir(watched)) == ".git" {
			t.Errorf(".git is being watched: %s", watched)
		}
	}
}

func waitFor(t *testing.T, ch chan fsnotify.Event, d time.Duration) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(d):
		t.Fatal("no filesystem event arrived")
	}
}

// writeCatalogFixture writes n services under root, each with a docs/
// directory containing index.md. That makes scorecard's docs-fresh check
// call env.LastEdit(e.Spec.Docs) for every one of them during Build -- the
// exact call that writes into gitLastEdit's cache map -- and n large enough
// makes one Build call slow enough (n forked, failing `git log` processes,
// since root is not a git repository) for concurrent calls to overlap.
func writeCatalogFixture(t *testing.T, root string, n int) {
	t.Helper()
	teams := "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n"
	if err := os.WriteFile(filepath.Join(root, "teams.yaml"), []byte(teams), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("svc%d", i)
		dir := filepath.Join(root, "services", name)
		docsDir := filepath.Join(dir, "docs")
		if err := os.MkdirAll(docsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(docsDir, "index.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		yaml := fmt.Sprintf(
			"apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: %s\n"+
				"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n"+
				"  path: services/%s\n  docs: services/%s/docs\n", name, name, name)
		if err := os.WriteFile(filepath.Join(dir, "service.yaml"), []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestRebuildSerialisesConcurrentTriggers proves a burst of rapid rebuild
// triggers cannot run Build concurrently.
//
// This is the exact hazard newRebuild's mutex exists to close: Build's
// LastEdit closure (gitLastEdit, lastedit.go) caches into a plain,
// unsynchronised map, and the debounce timer's Stop-then-AfterFunc pattern
// in Serve can start a second rebuild while the first is still running.
// Two Build calls writing that map concurrently panic with "fatal error:
// concurrent map writes" -- unrecoverable, and it takes the whole preview
// server down mid-session.
//
// It uses the real gitLastEdit closure against a real (non-git) temp
// directory, not a stand-in, so it exercises the exact code path
// newServeCmd wires up.
func TestRebuildSerialisesConcurrentTriggers(t *testing.T) {
	root := t.TempDir()
	writeCatalogFixture(t, root, 40)

	opts := BuildOptions{LastEdit: gitLastEdit(root)}
	srv := &siteServer{}
	w := buildWorkspace(t, os.DirFS(root))
	rebuild := newRebuild(root, w, opts, utcNow, srv, io.Discard)

	const bursts = 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < bursts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rebuild("")
		}()
	}
	close(start)
	wg.Wait()

	// Reaching this line at all -- rather than a fatal "concurrent map
	// writes" crash, or -race reporting a data race -- is most of what this
	// test proves. It also leaves a complete site behind, not a partial one:
	// whichever rebuild last held the lock replaced the whole map in one
	// swap (siteServer.set), never merged.
	if _, ok := srv.lookup("index.html"); !ok {
		t.Fatal("index.html missing after a burst of concurrent rebuild triggers")
	}
}

// writeBrokenCatalog writes a service.yaml under root whose owner is not
// defined in teams.yaml -- the same fixture shape as build_test.go's
// TestBuildRefusesABrokenCatalog -- guaranteeing Build returns exitValidation
// and renders nothing.
func writeBrokenCatalog(t *testing.T, root string) {
	t.Helper()
	teams := "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n"
	if err := os.WriteFile(filepath.Join(root, "teams.yaml"), []byte(teams), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "services", "ledger-api")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	svc := "apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
		"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
		"  path: services/ledger-api\n"
	if err := os.WriteFile(filepath.Join(dir, "service.yaml"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixBrokenCatalog corrects the dangling owner reference writeBrokenCatalog
// left behind -- the same edit a developer mid-session would make to fix it.
func fixBrokenCatalog(t *testing.T, root string) {
	t.Helper()
	svc := "apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
		"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
		"  path: services/ledger-api\n"
	if err := os.WriteFile(filepath.Join(root, "services", "ledger-api", "service.yaml"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestServeReturns503BeforeAnyBuildHasSucceeded is the case a bare 404 would
// misdescribe: there is no previous version to fall back to, so ServeHTTP
// must say so honestly rather than looking like a missing route.
func TestServeReturns503BeforeAnyBuildHasSucceeded(t *testing.T) {
	rec := get(t, &siteServer{}, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	want := buildNeverSucceededMessage + "\n" // http.Error appends the newline.
	if rec.Body.String() != want {
		t.Errorf("body = %q, want %q", rec.Body.String(), want)
	}
}

// A preview that stays up for days must not grade against the moment it was
// launched.
//
// BuildOptions is a value: it was built once in RunE and captured by the
// rebuild closure, so a Now stamped into it never changed. The footer said so
// visibly, and less visibly scorecard.Ingest's stale-result window and
// docs-fresh's maxAgeDays both graded against a clock that stopped when
// `serve` started — a preview left running over a weekend graded Monday's
// catalog against Friday. The comment at that line claimed the opposite of
// what the code did.
func TestEachRebuildReadsTheClockAgain(t *testing.T) {
	root := t.TempDir()
	writeCatalogFixture(t, root, 1)

	first := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	second := first.Add(72 * time.Hour) // the weekend
	calls := 0
	clock := func() time.Time {
		calls++
		if calls == 1 {
			return first
		}
		return second
	}

	srv := &siteServer{}
	var errOut bytes.Buffer
	w := buildWorkspace(t, os.DirFS(root))
	rebuild := newRebuild(root, w, BuildOptions{LastEdit: noLastEdit()}, clock, srv, &errOut)

	if code := rebuild(""); code != exitOK {
		t.Fatalf("the first build must succeed; stderr:\n%s", errOut.String())
	}
	page, ok := srv.lookup("index.html")
	if !ok {
		t.Fatal("index.html missing after the first build")
	}
	if !strings.Contains(string(page), "2026-09-09 12:00 UTC") {
		t.Fatalf("the first build did not use the injected clock:\n%s", page)
	}

	if code := rebuild(""); code != exitOK {
		t.Fatalf("the second build must succeed; stderr:\n%s", errOut.String())
	}
	page, ok = srv.lookup("index.html")
	if !ok {
		t.Fatal("index.html missing after the second build")
	}
	if !strings.Contains(string(page), "2026-09-12 12:00 UTC") {
		t.Errorf("the second rebuild still carries the first build's clock:\n%s", page)
	}
	if calls != 2 {
		t.Errorf("the clock was read %d times for 2 rebuilds", calls)
	}
}

// TestRebuildAfterAGoodBuildKeepsServingLastGoodSite is the genuine case for
// "still serving the previous version": a good build already landed, so
// that message is true, unlike the never-built case above.
func TestRebuildAfterAGoodBuildKeepsServingLastGoodSite(t *testing.T) {
	root := t.TempDir()
	writeCatalogFixture(t, root, 1)

	srv := &siteServer{}
	var errOut bytes.Buffer
	opts := BuildOptions{LastEdit: noLastEdit()}
	w := buildWorkspace(t, os.DirFS(root))
	rebuild := newRebuild(root, w, opts, utcNow, srv, &errOut)

	if code := rebuild(""); code != exitOK {
		t.Fatalf("the first, valid build must succeed; stderr:\n%s", errOut.String())
	}
	goodIndex, ok := srv.lookup("index.html")
	if !ok {
		t.Fatal("index.html missing after a successful build")
	}

	errOut.Reset()
	writeBrokenCatalog(t, root) // adds a second entity with an undefined owner
	if code := rebuild(""); code == exitOK {
		t.Fatal("a build with a dangling owner reference must fail")
	}
	want := "  build failed; still serving the previous version\n"
	if !strings.HasSuffix(errOut.String(), want) {
		t.Errorf("stderr:\n%s\nmust end with:\n%s", errOut.String(), want)
	}
	if got, _ := srv.lookup("index.html"); string(got) != string(goodIndex) {
		t.Error("the last good site must still be served after a failed rebuild")
	}
}

// TestOpenReposReportsAMalformedReposYAML is Task 13 fix-round-1 finding #1,
// re-pinned at its Task 14 home.
//
// Before Task 14, singleRepoWorkspace's diagnostics went into a throwaway
// collector that Build never saw, so a repos.yaml error printed to stderr
// and then serve started anyway; the fix lived inside newRebuild because
// that is where repos.yaml was parsed -- once per rebuild.
//
// Task 14 moves that parse to openRepos, called exactly once, before the
// first build (ruling R33): a rebuild no longer touches repos.yaml at all,
// so there is no longer a rebuild for a bad repos.yaml to interrupt -- an
// edit to it after startup needs a restart, like any other remote. The gate
// this test pins moved with the parse: it is openRepos's own collector,
// checked once in newServeCmd's RunE before Serve is ever called, exactly
// as build and validate both refuse on the same diagnostic.
//
// This test exercises only the collector openRepos fills, not the refusal
// itself -- newServeCmd's gate is an inline os.Exit in RunE, the same
// un-extracted shape newBuildCmd's own gate already has, and there is no
// second caller here to justify extracting one just to make this test more
// direct. TestServeWithoutWatchExitsWhenReposYAMLIsMalformed and its
// --watch sibling below pin the actual refusal, through the real binary.
func TestOpenReposReportsAMalformedReposYAML(t *testing.T) {
	dir := materialize(t, malformedReposFixture())

	var c diag.Collector
	openRepos(context.Background(), reposOptions{
		Root: dir, RootFS: os.DirFS(dir),
		Lookup: func(string) (string, bool) { return "", false },
		ErrOut: io.Discard,
	}, &c)

	if !c.HasErrors() {
		t.Fatal("a malformed repos.yaml must report an error -- newServeCmd's RunE gates on exactly this before ever calling Serve")
	}
	var errOut bytes.Buffer
	reportDiagnostics(&errOut, c.Diagnostics(), false) // one repository: serve would pass false
	if errOut.String() != malformedReposStderr {
		t.Errorf("stderr = %q, want %q", errOut.String(), malformedReposStderr)
	}
}

// edgeServiceYAML and apiServiceYAML are minimal, valid Service entities,
// named for the repository each is used to stand in for below: edge is the
// fetched remote, api is what lands in the local repository after a rebuild.
const edgeServiceYAML = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: edge-gateway
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: .
`

const apiServiceYAML = `apiVersion: landsraad/v1
kind: Service
metadata:
  name: api
  owner: team-payments
  tier: 1
  lifecycle: production
spec:
  path: services/api
`

// Ruling R33: remotes are fetched once, before the first build. A rebuild
// swaps only the local filesystem. Without this, editing one character in a
// runbook costs a round trip to every repository in repos.yaml.
func TestRebuildReusesFetchedRemotes(t *testing.T) {
	remote := fstest.MapFS{
		"service.yaml": {Data: []byte(edgeServiceYAML)},
	}
	w := &workspace{
		sources: catalog.Sources{
			"platform": goodFixtureFS(t),
			"edge":     remote,
		},
		patterns: map[string][]string{"platform": {"services/*"}, "edge": {"."}},
		local:    "platform",
	}

	swapped := w.WithLocal(fstest.MapFS{"services/api/service.yaml": {Data: []byte(apiServiceYAML)}})

	// The remote value is the same object, not a re-fetch.
	before, _ := w.Sources().Get("edge")
	after, _ := swapped.Sources().Get("edge")
	if fmt.Sprintf("%p", before) != fmt.Sprintf("%p", after) {
		t.Error("WithLocal replaced the fetched remote filesystem")
	}
	// And the local one did change.
	local, ok := swapped.Sources().Get("platform")
	if !ok {
		t.Fatal("the swapped workspace has no local repository")
	}
	if _, err := fs.Stat(local, "services/api/service.yaml"); err != nil {
		t.Errorf("the swapped local filesystem does not hold the new file: %v", err)
	}
}

// A workspace with no local entry -- a platform repository holding only
// configuration -- must not crash on rebuild. There is simply nothing to
// swap.
func TestWithLocalOnAnAllRemoteWorkspace(t *testing.T) {
	w := &workspace{
		sources:  catalog.Sources{"edge": fstest.MapFS{}},
		patterns: map[string][]string{"edge": {"."}},
	}
	if got := w.WithLocal(fstest.MapFS{}); got != w {
		t.Error("WithLocal on a workspace with no local repository should return the receiver unchanged")
	}
}

// The --watch flag's help text is the only place a user learns that
// repos.yaml, like every remote it names, is read once, at startup -- so an
// edit to its paths: needs a restart just as much as an edit to a remote
// repository does. Exact-string tested per the project's rule that every
// new user-facing string ships with one -- against an independent literal,
// not against watchHelp itself: comparing the flag's Usage to the same
// constant newServeCmd registered it with proves only that cobra stores
// what it is given, and would still pass with the whole sentence below
// deleted.
func TestWatchFlagHelpAdmitsReposYAMLIsReadOnce(t *testing.T) {
	flag := newServeCmd().Flags().Lookup("watch")
	if flag == nil {
		t.Fatal("newServeCmd has no --watch flag")
	}
	want := "rebuild when a file changes. Only the local repository is watched: " +
		"remote repositories are fetched once at startup, and picking up a " +
		"change in one needs a restart. repos.yaml itself is also read only " +
		"once, so an edit to it (most often its paths:) needs a restart too"
	if flag.Usage != want {
		t.Errorf("--watch help = %q, want %q", flag.Usage, want)
	}
}

// servingLine is exact-string tested directly because newServeCmd's RunE,
// where it is actually printed, calls os.Exit and binds a real port -- not
// something a unit test can drive without spawning a subprocess and, for
// the multi-repository case, a fetch this suite has no network access to
// perform.
func TestServingLineNamesTheRepositoryCount(t *testing.T) {
	for _, tt := range []struct {
		name string
		w    *workspace
		want string
	}{
		{
			"a single repository is the common case and stays silent",
			&workspace{sources: catalog.Sources{"platform": fstest.MapFS{}}},
			"",
		},
		{
			"two or more repositories are named, so the count means something",
			&workspace{sources: catalog.Sources{"platform": fstest.MapFS{}, "edge": fstest.MapFS{}}},
			"serving 2 repositories\n",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := servingLine(tt.w); got != tt.want {
				t.Errorf("servingLine = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestServeWithoutWatchExitsWhenTheFirstBuildFails drives the real binary,
// not Serve in-process, because the exit code lives only in newServeCmd's
// RunE, which calls os.Exit directly (see binPath's doc in
// integration_test.go).
func TestServeWithoutWatchExitsWhenTheFirstBuildFails(t *testing.T) {
	dir := materialize(t, map[string]string{
		"teams.yaml": "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n",
		"services/ledger-api/service.yaml": "apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-ghost\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n",
	})
	r := run(t, dir, "serve")
	if r.exitCode != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", r.exitCode, exitValidation, r.stderr)
	}
	want := "  build failed; nothing has been rendered yet\n"
	if !strings.HasSuffix(r.stderr, want) {
		t.Errorf("stderr:\n%s\nmust end with:\n%s", r.stderr, want)
	}
	if strings.Contains(r.stderr, "serving on http://") {
		t.Error("must never announce that it is serving -- it did not start listening")
	}
}

// TestServeWithoutWatchExitsWhenReposYAMLIsMalformed is the never-had-a-good-
// build half of finding #1: a malformed repos.yaml must gate the FIRST
// build, not only a rebuild that follows a good one -- there is no
// repos.yaml at all in the sibling test above, so that one alone would not
// have caught this.
//
// Since Task 14, the gate fires in newServeCmd's RunE, from openRepos's own
// collector, before Serve is ever called -- so unlike the sibling test
// above, nothing here ever reaches a rebuild, and stderr carries the
// diagnostic and nothing else: no "build failed" line, because no build was
// attempted.
func TestServeWithoutWatchExitsWhenReposYAMLIsMalformed(t *testing.T) {
	dir := materialize(t, malformedReposFixture())
	r := run(t, dir, "serve")
	if r.exitCode != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", r.exitCode, exitValidation, r.stderr)
	}
	if r.stderr != malformedReposStderr {
		t.Errorf("stderr = %q, want %q", r.stderr, malformedReposStderr)
	}
}

// TestServeWithWatchAlsoExitsWhenReposYAMLIsMalformed is
// TestServeWithoutWatchExitsWhenReposYAMLIsMalformed's --watch counterpart,
// and the two must behave identically here.
//
// Before Task 14, --watch tolerated a bad first build (repos.yaml or
// otherwise): the server stayed up answering 503, because a later save
// could fix it (TestServeWithWatchRespondsThenRecoversAfterAFailedFirstBuild
// pins exactly that, for a broken catalog). repos.yaml is different since
// Task 14 -- it is read exactly once, before the first build, so no later
// save can ever reach it, and initialBuildFailedError's own doc comment
// says as much: "There is no later save that could fix it and nothing to
// serve." --watch must not be read as a reason to tolerate this one.
//
// Nothing here currently makes --watch special-case this gate, and nothing
// should: the risk is a future change that guards the openRepos gate with
// `if !watch`, reasoning by analogy to the broken-catalog case above. The
// sibling test above would not catch that regression, since it never
// passes --watch at all.
func TestServeWithWatchAlsoExitsWhenReposYAMLIsMalformed(t *testing.T) {
	dir := materialize(t, malformedReposFixture())
	r := run(t, dir, "serve", "--watch")
	if r.exitCode != exitValidation {
		t.Fatalf("exit = %d, want %d; stderr:\n%s", r.exitCode, exitValidation, r.stderr)
	}
	if r.stderr != malformedReposStderr {
		t.Errorf("stderr = %q, want %q", r.stderr, malformedReposStderr)
	}
}

// utcNow is the clock newServeCmd injects, spelled once so the tests exercise
// the same shape the command does.
func utcNow() time.Time { return time.Now().UTC() }

// ACCEPTED, BOUNDED LEAK — read this before adding a third.
//
// Serve has no shutdown path: it ends in http.Server.ListenAndServe, which
// only returns on an error, and the --watch goroutine and its fsnotify watcher
// live as long as the process. The two tests that call Serve in a goroutine
// (TestServeWithWatchRespondsThenRecoversAfterAFailedFirstBuild and
// TestServeWithWatchReflectsAnEditAfterAGoodBuild) therefore each leak one goroutine, one
// listener and one fsnotify file descriptor for the lifetime of the test
// binary. That is deliberate and it is affordable at two.
//
// It does not scale. A third such test means three held ports and three
// inotify/kqueue registrations in one binary, on CI runners with per-process
// fd limits, and nothing to fail loudly when it stops fitting. If a third is
// needed, give Serve a shutdown path first — a context, or an *http.Server the
// caller can Close — and convert these two to it. Do not simply add another.
//
// freeAddr reserves and immediately releases a loopback TCP port, so Serve
// can be given a real, currently-free address without hardcoding one — the
// standard trick for a test that needs to know the address before the real
// listener exists.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// pollHTTP retries an HTTP GET against url until it returns want or d
// elapses, then returns the response body. There is no channel to select on
// for "has the listener come up yet" or "has the watched rebuild landed
// yet", so this polls with a bound instead of a single sleep-and-hope.
func pollHTTP(t *testing.T, url string, want int, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var lastStatus int
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == want {
			return string(body)
		}
		lastStatus = resp.StatusCode
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("polling %s for status %d timed out: last status %d, last error %v", url, want, lastStatus, lastErr)
	return ""
}

// TestServeWithWatchRespondsThenRecoversAfterAFailedFirstBuild is the
// --watch counterpart of TestServeWithoutWatchExitsWhenTheFirstBuildFails:
// the user is mid-edit, so a later save should fix it, and the server must
// stay up to see that save arrive.
func TestServeWithWatchRespondsThenRecoversAfterAFailedFirstBuild(t *testing.T) {
	root := t.TempDir()
	writeBrokenCatalog(t, root)

	addr := freeAddr(t)
	w := buildWorkspace(t, os.DirFS(root))
	done := make(chan error, 1)
	go func() {
		done <- Serve(root, w, addr, BuildOptions{}, utcNow, true, io.Discard)
	}()

	select {
	case err := <-done:
		t.Fatalf("Serve returned early (err=%v); --watch must keep serving after a failed first build", err)
	case <-time.After(200 * time.Millisecond):
	}

	body := pollHTTP(t, "http://"+addr+"/", http.StatusServiceUnavailable, 2*time.Second)
	want := buildNeverSucceededMessage + "\n"
	if body != want {
		t.Errorf("503 body = %q, want %q", body, want)
	}

	fixBrokenCatalog(t, root)
	if body := pollHTTP(t, "http://"+addr+"/", http.StatusOK, 3*time.Second); body == "" {
		t.Error("expected a non-empty catalog page once the fix landed")
	}
}

// pollHTTPContains retries an HTTP GET against url until the 200 body
// contains want or d elapses. TestServeWithWatchRespondsThenRecoversAfter...
// above only ever needs "some page landed" or a fixed 503 string; this is
// for the case where the page changes shape between the two builds and the
// test has to recognise the second one by content, not just status.
func pollHTTPContains(t *testing.T, url, want string, d time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(d)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			last = string(body)
			if strings.Contains(last, want) {
				return last
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("polling %s for body containing %q timed out; last body:\n%s", url, want, last)
	return ""
}

// TestServeWithWatchRespondsThenRecoversAfterAFailedFirstBuild (above) proves
// the *recovery* path: a broken first build, then a save that fixes it. It
// never proves the everyday path: a first build that already succeeds, then
// an ordinary edit. That gap matters because it is a different code path
// through Serve -- rebuild's hadGoodBuild branch, not its "nothing has been
// rendered yet" branch -- and nothing else exercises it through a real
// listener, a real fsnotify event and a real debounced rebuild wired
// together the way newServeCmd actually wires them. newRebuild and
// watchDirs are covered in isolation elsewhere in this file; this is the
// composition of both, driven by Serve itself.
func TestServeWithWatchReflectsAnEditAfterAGoodBuild(t *testing.T) {
	root := t.TempDir()
	teams := "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n"
	if err := os.WriteFile(filepath.Join(root, "teams.yaml"), []byte(teams), 0o644); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(root, "services", "api")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcYAML := func(description string) []byte {
		return []byte("apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: api\n" +
			"  description: " + description + "\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n  path: services/api\n")
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yaml"), svcYAML("before the edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	addr := freeAddr(t)
	w := buildWorkspace(t, os.DirFS(root))
	done := make(chan error, 1)
	go func() {
		done <- Serve(root, w, addr, BuildOptions{}, utcNow, true, io.Discard)
	}()

	select {
	case err := <-done:
		t.Fatalf("Serve returned early (err=%v)", err)
	case <-time.After(200 * time.Millisecond):
	}

	first := pollHTTPContains(t, "http://"+addr+"/", "before the edit", 2*time.Second)
	if strings.Contains(first, "after the edit") {
		t.Fatalf("index already shows the post-edit description before any edit was made:\n%s", first)
	}

	if err := os.WriteFile(filepath.Join(svcDir, "service.yaml"), svcYAML("after the edit"), 0o644); err != nil {
		t.Fatal(err)
	}

	pollHTTPContains(t, "http://"+addr+"/", "after the edit", 3*time.Second)
}

// reportFetchFailures is shared by build and serve, and its trailer tells
// the reader to pass --allow-partial. serve answered "Error: unknown flag:
// --allow-partial" to anyone who took that advice. Advice a command cannot
// take is worse than no advice, so both halves are pinned here: the trailer
// still names the flag, and both commands still have it.
func TestBothCommandsHaveTheFlagTheRefusalTrailerAdvises(t *testing.T) {
	var errOut bytes.Buffer
	reportFetchFailures([]repoFailure{{Name: "edge", Err: errors.New("boom")}}, false, &errOut)
	if !strings.Contains(errOut.String(), "--allow-partial") {
		t.Fatalf("the refusal trailer no longer names a flag; it says %q", errOut.String())
	}
	for _, cmd := range []*cobra.Command{newBuildCmd(), newServeCmd()} {
		if cmd.Flags().Lookup("allow-partial") == nil {
			t.Errorf("%s has no --allow-partial, and the shared refusal trailer tells the reader to pass it", cmd.Name())
		}
	}
}

// The same claim against the real binary, where cobra does the parsing.
// --help is enough: an unknown flag fails before help is ever printed, and
// this way the test never binds a port.
func TestServeAcceptsAllowPartial(t *testing.T) {
	r := run(t, materialize(t, integrationFixture()), "serve", "--allow-partial", "--help")
	if r.exitCode != exitOK {
		t.Fatalf("serve --allow-partial --help exit = %d, want %d; stderr:\n%s", r.exitCode, exitOK, r.stderr)
	}
	if strings.Contains(r.stderr, "unknown flag") {
		t.Errorf("serve rejected the flag its own refusal trailer advises: %s", r.stderr)
	}
}

// serve --watch builds every rebuild from WithLocal's copy of the startup
// workspace. A copy that dropped lines would put every docs-fresh warning
// after the first rebuild back at an entry's line 0 (ruling R38).
func TestWithLocalKeepsEachRepositorysLine(t *testing.T) {
	w := &workspace{
		sources: catalog.Sources{"platform": fstest.MapFS{}},
		local:   "platform",
		lines:   map[string]int{"platform": 2, "edge-gateway": 5},
		edits:   newLastEditLog(),
	}
	rebuilt := w.WithLocal(fstest.MapFS{})
	rebuilt.RecordLastEditFailure("edge-gateway", errors.New("boom"))

	got := rebuilt.TakeLastEditFailures()
	if len(got) != 1 || got[0].Repo != "edge-gateway" || got[0].Line != 5 {
		t.Errorf("TakeLastEditFailures = %+v, want edge-gateway at line 5", got)
	}
}
