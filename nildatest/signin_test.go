package nildatest

import (
	"net/http"
	"net/http/httptest"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func TestAsksForSignInIsTheShellsOwnQuestion(t *testing.T) {
	asks := httptest.NewRecorder()
	nilda.CommerceSignInRequired(asks)
	if !AsksForSignIn(asks.Result()) {
		t.Fatal("the SDK's own sign-in answer is not recognised")
	}
	for name, write := range map[string]func(http.ResponseWriter){
		"401 without the header": func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) },
		"403": func(w http.ResponseWriter) {
			w.Header().Set(nilda.CommerceSignInHeader, "required")
			w.WriteHeader(http.StatusForbidden)
		},
		"200": func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
	} {
		rec := httptest.NewRecorder()
		write(rec)
		if AsksForSignIn(rec.Result()) {
			t.Errorf("%s was read as asking for sign-in: the shell would show it as a failure", name)
		}
	}
	if AsksForSignIn(nil) {
		t.Error("no response asks for nothing")
	}
}
