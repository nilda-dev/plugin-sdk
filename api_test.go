package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The API client is what replaced contract v1's five read methods, so these tests are about the thing a
// plugin author actually depends on: the token reaching Core, a write being possible at all, and a refusal
// arriving as something they can act on.
func TestAPICarriesTheTokenAndWrites(t *testing.T) {
	var gotAuth, gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotMethod, gotPath = r.Header.Get("Authorization"), r.Method, r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","title":"Widget"}`))
	}))
	defer srv.Close()

	api := newAPI(srv.URL, "nlda_secret", []string{"read:content", "write:content"})
	if api == nil {
		t.Fatal("no client was built from a valid base URL and token")
	}

	var out struct{ ID, Title string }
	if err := api.Post(context.Background(), "/content",
		map[string]any{"type": "product", "title": "Widget"}, &out); err != nil {
		t.Fatalf("create: %v", err)
	}
	if gotAuth != "Bearer nlda_secret" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotMethod != http.MethodPost || gotPath != "/content" {
		t.Fatalf("%s %s", gotMethod, gotPath)
	}
	if gotBody == "" || !json.Valid([]byte(gotBody)) {
		t.Fatalf("body = %q", gotBody)
	}
	if out.ID != "c1" {
		t.Fatalf("decoded = %+v", out)
	}
}

// A refusal must arrive with Core's own reason. A plugin author debugging a failed write should read why
// Core said no, not a bare status code — and 403 is called out because its fix is a manifest change rather
// than a retry.
func TestAPIErrorCarriesTheReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"message":"API token is missing the required scope: write:content"}}`))
	}))
	defer srv.Close()

	api := newAPI(srv.URL, "nlda_secret", []string{"read:content"})
	err := api.Post(context.Background(), "/content", map[string]any{"title": "x"}, nil)
	if err == nil {
		t.Fatal("a 403 was reported as success")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("error type = %T, want *APIError", err)
	}
	if !apiErr.Forbidden() {
		t.Fatalf("status %d was not recognised as a refusal", apiErr.Status)
	}
	if apiErr.Body == "" {
		t.Fatal("Core's reason was dropped — the author sees a status code and nothing else")
	}
}

// A plugin that declared no API-implying capability gets no client, and using it must say so in terms that
// point at the fix (declare a capability) rather than panicking.
func TestNoAPIGivesAUsableError(t *testing.T) {
	if newAPI("", "", nil) != nil {
		t.Fatal("a client was built with no base URL or token")
	}
	if newAPI("http://x", "", nil) != nil {
		t.Fatal("a client was built with no token")
	}

	var nilAPI *API
	err := nilAPI.JSON(context.Background(), http.MethodGet, "/content", nil, nil)
	if err == nil {
		t.Fatal("calling with no API access succeeded")
	}
	if _, isAPIErr := err.(*APIError); isAPIErr {
		t.Fatal("a missing client should not look like a server refusal")
	}
	if nilAPI.HasScope("read:content") || nilAPI.Scopes() != nil {
		t.Fatal("a nil client reported scopes")
	}
}

// Scopes are the mirror of what the owner approved at install. The server is the authority; this exists so
// a plugin can fail at startup with a clear message rather than halfway through an import.
func TestScopesAreReportedForFailingFast(t *testing.T) {
	api := newAPI("http://x", "t", []string{"read:content", "write:content"})
	if !api.HasScope("write:content") || api.HasScope("write:media") {
		t.Fatalf("scopes = %v", api.Scopes())
	}
	got := api.Scopes()
	got[0] = "tampered"
	if api.HasScope("tampered") {
		t.Fatal("Scopes() handed out the internal slice — a caller can rewrite what the plugin believes it holds")
	}
}

// Query parameters are how a plugin filters now, which is the thing contract v1 could not do at all.
func TestGetEncodesQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	api := newAPI(srv.URL, "t", []string{"read:content"})
	if err := api.Get(context.Background(), "/content",
		url.Values{"type": {"product"}, "status": {"published"}}, &struct{}{}); err != nil {
		t.Fatalf("get: %v", err)
	}
	if gotQuery != "status=published&type=product" {
		t.Fatalf("query = %q", gotQuery)
	}
}

// ---------------------------------------------------------------------------
// retries, backoff and idempotency (D2)
// ---------------------------------------------------------------------------

// A 429 with Retry-After is waited out and the call succeeds.
//
// This is in the SDK rather than left to each plugin because it cannot reasonably be left to each plugin:
// an importer creating five hundred products meets a rate limit sooner or later, and the difference
// between "the SDK waited 200ms" and "the import died at product 312" is the whole experience of writing
// against this platform.
func TestARateLimitIsWaitedOutAndRetried(t *testing.T) {
	var attempts int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"RATE_LIMITED","message":"slow down"}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"p1"}}`))
	}))
	defer srv.Close()

	api := newAPI(srv.URL, "tok", nil)
	var out struct{ Data struct{ ID string } }
	if err := api.Get(context.Background(), "/content/product", nil, &out); err != nil {
		t.Fatalf("a rate limit should have been retried, not returned: %v", err)
	}
	if out.Data.ID != "p1" {
		t.Errorf("the retried response was not decoded: %+v", out)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("attempts = %d, want exactly one retry", got)
	}
}

