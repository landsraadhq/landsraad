package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGitHub serves a directory as if it were a GitHub repository.
//
// A real directory rather than recorded JSON, so the fixture is readable and
// editable like every other one in testdata/. The blob shas are computed the
// way git computes them, which means Task 8's verification runs for real
// here instead of being skipped by fixture shas that were never right.
//
// TLS because repos.yaml requires https, and a test that had to relax that
// rule would stop testing the rule. The caller injects srv.Client().
func fakeGitHub(t *testing.T, dir string) *httptest.Server {
	t.Helper()

	type blob struct {
		sha  string
		data []byte
	}
	blobs := map[string]blob{}   // path -> blob
	bySHA := map[string][]byte{} // sha  -> content
	var dirs []string

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
			dirs = append(dirs, rel)
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
		blobs[rel] = blob{sha, data}
		bySHA[sha] = data
		return nil
	})
	if err != nil {
		t.Fatalf("building the fake host from %s: %v", dir, err)
	}

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/git/trees/main"):
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
