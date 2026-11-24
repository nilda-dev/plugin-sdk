package nildatest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// LookQuote asks a shop's LookQuote endpoint (nilda.CommerceEndpoints.LookQuote, v0.74.0, EXPERIMENTAL) as a widget's
// script does from the visitor's browser: a same-origin POST of req as JSON to path on h (your plugin's route handler).
// It fails the test when req is not a request nilda.LookQuoteRequest.Validate accepts, when the answer is not marked
// Cache-Control: no-store, when a 200 is not a nilda.LookQuoteAnswer that ValidateFor(req) accepts, or when any other
// status is not a nilda.LookQuoteRefusal answered with the status its Status names. It returns the answer, or the
// refusal.
//
//	ans, ref := nildatest.LookQuote(t, shop.Routes(), "/shop/look/quote",
//	    nilda.LookQuoteRequest{Lines: []nilda.LookQuoteLine{{SKU: "COAT-M", Qty: 1}}})
func LookQuote(t testing.TB, h http.Handler, path string, req nilda.LookQuoteRequest) (nilda.LookQuoteAnswer, *nilda.LookQuoteRefusal) {
	t.Helper()
	if err := req.Validate(); err != nil {
		t.Fatalf("nildatest.LookQuote: the request is not one a caller may send: %v", err)
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("nildatest.LookQuote: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "https://shop.example"+path, bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://shop.example")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	res := rec.Result()
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(strings.ToLower(cc), "no-store") {
		t.Fatalf("nildatest.LookQuote: %s answered %d with Cache-Control %q — a look quote is the visitor's own: answer it no-store",
			path, res.StatusCode, cc)
	}
	dec := json.NewDecoder(rec.Body)
	if res.StatusCode == http.StatusOK {
		var a nilda.LookQuoteAnswer
		if err := dec.Decode(&a); err != nil {
			t.Fatalf("nildatest.LookQuote: %s answered 200 with no LookQuoteAnswer: %v", path, err)
		}
		if err := a.ValidateFor(req); err != nil {
			t.Fatalf("nildatest.LookQuote: %s: %v", path, err)
		}
		return a, nil
	}
	var ref nilda.LookQuoteRefusal
	if err := dec.Decode(&ref); err != nil {
		t.Fatalf("nildatest.LookQuote: %s answered %d with no LookQuoteRefusal: %v", path, res.StatusCode, err)
	}
	if err := ref.Validate(); err != nil {
		t.Fatalf("nildatest.LookQuote: %s: %v", path, err)
	}
	if ref.Status() != res.StatusCode {
		t.Fatalf("nildatest.LookQuote: %s refused with %q under status %d; that code is answered %d", path, ref.Code,
			res.StatusCode, ref.Status())
	}
	return nilda.LookQuoteAnswer{}, &ref
}
