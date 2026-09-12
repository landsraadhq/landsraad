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
	"unicode/utf8"
)

// ClientOptions constructs a Client. Every field is injected — there is no
// default client, no ambient http.DefaultClient and no package-level token,
// because two repositories on two hosts with two tokens must be able to
// coexist in one process.
type ClientOptions struct {
	HTTP       *http.Client
	BaseURL    string
	Token      string
	AuthHeader string // "Authorization" on GitHub, "PRIVATE-TOKEN" on GitLab
	AuthPrefix string // "Bearer " on GitHub, "" on GitLab
	Headers    map[string]string
	// Sleep is how the client waits between retries. Injected so the test
	// suite runs in milliseconds instead of in the backoff schedule.
	Sleep func(time.Duration)
	// Now is the client's clock, read only to judge whether a retry could
	// outlast a spent rate limit (ruling R44). Injected for the reason Sleep
	// is, and defaulted the same way.
	Now func() time.Time
}

// Client is one host's API, already authenticated.
type Client struct {
	http       *http.Client
	base       string
	token      string
	authHeader string
	authPrefix string
	headers    map[string]string
	sleep      func(time.Duration)
	now        func() time.Time
}

func NewClient(o ClientOptions) *Client {
	c := &Client{
		http: o.HTTP, base: strings.TrimSuffix(o.BaseURL, "/"),
		token: o.Token, authHeader: o.AuthHeader, authPrefix: o.AuthPrefix,
		headers: o.Headers, sleep: o.Sleep, now: o.Now,
	}
	if c.http == nil {
		c.http = &http.Client{Timeout: 30 * time.Second}
	}
	if c.sleep == nil {
		c.sleep = time.Sleep
	}
	if c.now == nil {
		c.now = time.Now
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
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		body, header, err := c.once(ctx, target, endpoint, accept)
		if err == nil {
			return body, header, nil
		}
		last = err
		if !retryable(err) || attempt == maxAttempts {
			return nil, nil, err
		}
		wait := backoff(attempt, err)
		if c.tooEarlyToRetry(err, remainingBackoff(attempt, maxAttempts, err)) {
			return nil, nil, err
		}
		c.sleep(wait)
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

// redact removes the token from anything on its way to a user, keeping the
// error itself reachable underneath.
//
// It used to return errors.New(redacted), which threw the chain away: with a
// token set, errors.Is(err, context.Canceled) was false, retryable said yes,
// and Ctrl-C during an authenticated build — the normal path — made every
// in-flight request sleep 1s then 2s and fire two more doomed requests
// before giving up. Measured: no token, errors.Is true and no sleeps; token,
// false and 3s of sleeping. The redaction is unchanged; only the chain is
// kept.
func (c *Client) redact(err error) error {
	if c.token == "" {
		return err
	}
	redacted := c.redactString(err.Error())
	if redacted == err.Error() {
		return err
	}
	return &redactedError{msg: redacted, err: err}
}

// redactedError prints a message with the token removed and unwraps to the
// error it replaced.
//
// Unwrap hands back the original, whose Error() still contains the token, so
// nothing may print the wrapped error directly — errors.Is and errors.As are
// what this exists for. That is the same bargain fmt.Errorf("%w") strikes,
// and every printer in this codebase reaches an error through Error().
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func (c *Client) redactString(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

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

// retryable is true for the failures that go away on their own: a 5xx, a
// rate limit, and a transport error. A 404 is an answer and a 401 is a
// decision; repeating either spends somebody's quota to be told the same
// thing three times. So is a response too large to accept.
func retryable(err error) bool {
	var se *StatusError
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled) &&
			!errors.Is(err, context.DeadlineExceeded) &&
			!errors.Is(err, errTooLarge)
	}
	return se.Status/100 == 5 || IsRateLimited(err)
}

// tooEarlyToRetry reports a spent rate limit that no remaining attempt can
// outlast: the host named when its quota returns, and even the client's
// last attempt — made after remaining, the sum of every backoff still to
// come — would still land before that. A single too-soon retry does not end
// the schedule by itself; only a reset the very last attempt cannot reach
// does, because giving up then means every attempt still available would
// fail the same way, and failureMessage already names the reset time, which
// is the advice that helps (ruling R44).
//
// A host that sent Retry-After is obeyed instead: it said how long to wait,
// and backoff already waits exactly that.
func (c *Client) tooEarlyToRetry(err error, remaining time.Duration) bool {
	if !IsRateLimited(err) {
		return false
	}
	// IsRateLimited only returns true when err unwraps to a *StatusError, so
	// this always succeeds; the call is here only to bind se.
	var se *StatusError
	errors.As(err, &se)
	if se.RetryAfter > 0 || se.RateReset.IsZero() {
		return false
	}
	return c.now().Add(remaining).Before(se.RateReset)
}

// remainingBackoff sums the wait before every attempt still to come, from
// attempt up to lastAttempt-1 — the earliest moment the final attempt could
// fire. That is the total a spent rate limit's reset has to outlast for
// giving up to be correct: a reset inside any single step's wait still
// leaves a later attempt worth making, so tooEarlyToRetry decides on this
// sum rather than on the next wait alone (ruling R44).
func remainingBackoff(attempt, lastAttempt int, err error) time.Duration {
	var total time.Duration
	for a := attempt; a < lastAttempt; a++ {
		total += backoff(a, err)
	}
	return total
}

// maxAttempts is how many times a retryable request is tried: once, then
// twice more after backoff's 1s and 2s. A constant, not an option: nothing
// in production ever set it, and a knob only tests turn lets a test pass
// against a retry path production never takes.
const maxAttempts = 3

// backoff is 1s, 2s, 4s, unless the host said how long to wait.
func backoff(attempt int, err error) time.Duration {
	var se *StatusError
	if errors.As(err, &se) && se.RetryAfter > 0 {
		return se.RetryAfter
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
