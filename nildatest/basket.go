package nildatest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Basket asks a shop's Basket endpoint (nilda.CommerceEndpoints.Basket, v0.75.0, EXPERIMENTAL) as a widget's script
// does from the visitor's browser: a same-origin GET of path on h (your plugin's route handler). It fails the test when
// the answer is not a 200 marked Cache-Control: no-store, or not a body nilda.DecodeBasketAnswer accepts — "lines" an
// array of {"sku", "qty"} spelled exactly so, within the bounds. It returns the answer. Put the visitor's basket cookie
// on the request by wrapping h, or ask a shop whose test basket needs none.
//
//	basket := nildatest.Basket(t, shop.Routes(), "/shop/api/cart")
func Basket(t testing.TB, h http.Handler, path string) nilda.BasketAnswer {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "https://shop.example"+path, nil)
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("nildatest.Basket: %s answered %d, want 200 with the visitor's basket", path, res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(strings.ToLower(cc), "no-store") {
		t.Fatalf("nildatest.Basket: %s answered with Cache-Control %q — a basket is the visitor's own: answer it no-store", path, cc)
	}
	a, err := nilda.DecodeBasketAnswer(rec.Body)
	if err != nil {
		t.Fatalf("nildatest.Basket: %s: %v", path, err)
	}
	return a
}
