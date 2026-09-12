# Carryall follow-ups: hygiene Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close every item in the spec's "Hygiene: no decision needed" section, then close the follow-ups file itself.

**Architecture:** Each item is small and independent. The fetch-client items come first because they share one file (`internal/fetch/client.go`); the rest follow in any order a reviewer likes, except that Task 5 changes `FetchError`'s message before Task 6 pins it, and Task 14 comes last because it records where everything went. Nothing here changes a user-facing exit code, check id or schema: those are the behaviour plan's.

**Tech Stack:** Go 1.26, go-task, `net/http/httptest`, `github.com/google/go-cmp/cmp`, `testing/fstest`.

**Spec:** `docs/superpowers/specs/2026-09-11-carryall-followups-design.md`, sections "Hygiene: no decision needed", "Not done", "Order" step 4–5 and "What every task inherits".

**Runs after** the behaviour plan (R40, R45, R36–R44), on the same branch, `carryall-followups`. Where the behaviour plan changes a file, this plan names symbols rather than line numbers. It relies on these results of the behaviour plan:

- `internal/fetch/client.go`: `ClientOptions` has `Now func() time.Time`, defaulting to `time.Now` in `NewClient` like `Sleep`. `Get` returns without sleeping when the error `IsRateLimited`, `RateReset` is non-zero, and **no remaining retry** could fire after `RateReset` — the decision is made on `remainingBackoff(attempt, maxAttempts, err)`, the sum of every wait still to come, not on the next wait alone. `MaxAttempts` still exists. Note for Task 2: `remainingBackoff` takes the attempt budget as a parameter, so `Get` now names `c.maxAttempts` in **three** places, not two.
- `cmd/landsraad/build.go`: `repoFailure` has no `URL` field; it keeps `Name`, `Line`, `Kind`, `Err`. `failureMessage` is unchanged. `reportFetchFailures` prints `"%s: repos.yaml:%d: %s\n"` (level, line, message) when `Line > 0`, else `"%s: %s\n"`.
- `internal/fetch/gitlab.go`: `Open` and `Expand` rewritten per R45; `literalPrefixes` unchanged; `ancestorsOf` (in `githubwalk.go`) now used by GitLab too.
- `internal/fetch/fs.go`: `NewFS` marks nothing listed; `FromEntries` marks `"."`.

## Global Constraints

- Build, test and lint through go-task: `task test` (runs `-race`), `task lint`, `task ci`. Never `make`.
- No non-test file under `internal/` imports `os` or any `os/*` package.
- No `sync.Once`, no `init()` below `cmd/`.
- Every diagnostic and error message a test touches is asserted with an exact string (`got != want`), never `strings.Contains`.
- Only `internal/fetch` imports `net/http` or `net`.
- Go stays gofmt-clean and vet-clean.
- `task ci` runs under `-race`, so a fake shared by `fetchBlobs`' workers needs a lock.
- The failing test lands before the fix. Where a task pins behaviour already believed correct, it has no red step; instead it proves the test can fail with a stated mutation, then reverts the mutation.
- Commits: `git add` each file by name, never `git add .`. Messages follow the repository's style (`fix(fetch): …`, `test: …`, `docs: …`, `refactor: …`, lowercase). No AI attribution lines.
- In shell commands, quote every glob and every `-run` regex: the shell is zsh with NOMATCH.

## Files

| File | Tasks | Responsibility |
|---|---|---|
| `internal/fetch/client.go` | 1, 2, 3, 4 (mutation only) | the HTTP client: summarising, retry budget, size limit |
| `internal/fetch/client_test.go` | 1, 2, 3, 4 | client tests |
| `internal/fetch/blobs.go` | 5, 6 | `FetchError`, `fetchBlobs` |
| `internal/fetch/blobs_test.go` (new) | 5, 6 | `FetchError` and `fetchBlobs` tests, with a locked in-memory cache |
| `internal/fetch/github.go`, `internal/fetch/gitlab.go` | 5, 11 | `fetchBlobs` call sites; `GitHub`'s field comments |
| `internal/fetch/github_test.go`, `internal/fetch/gitlab_test.go` | 2, 7 | test client literals; `literalPrefixes` table |
| `internal/fetch/githubwalk_test.go` | 7 | `ancestorsOf` table |
| `internal/fetch/fetch.go` | 11 | `Cache`'s contract |
| `internal/render/docs.go` | 11 | `docsFor`'s comment |
| `cmd/landsraad/build_test.go` | 5, 9, 12 | `failureMessage`/`reportFetchFailures` tests; `multiLastEdit` routing; fixture |
| `cmd/landsraad/lastedit_test.go` | 9 | `multiLastEdit` against real git |
| `cmd/landsraad/blobcache.go`, `blobcache_test.go` | 8 | comments; exact `Put` refusals |
| `cmd/landsraad/repos.go`, `repos_test.go` | 10, 13 | `tokenVarName`'s comment and test; `expanded` |
| `cmd/landsraad/serve.go` | 11 | the watcher's name |
| `cmd/landsraad/integration_test.go`, `serve_test.go` | 2, 12 | a stale comment; the shared fixture |
| `README.md` | 10 | `tokenVarName`'s wording |
| `docs/superpowers/plans/2026-09-11-carryall-followups.md` | 14 | closed |

---

### Task 1: `summarise` never cuts a character in half

