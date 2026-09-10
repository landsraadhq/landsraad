package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
