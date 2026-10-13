package nilda

import (
	"context"
	"encoding/json"
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
