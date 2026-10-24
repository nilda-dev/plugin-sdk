package nildatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Reference is a fake of Core's /reference routes about the site itself — who sells on it (Seller) and whether it
// publishes a privacy policy (PrivacyPolicy, v0.42.0) — answering what the test set, as Core answers plugins.
//
//	ref := nildatest.NewReference()
//	ref.SetPrivacyPolicy(nilda.PrivacyPolicy{Published: true, Path: "/privacy"})
//	core, _, srv := ref.Core("shop")
//	defer srv.Close()
type Reference struct {
	mu      sync.Mutex
	seller  nilda.Seller
	privacy nilda.PrivacyPolicy
}

// NewReference builds a site with no imprint and no privacy policy.
func NewReference() *Reference { return &Reference{} }

// SetSeller sets the imprint Seller answers.
func (r *Reference) SetSeller(s nilda.Seller) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seller = s
}

// SetPrivacyPolicy sets what PrivacyPolicy answers.
func (r *Reference) SetPrivacyPolicy(p nilda.PrivacyPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.privacy = p
}

// Core builds a fake Core for the plugin key whose API answers the two routes. Close the server when done.
func (r *Reference) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		var data any
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/reference/seller":
			data = r.seller
		case req.Method == http.MethodGet && req.URL.Path == "/reference/privacy-policy":
			data = r.privacy
		}
		r.mu.Unlock()
		if data == nil {
			writeError(w, notFound("no such route: %s %s", req.Method, req.URL.Path))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}
