package nilda

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// The plugin's door to Nilda's real data.
//
// gRPC is how Core calls the plugin — hooks, events, lifecycle. HTTP is how the plugin calls Core. That
// split is the whole design: Nilda already has a complete API (create, edit, publish, schedule, delete,
// media, import, GraphQL, filtering) and the plugin contract used to expose five hand-picked read methods
// instead of it. No shop, no booking system and no importer could be built against those five, not because
// out-of-process plugins are limited but because nothing had connected them to the API that already
// existed.
//
// A plugin therefore holds a scoped API token and calls the documented endpoints, the same ones a
// first-party feature and any external integration call. There is one API to learn, one to maintain, and
// one place where authorization is decided.

// API is an HTTP client bound to Core's API, carrying this plugin's token.
//
// Nil when the plugin declared no capability implying API access. Check HasAPI before use rather than
// discovering it as a nil dereference three calls deep.
type API struct {
	baseURL string
	token   string
	scopes  []string
	http    *http.Client

	// maxRetries bounds automatic re-attempts of a retryable failure (429, 5xx, a dropped connection).
	maxRetries int
	// idempotencyKey, when set, is sent with every request and makes a POST safe to retry. Set through
	// WithIdempotencyKey, which returns a derived client rather than mutating this one — a key is
	// per-operation, and sharing one across two different creates would make the second a no-op that
	// returns the first one's result.
	idempotencyKey string
}

// HasAPI reports whether this plugin was given API access.
func (c *Core) HasAPI() bool { return c.api != nil }

// API returns the client for Core's API, or nil if this plugin has none.
func (c *Core) API() *API { return c.api }

// Scopes returns what the token may attempt. The server is still the authority — this is here so a plugin
// can fail fast with a clear message at startup instead of at the first write, halfway through an import.
func (a *API) Scopes() []string {
	if a == nil {
		return nil
	}
	return append([]string(nil), a.scopes...)
}

// HasScope reports whether the token carries a scope.
func (a *API) HasScope(s string) bool {
	if a == nil {
		return false
	}
	for _, have := range a.scopes {
		if have == s {
			return true
		}
	}
	return false
}

// newAPI builds the client from what Init handed over. Core is on the same host, so the timeout is
// generous enough for a bulk write and short enough that a wedged request cannot pin a plugin forever.
func newAPI(baseURL, token string, scopes []string) *API {
	if baseURL == "" || token == "" {
		return nil
	}
	return &API{
		baseURL:    strings.TrimRight(baseURL, "/"),
		token:      token,
		scopes:     scopes,
		http:       &http.Client{Timeout: 30 * time.Second, Transport: NewTransport(nil)},
		maxRetries: defaultMaxRetries,
	}
}

// NewTransport wraps an http.RoundTripper so requests carry this plugin's identity to Core's egress
// proxy. Exported because a plugin author making its OWN outbound calls — to a payment gateway, an SMS
// provider — should use it too: without the label the proxy cannot tell which manifest's declared hosts
// apply, and the call is refused.
//
// Pass nil for http.DefaultTransport. Go already routes through the proxy from the environment Core sets;
// this only adds the header that says who is asking.
func NewTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &pluginTransport{base: base, key: os.Getenv("NILDA_PLUGIN_KEY")}
}

// HTTPClient is a ready-made client for a plugin's own outbound calls, already carrying the identity the
// egress proxy needs. Using it is the difference between "declared api.stripe.com and it works" and
// "declared api.stripe.com and every call is refused".
func HTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{Timeout: timeout, Transport: NewTransport(nil)}
}

type pluginTransport struct {
	base http.RoundTripper
	key  string
}

func (t *pluginTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.key == "" {
		return t.base.RoundTrip(r)
	}
	// Clone: RoundTrip must not mutate the caller's request.
	clone := r.Clone(r.Context())
	clone.Header.Set("X-Nilda-Plugin", t.key)
	return t.base.RoundTrip(clone)
}

// WithIdempotencyKey returns a client that sends the given key with every request, which makes a POST
// safe to retry.
//
// A DERIVED client rather than a setting on this one, because a key belongs to ONE operation: reusing it
// across two different creates would make the second return the first one's result instead of creating
// anything. The intended shape is a fresh key per unit of work —
//
//	for _, row := range rows {
//	    api.WithIdempotencyKey(row.ID).Post(ctx, "/content/product", row, nil)
//	}
//
// — so an importer interrupted at row 312 can be run again from the start without producing 312 duplicates.
func (a *API) WithIdempotencyKey(key string) *API {
	if a == nil {
		return nil
	}
	clone := *a
	clone.idempotencyKey = key
	return &clone
}

// Do performs a single request against Core's API with the plugin's token attached.
//
// path is relative to the API root ("/content", "/media"). body may be nil, an io.Reader, or any value
// that marshals to JSON. The caller decodes the response — and is responsible for retries; the JSON
// helpers below handle those.
func (a *API) Do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if a == nil {
		return nil, errNoAPI
	}
	payload, err := encodeBody(body)
	if err != nil {
		return nil, err
	}
	return a.do(ctx, method, path, payload)
}