// A 5xx is retried; a 4xx that is the caller's fault is not.
//
// Retrying a 403 is a plugin hammering a door its manifest never asked for the key to — the fix is a
// manifest change, and no amount of backoff produces one.
func TestOnlyTheServersProblemsAreRetried(t *testing.T) {
	cases := map[string]struct {
		status    int
		wantCalls int32
		retryable bool
	}{
		"server error":    {http.StatusBadGateway, 4, true},
		"gateway timeout": {http.StatusGatewayTimeout, 4, true},
		"forbidden":       {http.StatusForbidden, 1, false},
		"not found":       {http.StatusNotFound, 1, false},
		"conflict":        {http.StatusConflict, 1, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var calls int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"code":"X"}`))
			}))
			defer srv.Close()

			api := newAPI(srv.URL, "tok", nil)
			err := api.Get(context.Background(), "/content/product", nil, nil)
			if err == nil {
				t.Fatal("expected the failure to surface")
			}
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("expected an *APIError, got %T", err)
			}
			if ae.Retryable() != tc.retryable {
				t.Errorf("Retryable() = %v for %d", ae.Retryable(), tc.status)
			}
			if got := atomic.LoadInt32(&calls); got != tc.wantCalls {
				t.Errorf("calls = %d, want %d", got, tc.wantCalls)
			}
		})
	}
}

// THE property that makes retries safe. A POST may have created something before the response was lost,
// so repeating it blindly is how an importer ends up with two of row 312. It is retried only when the
// caller supplied an idempotency key — which is exactly what the key is for.
func TestAPostIsOnlyRetriedWithAnIdempotencyKey(t *testing.T) {
	newServer := func(calls *int32, sawKey *string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(calls, 1)
			*sawKey = r.Header.Get("Idempotency-Key")
			w.WriteHeader(http.StatusBadGateway)
		}))
	}

	var plainCalls int32
	var plainKey string
	plain := newServer(&plainCalls, &plainKey)
	defer plain.Close()
	if err := newAPI(plain.URL, "tok", nil).
		Post(context.Background(), "/content/product", map[string]any{"title": "x"}, nil); err == nil {
		t.Fatal("expected the 502 to surface")
	}
	if plainCalls != 1 {
		t.Errorf("a keyless POST was sent %d times — a create may have succeeded on the first", plainCalls)
	}
	if plainKey != "" {
		t.Errorf("an idempotency key was sent without being asked for: %q", plainKey)
	}

	var keyedCalls int32
	var keyedKey string
	keyed := newServer(&keyedCalls, &keyedKey)
	defer keyed.Close()
	if err := newAPI(keyed.URL, "tok", nil).WithIdempotencyKey("row-312").
		Post(context.Background(), "/content/product", map[string]any{"title": "x"}, nil); err == nil {
		t.Fatal("expected the 502 to surface")
	}
	if keyedCalls != 4 {
		t.Errorf("a keyed POST was sent %d times, want 4 (one attempt plus three retries)", keyedCalls)
	}
	if keyedKey != "row-312" {
		t.Errorf("Idempotency-Key = %q", keyedKey)
	}
}

// A key belongs to ONE operation, so WithIdempotencyKey derives a client instead of mutating the shared
// one. Sharing a key across two creates would make the second return the first one's result.
func TestWithIdempotencyKeyDoesNotMutateTheSharedClient(t *testing.T) {
	api := newAPI("http://example.invalid", "tok", nil)
	derived := api.WithIdempotencyKey("k1")

	if api.idempotencyKey != "" {
		t.Error("the shared client picked up a key belonging to one operation")
	}
	if derived.idempotencyKey != "k1" {
		t.Errorf("the derived client carries %q", derived.idempotencyKey)
	}
	if second := api.WithIdempotencyKey("k2"); second.idempotencyKey == derived.idempotencyKey {
		t.Error("two operations ended up sharing a key")
	}
}

// The request body must be replayable: an io.Reader is consumed by the first attempt, and a retry that
// sends an empty body is a bug appearing only under the conditions the retry exists for.
func TestARetriedRequestSendsTheSameBody(t *testing.T) {
	var bodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		n := len(bodies)
		mu.Unlock()
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	// With a key, because PATCH is not idempotent on its own (RFC 5789) — "add one to the stock count"
	// applied twice is simply wrong.
	if err := newAPI(srv.URL, "tok", nil).WithIdempotencyKey("edit-1").
		Patch(context.Background(), "/content/product/1", map[string]any{"title": "v2"}, nil); err != nil {
		t.Fatalf("the retry should have succeeded: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("attempts = %d", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("the retry sent a different body:\n first: %q\nsecond: %q", bodies[0], bodies[1])
	}
	if bodies[0] == "" {
		t.Error("the body was empty on the first attempt")
	}
}

// Core's own error code reaches the plugin, so it can branch on WHAT went wrong instead of pattern-matching
// a sentence that may be reworded or translated.
func TestTheErrorCarriesCoresCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"FORBIDDEN","message":"the token lacks content.write"}`))
	}))
	defer srv.Close()

	err := newAPI(srv.URL, "tok", nil).Post(context.Background(), "/content/product", map[string]any{}, nil)
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("expected an *APIError, got %T", err)
	}
	if ae.Code != "FORBIDDEN" {
		t.Errorf("Code = %q", ae.Code)
	}
	if !ae.Forbidden() {
		t.Error("Forbidden() is false for a 403")
	}
}

