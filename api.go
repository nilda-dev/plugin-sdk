package nilda

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
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
	// per-operation, and sharing one across two different creates gets the second REFUSED (422): Core
	// remembers which request a key named.
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
// proxy. Exported for a plugin author making its OWN outbound calls — to a payment gateway, an SMS
// provider. The proxy has to know who is calling to apply the right manifest's hosts; a client that takes
// its proxy from the environment is identified already, by the proxy address Core hands the process (see
// below), and this adds the key as a header too.
//
// Pass nil for http.DefaultTransport. Go already routes through the proxy from the environment Core sets;
// this adds the label that says who is asking.
//
// A base that is not an *http.Transport — a tracing or retry wrapper — cannot have the tunnel label set
// from here, since only *http.Transport has ProxyConnectHeader. That no longer loses the identity: Core
// puts the plugin's key in the proxy address it hands the process (HTTPS_PROXY=http://<key>@…), and any
// transport that takes its proxy from the environment sends it on every request and every CONNECT. The
// same holds for a bare http.Client and for a plugin written in another language. Wrap outward — your
// wrapper around NewTransport(nil) — and both labels are present.
//
// # Why the label goes on in TWO places
//
// For a plain http request the proxy reads the request's own headers, so setting one is enough. For an
// HTTPS request it is not, and the reason is easy to miss: Go opens the tunnel with a CONNECT request and
// then sends everything else INSIDE the TLS session. The proxy never sees the request's headers at all —
// it sees only the CONNECT. So the identity has to be on the CONNECT itself, which is what
// ProxyConnectHeader is for.
//
// Shipping only the request header meant identification worked for exactly the calls nobody makes and
// failed for every call to a real provider. It surfaced against a live install: the manifest declared
// accounts.google.com, the proxy logged "host not declared" with an EMPTY plugin key, and the plugin
// reported itself unusable. Both halves are set here so an author who uses this transport is identified
// on both kinds of call.
func NewTransport(base http.RoundTripper) http.RoundTripper {
	key := os.Getenv("NILDA_PLUGIN_KEY")
	if base == nil {
		// A clone of the default rather than the default itself: ProxyConnectHeader is per-transport state,
		// and mutating http.DefaultTransport would label every request the PROCESS makes, including ones
		// made by code that never asked for this.
		def, _ := http.DefaultTransport.(*http.Transport)
		if def != nil {
			base = withConnectIdentity(def.Clone(), key)
		} else {
			base = http.DefaultTransport
		}
	} else if tr, ok := base.(*http.Transport); ok {
		base = withConnectIdentity(tr.Clone(), key)
	}
	return &pluginTransport{base: base, key: key}
}

// withConnectIdentity puts the plugin's key on the CONNECT request that opens an HTTPS tunnel, which is
// the only part of such a request the egress proxy can read.
func withConnectIdentity(tr *http.Transport, key string) *http.Transport {
	if key == "" {
		return tr
	}
	if tr.ProxyConnectHeader == nil {
		tr.ProxyConnectHeader = http.Header{}
	}
	tr.ProxyConnectHeader.Set("X-Nilda-Plugin", key)
	return tr
}

// HTTPClient is a ready-made client for a plugin's own outbound calls, carrying the identity the egress
// proxy needs and a timeout. A bare http.Client that takes its proxy from the environment is identified
// too, by the proxy address Core hands the process; what the proxy refuses — "this plugin did not declare
// that host" for a host the manifest declares — is a client pointed at it by hand with no identity at all.
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
	// PLAIN HTTP ONLY. For an https request the identity rides the CONNECT (withConnectIdentity), and the
	// request's own headers travel INSIDE the TLS session to the provider, where the proxy cannot strip them:
	// every call a plugin made to api.stripe.com told Stripe the plugin's key (Core's 2026-09-24 whole-plan
	// review, A-9). A plain http request is read by the proxy, which removes the header before forwarding.
	if t.key == "" || !strings.EqualFold(r.URL.Scheme, "http") {
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
// across two different creates gets the second refused. The intended shape is a fresh key per unit of work —
//
//	for _, row := range rows {
//	    api.WithIdempotencyKey(row.ID).Post(ctx, "/content/product", row, nil)
//	}
//
// — so an importer interrupted at row 312 can be run again from the start, within 24 hours, without
// producing 312 duplicates.
//
// What Core does with the key, on every write under /api/rest/v1:
//
//   - the FIRST request's result is kept for 24 hours, errors included, and every repeat with the same key
//     and the same request gets it back without running again (the response carries Idempotent-Replayed:
//     true) — so a retry after a 5xx returns that 5xx rather than risking a second write;
//   - the same key on a DIFFERENT request is refused with 422;
//   - a repeat that arrives while the first is still running is refused with 409;
//   - a request Core REFUSED (any 4xx) changed nothing, so its key is given back and a corrected retry runs;
//   - keys are scoped to your plugin's identity, not its token, so they hold across a restart.
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
	return a.do(ctx, method, path, payload, "application/json")
}

