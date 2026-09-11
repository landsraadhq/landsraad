package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves a directory as if it were a GitHub repository, small
// enough that one recursive tree listing describes all of it.
//
// A real directory rather than recorded JSON, so the fixture is readable and
// editable like every other one in testdata/. The blob shas are computed the
// way git computes them, which means Task 8's verification runs for real
// here instead of being skipped by fixture shas that were never right.
//
// TLS because repos.yaml requires https, and a test that had to relax that
// rule would stop testing the rule. The caller injects srv.Client().
func fakeGitHub(t *testing.T, dir string) *httptest.Server {
	return fakeGitHubHost(t, dir, false)
}

// fakeGitHubTruncatedTree serves the same directory as a repository too big
// for one listing: the recursive request comes back truncated and the
// adapter has to descend a tree at a time (ruling R28).
//
// This is the only mode where "listed" and "exists" come apart, and it is
// therefore the only mode in which a satellite's ordinary layout —
// `paths: [services/*]` with a runbook under docs/ — is not covered by
// Open. A fixture served the complete way cannot tell whether docsDirs
// expanded anything, because FromEntries marks every directory in the
// listing and the runbook is found either way.
func fakeGitHubTruncatedTree(t *testing.T, dir string) *httptest.Server {
	return fakeGitHubHost(t, dir, true)
}

func fakeGitHubHost(t *testing.T, dir string, truncate bool) *httptest.Server {
	t.Helper()

	repo := readFixtureRepo(t, dir)
	blobs, bySHA, dirs := repo.blobs, repo.bySHA, repo.dirs

	// childrenOf is one non-recursive tree listing. GitHub reports paths
	// relative to the tree being listed, not to the repository root, which is
	// what GitHub.record joins back on; a fake returning full paths here
	// would make the descent look like it worked while recording every path
	// twice-prefixed.
	childrenOf := func(parent string) []map[string]any {
		rows := []map[string]any{}
		for _, d := range dirs {
			if path.Dir(d) == parent {
				rows = append(rows, map[string]any{"path": path.Base(d), "type": "tree", "sha": "t-" + d})
			}
		}
		for p, b := range blobs {
			if path.Dir(p) == parent {
				rows = append(rows, map[string]any{
					"path": path.Base(p), "type": "blob", "sha": b.sha, "size": len(b.data),
				})
			}
		}
		return rows
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/git/trees/"):
			// The sha is whatever follows /git/trees/. url.PathEscape turns
			// the "/" in a subtree id into %2F, and net/http hands it back
			// decoded in URL.Path, so this reads "t-docs/runbooks" whole.
			sha := r.URL.Path[strings.Index(r.URL.Path, "/git/trees/")+len("/git/trees/"):]
			if sha == "main" && r.URL.Query().Get("recursive") == "1" {
				if truncate {
					// GitHub returns what it managed plus truncated:true.
					// Returning only the flag is the same thing from the
					// adapter's side and leaves the descent the only way in.
					json.NewEncoder(w).Encode(map[string]any{"tree": []any{}, "truncated": true})
					return
				}
				rows := []map[string]any{}
				for _, d := range dirs {
					rows = append(rows, map[string]any{"path": d, "type": "tree", "sha": "t-" + d})
				}
				for p, b := range blobs {
					rows = append(rows, map[string]any{
						"path": p, "type": "blob", "sha": b.sha, "size": len(b.data),
					})
				}
				json.NewEncoder(w).Encode(map[string]any{"tree": rows, "truncated": false})
				return
			}
			parent := "."
			if sha != "main" {
				if !strings.HasPrefix(sha, "t-") {
					t.Errorf("fake host was asked for an unknown tree %q", sha)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				parent = strings.TrimPrefix(sha, "t-")
			}
			json.NewEncoder(w).Encode(map[string]any{"tree": childrenOf(parent), "truncated": false})
		case strings.Contains(r.URL.Path, "/git/blobs/"):
			sha := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			data, ok := bySHA[sha]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/commits"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"commit": map[string]any{"committer": map[string]any{"date": "2026-09-01T00:00:00Z"}}},
			})
		default:
			t.Errorf("fake host got an unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fixtureRepo is a directory described the way a git host describes a
// repository: every file with the blob sha git would give it, and every
// directory. Both fake hosts serve one.
type fixtureRepo struct {
	blobs map[string]fixtureBlob // path -> blob
	bySHA map[string][]byte      // sha  -> content
	dirs  []string
}

type fixtureBlob struct {
	sha  string
	data []byte
}

// readFixtureRepo walks dir into a fixtureRepo, computing each blob sha the
// way git does, so fetchBlobs' verification runs for real against either
// fake host.
func readFixtureRepo(t *testing.T, dir string) fixtureRepo {
	t.Helper()
	repo := fixtureRepo{blobs: map[string]fixtureBlob{}, bySHA: map[string][]byte{}}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			repo.dirs = append(repo.dirs, rel)
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha1.New()
		fmt.Fprintf(h, "blob %d\x00", len(data))
		h.Write(data)
		sha := hex.EncodeToString(h.Sum(nil))
		repo.blobs[rel] = fixtureBlob{sha, data}
		repo.bySHA[sha] = data
		return nil
	})
	if err != nil {
		t.Fatalf("building the fake host from %s: %v", dir, err)
	}
	return repo
}