var errNoAPI = errors.New("nilda: this plugin has no API access — declare a capability that grants it " +
	"(content.read, content.write, media.read, …)")

// do sends one attempt with an already-encoded body, so a retry replays exactly the same bytes.
func (a *API) do(ctx context.Context, method, path string, payload []byte) (*http.Response, error) {
	if a == nil {
		return nil, errNoAPI
	}
	var r io.Reader
	if payload != nil {
		r = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if a.idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", a.idempotencyKey)
	}
	return a.http.Do(req)
}

// JSON performs a request and decodes a successful response into out (which may be nil to discard it).
//
// A non-2xx status becomes an *APIError carrying Core's own error body, so a plugin author debugging a
// failed write reads the reason Core gave rather than a bare status code.
//
// # Retries
//
// A 429 or a 5xx is retried with exponential backoff and jitter, honouring Retry-After when the server
// sends one. This is in the SDK rather than left to each plugin because it cannot reasonably be left to
// each plugin: an importer creating five hundred products meets a rate limit or a restarting Core sooner
// or later, and the difference between "the SDK waited 200ms" and "the import died at product 312" is the
// whole experience of the platform. Every third-party SDK a plugin author has used — Stripe's, GitHub's,
// the AWS ones — does this for them.
//
// A NON-idempotent request (POST without an Idempotency-Key) is never retried automatically. Repeating a
// create that may already have succeeded is how an importer produces duplicate rows, which is worse than
// the error it was trying to survive.
func (a *API) JSON(ctx context.Context, method, path string, body, out any) error {
	// The body has to be replayable across attempts. Buffering it here is what makes a retry possible at
	// all — an io.Reader is consumed by the first attempt and empty for the second.
	payload, err := encodeBody(body)
	if err != nil {
		return err
	}

	if a == nil {
		return errNoAPI
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		res, err := a.do(ctx, method, path, payload)
		if err != nil {
			// A transport error (connection refused, reset) is retryable: Core restarting is exactly the
			// case worth surviving.
			lastErr = err
		} else {
			apiErr := a.readResponse(res, method, path, out)
			if apiErr == nil {
				return nil
			}
			lastErr = apiErr
			var ae *APIError
			if !errors.As(apiErr, &ae) || !ae.Retryable() {
				return apiErr
			}
		}
		if attempt >= a.maxRetries || !a.retryable(method, payload) {
			return lastErr
		}
		if err := sleepBackoff(ctx, attempt, retryAfterOf(lastErr)); err != nil {
			return lastErr // the context ended; the underlying reason is the more useful error
		}
	}
}

// readResponse consumes the response, decoding a success into out or building the *APIError for a failure.
func (a *API) readResponse(res *http.Response, method, path string, out any) error {
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return &APIError{
			Status: res.StatusCode, Method: method, Path: path, Body: string(payload),
			Code:       errorCodeOf(payload),
			RetryAfter: parseRetryAfter(res.Header.Get("Retry-After")),
		}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, res.Body)
		return nil
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// Get is a convenience for a read with query parameters.
func (a *API) Get(ctx context.Context, path string, query url.Values, out any) error {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	return a.JSON(ctx, http.MethodGet, path, nil, out)
}

// Post creates.
func (a *API) Post(ctx context.Context, path string, body, out any) error {
	return a.JSON(ctx, http.MethodPost, path, body, out)
}

// Patch edits.
func (a *API) Patch(ctx context.Context, path string, body, out any) error {
	return a.JSON(ctx, http.MethodPatch, path, body, out)
}

// Delete removes.
func (a *API) Delete(ctx context.Context, path string) error {
	return a.JSON(ctx, http.MethodDelete, path, nil, nil)
}

