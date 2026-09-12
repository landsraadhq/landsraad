package fetch

import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
)

// FetchError's three shapes, exactly. It does not name its repository: the
// one place it is printed, cmd/'s failureMessage, already has.
func TestFetchErrorNamesWhatFailed(t *testing.T) {
	boom := errors.New("boom")
	for _, tt := range []struct {
		name string
		err  *FetchError
		want string
	}{
		{"no paths", &FetchError{Err: boom}, "cannot fetch files: boom"},
		{"one path", &FetchError{Paths: []string{"docs/index.md"}, Err: boom}, "cannot fetch docs/index.md: boom"},
		{"several paths", &FetchError{Paths: []string{"a.md", "b.md", "c.md"}, Err: boom},
			"cannot fetch 3 files, starting with a.md: boom"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// mapCache is a Cache held in memory. Locked, because fetchBlobs calls Get
// and Put from its worker goroutines and task ci runs under -race.
type mapCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (c *mapCache) Get(sha string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d, ok := c.data[sha]
	return d, ok
}

func (c *mapCache) Put(sha string, data []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[sha] = data
	return nil
}

// A cache entry whose bytes do not hash to its key — a torn write, a bad
// disk — is fetched again and overwritten, neither served nor fatal.
func TestFetchBlobsRefetchesACorruptCacheEntry(t *testing.T) {
	const body = "# Docs\n"
	sha := gitBlobSHA([]byte(body))
	cache := &mapCache{data: map[string][]byte{sha: []byte("TAMPERED")}}
	f := FromEntries([]Entry{{Path: "docs/index.md", SHA: sha, Size: int64(len(body))}})

	var gets int
	get := func(context.Context, string) ([]byte, error) { gets++; return []byte(body), nil }
	if err := fetchBlobs(context.Background(), f, []string{"docs/index.md"}, 1, cache, get); err != nil {
		t.Fatalf("fetchBlobs: %v", err)
	}
	if gets != 1 {
		t.Errorf("asked the host %d times, want 1: the cached bytes were corrupt", gets)
	}
	if got, err := fs.ReadFile(f, "docs/index.md"); err != nil || string(got) != body {
		t.Errorf("ReadFile = %q, %v; want %q", got, err, body)
	}
	if got, _ := cache.Get(sha); string(got) != body {
		t.Errorf("cache holds %q after the refetch, want %q", got, body)
	}
}

// A valid hit is ruling R27's whole point: content-addressed, so it cannot
// be stale, and it costs no request.
func TestFetchBlobsServesAValidCacheHitWithoutAsking(t *testing.T) {
	const body = "# Docs\n"
	sha := gitBlobSHA([]byte(body))
	cache := &mapCache{data: map[string][]byte{sha: []byte(body)}}
	f := FromEntries([]Entry{{Path: "docs/index.md", SHA: sha, Size: int64(len(body))}})

	get := func(context.Context, string) ([]byte, error) {
		t.Error("asked the host for a blob the cache already held")
		return nil, errors.New("unreachable")
	}
	if err := fetchBlobs(context.Background(), f, []string{"docs/index.md"}, 1, cache, get); err != nil {
		t.Fatalf("fetchBlobs: %v", err)
	}
	if got, err := fs.ReadFile(f, "docs/index.md"); err != nil || string(got) != body {
		t.Errorf("ReadFile = %q, %v; want %q", got, err, body)
	}
}

// The error reported is the one that belongs to the path reported.
//
// With one worker the order is fixed: b.md is dispatched first and fails
// first. Paths are sorted, so a.md is printed; the error printed beside it
// used to be b.md's, because it arrived first.
func TestFetchBlobsPairsTheReportedPathWithItsOwnError(t *testing.T) {
	f := FromEntries([]Entry{
		{Path: "a.md", SHA: "sha-a"},
		{Path: "b.md", SHA: "sha-b"},
	})
	get := func(_ context.Context, sha string) ([]byte, error) {
		return nil, errors.New(sha + " failed")
	}
	err := fetchBlobs(context.Background(), f, []string{"b.md", "a.md"}, 1, NopCache{}, get)
	want := "cannot fetch 2 files, starting with a.md: sha-a failed"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// A path never dispatched, because the build was cancelled, carries the
// cancellation as its error. The getter fails the way a request under a
// cancelled context does, so the message is the same whichever way the
// producer's select goes.
func TestFetchBlobsReportsCancellationForUndispatchedPaths(t *testing.T) {
	f := FromEntries([]Entry{{Path: "docs/index.md", SHA: "sha-idx"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	get := func(ctx context.Context, _ string) ([]byte, error) { return nil, ctx.Err() }

	err := fetchBlobs(ctx, f, []string{"docs/index.md"}, 1, NopCache{}, get)
	want := "cannot fetch docs/index.md: context canceled"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false for %v", err)
	}
}
