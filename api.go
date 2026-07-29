package nilda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		scopes:  scopes,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Do performs a request against Core's API with the plugin's token attached.
//
// path is relative to the API root ("/content", "/media"). body may be nil, an io.Reader, or any value
// that marshals to JSON. The caller decodes the response.
func (a *API) Do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if a == nil {
		return nil, fmt.Errorf("nilda: this plugin has no API access — declare a capability that grants it (content.read, content.write, media.read, …)")
	}
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case io.Reader:
		r = b
	default:
		buf, err := json.Marshal(b)
		if err != nil {
			return nil, fmt.Errorf("nilda: encoding request body: %w", err)
		}
		r = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	return a.http.Do(req)
}

// JSON performs a request and decodes a successful response into out (which may be nil to discard it).
//
// A non-2xx status becomes an *APIError carrying Core's own error body, so a plugin author debugging a
// failed write reads the reason Core gave rather than a bare status code.
func (a *API) JSON(ctx context.Context, method, path string, body, out any) error {
	res, err := a.Do(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		return &APIError{Status: res.StatusCode, Method: method, Path: path, Body: string(payload)}
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
}

func (e *APIError) Error() string {
	return fmt.Sprintf("nilda api: %s %s: %d: %s", e.Method, e.Path, e.Status, e.Body)
}

// Forbidden reports whether the call was refused for want of permission or scope — the error a plugin
// gets when it uses a capability it did not declare. Distinguished because it is not retryable and the fix
// is a manifest change, not a backoff.
func (e *APIError) Forbidden() bool { return e.Status == http.StatusForbidden }
