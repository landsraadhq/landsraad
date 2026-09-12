package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		HTTP:       srv.Client(),
		BaseURL:    srv.URL,
		Token:      "secret-token-value",
		AuthHeader: "Authorization",
		AuthPrefix: "Bearer ",
		Headers:    map[string]string{"X-GitHub-Api-Version": "2026-03-10"},
		Sleep:      func(time.Duration) {}, // tests never wait
	}), srv
}

func TestClientSendsAuthAndHeaders(t *testing.T) {
	var gotAuth, gotVersion string
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotVersion = r.Header.Get("X-GitHub-Api-Version")
		w.Write([]byte(`{}`))
	})
	if _, _, err := c.Get(context.Background(), "/repos/o/r", nil, ""); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotAuth != "Bearer secret-token-value" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotVersion != "2026-03-10" {
		t.Errorf("X-GitHub-Api-Version = %q", gotVersion)
	}
}

// A token must never reach a diagnostic, a log line or the generated site
// (spec §14.1). The error path is where it would leak, because that is the
// one place the request gets described back to the user. Test both the
// statusError path (response body) and the transport-error path.
func TestClientErrorsNeverCarryTheToken(t *testing.T) {
	t.Run("short_body_on_status_error", func(t *testing.T) {
		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("upstream said secret-token-value"))
		})
		_, _, err := c.Get(context.Background(), "/repos/o/r", nil, "")
		if err == nil {
			t.Fatal("Get succeeded on a 500")
		}
		if strings.Contains(err.Error(), "secret-token-value") {
			t.Fatalf("token leaked into the error: %v", err)
		}
	})

	t.Run("token_straddling_truncation_boundary", func(t *testing.T) {
		// Create a body with the token starting at byte 190.
		// Token "secret-token-value" is 18 chars, so it ends at 208.
		// Truncation at 200 bytes would cut the token in half if redaction
		// happens after truncation. The first 10 chars ("secret-tok")
		// would leak through because the full token is no longer in the
		// truncated string and thus won't be redacted.
		padding := strings.Repeat("x", 190)
		token := "secret-token-value" // 18 chars, gets cut to "secret-tok" by truncation
		suffix := strings.Repeat("y", 50)
		body := padding + token + suffix
		if len(body) < 210 {
			t.Fatalf("test setup error: body only %d bytes", len(body))
		}

		c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(body))
		})
		_, _, err := c.Get(context.Background(), "/x", nil, "")
		if err == nil {
			t.Fatal("Get succeeded on a 500")
		}
		errStr := err.Error()
		// The full token must not leak
		if strings.Contains(errStr, token) {
			t.Fatalf("full token leaked into the error: %v", err)
		}
		// The token prefix (straddling boundary) must not leak either
		if strings.Contains(errStr, "secret-tok") {
			t.Fatalf("token prefix leaked into the error: %v", err)
		}
	})

	t.Run("transport_error_path", func(t *testing.T) {
		// Trigger a transport error by pointing at a non-existent server.
		c := NewClient(ClientOptions{
			HTTP:       &http.Client{Timeout: 100 * time.Millisecond},
			BaseURL:    "http://127.0.0.1:1", // unlikely to be listening
			Token:      "secret-token-value",
			AuthHeader: "Authorization",
			AuthPrefix: "Bearer ",
			Sleep:      func(time.Duration) {},
		})
		_, _, err := c.Get(context.Background(), "/x", nil, "")
		if err == nil {
			t.Fatal("Get succeeded against unreachable server")
		}
		if strings.Contains(err.Error(), "secret-token-value") {
			t.Fatalf("token leaked into transport error: %v", err)
		}
	})
}

func TestClientRetriesServerErrorsThenGivesUp(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadGateway)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if err == nil {
		t.Fatal("Get succeeded on repeated 502s")
	}
	if calls != 3 {
		t.Errorf("made %d attempts, want 3", calls)
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Status != http.StatusBadGateway {
		t.Errorf("error = %v, want a StatusError with 502", err)
	}
}

func TestClientRetriesThenSucceeds(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	body, _, err := c.Get(context.Background(), "/x", nil, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

// 4xx other than 429 must not be retried: a 404 is an answer, and retrying
// it three times turns one wrong url into three requests against a rate
// limit somebody else is sharing.
func TestClientDoesNotRetryNotFound(t *testing.T) {
	var calls int
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	if calls != 1 {
		t.Errorf("made %d attempts, want 1", calls)
	}
}

func TestClientClassifies(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		header map[string]string
		check  func(error) bool
	}{
		{"unauthorized", http.StatusUnauthorized, nil, IsUnauthorized},
		{"forbidden with no quota", http.StatusForbidden, map[string]string{"X-RateLimit-Remaining": "0"}, IsRateLimited},
		{"too many requests", http.StatusTooManyRequests, nil, IsRateLimited},
		{"not found", http.StatusNotFound, nil, IsNotFound},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.header {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
			})
			_, _, err := c.Get(context.Background(), "/x", nil, "")
			if !tt.check(err) {
				t.Errorf("classifier said no for %v", err)
			}
		})
	}
}

// A 403 that is NOT a rate limit — an org policy, SAML enforcement — must
// not be reported as one. "wait for the reset" is useless advice for a
// permission that will never change on its own.
func TestForbiddenWithQuotaLeftIsNotRateLimited(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "4998")
		w.WriteHeader(http.StatusForbidden)
	})
	_, _, err := c.Get(context.Background(), "/x", nil, "")
	if IsRateLimited(err) {
		t.Errorf("403 with quota remaining classified as a rate limit: %v", err)
	}
}

