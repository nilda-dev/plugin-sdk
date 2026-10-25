package nildatest

import (
	"net/http"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// AsksForSignIn reports whether a response is the members-only answer Core's account shell draws its sign-in panel
// for (docs/COMMERCE.md "Sign-in"): 401 with Nilda-Sign-In: required. Any other refusal — a 403, a 401 without the
// header — the shell shows as a failure, so a view that means "sign in first" and answers anything else leaves a
// signed-out buyer with nowhere to go.
//
//	rec := httptest.NewRecorder()
//	shop.ServeHTTP(rec, httptest.NewRequest("GET", "/shop/account?view=orders", nil))
//	if !nildatest.AsksForSignIn(rec.Result()) { t.Fatal("a signed-out buyer is not offered sign-in") }
func AsksForSignIn(res *http.Response) bool {
	return res != nil && res.StatusCode == http.StatusUnauthorized && res.Header.Get(nilda.CommerceSignInHeader) == "required"
}
