package nildatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Reference is a fake of Core's /reference routes — who sells on the site (Seller), whether it publishes a privacy
// policy (PrivacyPolicy, v0.42.0), and, from v0.50.0, the EU VAT rates (EUVATRates), the ECB's exchange rates
// (ExchangeRates) and the tax table (TaxTable) — answering what the test set, as Core answers plugins. A read the test
// set nothing for answers 404, as Core does with no Nilda Central behind it; Down makes a route fail with a status (an
// outage), and Asked says which routes the plugin read, in order.
//
//	ref := nildatest.NewReference()
//	ref.SetPrivacyPolicy(nilda.PrivacyPolicy{Published: true, Path: "/privacy"})
//	ref.SetExchangeRates(nilda.ExchangeRateSet{Day: "2026-11-02", Rates: map[string]string{"USD": "1.0842"}})
//	core, _, srv := ref.Core("shop")
//	defer srv.Close()
type Reference struct {
	mu       sync.Mutex
	seller   nilda.Seller
	privacy  nilda.PrivacyPolicy
	euVAT    *nilda.EUVATRateSet
	rates    *nilda.ExchangeRateSet
	taxTable *nilda.TaxTable
	down     map[string]int
	asked    []string
}

// SetEUVATRates sets what EUVATRates answers, whatever day is asked.
func (r *Reference) SetEUVATRates(set nilda.EUVATRateSet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.euVAT = &set
}

// SetExchangeRates sets what ExchangeRates answers, whatever instant is asked.
func (r *Reference) SetExchangeRates(set nilda.ExchangeRateSet) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rates = &set
}

// SetTaxTable sets what TaxTable answers, whatever day is asked.
func (r *Reference) SetTaxTable(t nilda.TaxTable) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.taxTable = &t
}

// Down makes the route at path ("/reference/exchange-rates") answer status from now on — Core or Central out — and
// status 0 brings it back.
func (r *Reference) Down(path string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.down == nil {
		r.down = map[string]int{}
	}
	if status == 0 {
		delete(r.down, path)
		return
	}
	r.down[path] = status
}

// Asked is every route the plugin read, with its query, in order.
func (r *Reference) Asked() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.asked...)
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

// Core builds a fake Core for the plugin key whose API answers the reference routes. Close the server when done.
func (r *Reference) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(http.HandlerFunc(r.serve))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

// serve answers one request to the reference routes, as Core does. NewSite's fake site serves its own Reference through
// it, so a plugin that reads both Core's resources and its reference data runs against one fake Core.
func (r *Reference) serve(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.URL.RequestURI())
	status, isDown := r.down[req.URL.Path]
	var data any
	switch {
	case req.Method != http.MethodGet:
	case req.URL.Path == "/reference/seller":
		data = r.seller
	case req.URL.Path == "/reference/privacy-policy":
		data = r.privacy
	case req.URL.Path == "/reference/eu-vat-rates" && r.euVAT != nil:
		data = *r.euVAT
	case req.URL.Path == "/reference/exchange-rates" && r.rates != nil:
		data = *r.rates
	case req.URL.Path == "/reference/tax-table" && r.taxTable != nil:
		data = *r.taxTable
	}
	r.mu.Unlock()
	if isDown {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "unavailable",
			"message": "reference data is not available"}})
		return
	}
	if data == nil {
		writeError(w, notFound("no such route: %s %s", req.Method, req.URL.Path))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