func TestParseRepo(t *testing.T) {
	for _, tt := range []struct {
		url       string
		wantOwner string
		wantSlug  string
		wantErr   bool
	}{
		{"https://github.com/org/monorepo", "org", "monorepo", false},
		{"https://github.com/org/monorepo.git", "org", "monorepo", false},
		{"https://github.com/org/monorepo/", "org", "monorepo", false},
		{"https://gitlab.com/group/sub/project", "group/sub", "project", false},
		{"https://github.com/onlyowner", "", "", true},
	} {
		t.Run(tt.url, func(t *testing.T) {
			r, err := ParseRepo("n", tt.url, "")
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseRepo error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if r.Owner != tt.wantOwner || r.Slug != tt.wantSlug {
				t.Errorf("Owner/Slug = %q/%q, want %q/%q", r.Owner, r.Slug, tt.wantOwner, tt.wantSlug)
			}
		})
	}
}

// Redaction must not cost the error chain.
//
// redact used to return errors.New(redacted), which threw away everything
// underneath: with a token set, errors.Is(err, context.Canceled) was false,
// retryable therefore said yes, and Ctrl-C during an authenticated
// `landsraad build` -- the normal path -- made every in-flight request sleep
// a real 1s then 2s and fire two more doomed requests before giving up.
//
// Both halves are asserted here, because fixing either one alone is a
// regression: the token must still be gone, and the cause must still be
// reachable.
func TestRedactRemovesTheTokenAndKeepsTheChain(t *testing.T) {
	c := NewClient(ClientOptions{Token: "secret-token-value"})
	cause := fmt.Errorf("dial tcp 10.0.0.1:443: %w", context.Canceled)
	err := c.redact(fmt.Errorf(`Get "https://secret-token-value@host/x": %w`, cause))

	if strings.Contains(err.Error(), "secret-token-value") {
		t.Errorf("token leaked into the error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("err = %q, want the token replaced with [redacted]", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(err, context.Canceled) = false; redaction threw the chain away")
	}
}

// A cancelled request is an answer, not a failure to retry -- and it must be
// one whether or not a token is set, because retryable reads the chain that
// redaction used to destroy.
//
// Measured before the fix: without a token, errors.Is(ctx.Canceled) was true
// and there were no sleeps; with a token, false, and sleeps of 1s then 2s.
func TestGetDoesNotRetryACancelledRequest(t *testing.T) {
	for _, tt := range []struct{ name, token string }{
		{"no token", ""},
		{"token set", "secret-token-value"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var sleeps int
			c := NewClient(ClientOptions{
				HTTP:       &http.Client{Timeout: time.Second},
				BaseURL:    "http://127.0.0.1:1",
				Token:      tt.token,
				AuthHeader: "Authorization", AuthPrefix: "Bearer ",
				Sleep: func(time.Duration) { sleeps++ },
			})
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			_, _, err := c.Get(ctx, "/repos/o/r", nil, "")
			if err == nil {
				t.Fatal("Get succeeded against a cancelled context")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("errors.Is(err, context.Canceled) = false for %v", err)
			}
			if sleeps != 0 {
				t.Errorf("slept %d times before giving up on a cancelled request, want 0", sleeps)
			}
		})
	}
}

// Ruling R44. A 403 for a spent quota names when the quota returns, and a
// retry that fires before then cannot succeed: it spends a request, and the
// build's time, to be told the same thing. The client used to retry at 1s and
// 2s regardless. A reset inside the backoff is still worth waiting for, and a
// host that sends Retry-After is still obeyed.
func TestClientDoesNotRetryARateLimitBeforeItResets(t *testing.T) {
	now := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name      string
		headers   map[string]string
		wantCalls int
		wantSleep []time.Duration
	}{
		{
			name: "reset an hour away: one call, no sleeps",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
			},
			wantCalls: 1,
		},
		{
			name: "reset inside the backoff: retried as before",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Unix(), 10),
			},
			wantCalls: 3, wantSleep: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name:      "no reset named: retried as before",
			headers:   map[string]string{"X-RateLimit-Remaining": "0"},
			wantCalls: 3, wantSleep: []time.Duration{time.Second, 2 * time.Second},
		},
		{
			name: "Retry-After is obeyed whatever the reset says",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
				"Retry-After":           "7",
			},
			wantCalls: 3, wantSleep: []time.Duration{7 * time.Second, 7 * time.Second},
		},
		{
			// maxAttempts is 3, so the backoffs are 1s then 2s: the next
			// wait alone (1s) is short of this reset, but the sum still
			// owed before the last attempt (1s+2s=3s at the first
			// decision, then 2s at the second) reaches it, so every
			// attempt is made. A single-step lookahead would stop after
			// one call here -- that was the bug.
			name: "a reset the last attempt would outlast is still retried",
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     strconv.FormatInt(now.Add(2*time.Second).Unix(), 10),
			},
			wantCalls: 3, wantSleep: []time.Duration{time.Second, 2 * time.Second},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusForbidden)
			}))
			t.Cleanup(srv.Close)
			var slept []time.Duration
			c := NewClient(ClientOptions{
				HTTP: srv.Client(), BaseURL: srv.URL,
				Sleep: func(d time.Duration) { slept = append(slept, d) },
				Now:   func() time.Time { return now },
			})

			_, _, err := c.Get(context.Background(), "/repos/o/r", nil, "")

			if !IsRateLimited(err) {
				t.Fatalf("err = %v, want the rate limit returned", err)
			}
			if calls != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
			}
			if diff := cmp.Diff(tt.wantSleep, slept); diff != "" {
				t.Errorf("sleeps mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

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
