package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// bigRepoServer always reports the recursive listing as truncated, so every
// test here exercises the descent. Trees are addressed by sha, as GitHub
// addresses them.
func bigRepoServer(t *testing.T, listed *[]string) *httptest.Server {
	t.Helper()
	trees := map[string][]map[string]any{
		"trunk": {
			{"path": "services", "type": "tree", "sha": "t-services"},
			{"path": "vendor", "type": "tree", "sha": "t-vendor"},
			{"path": "README.md", "type": "blob", "sha": "b-readme", "size": 3},
		},
		"t-services": {
			{"path": "api", "type": "tree", "sha": "t-api"},
			{"path": "worker", "type": "tree", "sha": "t-worker"},
		},
		"t-api": {
			{"path": "service.yaml", "type": "blob", "sha": "b-api-svc", "size": 10},
			{"path": "docs", "type": "tree", "sha": "t-api-docs"},
		},
		"t-api-docs": {
			{"path": "index.md", "type": "blob", "sha": "b-api-idx", "size": 5},
			{"path": "deep", "type": "tree", "sha": "t-api-deep"},
		},
		"t-api-deep": {
			{"path": "more.md", "type": "blob", "sha": "b-more", "size": 4},
		},
		"t-worker": {
			{"path": "service.yaml", "type": "blob", "sha": "b-wk-svc", "size": 10},
		},
		"t-vendor": {},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sha := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/trees/")
		if r.URL.Query().Get("recursive") == "1" {
			// The whole-repository listing never fits.
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": true})
			return
		}
		entries, ok := trees[sha]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		*listed = append(*listed, sha)
		json.NewEncoder(w).Encode(map[string]any{"tree": entries, "truncated": false})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubDescendsWhenTruncated(t *testing.T) {
	var listed []string
	g := newTestGitHub(t, bigRepoServer(t, &listed), "trunk")

	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// The service.yaml files the patterns point at are listed.
	for _, p := range []string{"services/api/service.yaml", "services/worker/service.yaml"} {
		if _, err := fs.Stat(f, p); err != nil {
			t.Errorf("Stat %s: %v", p, err)
		}
	}
	// vendor/ was never walked, so nothing under it was listed -- and asking
	// about it must say so rather than claim the file is not there.
	if _, err := fs.Stat(f, "vendor/huge/thing.md"); !errors.Is(err, ErrNotListed) {
		t.Errorf("vendor path error = %v, want ErrNotListed", err)
	}
	sort.Strings(listed)
	if got := strings.Join(listed, ","); strings.Contains(got, "t-vendor") {
		t.Errorf("walked vendor/, which no pattern can reach: %s", got)
	}
}

// spec.docs is not known until entities are parsed, so the descent cannot
// have listed it. Expand fills it in, recursively, because a docs directory
// has subdirectories and every .md under it becomes a page.
func TestGitHubExpandListsADocsTree(t *testing.T) {
	var listed []string
	g := newTestGitHub(t, bigRepoServer(t, &listed), "trunk")
	f, err := g.Open(context.Background(), []string{"services/*"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if err := g.Expand(context.Background(), f, []string{"services/api/docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	for _, p := range []string{"services/api/docs/index.md", "services/api/docs/deep/more.md"} {
		if _, err := fs.Stat(f, p); err != nil {
			t.Errorf("Stat %s after Expand: %v", p, err)
		}
	}
}

// Expand on an already-complete listing costs nothing. This is what lets
// cmd/ call it unconditionally rather than branching on which host, which
// ref, and whether the listing happened to be truncated.
func TestGitHubExpandIsFreeOnACompleteListing(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]any{
			{"path": "docs", "type": "tree", "sha": "d"},
			{"path": "docs/index.md", "type": "blob", "sha": "i", "size": 1},
		}, "truncated": false})
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "main")
	f, err := g.Open(context.Background(), []string{"."})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	before := requests
	if err := g.Expand(context.Background(), f, []string{"docs"}); err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if requests != before {
		t.Errorf("Expand made %d requests against a complete listing, want 0", requests-before)
	}
}

// A tree request failing partway through the descent must fail Open
// outright, not return a filesystem that looks complete but silently
// stopped walking.
func TestGitHubWalkStopsOnAMidDescentError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sha := strings.TrimPrefix(r.URL.Path, "/repos/org/repo/git/trees/")
		if r.URL.Query().Get("recursive") == "1" {
			json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": true})
			return
		}
		switch sha {
		case "trunk":
			json.NewEncoder(w).Encode(map[string]any{"tree": []map[string]any{
				{"path": "services", "type": "tree", "sha": "t-services"},
			}, "truncated": false})
		case "t-services":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	g := newTestGitHub(t, srv, "trunk")
	if _, err := g.Open(context.Background(), []string{"services/*"}); err == nil {
		t.Fatal("Open with a failing mid-descent tree request returned a nil error")
	}
}
