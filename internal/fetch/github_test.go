package fetch

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// githubServer serves a fixed repository. Every test in this file runs
// against it; no test in this package ever reaches the network (spec §14).
func githubServer(t *testing.T, blobs map[string]string, truncated bool) *httptest.Server {
	t.Helper()
	tree := []map[string]any{
		{"path": "service.yaml", "type": "blob", "sha": "sha-svc", "size": 20},
		{"path": "docs", "type": "tree", "sha": "sha-docs"},
		{"path": "docs/index.md", "type": "blob", "sha": "sha-idx", "size": 9},
		{"path": "vendored", "type": "commit", "sha": "sha-sub"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "trunk"})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/trees/"):
			if r.URL.Query().Get("recursive") != "1" {
				t.Errorf("tree request was not recursive: %s", r.URL)
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": tree, "truncated": truncated})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/blobs/"):
			sha := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/blobs/")
			if got := r.Header.Get("Accept"); got != "application/vnd.github.raw+json" {
				t.Errorf("blob Accept = %q, want the raw media type", got)
			}
			body, ok := blobs[sha]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(body))
		case r.URL.Path == "/repos/org/repo/commits":
			if r.URL.Query().Get("path") == "nohistory" {
				w.Write([]byte(`[]`))
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-08-01T09:30:00Z"}}},
			})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newTestGitHub(t *testing.T, srv *httptest.Server, ref string) *GitHub {
	t.Helper()
	r, err := ParseRepo("repo", "https://github.com/org/repo", ref)
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL, Token: "t",
		AuthHeader: "Authorization", AuthPrefix: "Bearer ",
		Headers: map[string]string{"X-GitHub-Api-Version": "2026-03-10", "Accept": "application/vnd.github+json"},
		Sleep:   func(time.Duration) {},
	})
	return NewGitHub(r, c, NopCache{}, 4)
}

func TestGitHubOpenListsTheTree(t *testing.T) {
	g := newTestGitHub(t, githubServer(t, nil, false), "main")
	f, err := g.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "docs/index.md"); err != nil {
		t.Errorf("Stat docs/index.md: %v", err)
	}
	info, err := fs.Stat(f, "docs")
	if err != nil || !info.IsDir() {
		t.Errorf("docs should be a directory: info=%v err=%v", info, err)
	}
	// A submodule is a pointer to another repository with no content here.
	// Listing it as a file would make CheckFiles see a path it can never read.
	if _, err := fs.Stat(f, "vendored"); err == nil {
		t.Error("submodule (type commit) was listed as a file")
	}
}

func TestGitHubOpenResolvesTheDefaultBranch(t *testing.T) {
	var askedRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "trunk"})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/trees/"):
			askedRef = strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/trees/")
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": false})
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "") // no ref configured
	if _, err := g.Open(context.Background(), nil); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if askedRef != "trunk" {
		t.Errorf("listed ref %q, want the host's default branch %q", askedRef, "trunk")
	}
}

