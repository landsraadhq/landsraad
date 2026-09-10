package fetch

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(ClientOptions{
		HTTP:        srv.Client(),
		BaseURL:     srv.URL,
		Token:       "secret-token-value",
		AuthHeader:  "Authorization",
		AuthPrefix:  "Bearer ",
		Headers:     map[string]string{"X-GitHub-Api-Version": "2026-03-10"},
		MaxAttempts: 3,
		Sleep:       func(time.Duration) {}, // tests never wait
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
			HTTP:        &http.Client{Timeout: 100 * time.Millisecond},
			BaseURL:     "http://127.0.0.1:1", // unlikely to be listening
			Token:       "secret-token-value",
			AuthHeader:  "Authorization",
			AuthPrefix:  "Bearer ",
			MaxAttempts: 1,
			Sleep:       func(time.Duration) {},
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