// Retries must not outlive the caller's context: a hook has an aggregate budget (Core gives every
// subscriber ten seconds between them), and a plugin that keeps backing off past a cancelled context has
// turned an error into a hang.
func TestRetriesStopWhenTheContextEnds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := newAPI(srv.URL, "tok", nil).Get(ctx, "/content/product", nil, nil)
	if err == nil {
		t.Fatal("expected a failure")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("retries ran for %s after the context ended", elapsed)
	}
}

// The backoff must be safe at any attempt number, because WithMaxRetries is exported and its own comment
// suggests a bulk-writing plugin might raise it.
//
// `baseBackoff << attempt` is int64 nanoseconds, so from around attempt 36 it wrapped to a NEGATIVE
// duration — which slipped past the `> maxBackoff` clamp, because a negative number is not greater than
// ten seconds, and reached rand.Int64N, which panics on a non-positive argument. A plugin that followed
// the advice in the comment crashed its own process.
func TestBackoffIsBoundedAtAnyAttemptNumber(t *testing.T) {
	for _, attempt := range []int{-1, 0, 1, 5, 6, 7, 20, 36, 62, 63, 64, 1000} {
		got := backoffFor(attempt)
		if got <= 0 {
			t.Errorf("attempt %d produced a non-positive wait (%v) — the jitter would panic on it", attempt, got)
		}
		if got > maxBackoff {
			t.Errorf("attempt %d waits %v, past the %v ceiling", attempt, got, maxBackoff)
		}
	}
	// And it really does grow before it flattens, or the retries are all instant.
	if backoffFor(0) >= backoffFor(3) {
		t.Error("the backoff does not increase between attempts")
	}
}

