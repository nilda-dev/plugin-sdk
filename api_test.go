package nilda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
