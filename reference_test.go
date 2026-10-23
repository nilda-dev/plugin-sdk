package nilda

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTheEUVATRatesAreReadForTheDayInBrussels(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reference/eu-vat-rates" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = r.URL.Query().Get("date")
		bps := func(v int) *int { return &v }
		_ = json.NewEncoder(w).Encode(map[string]any{"data": EUVATRateSet{SituationOn: "2026-10-13", Source: "TEDB",
			Rates: []EUVATRate{
				{Country: "AT", Type: "standard", Kind: "default", RateBps: bps(2000), Since: "2026-07-01"},
				{Country: "GR", Type: "standard", Kind: "default", RateBps: bps(2400), Since: "2026-07-01"},
				{Country: "DE", Type: "standard", Kind: "default", RateBps: bps(1900), Scope: "import", Since: "2026-07-01"},
				{Country: "AT", Type: "reduced", Kind: "reduced_rate", RateBps: bps(1000), Category: "FOODSTUFFS", Since: "2026-07-01"},
			}}})
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", nil, nil, srv.URL, "t", nil)
	// 23:30 UTC on the 12th is already the 13th in Brussels.
	set, err := core.API().Reference().EUVATRates(context.Background(), time.Date(2026, 10, 12, 23, 30, 0, 0, time.UTC))
	if err != nil || asked != "2026-10-13" {
		t.Fatalf("asked %q (%v)", asked, err)
	}
	if v, ok := set.Standard("at"); !ok || v != 2000 {
		t.Fatalf("AT %d %v", v, ok)
	}
	if v, ok := set.Standard("EL"); !ok || v != 2400 {
		t.Fatal("Greece by its VAT prefix EL is GR")
	}
	if _, ok := set.Standard("DE"); ok {
		t.Fatal("Germany's import rate was read as its nationwide rate")
	}
	if _, ok := set.Standard("US"); ok {
		t.Fatal("a country outside the EU has a standard rate")
	}
	if r := set.Reduced("AT", "foodstuffs"); len(r) != 1 || *r[0].RateBps != 1000 {
		t.Fatalf("AT foodstuffs: %+v", r)
	}
}

func TestVisitorCountryIsTwoCapitalLettersOrNothing(t *testing.T) {
	for in, want := range map[string]string{"AT": "AT", "": "", "at": "", "AUT": "", "1A": ""} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if in != "" {
			r.Header.Set("X-Nilda-Visitor-Country", in)
		}
		got, ok := VisitorCountry(r)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
}

func TestTheSellerIsReadFromTheImprint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reference/seller" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"name":"Kari Shop e.U.","vat_id":"ATU12345678","street":"Ring 1","postal_code":"1010","locality":"Wien","country":"AT"}}`))
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", nil, nil, srv.URL, "t", nil)
	s, err := core.API().Reference().Seller(context.Background())
	if err != nil || s.Name != "Kari Shop e.U." || s.VATID != "ATU12345678" || s.Postcode != "1010" || s.Country != "AT" {
		t.Fatalf("%+v %v", s, err)
	}
}

// The ECB's euro rates are read for an instant, and a cross rate goes through the euro exactly (math/big, never a float):
// USD per EUR 1.0850 and PLN per EUR 4.2800 make PLN per USD 4.2800/1.0850.
func TestTheEuroRatesAreReadForAnInstantAndCrossRatesAreExact(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reference/exchange-rates" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = r.URL.Query().Get("at")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": ExchangeRateSet{Day: "2026-10-16",
			PublishedAt: time.Date(2026, 10, 16, 14, 0, 0, 0, time.UTC), Source: "ECB",
			Rates: map[string]string{"USD": "1.0850", "PLN": "4.2800", "JPY": "177.82"}}})
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", nil, nil, srv.URL, "t", nil)
	at := time.Date(2026, 10, 16, 14, 5, 0, 0, time.UTC)
	set, err := core.API().Reference().ExchangeRates(context.Background(), at)
	if err != nil || asked != "2026-10-16T14:05:00Z" || set.Day != "2026-10-16" {
		t.Fatalf("asked %q (%v): %+v", asked, err, set)
	}
	pln, ok := set.Rate("USD", "PLN")
	if !ok || pln.Cmp(big.NewRat(42800, 10850)) != 0 {
		t.Fatalf("PLN per USD = %v %v, want 4.2800/1.0850 exactly", pln, ok)
	}
	if usd, ok := set.Rate("EUR", "USD"); !ok || usd.Cmp(big.NewRat(10850, 10000)) != 0 {
		t.Fatalf("USD per EUR = %v %v", usd, ok)
	}
	if eur, ok := set.Rate("USD", "EUR"); !ok || eur.Cmp(big.NewRat(10000, 10850)) != 0 {
		t.Fatalf("EUR per USD = %v %v", eur, ok)
	}
	if one, ok := set.Rate("usd", "USD"); !ok || one.Cmp(big.NewRat(1, 1)) != 0 {
		t.Fatalf("a currency to itself = %v %v", one, ok)
	}
	if _, ok := set.Rate("USD", "XXX"); ok {
		t.Fatal("an unknown currency has a rate")
	}
	if _, ok := set.Rate("EUR", "EUR"); !ok {
		t.Fatal("EUR to EUR is 1")
	}
}