// fakeGitLab serves a directory as if it were a GitLab repository, through
// the two endpoints a build with a configured ref calls: the tree listing
// and raw blobs. The project and commit endpoints are not served; a test
// that needs resolveRef or docs-fresh adds them, and until then a request
// for either fails the test rather than being answered with a guess.
//
// A listing for a path that is not a directory answers 404, as GitLab has
// since 17.7 (earlier versions answered 200 and []). Ruling R45 means the
// adapter never asks for a directory no listing has shown, so it must not
// care which; this fake takes the stricter of the two.
//
// Every listing fits one page. Pagination is TestGitLabFollowsEveryPage's
// subject, not this fixture's.
func fakeGitLab(t *testing.T, dir string) *httptest.Server {
	t.Helper()
	repo := readFixtureRepo(t, dir)
	isDir := map[string]bool{".": true}
	for _, d := range repo.dirs {
		isDir[d] = true
	}

	// rows is one tree listing in GitLab's shape: full paths from the
	// repository root, the opposite of GitHub's relative ones.
	rows := func(parent string, recursive bool) []map[string]any {
		under := func(p string) bool {
			if recursive {
				return parent == "." || strings.HasPrefix(p, parent+"/")
			}
			return path.Dir(p) == parent
		}
		out := []map[string]any{}
		for _, d := range repo.dirs {
			if under(d) {
				out = append(out, map[string]any{"id": "t-" + d, "name": path.Base(d), "type": "tree", "path": d})
			}
		}
		for p, b := range repo.blobs {
			if under(p) {
				out = append(out, map[string]any{"id": b.sha, "name": path.Base(p), "type": "blob", "path": p})
			}
		}
		return out
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/repository/tree"):
			parent := r.URL.Query().Get("path")
			if parent == "" {
				parent = "."
			}
			if !isDir[parent] {
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message":"404 Tree Not Found"}`))
				return
			}
			json.NewEncoder(w).Encode(rows(parent, r.URL.Query().Get("recursive") == "true"))
		case strings.Contains(r.URL.Path, "/repository/blobs/"):
			rest := r.URL.Path[strings.Index(r.URL.Path, "/repository/blobs/")+len("/repository/blobs/"):]
			data, ok := repo.bySHA[strings.TrimSuffix(rest, "/raw")]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write(data)
		default:
			t.Errorf("fake GitLab got an unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// multirepoRoot copies testdata/multirepo/platform into a temp directory
// with REMOTE_HOST substituted, and returns the path.
func multirepoRoot(t *testing.T, host string) string {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join("..", "..", "testdata", "multirepo", "platform")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		dst := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, []byte(strings.ReplaceAll(string(data), "REMOTE_HOST", host)), 0o644)
	})
	if err != nil {
		t.Fatalf("staging the multirepo fixture: %v", err)
	}
	return root
}
