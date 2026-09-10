package fetch

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// blobGetter fetches one blob by its sha.
type blobGetter func(ctx context.Context, sha string) ([]byte, error)

// FetchError is one repository's failed reads.
//
// It names every path rather than only the first, because --allow-partial's
// banner and the operator's next move both depend on knowing whether one
// file or forty went missing. Err is kept for classification: IsNotFound and
// IsRateLimited see through Unwrap, so cmd/ can tell "your token expired"
// from "that file is gone".
type FetchError struct {
	Repo  string
	Paths []string
	Err   error
}

func (e *FetchError) Error() string {
	if len(e.Paths) == 0 {
		return fmt.Sprintf("%s: cannot fetch files: %v", e.Repo, e.Err)
	}
	if len(e.Paths) == 1 {
		return fmt.Sprintf("%s: cannot fetch %s: %v", e.Repo, e.Paths[0], e.Err)
	}
	return fmt.Sprintf("%s: cannot fetch %d files, starting with %s: %v",
		e.Repo, len(e.Paths), e.Paths[0], e.Err)
}

func (e *FetchError) Unwrap() error { return e.Err }

// fetchBlobs loads the content of paths into f, at most parallel at a time.
//
// Shared by both adapters: they differ in how a blob is addressed and in
// nothing else that happens here. The cache is consulted first and populated
// after, keyed on the git blob sha from the tree listing — content-addressed,
// so a hit cannot be stale (ruling R27).
func fetchBlobs(ctx context.Context, repo string, f *FS, paths []string, parallel int, cache Cache, get blobGetter) error {
	if parallel < 1 {
		parallel = 1
	}
	byPath := map[string]Entry{}
	for _, e := range f.Entries() {
		byPath[e.Path] = e
	}

	type outcome struct {
		path string
		data []byte
		err  error
	}
	work := make(chan string)
	results := make(chan outcome)

	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range work {
				e, ok := byPath[p]
				if !ok {
					// The caller computed this set from f's own listing, so
					// a path that is not in it means the planner is wrong.
					results <- outcome{p, nil, fmt.Errorf("%s is not in the repository listing", p)}
					continue
				}
				// A corrupt cache entry is not fatal: it falls through and
				// is fetched again from the host, which then overwrites it.
				if cached, hit := cache.Get(e.SHA); hit && verifyBlob(e.SHA, cached) == nil {
					results <- outcome{p, cached, nil}
					continue
				}
				data, err := get(ctx, e.SHA)
				if err == nil {
					err = verifyBlob(e.SHA, data)
				}
				if err == nil {
					_ = cache.Put(e.SHA, data) // a cache that cannot write is a slow build
				}
				results <- outcome{p, data, err}
			}
		}()
	}

	go func() {
		defer close(work)
		for _, p := range paths {
			select {
			case work <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()

	seen := make(map[string]bool)
	var failed []string
	var firstErr error
	for r := range results {
		seen[r.path] = true
		if r.err != nil {
			failed = append(failed, r.path)
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		f.Put(r.path, r.data)
	}

	// Check that every requested path was accounted for. If the context was
	// cancelled, paths the producer never dispatched will be missing.
	for _, p := range paths {
		if !seen[p] {
			failed = append(failed, p)
			if firstErr == nil {
				firstErr = ctx.Err()
			}
		}
	}

	if firstErr != nil {
		// Sorted so the message is the same on every run: the worker pool
		// finishes in whatever order it finishes.
		slices.Sort(failed)
		return &FetchError{Repo: repo, Paths: failed, Err: firstErr}
	}
	return nil
}

// verifyBlob checks that data hashes to sha.
//
// Not a security control — the trust boundary in spec §14.1 is that these
// repositories are same-organisation content. It catches a truncated
// transfer and a corrupted cache entry, both of which would otherwise put
// wrong bytes in the portal with nothing to notice them by.
//
// Only SHA-1 object ids are checked. A repository using SHA-256 object
// format returns 64-character ids, and verifying those against SHA-1 would
// fail every blob in it; landsraad would rather not check than be wrong.
func verifyBlob(sha string, data []byte) error {
	if len(sha) != 40 {
		return nil
	}
	if got := gitBlobSHA(data); !strings.EqualFold(got, sha) {
		return fmt.Errorf("content does not match its sha: the host said %s, the bytes hash to %s", sha, got)
	}
	return nil
}

// gitBlobSHA is git's object id for a blob: sha1 over "blob <len>\x00" and
// the content. SHA-1 is git's choice, not a security choice.
func gitBlobSHA(data []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(data))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
