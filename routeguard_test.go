package nilda

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestOnlyCoresProxyReachesTheRoute is the 2026-11-11 review's finding 1: any process on the machine could dial the
// loopback port with X-Nilda-User naming anybody, and CurrentUser believed it.
func TestOnlyCoresProxyReachesTheRoute(t *testing.T) {
	routeKey.Store("k-this-launch")
	t.Cleanup(func() { routeKey.Store("") })
	var seenKey string
	addr, err := StartHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenKey = r.Header.Get(RouteKeyHeader)
		v, _ := CurrentUser(r)
		_, _ = io.WriteString(w, v.UserID)
	}))
	if err != nil {
		t.Fatal(err)
	}
	get := func(key string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/shop/account", nil)
		req.Header.Set("X-Nilda-User", "owner-id")
		if key != "" {
			req.Header.Set(RouteKeyHeader, key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	for _, forged := range []string{"", "wrong", "k-this-launc", "k-this-launch2"} {
		if code, body := get(forged); code != http.StatusForbidden || strings.Contains(body, "owner-id") {
			t.Fatalf("a request with key %q was answered %d %q", forged, code, body)
		}
	}
	if code, body := get("k-this-launch"); code != http.StatusOK || body != "owner-id" {
		t.Fatalf("Core's own request was answered %d %q", code, body)
	}
	if seenKey != "" {
		t.Fatalf("the handler saw the route key %q", seenKey)
	}
}

// TestARouteWithoutAKeyFromCoreStillAnswers: a Core that sends no key is the old Core; nothing it proxies is refused.
func TestARouteWithoutAKeyFromCoreStillAnswers(t *testing.T) {
	routeKey.Store("")
	addr, err := StartHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("answered %d", res.StatusCode)
	}
}

// TestARouteBodyIsBounded is the review's finding 14: StartHTTP read bodies of any size.
func TestARouteBodyIsBounded(t *testing.T) {
	routeKey.Store("")
	var readErr error
	addr, err := StartHTTPWith(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}), HTTPOptions{MaxBodyBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.Post("http://"+addr+"/", "application/octet-stream", bytes.NewReader(make([]byte, 4096)))
	if err == nil {
		res.Body.Close()
	}
	var tooBig *http.MaxBytesError
	if !errors.As(readErr, &tooBig) {
		t.Fatalf("a 4 KiB body read past a 1 KiB bound: %v", readErr)
	}
}

// The review of 2026-11-11: StartHTTP(GuardRoute(mux)) answered 403 to everything, the inner guard looking for the header
// the outer one had removed.
func TestAGuardInsideAGuardLetsCoresRequestThrough(t *testing.T) {
	routeKey.Store("k")
	t.Cleanup(func() { routeKey.Store("") })
	addr, err := StartHTTP(GuardRoute(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })))
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/", nil)
	req.Header.Set(RouteKeyHeader, "k")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("a doubly guarded route answered %d", res.StatusCode)
	}
}
