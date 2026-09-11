package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func newTestGitLab(t *testing.T, srv *httptest.Server, ref string) *GitLab {
	t.Helper()
	r, err := ParseRepo("billing", "https://gitlab.com/group/sub/billing", ref)
	if err != nil {
		t.Fatalf("ParseRepo: %v", err)
	}
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL, Token: "glpat-x",
		AuthHeader: "PRIVATE-TOKEN", AuthPrefix: "",
		MaxAttempts: 1, Sleep: func(time.Duration) {},
	})
	return NewGitLab(r, c, NopCache{}, 4)
}

// The project id is the whole path, percent-encoded as one segment. Getting
// this wrong produces a 404 that looks exactly like a missing repository.
func TestGitLabEncodesTheProjectPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "")
	_, _ = g.Open(context.Background(), []string{"."})
	if want := "/projects/group%2Fsub%2Fbilling"; gotPath[:len(want)] != want {
		t.Errorf("project path = %q, want it to start with %q", gotPath, want)
	}
}

func TestGitLabSendsThePrivateTokenHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("PRIVATE-TOKEN")
		w.Write([]byte(`[]`))
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	_, _ = g.Open(context.Background(), []string{"."})
	if got != "glpat-x" {
		t.Errorf("PRIVATE-TOKEN = %q, want %q", got, "glpat-x")
	}
}

// per_page defaults to 20, so a repository with more than twenty files
// returns a first page that looks like the whole tree. Every page must be
// followed.
func TestGitLabFollowsEveryPage(t *testing.T) {
	const total = 250
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling" {
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
			return
		}
		perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		if perPage != 100 {
			t.Errorf("per_page = %d, want 100", perPage)
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		start := (page - 1) * perPage
		var rows []map[string]any
		for i := start; i < min(start+perPage, total); i++ {
			rows = append(rows, map[string]any{
				"id": fmt.Sprintf("blob-%d", i), "name": fmt.Sprintf("f%d.md", i),
				"type": "blob", "path": fmt.Sprintf("f%d.md", i),
			})
		}
		if start+perPage < total {
			w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
		}
		json.NewEncoder(w).Encode(rows)
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := fs.Stat(f, "f249.md"); err != nil {
		t.Errorf("Stat of the last file across three pages: %v", err)
	}
	if got := len(f.Entries()); got != total {
		t.Errorf("listed %d entries, want %d", got, total)
	}
}

func TestGitLabFetchAndLastEdit(t *testing.T) {
	body := "# Billing\n"
	sha := gitBlobSHA([]byte(body))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/tree":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": sha, "name": "runbook.md", "type": "blob", "path": "runbook.md"},
			})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/blobs/"+sha+"/raw":
			w.Write([]byte(body))
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/commits":
			json.NewEncoder(w).Encode([]map[string]any{{"committed_date": "2026-07-15T11:00:00+02:00"}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := g.Fetch(context.Background(), f, []string{"runbook.md"}); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	got, err := fs.ReadFile(f, "runbook.md")
	if err != nil || string(got) != body {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}

	edited, ok, err := g.LastEdit(context.Background(), "runbook.md")
	if err != nil || !ok {
		t.Fatalf("LastEdit = (%v, %v, %v)", edited, ok, err)
	}
	want := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	if !edited.Equal(want) {
		t.Errorf("LastEdit = %v, want %v (the offset must be honoured)", edited, want)
	}
}

// When two methods are called concurrently on the same *GitLab with an
// unset ref, they must not race on the ref field. This is the same
// regression a prior review caught on GitHub — resolveRef must return the
// ref rather than callers reading g.ref directly.
func TestGitLabConcurrentRefAccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling":
			json.NewEncoder(w).Encode(map[string]any{"default_branch": "main"})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/tree":
			json.NewEncoder(w).Encode([]map[string]any{})
		case r.URL.EscapedPath() == "/projects/group%2Fsub%2Fbilling/repository/commits":
			json.NewEncoder(w).Encode([]map[string]any{
				{"committed_date": "2026-08-01T09:30:00Z"},
			})
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitLab(t, srv, "") // no ref configured, will race on resolution

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

func TestGitLabBaseURL(t *testing.T) {
	for _, tt := range []struct{ url, want string }{
		{"https://gitlab.com/group/project", "https://gitlab.com/api/v4"},
		{"https://gl.internal/group/project", "https://gl.internal/api/v4"},
	} {
		r, err := ParseRepo("n", tt.url, "")
		if err != nil {
			t.Fatalf("ParseRepo: %v", err)
		}
		if got := GitLabBaseURL(r); got != tt.want {
			t.Errorf("GitLabBaseURL(%s) = %q, want %q", tt.url, got, tt.want)
		}
	}
}