var errNoAPI = errors.New("nilda: this plugin has no API access — declare a capability that grants it " +
	"(content.read, content.write, media.read, …)")

// do sends one attempt with an already-encoded body, so a retry replays exactly the same bytes. contentType
// labels a non-nil body: JSON for everything but an upload, whose multipart boundary is part of its type.
func (a *API) do(ctx context.Context, method, path string, payload []byte, contentType string) (*http.Response, error) {
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
		req.Header.Set("Content-Type", contentType)
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
	return a.send(ctx, method, path, payload, "application/json", out)
}

// send is JSON's request loop for an already-encoded body of any type: the retries, the backoff and the
// error shape are the same for an upload as for a JSON write.
func (a *API) send(ctx context.Context, method, path string, payload []byte, contentType string, out any) error {
	if a == nil {
		return errNoAPI
	}
	var lastErr error
	for attempt := 0; ; attempt++ {
		res, err := a.do(ctx, method, path, payload, contentType)
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

// UploadMedia puts one file in the site's media library — POST /media, which takes a multipart upload, not
// JSON (the 2026-09-23 plugin hunt's M19: this client could only send JSON, so the "media upload" the SDK
// promised had no way to be written). Requires `media.write`. out receives Core's answer, the new media item
// inside `data`, exactly as the other writes do; alt is the image's description and may be empty.
//
// The file is read whole before sending, so a retry replays the same bytes — and so is bounded by memory:
// an upload is an image or a document, not a stream. Core runs it through the same checks as the admin's
// own uploader (type sniffing, the allowlist, SVG sanitising, metadata stripping) and may refuse it.
func (a *API) UploadMedia(ctx context.Context, filename string, file io.Reader, alt string, out any) error {
	if a == nil {
		return errNoAPI
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	// A KEYED upload sends the same bytes every time it is run (Core's 2026-09-24 whole-plan review, I-12). The
	// writer picks a random boundary, and Core fingerprints the body under the key: the importer re-run after a
	// crash — the case a key exists for — was refused "already used for a different request". Derived from the
	// key, the boundary is the same on every run of it, and still unguessable to anything reading the file.
	if a.idempotencyKey != "" {
		sum := sha256.Sum256([]byte("nilda-upload\x00" + a.idempotencyKey))
		if err := mw.SetBoundary("nilda" + hex.EncodeToString(sum[:24])); err != nil {
			return err
		}
	}
	part, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, file); err != nil {
		return fmt.Errorf("nilda: reading the file to upload: %w", err)
	}
	if alt != "" {
		if err := mw.WriteField("alt", alt); err != nil {
			return err
		}
	}
	if err := mw.Close(); err != nil {
		return err
	}
	return a.send(ctx, http.MethodPost, "/media", buf.Bytes(), mw.FormDataContentType(), out)
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

// WithMaxRetries returns a client that re-attempts a retryable failure at most n times. Zero disables
// retries.
//
// A plugin doing bulk work may want more patience; one on a request path may want none at all, because a
// hook has an aggregate budget it must not blow through (Core's dispatcher gives every subscriber ten
// seconds between them). Both wants are per-CALL-SITE, which is why this returns a derived client the way
// WithIdempotencyKey does:
//
//	bulk := core.API().WithMaxRetries(10)
//	fast := core.API().WithMaxRetries(0)
//
// It was `SetMaxRetries(n)`, mutating the one *API that core.API() hands to everybody. Core's bulkhead
// allows SIXTEEN concurrent calls into a plugin, so one handler tuning retries while another was mid-flight
// was a data race on a shared int — silent in production, and the kind of thing a library must not hand an
// author who did exactly what the doc suggested.
func (a *API) WithMaxRetries(n int) *API {
	if a == nil || n < 0 {
		return a
	}
	clone := *a
	clone.maxRetries = n
	return &clone
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
	// non-positive argument. WithMaxRetries is exported and documented as something a bulk-writing plugin
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