// sleepBackoff itself must not panic either, which is the path the bug actually surfaced through.
func TestSleepBackoffSurvivesAHugeAttemptNumber(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	for _, attempt := range []int{0, 40, 63, 1000} {
		if err := sleepBackoff(ctx, attempt, 0); err == nil {
			t.Errorf("attempt %d: expected the context deadline to end the wait", attempt)
		}
	}
}

// TestWithMaxRetriesDerivesRatherThanMutates.
//
// It was SetMaxRetries, mutating the one *API that core.API() returns to every handler. Core's bulkhead
// allows sixteen concurrent calls into a plugin, so a handler tuning retries while another was mid-request
// raced on a shared int — and the doc actively suggested per-call-site tuning, which is the racy use.
//
// Run under -race with concurrent readers, because that is the shape the bug had.
func TestWithMaxRetriesDerivesRatherThanMutates(t *testing.T) {
	shared := newAPI("http://core.test/api/v1", "t", []string{"content:write"})
	if shared.maxRetries != defaultMaxRetries {
		t.Fatalf("default = %d, want %d", shared.maxRetries, defaultMaxRetries)
	}

	patient := shared.WithMaxRetries(10)
	none := shared.WithMaxRetries(0)

	if shared.maxRetries != defaultMaxRetries {
		t.Errorf("deriving a client must not touch the shared one (now %d)", shared.maxRetries)
	}
	if patient.maxRetries != 10 || none.maxRetries != 0 {
		t.Errorf("derived clients carry the wrong value: %d / %d", patient.maxRetries, none.maxRetries)
	}
	// The rest of the client comes with it — a derived client that lost its token is worse than no helper.
	if patient.token != shared.token || patient.baseURL != shared.baseURL || patient.http != shared.http {
		t.Error("a derived client must carry the token, base URL and http client")
	}

	// A negative is refused rather than turned into "never retry", which would silently disable the
	// resilience an author thought they were tuning.
	if got := shared.WithMaxRetries(-1); got.maxRetries != defaultMaxRetries {
		t.Errorf("a negative must be ignored, got %d", got.maxRetries)
	}
	if (*API)(nil).WithMaxRetries(3) != nil {
		t.Error("a nil API must stay nil")
	}

	// Concurrent derivation + reads: the race detector is the assertion.
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c := shared.WithMaxRetries(n)
			_ = c.maxRetries
			_ = shared.maxRetries
		}(i)
	}
	wg.Wait()
}

// M19 (2026-09-23 plugin hunt): the SDK promised plugins "media upload" over Core's API, and its client could
// send only JSON — Core's POST /media reads a multipart `file` field, so no plugin could upload anything.
// This server reads the request the way Core's uploadMedia does; a retried attempt must carry the same file.
func TestAPluginCanUploadMedia(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Method != http.MethodPost || r.URL.Path != "/media" {
			t.Errorf("upload went to %s %s", r.Method, r.URL.Path)
		}
		file, fh, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":{"code":"VALIDATION","message":"a multipart file field is required"}}`, http.StatusUnprocessableEntity)
			return
		}
		body, _ := io.ReadAll(file)
		if fh.Filename != "kettle.png" || string(body) != "PNGBYTES" || r.FormValue("alt") != "A kettle" {
			t.Errorf("upload carried %q %q alt=%q", fh.Filename, body, r.FormValue("alt"))
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable) // Core restarting: retried, with the same bytes
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"m1"}}`))
	}))
	defer srv.Close()

	api := newAPI(srv.URL, "tok", []string{"write:media"}).WithIdempotencyKey("upload-1")
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := api.UploadMedia(context.Background(), "kettle.png", strings.NewReader("PNGBYTES"), "A kettle", &out); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if out.Data.ID != "m1" || attempts != 2 {
		t.Fatalf("id = %q after %d attempts, want m1 after a retry", out.Data.ID, attempts)
	}
}
