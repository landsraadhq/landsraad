package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ClientOptions constructs a Client. Every field is injected — there is no
// default client, no ambient http.DefaultClient and no package-level token,
// because two repositories on two hosts with two tokens must be able to
// coexist in one process.
type ClientOptions struct {
	HTTP        *http.Client
	BaseURL     string
	Token       string
	AuthHeader  string // "Authorization" on GitHub, "PRIVATE-TOKEN" on GitLab
	AuthPrefix  string // "Bearer " on GitHub, "" on GitLab
	Headers     map[string]string
	MaxAttempts int
	// Sleep is how the client waits between retries. Injected so the test
	// suite runs in milliseconds instead of in the backoff schedule.
	Sleep func(time.Duration)
}

// Client is one host's API, already authenticated.
type Client struct {
	http        *http.Client
	base        string
	token       string
	authHeader  string
	authPrefix  string
	headers     map[string]string
	maxAttempts int
	sleep       func(time.Duration)
}

func NewClient(o ClientOptions) *Client {
	c := &Client{
		http: o.HTTP, base: strings.TrimSuffix(o.BaseURL, "/"),
		token: o.Token, authHeader: o.AuthHeader, authPrefix: o.AuthPrefix,
		headers: o.Headers, maxAttempts: o.MaxAttempts, sleep: o.Sleep,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.maxAttempts < 1 {
		c.maxAttempts = 3
	}
	if c.sleep == nil {
		c.sleep = time.Sleep
	}
	return c
}

// StatusError is a request that came back with a status the caller has to
// reason about.
//
// Endpoint is the path only, never the full URL with its query, and Body is
// truncated: both are printed to users, and a token that reached either
// would be printed with them.
type StatusError struct {
	Status     int
	Method     string
	Endpoint   string
	Body       string
	RetryAfter time.Duration
	// RateRemaining is the host's remaining quota, -1 when it said nothing.
	// It is what separates "you are rate limited" from "you are not allowed",
	// which arrive as the same 403.
	RateRemaining int
	// RateReset is when the quota returns, zero when unknown.
	RateReset time.Time
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Endpoint, e.Status, e.Body)
}

func IsNotFound(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusNotFound
}

func IsUnauthorized(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Status == http.StatusUnauthorized
}

// IsRateLimited is true for 429, and for the 403 both hosts return when a
// quota is spent.
//
// The quota check is what stops an org-policy 403 — SAML enforcement, an
// app with no access to a repository — being reported as a rate limit.
// "wait for the reset" is useless advice for a permission that will never
// change on its own, and it would send somebody away for an hour to come
// back to the same error.
func IsRateLimited(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return false
	}
	if se.Status == http.StatusTooManyRequests {
		return true
	}
	return se.Status == http.StatusForbidden && se.RateRemaining == 0
}

// Get performs an authenticated GET with retries.
//
// accept overrides the Accept header for one call, which is how a blob is
// asked for as raw bytes rather than as base64 inside JSON.
func (c *Client) Get(ctx context.Context, endpoint string, query url.Values, accept string) ([]byte, http.Header, error) {
	target := c.base + endpoint
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var last error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		body, header, err := c.once(ctx, target, endpoint, accept)
		if err == nil {
			return body, header, nil
		}
		last = err
		if !retryable(err) || attempt == c.maxAttempts {
			return nil, nil, err
		}
		c.sleep(backoff(attempt, err))
	}
	return nil, nil, last
}

func (c *Client) once(ctx context.Context, target, endpoint, accept string) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, err)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if c.token != "" && c.authHeader != "" {
		req.Header.Set(c.authHeader, c.authPrefix+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// A transport error's message can contain the URL. It cannot
		// contain the token — the token is a header — but redact anyway:
		// this is the one place a future change could put it there.
		return nil, nil, fmt.Errorf("GET %s: %w", endpoint, c.redact(err))
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if resp.StatusCode/100 != 2 {
		return nil, nil, c.statusError(resp, endpoint, body)
	}
	if readErr != nil {
		return nil, nil, fmt.Errorf("GET %s: cannot read the response: %w", endpoint, readErr)
	}
	return body, resp.Header, nil
}

func (c *Client) statusError(resp *http.Response, endpoint string, body []byte) error {
	se := &StatusError{
		Status: resp.StatusCode, Method: http.MethodGet, Endpoint: endpoint,
		Body:          summarise(c.redactString(string(body))),
		RateRemaining: -1,
	}
	if v := resp.Header.Get("X-RateLimit-Remaining"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			se.RateRemaining = n
		}
	}
	if v := resp.Header.Get("X-RateLimit-Reset"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			se.RateReset = time.Unix(n, 0).UTC()
		}
	}
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			se.RetryAfter = time.Duration(n) * time.Second
		}
	}
	return se
}

// redact removes the token from anything on its way to a user.
func (c *Client) redact(err error) error {
	if c.token == "" {
		return err
	}
	return errors.New(c.redactString(err.Error()))
}

func (c *Client) redactString(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

// summarise trims a response body to something printable. A host's error
// body can be a full HTML page, and a diagnostic is one line.
func summarise(body string) string {
	s := strings.TrimSpace(body)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	if s == "" {
		s = "(empty response body)"
	}
	return s
}

// retryable is true for the failures that go away on their own: a 5xx, a
// rate limit, and a transport error. A 404 is an answer and a 401 is a
// decision; repeating either spends somebody's quota to be told the same
// thing three times.
func retryable(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
	}
	return se.Status/100 == 5 || IsRateLimited(err)
}

// backoff is 1s, 2s, 4s, unless the host said how long to wait.
func backoff(attempt int, err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) && se.RetryAfter > 0 {
		return se.RetryAfter
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
