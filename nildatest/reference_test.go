package nildatest

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// The kit answers Reference().PrivacyPolicy and Seller with what the test set, as Core answers them: a site with
// no written policy and no page says so, rather than erroring.
func TestReferencePrivacyPolicyFake(t *testing.T) {
	ctx := context.Background()
	ref := NewReference()
	core, _, srv := ref.Core("shop")
	defer srv.Close()
	api := core.API().Reference()

	p, err := api.PrivacyPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Published || p.Path != "" {
		t.Errorf("a site with no policy read as %+v", p)
	}
	ref.SetPrivacyPolicy(nilda.PrivacyPolicy{Published: true, Path: "/privacy"})
	ref.SetSeller(nilda.Seller{Name: "Santoor GmbH", Country: "AT"})
	if p, _ = api.PrivacyPolicy(ctx); !p.Published || p.Path != "/privacy" {
		t.Errorf("a published policy read as %+v", p)
	}
	if s, err := api.Seller(ctx); err != nil || s.Name != "Santoor GmbH" || s.Country != "AT" {
		t.Errorf("the seller read as %+v (%v)", s, err)
	}
}

// The reads a shop's invoices and tax table make answer what the test set, a read nothing was set for is a 404 as
// with no Central, an outage is an error, and Asked records each read.
func TestReferenceAnswersTheRatesAndTheTable(t *testing.T) {
	ref := NewReference()
	ref.SetExchangeRates(nilda.ExchangeRateSet{Day: "2026-11-02", Rates: map[string]string{"USD": "1.0842"}})
	ref.SetEUVATRates(nilda.EUVATRateSet{Rates: []nilda.EUVATRate{{Country: "AT", Type: "standard"}}})
	core, _, srv := ref.Core("shop")
	defer srv.Close()
	ctx := context.Background()
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	rates, err := core.API().Reference().ExchangeRates(ctx, now)
	if err != nil || rates.Rates["USD"] != "1.0842" {
		t.Fatalf("exchange rates: %+v %v", rates, err)
	}
	if vat, err := core.API().Reference().EUVATRates(ctx, now); err != nil || len(vat.Rates) != 1 {
		t.Fatalf("EU VAT rates: %+v %v", vat, err)
	}
	if _, err := core.API().Reference().TaxTable(ctx, now); err == nil {
		t.Error("a tax table nobody set was answered")
	}
	ref.Down("/reference/exchange-rates", http.StatusServiceUnavailable)
	if _, err := core.API().Reference().ExchangeRates(ctx, now); err == nil {
		t.Error("an outage was answered")
	}
	if asked := ref.Asked(); len(asked) < 4 || !strings.HasPrefix(asked[0], "/reference/exchange-rates?at=") {
		t.Errorf("asked: %v", asked)
	}
}