func TestGitHubFetchLoadsContent(t *testing.T) {
	body := "# Docs\n"
	srv := githubServer(t, map[string]string{gitBlobSHA([]byte(body)): body}, false)

	// The tree's sha must be the real one for verification to pass.
	g := newTestGitHub(t, srv, "main")
	f, err := g.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.AddDir("docs", []Entry{{Path: "docs/index.md", SHA: gitBlobSHA([]byte(body)), Size: int64(len(body))}})

	if err := g.Fetch(context.Background(), f, []string{"docs/index.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := fs.ReadFile(f, "docs/index.md")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != body {
		t.Errorf("ReadFile = %q, want %q", got, body)
	}
}

// A blob whose bytes do not hash to the sha the tree promised is corruption,
// on the wire or in the cache. Serving it would put wrong content in the
// portal with nothing to notice it by.
func TestGitHubFetchRejectsACorruptBlob(t *testing.T) {
	real := "# Docs\n"
	sha := gitBlobSHA([]byte(real))
	srv := githubServer(t, map[string]string{sha: "TAMPERED"}, false)

	g := newTestGitHub(t, srv, "main")
	f, _ := g.Open(context.Background(), nil)
	f.AddDir("docs", []Entry{{Path: "docs/index.md", SHA: sha, Size: int64(len(real))}})

	err := g.Fetch(context.Background(), f, []string{"docs/index.md"})
	if err == nil {
		t.Fatal("Fetch accepted a blob that does not match its sha")
	}
	if !strings.Contains(err.Error(), "does not match") {
		t.Errorf("error = %v, want it to name the mismatch", err)
	}
}

func TestGitHubLastEdit(t *testing.T) {
	g := newTestGitHub(t, githubServer(t, nil, false), "main")

	got, ok, err := g.LastEdit(context.Background(), "docs")
	if err != nil {
		t.Fatalf("LastEdit: %v", err)
	}
	if !ok {
		t.Fatal("LastEdit ok = false, want true")
	}
	want := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("LastEdit = %v, want %v", got, want)
	}

	// A path with no commits is "unknown", never "now" and never the zero
	// time: docs-fresh reports not-reported rather than inventing a date.
	if _, ok, err := g.LastEdit(context.Background(), "nohistory"); err != nil || ok {
		t.Errorf("LastEdit for a path with no history = (ok %v, err %v), want (false, nil)", ok, err)
	}
}

func TestGitHubLastEditIsMemoized(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode([]map[string]any{
			{"commit": map[string]any{"committer": map[string]any{"date": "2026-08-01T09:30:00Z"}}},
		})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "main")
	for range 3 {
		if _, _, err := g.LastEdit(context.Background(), "docs"); err != nil {
			t.Fatalf("LastEdit: %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("made %d requests for the same path, want 1", calls)
	}
}

func TestGitHubBaseURL(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://github.com/org/repo", "https://api.github.com"},
		{"https://ghe.internal/org/repo", "https://ghe.internal/api/v3"},
	} {
		r, err := ParseRepo("n", tt.url, "")
		if err != nil {
			t.Fatalf("ParseRepo: %v", err)
		}
		if got := GitHubBaseURL(r); got != tt.want {
			t.Errorf("GitHubBaseURL(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestFetchBlobsCancellationBeforeDispatch(t *testing.T) {
	// When the context is cancelled before any path is dispatched, fetchBlobs
	// should return an error naming the undelivered paths, not nil.
	body := "# Docs\n"
	sha := gitBlobSHA([]byte(body))
	srv := githubServer(t, map[string]string{sha: body}, false)

	g := newTestGitHub(t, srv, "main")
	f, err := g.Open(context.Background(), nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.AddDir("docs", []Entry{{Path: "docs/index.md", SHA: sha, Size: int64(len(body))}})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel before Fetch dispatches any work

	err = g.Fetch(ctx, f, []string{"docs/index.md"})
	if err == nil {
		t.Fatal("Fetch with cancelled context returned nil, want a FetchError")
	}
	if !strings.Contains(err.Error(), "docs/index.md") {
		t.Errorf("error = %v, want it to name the undelivered path", err)
	}
}

func TestGitHubConcurrentRefAccess(t *testing.T) {
	// When two methods are called concurrently on the same *GitHub with unset ref,
	// they should not race on the ref field.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/org/repo":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
		case strings.HasPrefix(r.URL.Path, "/repos/org/repo/git/trees/"):
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": false})
		case r.URL.Path == "/repos/org/repo/commits":
			json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-08-01T09:30:00Z"}}},
			})
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "") // no ref configured, will race on resolution

	var wg sync.WaitGroup
	var errOpen, errLastEdit error

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errOpen = g.Open(context.Background(), nil)
	}()
	go func() {
		defer wg.Done()
		_, _, errLastEdit = g.LastEdit(context.Background(), "file.txt")
	}()

	wg.Wait()

	if errOpen != nil {
		t.Errorf("Open: %v", errOpen)
	}
	if errLastEdit != nil {
		t.Errorf("LastEdit: %v", errLastEdit)
	}
}
