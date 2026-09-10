package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"

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

	opts := BuildOptions{
		Now:      time.Now().UTC(),
		LastEdit: gitLastEdit(root),
	}
	srv := &siteServer{}
	rebuild := newRebuild(root, opts, srv, io.Discard)

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

// TestRebuildAfterAGoodBuildKeepsServingLastGoodSite is the genuine case for
// "still serving the previous version": a good build already landed, so
// that message is true, unlike the never-built case above.
func TestRebuildAfterAGoodBuildKeepsServingLastGoodSite(t *testing.T) {
	root := t.TempDir()
	writeCatalogFixture(t, root, 1)

	srv := &siteServer{}
	var errOut bytes.Buffer
	opts := BuildOptions{Now: time.Now().UTC(), LastEdit: noLastEdit()}
	rebuild := newRebuild(root, opts, srv, &errOut)

	if ok := rebuild(""); !ok {
		t.Fatalf("the first, valid build must succeed; stderr:\n%s", errOut.String())
	}
	goodIndex, ok := srv.lookup("index.html")
	if !ok {
		t.Fatal("index.html missing after a successful build")
	}

	errOut.Reset()
	writeBrokenCatalog(t, root) // adds a second entity with an undefined owner
	if ok := rebuild(""); ok {
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
	done := make(chan error, 1)
	go func() {
		done <- Serve(root, addr, BuildOptions{Now: time.Now().UTC()}, true, io.Discard)
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