// APIError is a non-2xx response from Core.
type APIError struct {
	Status int
	Method string
	Path   string
	Body   string
	// Code is Core's own error code from the response body (apperr), when there was one. A plugin that
	// wants to branch on WHAT went wrong should read this rather than scan Body for a phrase.
	Code string
	// RetryAfter is the server's own instruction, from the Retry-After header. Honoured by the retry loop;
	// exposed so a plugin doing its own scheduling can honour it too.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	return fmt.Sprintf("nilda api: %s %s: %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// Forbidden reports whether the call was refused for want of permission or scope — the error a plugin
// gets when it uses a capability it did not declare. Distinguished because it is not retryable and the fix
// is a manifest change, not a backoff.
func (e *APIError) Forbidden() bool { return e.Status == http.StatusForbidden }

// NotFound, Conflict and RateLimited name the other three a plugin actually branches on.
func (e *APIError) NotFound() bool    { return e.Status == http.StatusNotFound }
func (e *APIError) Conflict() bool    { return e.Status == http.StatusConflict }
func (e *APIError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

// Retryable reports whether the same request has a chance of succeeding later.
//
// 429 and 5xx: the server said "not now" or "something broke here". Everything else is about the request
// itself, and repeating it changes nothing — retrying a 403 is a plugin hammering a door its manifest
// never asked for the key to.
func (e *APIError) Retryable() bool {
	return e.Status == http.StatusTooManyRequests || e.Status >= 500
}

// ---------------------------------------------------------------------------
// retry machinery
// ---------------------------------------------------------------------------

// defaultMaxRetries is how many times a retryable failure is re-attempted.
//
// Three, deliberately small. The point is to survive a rate limit and a restarting Core, not to sit in a
// loop while something is genuinely broken — a plugin that retries for two minutes has turned an error
// into a hang, and a hang is harder to diagnose than the error was.
const defaultMaxRetries = 3

// SetMaxRetries overrides how many times a retryable failure is re-attempted. Zero disables retries.
//
// Exported because a plugin doing bulk work may want more patience, and one on a request path may want
// none at all — a hook has an aggregate budget it must not blow through (Core's dispatcher gives every
// subscriber ten seconds between them).
func (a *API) SetMaxRetries(n int) {
	if a == nil || n < 0 {
		return
	}
	a.maxRetries = n
}

// encodeBody renders a request body to bytes ONCE, so every attempt sends the same thing.
//
// An io.Reader body is consumed by the first attempt, and a retry would send an empty one — a bug that
// only appears under exactly the conditions the retry exists for.
func encodeBody(body any) ([]byte, error) {
	switch b := body.(type) {
	case nil:
		return nil, nil
	case []byte:
		return b, nil
	case io.Reader:
		raw, err := io.ReadAll(b)
		if err != nil {
			return nil, fmt.Errorf("nilda: reading request body: %w", err)
		}
		return raw, nil
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("nilda: encoding request body: %w", err)
		}
		return raw, nil
	}
}

// retryable reports whether this request may be sent again.
//
// GET, HEAD, PUT and DELETE are idempotent by definition, so repeating one is safe.
//
// POST and PATCH are not. A POST may have created something before the response was lost, and sending it
// again would create a second. PATCH is not idempotent either — RFC 5789 is explicit about that, and a
// patch expressed as "add one to the stock count" applied twice is simply wrong.
//
// For both, the Idempotency-Key header is the answer: it is exactly what the header is for, and it is the
// difference between an importer that survives a 502 at product 312 and one that ends up with two of it.
func (a *API) retryable(method string, _ []byte) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete:
		return true
	case http.MethodPost, http.MethodPatch:
		return a.idempotencyKey != ""
	default:
		return false
	}
}

// retryAfterOf pulls the server's own wait instruction out of an error, if it gave one.
func retryAfterOf(err error) time.Duration {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.RetryAfter
	}
	return 0
}

// parseRetryAfter reads a Retry-After header. Only the delta-seconds form is honoured; an HTTP-date would
// need a trusted clock on both ends, and guessing wrong there means either hammering or stalling.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || secs < 0 {
		return 0
	}
	if d := time.Duration(secs) * time.Second; d <= maxBackoff {
		return d
	}
	// A server asking for longer than we are willing to wait is refused rather than obeyed: a plugin
	// blocked for ten minutes inside a hook is worse than a failed call.
	return maxBackoff
}

const (
	baseBackoff = 200 * time.Millisecond
	maxBackoff  = 10 * time.Second
	// maxBackoffShift is where doubling stops mattering: 200ms << 6 is 12.8s, already past maxBackoff.
	//
	// It exists because the shift OVERFLOWS. `baseBackoff << attempt` is int64 nanoseconds, so from around
	// attempt 36 it wraps to a negative duration — which then slips past the `> maxBackoff` clamp (a
	// negative number is not greater than ten seconds) and reaches rand.Int64N, which panics on a
	// non-positive argument. SetMaxRetries is exported and documented as something a bulk-writing plugin
	// might raise, so this was reachable by following the advice in its own comment.
	maxBackoffShift = 6
)

// backoffFor is the un-jittered wait before one attempt: exponential, clamped, and safe at any attempt
// number a caller can produce.
func backoffFor(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	if attempt > maxBackoffShift {
		attempt = maxBackoffShift
	}
	if wait := baseBackoff << attempt; wait < maxBackoff {
		return wait
	}
	return maxBackoff
}

// sleepBackoff waits before the next attempt: the server's instruction if it gave one, otherwise
// exponential backoff with jitter.
//
// Jitter matters more than it looks. Core dispatches a hook to every subscribed plugin at once, so a
// rate limit hits them together — and without jitter they would all wake at the same instant and produce
// the same burst that caused it.
func sleepBackoff(ctx context.Context, attempt int, serverWait time.Duration) error {
	wait := serverWait
	if wait <= 0 {
		wait = backoffFor(attempt)
		// Jitter is half the wait at most. Guarded because rand.Int64N panics on a non-positive argument,
		// and "the backoff is so small it rounds to nothing" should not be a crash.
		if half := int64(wait / 2); half > 0 {
			wait += time.Duration(rand.Int64N(half))
		}
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errorCodeOf pulls Core's own error code out of a response body, so a plugin can branch on WHAT went
// wrong rather than pattern-matching a sentence that may be translated or reworded.
func errorCodeOf(payload []byte) string {
	var envelope struct {
		Code  string `json:"code"`
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return ""
	}
	if envelope.Code != "" {
		return envelope.Code
	}
	return envelope.Error.Code
}