**Files:**
- Modify: `internal/fetch/client.go` (`summarise`, and the import block)
- Test: `internal/fetch/client_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: `summarise(body string) string`, unchanged in signature.

- [ ] **Step 1: Write the failing test**

Add `"unicode/utf8"` to `client_test.go`'s import block, then append:

```go
// A diagnostic is printed to a terminal and embedded in JSON, and a byte
// slice through the middle of a character is valid in neither. The cut was
// a bare s[:200], so an error body with an em dash straddling byte 200 came
// back with half the dash and then "…".
func TestSummariseNeverSplitsACharacter(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{
			// "—" is three bytes, at 199, 200 and 201: a cut at 200 lands inside it.
			"a character straddling the cut is dropped whole",
			strings.Repeat("a", 199) + "—" + strings.Repeat("b", 10),
			strings.Repeat("a", 199) + "…",
		},
		{"ASCII is cut at exactly 200 bytes", strings.Repeat("a", 250), strings.Repeat("a", 200) + "…"},
		{"a short body is untouched", "Not Found", "Not Found"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := summarise(tt.body)
			if got != tt.want {
				t.Errorf("summarise = %q, want %q", got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Errorf("summarise returned invalid UTF-8: %q", got)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/fetch/ -run 'TestSummariseNeverSplitsACharacter' -count=1`
Expected: FAIL in `a_character_straddling_the_cut_is_dropped_whole`, with `got` ending in `a\xe2…` and `summarise returned invalid UTF-8`. The other two subtests pass.

- [ ] **Step 3: Back off to a character start**

Add `"unicode/utf8"` to `client.go`'s import block (after `"time"`). Replace `summarise` with:

```go
// summarise trims a response body to something printable. A host's error
// body can be a full HTML page, and a diagnostic is one line.
//
// The cut backs off to the start of a character. A bare s[:200] can end
// inside a multi-byte one, and a diagnostic is printed to a terminal and
// embedded in JSON, neither of which should carry half a character.
func summarise(body string) string {
	s := strings.TrimSpace(body)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		cut := 200
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	if s == "" {
		s = "(empty response body)"
	}
	return s
}
```

- [ ] **Step 4: Run it and watch it pass, with the token tests**

Run: `go test ./internal/fetch/ -run 'TestSummariseNeverSplitsACharacter|TestClientErrorsNeverCarryTheToken' -count=1`
Expected: PASS. `token_straddling_truncation_boundary` is all ASCII, so its cut is unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/client.go internal/fetch/client_test.go
git commit -m "fix(fetch): summarise never cuts a character in half"
```

---

### Task 2: `MaxAttempts` becomes the constant production always used

Nothing in production ever set `ClientOptions.MaxAttempts`; `NewClient` turned zero into 3. Only tests set it, three of them to 1, so those tests exercised a retry budget production never has. A knob with no production consumer is the thing this project's composition rules say not to keep.

**Files:**
- Modify: `internal/fetch/client.go` (`ClientOptions`, `Client`, `NewClient`, `Get`; new const beside `backoff`)
- Modify: `internal/fetch/client_test.go` (`testClient`, `TestClientErrorsNeverCarryTheToken/transport_error_path`, `TestGetDoesNotRetryACancelledRequest`)
- Modify: `internal/fetch/github_test.go` (`newTestGitHub`), `internal/fetch/gitlab_test.go` (`newTestGitLab`)
- Modify: `cmd/landsraad/integration_test.go` (the comment in `TestBuildWithAnUnreachableRemote` that names `MaxAttempts: 1`)

**Interfaces:**
- Consumes: the behaviour plan's `Get` (with its R44 early return).
- Produces: `ClientOptions` without `MaxAttempts`; unexported `const maxAttempts = 3` in package `fetch`. Tasks 3 and 4 construct clients without `MaxAttempts`.

- [ ] **Step 1: Remove `MaxAttempts` from every test literal**

Find every one, including any the behaviour plan added:

Run: `grep -rn 'MaxAttempts' --include='*.go' .`
Expected: `client.go` (field and `NewClient`), `client_test.go` (three literals), `github_test.go` (`newTestGitHub`), `gitlab_test.go` (`newTestGitLab`), the comment in `integration_test.go`, and possibly literals the behaviour plan added.

Delete the `MaxAttempts: N,` entry from every test literal (keep each literal's `Sleep`). In `newTestGitHub` and `newTestGitLab` the line reads `MaxAttempts: 1, Sleep: func(time.Duration) {},` and becomes `Sleep: func(time.Duration) {},`.

In `integration_test.go`, replace

```go
	// This is what proves the retry path actually ran rather than being
	// short-circuited by MaxAttempts: 1 or a Sleep that was never wired up.
```

with

```go
	// This is what proves the retry path actually ran rather than being
	// short-circuited by a Sleep that was never wired up.
```

- [ ] **Step 2: Run everything that builds a client, before touching `client.go`**

Run: `go test ./internal/fetch/ ./cmd/landsraad/ -count=1`
Expected: PASS. Every literal that set 1 now gets 3 from `NewClient`'s default, and every one injects a no-op `Sleep`, so nothing waits. If a test fails because it counted requests against a 5xx route, it was relying on `MaxAttempts: 1`: change its expected count to 3 and say so in the commit message.

- [ ] **Step 3: Make it a constant**

In `client.go`:

1. Delete the `MaxAttempts int` line from `ClientOptions`.
2. Delete the `maxAttempts int` line from `Client`.
3. In `NewClient`, delete `maxAttempts: o.MaxAttempts,` from the struct literal, and delete the block

   ```go
   	if c.maxAttempts < 1 {
   		c.maxAttempts = 3
   	}
   ```
4. In `Get`, replace both `c.maxAttempts` with `maxAttempts`.
5. Immediately above `// backoff is 1s, 2s, 4s, unless the host said how long to wait.`, add:

```go
// maxAttempts is how many times a retryable request is tried: once, then
// twice more after backoff's 1s and 2s. A constant, not an option: nothing
// in production ever set it, and a knob only tests turn lets a test pass
// against a retry path production never takes.
const maxAttempts = 3
```

- [ ] **Step 4: Verify**

Run: `grep -rn 'MaxAttempts\|c\.maxAttempts' --include='*.go' .`
Expected: no output.

Run: `go vet ./... && go test ./internal/fetch/ ./cmd/landsraad/ -count=1`
Expected: PASS. `TestClientRetriesServerErrorsThenGivesUp`'s "want 3" now tests the constant production uses.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/client.go internal/fetch/client_test.go internal/fetch/github_test.go internal/fetch/gitlab_test.go cmd/landsraad/integration_test.go
git commit -m "refactor(fetch): MaxAttempts becomes the constant production always used"
```

---

### Task 3: A response over 64 MB says so, once

`io.LimitReader` stopped at 64 MB and returned no error, so a 65 MB blob arrived truncated and failed its sha check. The user read `content does not match its sha: the host said …, the bytes hash to …`, a corruption message, for a size limit. Tree listings cannot reach the limit (GitHub truncates at 7 MB, GitLab pages at 100 rows); blobs can, up to GitHub's 100 MB. The trap: `retryable` treats every non-status error as transient, so an unexcluded limit error would download 64 MB three times.

**Files:**
- Modify: `internal/fetch/client.go` (`once`, `retryable`; new const and error beside `summarise`)
- Test: `internal/fetch/client_test.go`

**Interfaces:**
- Consumes: `testClient` (without `MaxAttempts`, from Task 2).
- Produces: unexported `const maxResponseBytes = 64 << 20` and `var errTooLarge`; a 2xx response over the limit returns `GET <endpoint>: response larger than 64 MB`.

- [ ] **Step 1: Write the failing test**

Append to `client_test.go`:

```go
// A response over the limit is refused by name, once.
//
// io.LimitReader used to stop at 64 MB and say nothing, so a 65 MB blob
// arrived truncated and failed its sha check: a corruption message for a
// size limit. And retryable treats any non-status error as transient, so
// the refusal must be excluded or it downloads 64 MB three times.
//
// Two real 64 MB responses, deliberately. The limit is a constant with no
// test knob (see maxAttempts for why), and the exact boundary is where an
// off-by-one would hide.
func TestClientRefusesAResponseOverTheLimit(t *testing.T) {
	for _, tt := range []struct {
		name    string
		size    int
		wantErr string
	}{
		{"exactly the limit is accepted", maxResponseBytes, ""},
		{"one byte over is refused", maxResponseBytes + 1, "GET /big: response larger than 64 MB"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Write(make([]byte, tt.size))
			})
			body, _, err := c.Get(context.Background(), "/big", nil, "")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if len(body) != tt.size {
					t.Errorf("len(body) = %d, want %d", len(body), tt.size)
				}
				return
			}
			if err == nil || err.Error() != tt.wantErr {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
			if calls != 1 {
				t.Errorf("made %d requests, want 1: a response too large once is too large every time", calls)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/fetch/ -run 'TestClientRefusesAResponseOverTheLimit' -count=1`
Expected: build failure, `undefined: maxResponseBytes`. That is the red state: the limit has no name yet.

- [ ] **Step 3: Name the limit and refuse past it**

In `client.go`, immediately above `// summarise trims a response body`, add:

```go
// maxResponseBytes caps one response. A tree listing never gets near it —
// GitHub truncates a recursive listing at 7 MB and GitLab pages at 100 rows
// — so in practice this is a blob, which GitHub serves up to 100 MB.
const maxResponseBytes = 64 << 20

// errTooLarge is a response that did not fit under maxResponseBytes.
//
// The limit used to truncate silently, and the truncated blob then failed
// its sha check, so a size limit reached the user as corruption. It is not
// retryable: asking again downloads the same bytes to be refused the same
// way.
var errTooLarge = fmt.Errorf("response larger than %d MB", maxResponseBytes>>20)
```

In `once`, replace

```go
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode/100 != 2 {
		return nil, nil, c.statusError(resp, endpoint, body)
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("GET %s: cannot read the response: %w", endpoint, readErr)
	}
	return body, resp.Header, nil
```

with

```go
	// One byte past the limit, so a response that fills it exactly is told
	// apart from one that did not fit.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if resp.StatusCode/100 != 2 {
		return nil, nil, c.statusError(resp, endpoint, body)
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("GET %s: cannot read the response: %w", endpoint, readErr)
	}
	if len(body) > maxResponseBytes {
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, errTooLarge)
	}
	return body, resp.Header, nil
```

In `retryable`, replace

```go
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
```

with

```go
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, errTooLarge)
	}
```

and extend `retryable`'s doc comment's last sentence to: `A 404 is an answer and a 401 is a decision; repeating either spends somebody's quota to be told the same thing three times. So is a response too large to accept.`

- [ ] **Step 4: Run it and watch it pass, then prove the exclusion matters**

Run: `go test ./internal/fetch/ -run 'TestClientRefusesAResponseOverTheLimit' -count=1`
Expected: PASS (a few seconds: two 64 MB bodies).

Mutation: delete the `&& !errors.Is(err, errTooLarge)` clause from `retryable` and rerun the same command.
Expected: FAIL in `one_byte_over_is_refused`: `made 3 requests, want 1`. Restore the clause, rerun, PASS.

Run: `go test ./internal/fetch/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/client.go internal/fetch/client_test.go
git commit -m "fix(fetch): a response over 64 MB says so, once, instead of failing its sha"
```

---

### Task 4: Pin the client behaviour nothing asserted

Five behaviours believed correct and never asserted: a trailing-slash `BaseURL`, query encoding, `StatusError` leaving the query out, a 403 with quota left being tried once, and a 429's `Retry-After`. The 403-for-a-spent-quota case is the behaviour plan's (R44) and is not repeated here.

**Files:**
- Test: `internal/fetch/client_test.go`

**Interfaces:**
- Consumes: `NewClient`, `ClientOptions` (without `MaxAttempts`), `testClient`, `IsRateLimited`.
- Produces: nothing.

- [ ] **Step 1: Write the tests**

Add `"net/url"` and `"slices"` to `client_test.go`'s import block, then append:

```go
// NewClient trims a trailing slash from BaseURL, so a base written with one
// does not put a doubled slash into every request path.
func TestClientJoinsATrailingSlashBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{HTTP: srv.Client(), BaseURL: srv.URL + "/", Sleep: func(time.Duration) {}})

	if _, _, err := c.Get(context.Background(), "/repos/o/r", nil, ""); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/repos/o/r" {
		t.Errorf("server saw path %q, want %q", gotPath, "/repos/o/r")
	}
}

// The query is url.Values' encoding — sorted by key, a space as "+" — and
// the path the server sees is the endpoint alone.
func TestClientEncodesTheQuery(t *testing.T) {
	var gotPath, gotQuery string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`{}`))
	})
	q := url.Values{"b": {"2"}, "a": {"1 2"}}
	if _, _, err := c.Get(context.Background(), "/x", q, ""); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotPath != "/x" || gotQuery != "a=1+2&b=2" {
		t.Errorf("server saw %q with query %q, want %q with %q", gotPath, gotQuery, "/x", "a=1+2&b=2")
	}
}

// StatusError's doc comment promises Endpoint is the path only, never the
// full URL with its query: the error is printed to users, and a query is
// where a future change would put something that must not be.
func TestStatusErrorLeavesOutTheQuery(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	_, _, err := c.Get(context.Background(), "/x", url.Values{"ref": {"main"}}, "")
	want := "GET /x: HTTP 404: (empty response body)"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// A 403 with quota left is a permission, not a rate limit, and a permission
// does not change if asked again: one request, no waiting.
func TestClientDoesNotRetryAForbiddenWithQuotaLeft(t *testing.T) {
	var calls int
	var slept []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL,
		Sleep: func(d time.Duration) { slept = append(slept, d) },
	})

	if _, _, err := c.Get(context.Background(), "/x", nil, ""); err == nil {
		t.Fatal("Get succeeded on a 403")
	}
	if calls != 1 || len(slept) != 0 {
		t.Errorf("calls = %d, slept %v; want 1 call and no waiting", calls, slept)
	}
}

// A 429's Retry-After is how long the host asked us to wait, and backoff
// uses it instead of its own 1s, 2s. No test reached that branch before.
// No X-RateLimit-Reset here, so R44's early return does not apply.
func TestClientWaitsWhatA429Asks(t *testing.T) {
	var calls int
	var slept []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	c := NewClient(ClientOptions{
		HTTP: srv.Client(), BaseURL: srv.URL,
		Sleep: func(d time.Duration) { slept = append(slept, d) },
	})

	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if !IsRateLimited(err) {
		t.Fatalf("IsRateLimited(%v) = false, want true", err)
	}
	if calls != 3 {
		t.Errorf("made %d requests, want 3", calls)
	}
	if want := []time.Duration{7 * time.Second, 7 * time.Second}; !slices.Equal(slept, want) {
		t.Errorf("slept %v, want %v", slept, want)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/fetch/ -run 'TestClientJoinsATrailingSlashBaseURL|TestClientEncodesTheQuery|TestStatusErrorLeavesOutTheQuery|TestClientDoesNotRetryAForbiddenWithQuotaLeft|TestClientWaitsWhatA429Asks' -count=1 -v`
Expected: PASS, all five. If one fails, stop: the behaviour was believed correct and is not, which is a finding for Q, not a test to adjust.

- [ ] **Step 3: Prove each can fail**

Apply one mutation to `client.go` at a time, run the Step 2 command, check the named test fails, then restore:

| Mutation in `client.go` | Test that must fail |
|---|---|
| In `NewClient`, `base: strings.TrimSuffix(o.BaseURL, "/")` → `base: o.BaseURL` | `TestClientJoinsATrailingSlashBaseURL` (server saw `//repos/o/r`) |
| In `once`, `c.statusError(resp, endpoint, body)` → `c.statusError(resp, target, body)` | `TestStatusErrorLeavesOutTheQuery` |
| In `retryable`, `return se.Status/100 == 5 \|\| IsRateLimited(err)` → `return se.Status/100 == 5 \|\| se.Status == 403 \|\| IsRateLimited(err)` | `TestClientDoesNotRetryAForbiddenWithQuotaLeft` |
| In `backoff`, delete the `if errors.As(err, &se) && se.RetryAfter > 0 { … }` block | `TestClientWaitsWhatA429Asks` (slept `[1s 2s]`) |

After the last one: `git diff --stat` must show only `internal/fetch/client_test.go`. If `client.go` appears, run `git diff internal/fetch/client.go`, confirm it is only a leftover mutation, and `git checkout -- internal/fetch/client.go`.

- [ ] **Step 4: Commit**

```bash
git add internal/fetch/client_test.go
git commit -m "test(fetch): pin the client behaviour nothing asserted"
```

---

### Task 5: A failed blob names its repository once (N7)

`failureMessage`'s default branch printed `cannot read edge-gateway: edge-gateway: cannot fetch docs/index.md: …`: it names the repository, and so did the `FetchError` it wraps. `FetchError.Repo` is read by nothing except `Error()`, and the only place a `FetchError` reaches a user is through `failureMessage`, so the field goes, and with it `fetchBlobs`' `repo` parameter.

**Files:**
- Modify: `internal/fetch/blobs.go` (`FetchError`, `(*FetchError).Error`, `fetchBlobs`' signature and its `return &FetchError{…}`)
- Modify: `internal/fetch/github.go` (`(*GitHub).Fetch`), `internal/fetch/gitlab.go` (`(*GitLab).Fetch`)
- Create: `internal/fetch/blobs_test.go`
- Test: `cmd/landsraad/build_test.go` (`TestFailureMessage`, new `TestReportFetchFailuresNamesAFailedBlobsRepositoryOnce`)

**Interfaces:**
- Consumes: the behaviour plan's `repoFailure{Name, Line, Kind, Err}` and `reportFetchFailures` line prefix.
- Produces: `type FetchError struct { Paths []string; Err error }`, whose `Error()` is `cannot fetch files: <err>`, `cannot fetch <path>: <err>`, or `cannot fetch <n> files, starting with <path>: <err>`. `func fetchBlobs(ctx context.Context, f *FS, paths []string, parallel int, cache Cache, get blobGetter) error`. Task 6 uses both.

- [ ] **Step 1: Write the failing tests**

Create `internal/fetch/blobs_test.go`:

```go
package fetch

import (
	"errors"
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
```

In `cmd/landsraad/build_test.go`, add this row as the last entry of `TestFailureMessage`'s table:

```go
		// A FetchError used to name its repository too, so this line said
		// it twice: "cannot read edge: edge: cannot fetch docs/index.md: boom".
		{
			"a failed blob names the repository once",
			repoFailure{Name: "edge", Kind: "github", Err: &fetch.FetchError{
				Paths: []string{"docs/index.md"}, Err: errors.New("boom"),
			}},
			"cannot read edge: cannot fetch docs/index.md: boom",
		},
```

and append after `TestReportFetchFailuresRefusalTrailerIsPlural`:

```go
// The whole line a user reads for a failed blob, with R38's repos.yaml line
// in front of it: the repository named once.
func TestReportFetchFailuresNamesAFailedBlobsRepositoryOnce(t *testing.T) {
	fails := []repoFailure{{
		Name: "edge-gateway", Line: 4, Kind: "github",
		Err: &fetch.FetchError{Paths: []string{"docs/index.md"}, Err: errors.New("boom")},
	}}
	var errOut bytes.Buffer
	if code := reportFetchFailures(fails, false, &errOut); code != exitUsage {
		t.Errorf("code = %d, want %d", code, exitUsage)
	}
	want := "error: repos.yaml:4: cannot read edge-gateway: cannot fetch docs/index.md: boom\n" +
		"refusing to render a portal that is missing 1 repository; " +
		"pass --allow-partial to render one anyway, with a banner saying so\n"
	if got := errOut.String(); got != want {
		t.Errorf("errOut = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/fetch/ -run 'TestFetchErrorNamesWhatFailed' -count=1`
Expected: FAIL, all three subtests, e.g. `Error() = ": cannot fetch files: boom", want "cannot fetch files: boom"`: the empty `Repo` still prints its `": "`.

Run: `go test ./cmd/landsraad/ -run 'TestFailureMessage|TestReportFetchFailuresNamesAFailedBlobsRepositoryOnce' -count=1`
Expected: FAIL, with `cannot read edge: : cannot fetch docs/index.md: boom` in the output.

- [ ] **Step 3: Drop the field and the parameter**

In `blobs.go`, replace the `FetchError` type, its doc comment and its `Error` method with:

```go
// FetchError is one repository's failed reads.
//
// It names every path rather than only the first, because --allow-partial's
// banner and the operator's next move both depend on knowing whether one
// file or forty went missing. Err is kept for classification: IsNotFound and
// IsRateLimited see through Unwrap, so cmd/ can tell "your token expired"
// from "that file is gone".
//
// It does not name the repository. The one place that prints it,
// failureMessage, already opens with "cannot read <name>:", and a Repo field
// here made that line say the name twice: "cannot read edge-gateway:
// edge-gateway: cannot fetch …".
type FetchError struct {
	Paths []string
	Err   error
}

func (e *FetchError) Error() string {
	if len(e.Paths) == 0 {
		return fmt.Sprintf("cannot fetch files: %v", e.Err)
	}
	if len(e.Paths) == 1 {
		return fmt.Sprintf("cannot fetch %s: %v", e.Paths[0], e.Err)
	}
	return fmt.Sprintf("cannot fetch %d files, starting with %s: %v",
		len(e.Paths), e.Paths[0], e.Err)
}
```

Change `fetchBlobs`' signature to

```go
func fetchBlobs(ctx context.Context, f *FS, paths []string, parallel int, cache Cache, get blobGetter) error {
```

and its `return &FetchError{Repo: repo, Paths: failed, Err: firstErr}` to `return &FetchError{Paths: failed, Err: firstErr}`.

In `(*GitHub).Fetch` and `(*GitLab).Fetch`, change `return fetchBlobs(ctx, g.repo.Name, f, paths, g.parallel, g.cache,` to `return fetchBlobs(ctx, f, paths, g.parallel, g.cache,`.

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/fetch/ ./cmd/landsraad/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/blobs.go internal/fetch/blobs_test.go internal/fetch/github.go internal/fetch/gitlab.go cmd/landsraad/build_test.go
git commit -m "fix: a failed blob names its repository once"
```

---

### Task 6: `fetchBlobs` reports each path's own error, and its cache hits are pinned

Two things about `fetchBlobs`, one test file.

- **A fix.** `Paths` is sorted so the message is stable, but `Err` was whichever failure the collector received first. `FetchError.Error()` prints `Paths[0]` beside `Err`, so it could print one file's error against another file's name, and because `failureMessage` classifies through `Unwrap`, its advice ("your token", "the rate limit") could change between two runs of the same build.
- **Tests for behaviour believed correct.** No test takes a cache hit at all today. A corrupt hit must be fetched again and overwritten; a valid one must cost no request.

**Files:**
- Modify: `internal/fetch/blobs.go` (the collector loop at the end of `fetchBlobs`)
- Test: `internal/fetch/blobs_test.go`

**Interfaces:**
- Consumes: `fetchBlobs` and `FetchError` as Task 5 left them; `FromEntries`, `NopCache`, `gitBlobSHA`.
- Produces: `mapCache`, a test-only locked `Cache`.

- [ ] **Step 1: Write the tests**

Replace `blobs_test.go`'s import block with:

```go
import (
	"context"
	"errors"
	"io/fs"
	"sync"
	"testing"
)
```

and append:

```go
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
```

- [ ] **Step 2: Run them and watch exactly one fail**

Run: `go test -race ./internal/fetch/ -run 'TestFetchBlobs' -count=1 -v`
Expected: FAIL in `TestFetchBlobsPairsTheReportedPathWithItsOwnError` only: `err = cannot fetch 2 files, starting with a.md: sha-b failed, want "cannot fetch 2 files, starting with a.md: sha-a failed"`. The cache and cancellation tests, and the existing `TestFetchBlobsCancellationBeforeDispatch`, pass.

- [ ] **Step 3: Keep each path's error and report the first path's**

In `fetchBlobs`, replace everything from `seen := make(map[string]bool)` to the end of the function with:

```go
	seen := make(map[string]bool)
	errs := make(map[string]error)
	var failed []string
	for r := range results {
		seen[r.path] = true
		if r.err != nil {
			failed = append(failed, r.path)
			errs[r.path] = r.err
			continue
		}
		f.Put(r.path, r.data)
	}

	// Check that every requested path was accounted for. If the context was
	// cancelled, paths the producer never dispatched will be missing.
	for _, p := range paths {
		if !seen[p] {
			failed = append(failed, p)
			errs[p] = ctx.Err()
		}
	}

	if len(failed) > 0 {
		// Sorted so the message is the same on every run: the worker pool
		// finishes in whatever order it finishes. The error is the first
		// path's own, for the same reason. It used to be whichever failure
		// arrived first, printed beside whichever path sorted first — one
		// file's error against another file's name, and failureMessage's
		// advice could change between two runs of the same build.
		slices.Sort(failed)
		return &FetchError{Paths: failed, Err: errs[failed[0]]}
	}
	return nil
}
```

- [ ] **Step 4: Run them and watch them pass, then prove the cache tests can fail**

Run: `go test -race ./internal/fetch/ -run 'TestFetchBlobs' -count=1`
Expected: PASS.

Mutation: in `fetchBlobs`' worker, change `if cached, hit := cache.Get(e.SHA); hit && verifyBlob(e.SHA, cached) == nil {` to `if cached, hit := cache.Get(e.SHA); hit {` and rerun.
Expected: FAIL in `TestFetchBlobsRefetchesACorruptCacheEntry` (`asked the host 0 times`, `ReadFile = "TAMPERED"`). Restore the line.

Mutation: change the same line to `if cached, hit := cache.Get(e.SHA); false && hit && verifyBlob(e.SHA, cached) == nil {` and rerun.
Expected: FAIL in `TestFetchBlobsServesAValidCacheHitWithoutAsking` (`asked the host for a blob the cache already held`). Restore the line; `go vet ./internal/fetch/` is clean.

Run: `go test -race ./internal/fetch/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/fetch/blobs.go internal/fetch/blobs_test.go
git commit -m "fix(fetch): a failed read reports its own path's error, and cache hits are pinned"
```

---

### Task 7: `literalPrefixes` and `ancestorsOf`, row by row

Both are reached only through behavioural tests, and neither has a case where its interesting behaviour matters: no test gives `literalPrefixes` its edge shapes, and no test gives `ancestorsOf` more than one ancestor whose order counts.

**Files:**
- Test: `internal/fetch/gitlab_test.go`, `internal/fetch/githubwalk_test.go`

**Interfaces:**
- Consumes: `literalPrefixes(patterns []string) []string` (gitlab.go), `ancestorsOf(dir string) []string` (githubwalk.go).
- Produces: nothing.

- [ ] **Step 1: Write the tables**

Append to `gitlab_test.go` (it already imports `cmp`):

```go
// literalPrefixes decides which directories GitLab.Open lists recursively.
func TestLiteralPrefixes(t *testing.T) {
	for _, tt := range []struct {
		name     string
		patterns []string
		want     []string
	}{
		{"no patterns lists the root", nil, []string{"."}},
		{"each pattern's literal directory", []string{"services/*", "workers/*"}, []string{"services", "workers"}},
		{"a repeated pattern is listed once", []string{"services/*", "services/*"}, []string{"services"}},
		{"a leading wildcard needs the root", []string{"*/api"}, []string{"."}},
		{"the root subsumes everything else", []string{"services/*", "."}, []string{"."}},
		{"a literal path is its own prefix", []string{"services/api"}, []string{"services/api"}},
		{"an empty pattern is the root", []string{""}, []string{"."}},
		// Not subsumed, though services/api is inside services. Harmless
		// since R45, because GitLab.Open skips a prefix an earlier recursive
		// listing already covered; pinned so that changing it is a decision.
		{"a nested prefix is kept", []string{"services/*", "services/api/*"}, []string{"services", "services/api"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, literalPrefixes(tt.patterns)); diff != "" {
				t.Errorf("literalPrefixes(%q) mismatch (-want +got):\n%s", tt.patterns, diff)
			}
		})
	}
}
```

In `githubwalk_test.go`, add `"github.com/google/go-cmp/cmp"` to the import block (as its own group after the standard library, if it is not already there), then append:

```go
// ancestorsOf is the order both adapters list a directory's parents in:
// root first, so each listing can see the next segment. No behavioural test
// reaches a case with more than one ancestor whose order matters.
func TestAncestorsOf(t *testing.T) {
	for _, tt := range []struct {
		dir  string
		want []string
	}{
		{".", nil},
		{"a", nil},
		{"a/b", []string{"a"}},
		{"a/b/c/d", []string{"a", "a/b", "a/b/c"}},
	} {
		t.Run(tt.dir, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, ancestorsOf(tt.dir)); diff != "" {
				t.Errorf("ancestorsOf(%q) mismatch (-want +got):\n%s", tt.dir, diff)
			}
		})
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./internal/fetch/ -run 'TestLiteralPrefixes|TestAncestorsOf' -count=1 -v`
Expected: PASS. If a row fails, stop and report it: these rows were derived by reading the code, and a failure means the reading was wrong.

- [ ] **Step 3: Prove they can fail**

Mutation: in `ancestorsOf`, delete the reversal loop (`for i, j := 0, len(chain)-1; i < j; …`) and rerun.
Expected: FAIL in `TestAncestorsOf/a/b/c/d` (got `a/b/c, a/b, a`). Restore.

Mutation: in `literalPrefixes`, change `if prefix == "." { return []string{"."} }` to `if prefix == "." && len(out) == 0 { return []string{"."} }` and rerun.
Expected: FAIL in `the_root_subsumes_everything_else`. Restore; `git diff --stat` shows only the two test files.

- [ ] **Step 4: Commit**

```bash
git add internal/fetch/gitlab_test.go internal/fetch/githubwalk_test.go
git commit -m "test(fetch): literalPrefixes and ancestorsOf, row by row"
```

---

### Task 8: `blobCache`: its refusal is asserted exactly, and two comments are made true

**Files:**
- Modify: `cmd/landsraad/blobcache.go`
- Test: `cmd/landsraad/blobcache_test.go` (`TestBlobCacheRejectsAnythingThatIsNotASha`)

**Interfaces:**
- Consumes: `newBlobCache`, `safeBlobPath`, `(*blobCache).Put`, `(*blobCache).Get`.
- Produces: nothing.

- [ ] **Step 1: Assert each refusal exactly**

In `TestBlobCacheRejectsAnythingThatIsNotASha`, replace the loop from `for _, sha := range []string{` through its closing `}` (the one before `// The canary is the actual proof`) with:

```go
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
```

- [ ] **Step 2: Run it**

Run: `go test ./cmd/landsraad/ -run 'TestBlobCache' -count=1`
Expected: PASS. The message is already right; nothing asserted it.

Mutation: in `Put`, change `"refusing to cache under %q: not a git object id"` to `"refusing to cache %q"` and rerun.
Expected: FAIL, seven subtests. Restore.

- [ ] **Step 3: Make the two comments true**

In `blobcache.go`, delete the three comment lines above `var _ fetch.Cache = (*blobCache)(nil)`. `cacheFor` in `build.go` returns a `*blobCache` as a `fetch.Cache`, so "nothing else in the tree references *blobCache as a fetch.Cache" is false, and the assertion needs no apology. Keep the bare `var _` line, matching `github.go` and `gitlab.go`.

Replace `	defer os.Remove(tmp.Name())` with:

```go
	// A no-op only after a successful Rename, when the temp file is already
	// gone; it cleans up for the two returns above it and for a Rename that
	// fails.
	defer os.Remove(tmp.Name())
```

- [ ] **Step 4: Verify**

Run: `go vet ./cmd/landsraad/ && go test ./cmd/landsraad/ -run 'TestBlobCache' -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/blobcache.go cmd/landsraad/blobcache_test.go
git commit -m "test(cache): Put's refusal is asserted exactly, and two comments are made true"
```

---

### Task 9: `multiLastEdit`'s three untested routes

`multiLastEdit` sends each repository to the one source that can answer for it. Only its error route has a test (`TestBuildReportsAHostThatCannotAnswerDocsFresh`). The integration tests inject their own `LastEdit`, so the real routing never runs there. Its local-git, no-fetcher and remote-success routes have no coverage.

**Files:**
- Test: `cmd/landsraad/build_test.go` (new `fixedEditFetcher` beside `errFetcher`; new `TestMultiLastEditRoutesEachRepository`)
- Test: `cmd/landsraad/lastedit_test.go` (new `TestMultiLastEditAsksGitForTheLocalRepository`)

**Interfaces:**
- Consumes: `multiLastEdit(ctx context.Context, root string, w *workspace) scorecard.LastEditFunc`; `workspace{local, fetchers, edits}`; `newLastEditLog`; `(*workspace).TakeLastEditFailures`; `fetch.Fetcher`.
- Produces: `fixedEditFetcher`, a test-only `fetch.Fetcher` whose `LastEdit` always returns `(t, known, nil)`.

- [ ] **Step 1: Write the tests**

In `build_test.go`, immediately after `errFetcher`'s `LastEdit` method, add:

```go
// fixedEditFetcher is a host with one answer for every path. Like
// errFetcher, only LastEdit is reachable.
type fixedEditFetcher struct {
	t     time.Time
	known bool
}

func (f fixedEditFetcher) Open(context.Context, []string) (*fetch.FS, error) {
	panic("fixedEditFetcher.Open: openRepos is not part of this test")
}
func (f fixedEditFetcher) Expand(context.Context, *fetch.FS, []string) error {
	panic("fixedEditFetcher.Expand: openRepos is not part of this test")
}
func (f fixedEditFetcher) Fetch(context.Context, *fetch.FS, []string) error {
	panic("fixedEditFetcher.Fetch: openRepos is not part of this test")
}
func (f fixedEditFetcher) LastEdit(context.Context, string) (time.Time, bool, error) {
	return f.t, f.known, nil
}

// multiLastEdit's routes other than a host error, which
// TestBuildReportsAHostThatCannotAnswerDocsFresh covers. None of these fail,
// so none may record a failure.
func TestMultiLastEditRoutesEachRepository(t *testing.T) {
	when := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	w := &workspace{
		local: "platform",
		fetchers: map[string]fetch.Fetcher{
			"edge-gateway": fixedEditFetcher{t: when, known: true},
			"billing":      fixedEditFetcher{known: false},
		},
		edits: newLastEditLog(),
	}
	lastEdit := multiLastEdit(context.Background(), t.TempDir(), w)

	if got, ok := lastEdit("edge-gateway", "docs"); !ok || !got.Equal(when) {
		t.Errorf("remote: LastEdit = %v, %v; want %v, true — the host's answer", got, ok, when)
	}
	if got, ok := lastEdit("billing", "docs"); ok {
		t.Errorf("remote with no history: LastEdit = %v, true; want false — unknown, not a date", got)
	}
	if got, ok := lastEdit("nobody", "docs"); ok {
		t.Errorf("repository with no fetcher: LastEdit = %v, true; want false", got)
	}
	if got := w.TakeLastEditFailures(); len(got) != 0 {
		t.Errorf("recorded %v, want no failures: nothing here failed", got)
	}
}
```

In `lastedit_test.go`, replace the import block with:

```go
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/landsraadhq/landsraad/internal/fetch"
)
```

and append:

```go
// The local repository is answered by git, never by a fetcher: it is what
// the user is editing, and its history is on disk. openRepos never gives
// the local repository a fetcher; this one is here so that asking it would
// show.
func TestMultiLastEditAsksGitForTheLocalRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	when := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	// git's internal date format, "<unix seconds> <offset>": no parsing, no
	// timezone left to the machine running the test.
	stamp := fmt.Sprintf("%d +0000", when.Unix())
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
			"GIT_AUTHOR_DATE="+stamp, "GIT_COMMITTER_DATE="+stamp)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "index.md"), []byte("# hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "docs/index.md")
	runGit("commit", "-qm", "add docs")

	w := &workspace{
		local:    "platform",
		fetchers: map[string]fetch.Fetcher{"platform": fixedEditFetcher{t: time.Unix(0, 0).UTC(), known: true}},
		edits:    newLastEditLog(),
	}
	got, ok := multiLastEdit(context.Background(), root, w)("platform", "docs")
	if !ok || !got.Equal(when) {
		t.Errorf("LastEdit(platform, docs) = %v, %v; want %v, true — from git", got, ok, when)
	}
}
```

- [ ] **Step 2: Run them**

Run: `go test ./cmd/landsraad/ -run 'TestMultiLastEdit' -count=1 -v`
Expected: PASS, both. These routes were believed correct.

- [ ] **Step 3: Prove each route's test can fail**

Apply one mutation to `multiLastEdit` in `build.go` at a time, run the Step 2 command, check the named failure, then restore:

| Mutation | Must fail |
|---|---|
| `if repo == w.local {` → `if false && repo == w.local {` | `TestMultiLastEditAsksGitForTheLocalRepository` (got 1970, the fetcher's answer) |
| `return t, known` → `return t, known \|\| true` | `TestMultiLastEditRoutesEachRepository` (billing). Written this way on purpose: the obvious `return t, true` leaves `known` unused, so the package fails to compile instead of the named test failing, which proves nothing. |
| `if !ok { return time.Time{}, false }` → `if !ok { return time.Now(), true }` | `TestMultiLastEditRoutesEachRepository` (nobody) |

`git diff --stat` then shows only the two test files.

- [ ] **Step 4: Commit**

```bash
git add cmd/landsraad/build_test.go cmd/landsraad/lastedit_test.go
git commit -m "test: multiLastEdit's three untested routes"
```

---

### Task 10: `tokenVarName` replaces characters, not bytes

The README and the function's own comment both say every non-alphanumeric *byte* becomes `_`. The loop ranges over runes, so `café` becomes `LANDSRAAD_TOKEN_CAF_` with one underscore, not the two the wording predicts. Repository names are not restricted to ASCII, so the difference is reachable. The code stays: the variable name is what users have already set in CI. The wording changes, and a test pins the behaviour.

**Files:**
- Modify: `README.md` (the **Tokens.** paragraph under "Multi-repository catalogs")
- Modify: `cmd/landsraad/repos.go` (`tokenVarName`'s doc comment)
- Test: `cmd/landsraad/repos_test.go` (`TestTokenVarName`)

**Interfaces:**
- Consumes: `tokenVarName(name string) string`.
- Produces: nothing.

- [ ] **Step 1: Pin it**

In `TestTokenVarName`'s table, add after `{"api", "LANDSRAAD_TOKEN_API"},`:

```go
		// One underscore for É, which is two bytes in UTF-8: characters are
		// replaced, not bytes.
		{"café", "LANDSRAAD_TOKEN_CAF_"},
```

- [ ] **Step 2: Run it, then prove it can fail**

Run: `go test ./cmd/landsraad/ -run 'TestTokenVarName' -count=1`
Expected: PASS.

Mutation: in `tokenVarName`, replace `for _, r := range strings.ToUpper(name) {` with `for _, c := range []byte(strings.ToUpper(name)) { r := rune(c)` and rerun.
Expected: FAIL, `tokenVarName("café") = "LANDSRAAD_TOKEN_CAF__", want "LANDSRAAD_TOKEN_CAF_"`. Restore.

- [ ] **Step 3: Correct the wording**

In `README.md`, replace

```markdown
`LANDSRAAD_TOKEN_<NAME>` first (the repository's `name`, or its derived
identity, uppercased with every character that is not an ASCII letter or digit replaced by one `_`), then
```

with

```markdown
`LANDSRAAD_TOKEN_<NAME>` first (the repository's `name`, or its derived
identity, uppercased, with every character that is not an ASCII letter or
digit replaced by `_`), then
```

In `repos.go`, replace `tokenVarName`'s doc comment with:

```go
// tokenVarName is LANDSRAAD_TOKEN_<NAME>: uppercased, every character that
// is not an ASCII letter or digit replaced with one underscore. A character,
// not a byte: "café" is LANDSRAAD_TOKEN_CAF_. Stated rather than clever,
// because a variable nobody can guess is a variable nobody sets.
```

- [ ] **Step 4: Verify**

Run: `go test ./cmd/landsraad/ -run 'TestTokenVarName' -count=1 && grep -rn 'non-alphanumeric byte' README.md cmd/landsraad/`

The grep covers the whole of `cmd/landsraad/`, not just `repos.go`: the same wording also sits above `TestTokenVarName` in `repos_test.go`, and a grep narrowed to the two files the step edits would leave the file contradicting itself three lines from the case it adds.
Expected: PASS, and no grep output.

- [ ] **Step 5: Commit**

```bash
git add README.md cmd/landsraad/repos.go cmd/landsraad/repos_test.go
git commit -m "docs: tokenVarName replaces characters, not bytes"
```

---

### Task 11: Four comments that said less, or more, than the code

No behaviour changes. Each edit makes a comment state what the code does, or renames a variable that hid another.

**Files:**
- Modify: `internal/fetch/fetch.go` (`Cache`'s doc comment)
- Modify: `internal/render/docs.go` (`docsFor`'s doc comment)
- Modify: `internal/fetch/github.go` (`GitHub`'s fields)
- Modify: `cmd/landsraad/serve.go` (`Serve`'s `--watch` block)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

- [ ] **Step 1: `fetch.Cache` states its concurrency contract**

`*FS` documents a careful single-goroutine contract, while `Cache` next to it says nothing, although `fetchBlobs` calls `Get` and `Put` from parallel workers. Replace the doc comment above `type Cache interface` with:

```go
// Cache is a content-addressed blob store, keyed on the git blob SHA the
// tree listing returns (ruling R27).
//
// An interface here and an implementation in cmd/ because a cache reads and
// writes real files, and nothing under internal/ may import os. A miss is
// never an error: a cache that cannot answer is a slow build, not a broken
// one, so Get has no error return at all.
//
// Get and Put must be safe to call from several goroutines at once,
// including two Puts for the same sha: fetchBlobs calls both from inside its
// parallel workers, not from the single goroutine that funnels results into
// *FS. Two paths with identical content share a sha, so two workers writing
// one key at the same moment is an ordinary build, not a corner case.
// Content addressing makes that race harmless for any implementation whose
// writes are atomic — both writers hold the same bytes — which is why
// blobCache's temp-file-and-rename is enough and NopCache needs nothing.
//
// fetchBlobs discards Put's error, for the same reason a miss is not one: a
// cache that cannot write makes a slower build, not a wrong one.
```

- [ ] **Step 2: `docsFor` names its one early return**

In `internal/render/docs.go`, replace

```go
// docsFor renders one entity's documentation.
//
// Every failure is reported and skipped. One unreadable document must not
// cost the reader the other pages (spec §12).
```

with

```go
// docsFor renders one entity's documentation.
//
// A failure is reported and skipped: one unreadable document must not cost
// the reader the other pages (spec §12). The single exception is an entity
// whose repository has no filesystem at all, which is reported once and
// returns — see the comment where in.Sources is resolved.
```

- [ ] **Step 3: `GitHub`'s fields say what `mu` guards, and why**

`ref string // resolved on first use` sits above `mu`, which by Go convention reads as unguarded, yet every access holds `mu`. `gitlab.go`'s `mu` comment says "for the reason recorded on GitHub", and `github.go` records no reason. In `internal/fetch/github.go`, replace the fields after `parallel int` in `type GitHub struct` with:

```go
	// mu guards ref, edits and shas. A caller expanding one directory can
	// run against the same *GitHub as a LastEdit for a different path, and
	// either may be the first to resolve ref lazily; ref was read and
	// written unguarded until a review caught the race.
	mu    sync.Mutex
	ref   string            // repos.yaml's ref, or the default branch once resolveRef has asked
	edits map[string]edit   // memoized LastEdit answers (ruling R35)
	shas  map[string]string // subtree shas the truncated-tree descent (R28) has learned, keyed by path
```

(The blank line between `parallel int` and the guarded fields stays. `NewGitHub`'s composite literal names its fields, so the reorder needs no other change.)

- [ ] **Step 4: The watcher is not a second `w`**

In `cmd/landsraad/serve.go`, `Serve` takes `w *workspace` and its `--watch` block declares `w, err := fsnotify.NewWatcher()`, shadowing it. Using the workspace inside that block later would be a type error, not a silent bug, but the reader has to work that out. In the `if watch {` block of `Serve`, rename the watcher: `w, err := fsnotify.NewWatcher()` → `watcher, err := fsnotify.NewWatcher()`, then `defer w.Close()` → `defer watcher.Close()`, `watchDirs(root, w)` → `watchDirs(root, watcher)`, `<-w.Events` → `<-watcher.Events`, `watchDirs(event.Name, w)` → `watchDirs(event.Name, watcher)`, `<-w.Errors` → `<-watcher.Errors`. Six sites.

Run: `awk '/if watch \{/,/watching %s for changes/' cmd/landsraad/serve.go | grep -nw 'w'`
Expected: no output. Nothing in the block still says `w`. (Before the rename, the same command prints the six lines.)

- [ ] **Step 5: Verify**

Run: `go build ./... && go vet ./... && go test ./internal/fetch/ ./internal/render/ ./cmd/landsraad/ -count=1 && task lint`
Expected: PASS, lint clean.

- [ ] **Step 6: Commit**

```bash
git add internal/fetch/fetch.go internal/render/docs.go internal/fetch/github.go cmd/landsraad/serve.go
git commit -m "docs: four comments that said less, or more, than the code"
```

---

### Task 12: One malformed-`repos.yaml` fixture, not four copies

The follow-ups file said a fourth copy would justify extracting it. There were already four, and the four tests must all be looking at the same mistake: a fixture edited in one place and not the others would let `build`, `serve` and `openRepos` drift apart with every test still green.

**Files:**
- Modify: `cmd/landsraad/integration_test.go` (new `malformedReposFixture` and `malformedReposStderr`, beside `integrationFixture`)
- Modify: `cmd/landsraad/serve_test.go` (`TestOpenReposReportsAMalformedReposYAML`, `TestServeWithoutWatchExitsWhenReposYAMLIsMalformed`, `TestServeWithWatchAlsoExitsWhenReposYAMLIsMalformed`)
- Modify: `cmd/landsraad/build_test.go` (the `build` test against this fixture, `TestBuildExitsWhenReposYAMLIsMalformed` before the behaviour plan)

**Interfaces:**
- Consumes: `materialize(t *testing.T, files map[string]string) string`.
- Produces: `malformedReposFixture() map[string]string`; `const malformedReposStderr string`.

- [ ] **Step 1: Add the fixture beside `integrationFixture`**

In `integration_test.go`, immediately after `integrationFixture`'s closing brace, add:

```go
// malformedReposFixture is a repository whose only mistake is repos.yaml
// writing its url in ssh form, which validateRepos rejects as repos-url.
// Every command that reads repos.yaml is tested against it, and they must
// all be looking at the same mistake. A fresh map each call, because a test
// is free to change what it is given.
func malformedReposFixture() map[string]string {
	return map[string]string{
		"teams.yaml": "teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n",
		"repos.yaml": "repos:\n  - url: git@github.com:org/monorepo.git\n    paths: [services/*]\n",
		"services/ledger-api/service.yaml": "apiVersion: landsraad/v1\nkind: Service\nmetadata:\n  name: ledger-api\n" +
			"  owner: team-payments\n  tier: 1\n  lifecycle: production\nspec:\n" +
			"  path: services/ledger-api\n",
	}
}

// malformedReposStderr is exactly what every command prints for
// malformedReposFixture, and nothing else.
const malformedReposStderr = "error: repos.yaml:2 [repos-url]\n" +
	"  repository url must begin with https://, got \"git@github.com:org/monorepo.git\"\n" +
	"  hint: write it as https://github.com/org/monorepo\n"
```

- [ ] **Step 2: Find every copy**

Run: `grep -rn --include='*_test.go' 'git@github.com:org/monorepo.git' cmd/landsraad/`
Expected: the new helper in `integration_test.go`, three tests in `serve_test.go`, one in `build_test.go`, and any copy the behaviour plan added (convert those too, the same way).

- [ ] **Step 3: Point every copy at the helper**

In `TestOpenReposReportsAMalformedReposYAML`, replace

```go
	dir := t.TempDir()
	writeFile(t, dir, "teams.yaml",
		"teams:\n  - name: team-payments\n    members: [alice]\n    slack: \"#pay\"\n    pagerduty: PAY\n")
	writeFile(t, dir, "repos.yaml",
		"repos:\n  - url: git@github.com:org/monorepo.git\n    paths: [services/*]\n")
```

with

```go
	dir := materialize(t, malformedReposFixture())
```

(The fixture's `service.yaml` is inert here: `openRepos` parses only repositories that have a fetcher, and this one is local.)

and replace its

```go
	want := "error: repos.yaml:2 [repos-url]\n" +
		"  repository url must begin with https://, got \"git@github.com:org/monorepo.git\"\n" +
		"  hint: write it as https://github.com/org/monorepo\n"
	if errOut.String() != want {
		t.Errorf("stderr = %q, want %q", errOut.String(), want)
	}
```

with

```go
	if errOut.String() != malformedReposStderr {
		t.Errorf("stderr = %q, want %q", errOut.String(), malformedReposStderr)
	}
```

In each of the other three tests (the two `serve` tests and the `build` test), replace the whole `dir := materialize(t, map[string]string{ … })` literal with

```go
	dir := materialize(t, malformedReposFixture())
```

and replace its

```go
	want := "error: repos.yaml:2 [repos-url]\n" +
		"  repository url must begin with https://, got \"git@github.com:org/monorepo.git\"\n" +
		"  hint: write it as https://github.com/org/monorepo\n"
	if r.stderr != want {
		t.Errorf("stderr = %q, want %q", r.stderr, want)
	}
```

with

```go
	if r.stderr != malformedReposStderr {
		t.Errorf("stderr = %q, want %q", r.stderr, malformedReposStderr)
	}
```

Leave every exit-code assertion exactly as the behaviour plan left it.

- [ ] **Step 4: Verify**

Run: `go test ./cmd/landsraad/ -run 'MalformedReposYAML|ReposYAMLIsMalformed' -count=1 -v`
Expected: PASS, four tests (or more, if the behaviour plan added copies).

Run: `grep -rn --include='*_test.go' 'git@github.com:org/monorepo.git' cmd/landsraad/`
Expected: two lines, both in `integration_test.go`: the fixture and the const.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/integration_test.go cmd/landsraad/serve_test.go cmd/landsraad/build_test.go
git commit -m "test: one malformed-repos.yaml fixture, not four copies"
```

---

### Task 13: `Expand` before `contentSet` is a type, not a comment (optional)

The spec marks this last and optional: a wrong order already fails loudly, with the right label. It is here because this project's rule is "make ordering a compile error: if B requires A, B takes A's return type", and this is the one ordering in `cmd/` still held by a comment. If the reviewer at Task 12's gate says skip it, skip it and use Task 14's alternative wording.

**Files:**
- Modify: `cmd/landsraad/repos.go` (new `expanded` and `expand` above `contentSet`; `contentSet`'s signature; the phase-2 call site in `openRepos`)
- Test: `cmd/landsraad/repos_test.go` (the three `contentSet` call sites)

**Interfaces:**
- Consumes: `docsDirs(entities []*catalog.Entity) []string`; `fetch.Fetcher.Expand`.
- Produces: `type expanded struct { fsys fs.FS; entities []*catalog.Entity }`; `func expand(ctx context.Context, f fetch.Fetcher, remote *fetch.FS, entities []*catalog.Entity) (expanded, error)`; `func contentSet(x expanded) []string`.

- [ ] **Step 1: Change the tests first**

In `repos_test.go`, change each of the three `contentSet(fsys, []*catalog.Entity{e})` calls (in `TestContentSetIsEveryFileALaterStageReads`, `TestContentSetSkipsAPathThatIsNotInTheListing`, `TestContentSetSkipsUnsetFields`) to:

```go
contentSet(expanded{fsys: fsys, entities: []*catalog.Entity{e}})
```

- [ ] **Step 2: Watch it fail to build**

Run: `go test ./cmd/landsraad/ -run 'TestContentSet' -count=1`
Expected: build failure, `undefined: expanded`.

- [ ] **Step 3: Add the type and route the call through it**

In `repos.go`, immediately above `contentSet`'s doc comment, add:

```go
// expanded is one repository's filesystem after every directory its
// entities name has been listed: the only state in which contentSet's
// answer is complete.
//
// The order used to be held by a comment at the call site. Out of order,
// contentSet cannot see a runbook, an alerts file or a docs page outside
// the configured paths, so none is fetched, and the stage that reads it
// reports a landsraad bug (fetch.ErrNotFetched) instead of the page. expand
// is the only function that builds one, and contentSet takes nothing else.
// A struct literal in this package can still bypass it — the tests do, to
// hand contentSet a listing directly — so this is a signpost for the next
// reader, not a proof.
type expanded struct {
	fsys     fs.FS
	entities []*catalog.Entity
}

// expand lists every directory docsDirs names, and returns the one input
// contentSet accepts.
func expand(ctx context.Context, f fetch.Fetcher, remote *fetch.FS, entities []*catalog.Entity) (expanded, error) {
	if err := f.Expand(ctx, remote, docsDirs(entities)); err != nil {
		return expanded{}, err
	}
	return expanded{fsys: remote, entities: entities}, nil
}
```

Change `contentSet`'s first line from `func contentSet(fsys fs.FS, entities []*catalog.Entity) []string {` to:

```go
func contentSet(x expanded) []string {
	fsys, entities := x.fsys, x.entities
```

In `openRepos`, replace

```go
			// Expand before contentSet: a spec.docs directory outside the
			// configured paths may not be listed yet, and contentSet walks
			// it to find the pages. The order is load-bearing twice over
			// now: contentSet skips any path absent from the listing, so a
			// runbook or an alerts file outside those paths would be
			// dropped here — and then reported as missing by CheckFiles —
			// if its directory had not been listed first. docsDirs is what
			// puts all three kinds of directory into this call.
			if err := fetcher.Expand(ctx, remote, docsDirs(parsed)); err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, contentSet(fsys, parsed)); err != nil {
```

with

```go
			// expand lists every directory these entities name — spec.docs,
			// and the directories holding spec.runbook and spec.alerts —
			// and contentSet accepts nothing else, so the listing always
			// comes first. See expanded.
			x, err := expand(ctx, fetcher, remote, parsed)
			if err != nil {
				fail(err)
				continue
			}
			if err := fetcher.Fetch(ctx, remote, contentSet(x)); err != nil {
```

(The old comment's "and then reported as missing by CheckFiles" was one of the follow-ups file's four wrong claims; it goes with the comment.)

- [ ] **Step 4: Verify**

Run: `go vet ./cmd/landsraad/ && go test ./cmd/landsraad/ -count=1`
Expected: PASS, including `TestContentSet*` and `TestBuildFetchesARunbookOutsideTheConfiguredPaths`, the integration test that fails if the order breaks.

- [ ] **Step 5: Commit**

```bash
git add cmd/landsraad/repos.go cmd/landsraad/repos_test.go
git commit -m "refactor: Expand before contentSet is a type, not a comment"
```

---

### Task 14: Close the follow-ups file

**Files:**
- Modify: `docs/superpowers/plans/2026-09-11-carryall-followups.md` (whole file)

**Interfaces:**
- Consumes: every task above, and the behaviour plan's rulings R36–R45.
- Produces: nothing.

- [ ] **Step 1: Check the branch did what this file will claim**

Run: `git log --oneline main..HEAD`
Expected: the spec and plan commits, the behaviour plan's commits, then one commit for each of Tasks 1–13 of this plan (12 if Task 13 was skipped).

Run: `grep -n 'FetchError\|MaxAttempts\|summarise\|contentSet\|tokenVarName' CLAUDE.md`
Expected: no output. No `CLAUDE.md` claim is about anything this plan changed. If there is output, correct each claim it shows, and add `CLAUDE.md` to Step 4's `git add`.

- [ ] **Step 2: Replace the file**

Write `docs/superpowers/plans/2026-09-11-carryall-followups.md` as exactly:

````markdown
# Carryall follow-ups

What Plan 4 left behind, written down on the day it merged. Everything here
was found by a review or an audit, verified against the code, and
deliberately not fixed at the time — either because it needed a decision,
or because it was not worth widening a finished branch for.

**Closed, on the `carryall-followups` branch.** Every item was re-verified
before anything was decided, and the decisions are recorded in
`docs/superpowers/specs/2026-09-11-carryall-followups-design.md`. That pass
found this file wrong in four places, each corrected below where it
occurs, and found eight problems it did not mention (the spec's N1–N8).
Each item now says where it went: a ruling, R36–R45, described in the spec,
or a task in `docs/superpowers/plans/2026-09-11-carryall-followups-hygiene.md`.

## Two decisions

Both change what landsraad requires of a user's repository, which this
project treats as a one-way door.

### 1. `build` exits 1 where `validate` and `serve` exit 2

For the same malformed `repos.yaml`, `build` exited `exitUsage` (1) while
`validate` and `serve` exited `exitValidation` (2).

**Resolved by R36.** The inconsistency was wider than this: `validate`
itself exited 1 for a bad `paths:` and 2 for a bad `url:`, and the README
documented 1 for "a config error". There is now one rule: 2 when a file
you wrote has a problem a diagnostic can point at, 1 when landsraad could
not run. `build` exits 2 here.

### 2. `validate` fails inside a satellite repository

Ruling R34 puts `teams.yaml` and `standards.yaml` in the root repository
only, so `landsraad validate`, run inside a satellite's own checkout,
failed with `missing-teams` about a file that team was never meant to have.

**Resolved by R37:** `landsraad validate --satellite` skips owner
resolution and says so. Without the flag nothing changes.

## One root, three symptoms

`NewFS` marked `"."` listed at construction without proof that anything
enumerated the root. Three known issues were that one fact:

- `GitLab.Open` on non-root prefixes left a root marked listed whose
  contents nobody fetched, so `fs.Stat` answered `fs.ErrNotExist` for a
  file sitting in the repository.
- `FS.lookup`'s climb inherited the same imprecision at `"."`.
- `docsDirs`' comment was said to claim "both adapters already list the
  repository root". **Correction:** that was already out of date when this
  file was written; the comment had been corrected to say GitLab's root is
  not known.

**Resolved by R45**, and not by `NewFS` alone. Listing the GitLab root and
nothing else would have made a missing `docs/runbooks` under an existing
`docs/` read as "never looked", and a 404 cannot stand in for a listing:
GitLab answers one for a missing path only from 17.7, and also when Gitaly
is down. A directory is now marked listed only by a listing that covered
it, which is the discipline GitHub's descent already followed. `fs.Glob`
did not need to change: `discover.Find` treats both answers the same.

## Deferred

### Missing tests, behaviour believed correct

- A cache hit whose bytes fail `verifyBlob` falls through to a real fetch.
  **Hygiene Task 6**, with a valid hit alongside it: no test took any cache
  hit at all.
- No test asserted the retry *count* for a rate-limited 403. **R44** for a
  spent quota, which is no longer retried when the retry would fire before
  the reset; **hygiene Task 4** for a 403 with quota left and a 429's
  `Retry-After`.
- No coverage of a trailing-slash `BaseURL`, or of a populated query.
  **Hygiene Task 4.**
- `literalPrefixes`' edge cases and `ancestorsOf`'s multi-level reversal.
  **Hygiene Task 7.**
- `blobCache.Put`'s exact rejection message. **Hygiene Task 8.**
- `workspace.FetcherFor` was exercised only through `multiLastEdit`.
  **Hygiene Task 9**, which covers the three routes through `multiLastEdit`
  that had no test.
- `assemble`'s `len(src) > 1` gate. **R42.** It was a regression, not a
  neutral change: since Plan 4, a run where every `service.yaml` failed to
  parse also hid every `teams.yaml` diagnostic.

### Latent correctness

- `summarise` could split a multi-byte rune. **Hygiene Task 1.**
- `firstErr` in `fetchBlobs` was whichever worker lost the race. **Hygiene
  Task 6.** Worse than stated: the first sorted path was printed beside
  another path's error.
- A response over 64 MB was silently truncated. **Hygiene Task 3**, which
  also keeps the refusal from being retried.
- `ingest.go` collapsed `ErrNotListed` into "no results". **R40**, which
  also reports a `.landsraad/checks` that is a file, or cannot be read, on
  any filesystem.
- `diag.Text` never printed `Repo`. **R41.** `Repo` meant two things; it now
  always names the repository that holds `File`, and multi-repository
  `build` and `serve` print it.
- `Expand`-before-`contentSet` ordering was enforced by comments, not
  types. **Correction:** out of order, the result is `docs-unreadable`,
  labelled a landsraad bug, not a false `missing-file`, because
  `CheckFiles` stats after `Expand` has listed the directory. **Hygiene
  Task 13** makes the order a type.

### Cosmetic

- `blobcache.go`'s `var _ fetch.Cache` comment. **Hygiene Task 8.**
- `repoFailure.URL` and `.Line` were written and never read. **R38:** a
  fetch failure now cites the line that named the repository; `URL` is
  gone.
- `serve.go` shadowed its `w *workspace` parameter with the fsnotify
  watcher. **Correction:** the shadow would compile even if the workspace
  were used, so this was readability only. **Hygiene Task 11.**
- `ClientOptions.MaxAttempts` had no production consumer. **Hygiene Task 2:**
  a constant.
- `fetch.Cache` documented no concurrency contract. **Hygiene Task 11.**
- `README` said `tokenVarName` replaces every non-alphanumeric *byte*.
  **Hygiene Task 10.**
- A `local: true` entry with a `name:` and no `url:` is rejected. **R39:**
  the rejection stays, because loosening `repos.yaml` cannot be undone.
  `localRepoName` now picks the local entry by `local:` and `name:`.
- `docsFor`'s comment read as universal. **Hygiene Task 11.**
- `github.go`'s `ref` comment predated the mutex. **Hygiene Task 11.**
- `blobcache.go` deferred `os.Remove` even after a successful rename.
  **Hygiene Task 8:** commented, not restructured.
- The malformed-`repos.yaml` fixture was spelled out in three tests.
  **Correction:** four. **Hygiene Task 12.**

## Deliberately not done

The `GitHub` and `GitLab` adapters duplicate roughly 25–30 lines across
`resolveRef` and `LastEdit` — a memo-plus-mutex pattern in which a data race
was already found once and fixed in both copies by hand. Both a review and
an audit raised extracting it, and both agreed waiting is defensible: this
project requires three examples for an abstraction, and there are two —
still two when this file was closed. A third adapter is the trigger;
extract then, and extract only the lock discipline, not the endpoints or
JSON shapes, which differ honestly.
````

If Task 13 was skipped, replace that item's last sentence, `**Hygiene Task 13** makes the order a type.`, with `**Not done:** judged not worth a type, because a wrong order already fails loudly, with the right label.`

- [ ] **Step 3: Run everything, and audit before merge**

Run: `task ci`
Expected: lint clean, and every test passes under `-race`.

Dispatch the `composition-auditor` agent on the branch, as the project's `CLAUDE.md` requires before merging. Fix what it confirms in a separate commit, or record in that commit's message why not.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/plans/2026-09-11-carryall-followups.md
git commit -m "docs: close the Carryall follow-ups"
```
