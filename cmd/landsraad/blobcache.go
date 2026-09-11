package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/landsraadhq/landsraad/internal/fetch"
)

// A signature drift here would otherwise go unnoticed until Task 13 wires
// this into build.go — nothing else in the tree references *blobCache as a
// fetch.Cache yet.
var _ fetch.Cache = (*blobCache)(nil)

// blobCache is the on-disk half of ruling R27: a content-addressed store
// under .landsraad/cache/blobs, keyed on the git blob sha the tree listing
// returns.
//
// It lives in cmd/ because it reads and writes real files and nothing under
// internal/ may import os. internal/fetch declares the interface; this is
// the only implementation that touches a disk.
//
// Content-addressed means a hit cannot be stale: the key IS the content's
// identity, so there is no invalidation, no TTL and no revalidation request.
// It also means no automatic pruning in v1 — an unbounded directory, stated
// in the README, with `landsraad cache prune` left as an additive change
// rather than an eviction policy invented with no measurement behind it.
type blobCache struct{ root string }

func newBlobCache(root string) *blobCache { return &blobCache{root: root} }

// safeBlobPath turns a sha into a path under root, or refuses.
//
// The sha arrives from a remote host and becomes a filesystem path, which is
// exactly where CONTRIBUTING.md says the io/fs guarantee stops and a check
// belongs. A `../` in this string would otherwise let a compromised or
// simply broken host write outside the cache — the same shape that let a
// `../` line in dist/.landsraad-manifest delete the sibling it named, until
// safeManifestPath was added.
//
// Both git object-id lengths are accepted: 40 hex for SHA-1 and 64 for the
// SHA-256 object format. Anything else is not an object id, whatever it is.
func safeBlobPath(root, sha string) (string, bool) {
	if len(sha) != 40 && len(sha) != 64 {
		return "", false
	}
	for i := 0; i < len(sha); i++ {
		c := sha[i]
		if !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return "", false
		}
	}
	// Sharded by the first two characters: a flat directory with fifty
	// thousand entries is slow to list on every filesystem that matters.
	return filepath.Join(root, sha[:2], sha), true
}

// Get returns a cached blob. A miss is never an error — a cache that cannot
// answer is a slow build, not a broken one — which is why the interface has
// no error return here at all.
func (c *blobCache) Get(sha string) ([]byte, bool) {
	p, ok := safeBlobPath(c.root, sha)
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Put stores a blob.
//
// Written to a temporary file and renamed, so an interrupted build leaves
// no half-written entry behind. fetch.verifyBlob would catch one on the next
// read and refetch, but a torn file that hashes correctly by accident is not
// something to rely on being impossible.
func (c *blobCache) Put(sha string, data []byte) error {
	p, ok := safeBlobPath(c.root, sha)
	if !ok {
		return fmt.Errorf("refusing to cache under %q: not a git object id", sha)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
