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
//
// The proof of containment is a canary planted at the exact location an
// UNGUARDED join would write to — not a walk over root. filepath.WalkDir(root,
// …) can only ever enumerate paths inside root, so it can never observe an
// escape: a real escape, by definition, lands somewhere WalkDir never visits.
// Computing the canary's path with the same filepath.Join an unvalidated
// implementation would use means the test doesn't depend on guessing
// t.TempDir()'s nesting depth on any given machine.
func TestBlobCacheRejectsAnythingThatIsNotASha(t *testing.T) {
	base := t.TempDir()
	// root is nested several levels under base, deliberately, so every
	// traversal below resolves to somewhere still inside base rather than
	// wherever an unbounded "../../.." happens to land on the real
	// filesystem. The canary must live somewhere this test controls.
	root := filepath.Join(base, "n1", "n2", "n3", "n4", "cache")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	// "../../../etc/passwd" is the one input below whose traversal depth
	// (three "..") actually reaches outside root at this nesting. Its
	// unguarded destination — filepath.Join(root, sha), the same join
	// safeBlobPath performs after validation — is exactly where the canary
	// goes.
	const escapeSha = "../../../etc/passwd"
	canaryPath := filepath.Join(root, escapeSha)
	if !strings.HasPrefix(canaryPath, base+string(filepath.Separator)) {
		t.Fatalf("test bug: canary path %q would fall outside this test's own sandbox %q", canaryPath, base)
	}
	if strings.HasPrefix(canaryPath, root+string(filepath.Separator)) {
		t.Fatalf("test bug: canary path %q is not actually outside root %q", canaryPath, root)
	}
	canaryDir := filepath.Dir(canaryPath)
	if err := os.MkdirAll(canaryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const canaryContent = "canary: unmodified\n"
	if err := os.WriteFile(canaryPath, []byte(canaryContent), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(canaryDir)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct{ sha, wantPut string }{
		{"../../../etc/passwd", `refusing to cache under "../../../etc/passwd": not a git object id`},
		{"..", `refusing to cache under "..": not a git object id`},
		{"/absolute", `refusing to cache under "/absolute": not a git object id`},
		{"356a192b7913b04c54574d18c28d46e6395428ab/../../x",
			`refusing to cache under "356a192b7913b04c54574d18c28d46e6395428ab/../../x": not a git object id`},
		{"not-hex-at-all-not-hex-at-all-not-hex-aa",
			`refusing to cache under "not-hex-at-all-not-hex-at-all-not-hex-aa": not a git object id`},
		{"", `refusing to cache under "": not a git object id`},
		{"356a192b", `refusing to cache under "356a192b": not a git object id`}, // too short
	} {
		t.Run(tt.sha, func(t *testing.T) {
			if _, ok := safeBlobPath(root, tt.sha); ok {
				t.Errorf("safeBlobPath accepted %q", tt.sha)
			}
			c := newBlobCache(root)
			if err := c.Put(tt.sha, []byte("x")); err == nil || err.Error() != tt.wantPut {
				t.Errorf("Put(%q) = %v, want %q", tt.sha, err, tt.wantPut)
			}
			if _, hit := c.Get(tt.sha); hit {
				t.Errorf("Get accepted %q", tt.sha)
			}
		})
	}

	// The canary is the actual proof: Put writes a temp file into the
	// target's directory and renames it onto the target, so an escape would
	// either overwrite the canary's content or leave a stray temp file
	// beside it.
	got, err := os.ReadFile(canaryPath)
	if err != nil {
		t.Fatalf("canary file at %s disappeared: %v", canaryPath, err)
	}
	if string(got) != canaryContent {
		t.Fatalf("a write escaped the cache root: canary at %s changed from %q to %q", canaryPath, canaryContent, got)
	}
	after, err := os.ReadDir(canaryDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a write escaped the cache root: %s now has %d entries beside the canary, had %d", canaryDir, len(after), len(before))
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
