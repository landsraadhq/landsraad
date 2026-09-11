package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobCacheRoundTrip(t *testing.T) {
	c := newBlobCache(t.TempDir())
	const sha = "356a192b7913b04c54574d18c28d46e6395428ab"

	if _, hit := c.Get(sha); hit {
		t.Fatal("empty cache reported a hit")
	}
	if err := c.Put(sha, []byte("hello")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, hit := c.Get(sha)
	if !hit {
		t.Fatal("Get missed after Put")
	}
	if string(got) != "hello" {
		t.Errorf("Get = %q, want %q", got, "hello")
	}
}

// The sha comes from a remote host and becomes a filesystem path. This is
// the seam CONTRIBUTING.md names: every path cmd/ turns into a filesystem
// path is checked where it is joined.
func TestBlobCacheRejectsAnythingThatIsNotASha(t *testing.T) {
	root := t.TempDir()
	for _, sha := range []string{
		"../../../etc/passwd",
		"..",
		"/absolute",
		"356a192b7913b04c54574d18c28d46e6395428ab/../../x",
		"not-hex-at-all-not-hex-at-all-not-hex-aa",
		"",
		"356a192b", // too short
	} {
		t.Run(sha, func(t *testing.T) {
			if _, ok := safeBlobPath(root, sha); ok {
				t.Errorf("safeBlobPath accepted %q", sha)
			}
			c := newBlobCache(root)
			if err := c.Put(sha, []byte("x")); err == nil {
				t.Errorf("Put accepted %q", sha)
			}
			if _, hit := c.Get(sha); hit {
				t.Errorf("Get accepted %q", sha)
			}
		})
	}
	// Nothing may have escaped the cache root.
	var escaped []string
	filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
		escaped = append(escaped, p)
		return nil
	})
	for _, p := range escaped {
		if strings.Contains(p, "passwd") {
			t.Fatalf("a write escaped the cache root: %s", p)
		}
	}
}

func TestBlobCacheAcceptsBothShaLengths(t *testing.T) {
	root := t.TempDir()
	for _, sha := range []string{
		"356a192b7913b04c54574d18c28d46e6395428ab",                         // sha-1
		"2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824", // sha-256
	} {
		if _, ok := safeBlobPath(root, sha); !ok {
			t.Errorf("safeBlobPath rejected a real object id %q", sha)
		}
	}
}

// A cache that cannot be written is a slow build, never a failed one.
func TestBlobCacheGetSurvivesAnUnreadableRoot(t *testing.T) {
	c := newBlobCache(filepath.Join(t.TempDir(), "does", "not", "exist"))
	if _, hit := c.Get("356a192b7913b04c54574d18c28d46e6395428ab"); hit {
		t.Error("Get reported a hit from a directory that does not exist")
	}
}

func TestBlobCacheShardsByPrefix(t *testing.T) {
	root := t.TempDir()
	const sha = "356a192b7913b04c54574d18c28d46e6395428ab"
	got, ok := safeBlobPath(root, sha)
	if !ok {
		t.Fatal("safeBlobPath rejected a valid sha")
	}
	want := filepath.Join(root, "35", sha)
	if got != want {
		t.Errorf("safeBlobPath = %q, want %q", got, want)
	}
}
