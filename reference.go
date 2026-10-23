package nilda

import (
	"context"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// Reference is reference data Core holds for every plugin: no capability is needed to read it, only Core's API.
//
// Seller is who sells on this site, as its imprint says — what an invoice names as the supplier.
//
// The EU's VAT rates (EUVATRates) are Nilda Central's copy of the European Commission's Taxes in Europe Database,
// refreshed hourly and held a day ahead, which Core keeps a day and keeps through an outage. The Commission says TEDB
// is not the legal reference (the Official Journal is): the set says where and when it was read.
type Reference struct{ api *API }

// Reference is the reference data.
func (a *API) Reference() *Reference { return &Reference{api: a} }

// EUVATRate is one rate of one member state.
type EUVATRate struct {
	// Country is ISO 3166-1 alpha-2 — Greece is GR, as an address names it.
	Country string `json:"country"`
	// Type is "standard" or "reduced".
	Type string `json:"type"`
	// Kind is TEDB's: "default", "reduced_rate", "super_reduced_rate", "parking_rate", "exempted", …
	Kind string `json:"kind"`
	// RateBps is the rate in basis points (2000 = 20%, 2550 = 25.5%); nil where TEDB gives no number.
	RateBps *int `json:"rate_bps"`
	// Category is TEDB's category for a reduced rate ("FOODSTUFFS"); CategoryLabel its description.
	Category      string `json:"category,omitempty"`
	CategoryLabel string `json:"category_label,omitempty"`
	// Scope is "" for the nationwide rate; otherwise what it is limited to ("import", "canary islands").
	Scope string `json:"scope,omitempty"`
	// Since is the day the rate has applied since, yyyy-mm-dd.
	Since string `json:"since"`
}

// EUVATRateSet is the rates applying on a day.
type EUVATRateSet struct {
	// SituationOn is the day the set was written for — the newest on or before the day asked.
	SituationOn string      `json:"situation_on"`
	Source      string      `json:"source"`
	FetchedAt   time.Time   `json:"fetched_at"`
	CheckedAt   time.Time   `json:"checked_at"`
	Rates       []EUVATRate `json:"rates"`
}

// Standard is a member state's nationwide standard rate in basis points; ok is false for a country not in the set
// (any country outside the EU).
func (s EUVATRateSet) Standard(country string) (bps int, ok bool) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if country == "EL" {
		country = "GR"
	}
	for _, r := range s.Rates {
		if r.Country == country && r.Type == "standard" && r.Scope == "" && r.RateBps != nil {
			return *r.RateBps, true
		}
	}
	return 0, false
}

// Reduced is a member state's reduced rates for a TEDB category, nationwide; empty when it has none for it.
func (s EUVATRateSet) Reduced(country, category string) []EUVATRate {
	country = strings.ToUpper(strings.TrimSpace(country))
	var out []EUVATRate
	for _, r := range s.Rates {
		if r.Country == country && r.Type == "reduced" && r.Scope == "" && strings.EqualFold(r.Category, category) {
			out = append(out, r)
		}
	}
	return out
}

// EUVATRates reads the set applying on day (its calendar day in Brussels).
func (r *Reference) EUVATRates(ctx context.Context, day time.Time) (EUVATRateSet, error) {
	var out struct {
		Data EUVATRateSet `json:"data"`
	}
	brussels, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		brussels = time.FixedZone("CET", 3600)
	}
	q := url.Values{"date": {day.In(brussels).Format(time.DateOnly)}}
	if err := r.api.Get(ctx, "/reference/eu-vat-rates", q, &out); err != nil {
		return EUVATRateSet{}, err
	}
	return out.Data, nil
}

// ExchangeRateSet is the European Central Bank's euro reference rates published at or before an instant — the rates an
// invoice in a currency other than the seller's national one states its VAT in the seller's currency at (Directive
// 2006/112/EC Art. 91(2), 230). Rates are written as the ECB writes them: units of the currency per 1 EUR, as decimal
// strings, never floats, so no cent is lost to a binary fraction. Day is the ECB's day (yyyy-mm-dd), PublishedAt the
// moment it was published (16:00 Central European Time), Source and FetchedAt where and when Central read it.
type ExchangeRateSet struct {
	Day         string            `json:"day"`
	PublishedAt time.Time         `json:"published_at"`
	Rates       map[string]string `json:"rates"`
	Source      string            `json:"source"`
	FetchedAt   time.Time         `json:"fetched_at"`
}

// Rate is how many units of to one unit of from buys, through the euro: rates[to] / rates[from], with EUR = 1. Exact
// (a *big.Rat): round it only once, at the amount. ok is false when either currency has no rate in the set; a currency to
// itself is 1.
func (s ExchangeRateSet) Rate(from, to string) (*big.Rat, bool) {
	from, to = strings.ToUpper(strings.TrimSpace(from)), strings.ToUpper(strings.TrimSpace(to))
	per := func(cur string) (*big.Rat, bool) {
		if cur == "EUR" {
			return big.NewRat(1, 1), true
		}
		v, ok := s.Rates[cur]
		if !ok {
			return nil, false
		}
		r, ok := new(big.Rat).SetString(v)
		if !ok || r.Sign() <= 0 {
			return nil, false
		}
		return r, true
	}
	if from == to {
		if _, ok := per(from); !ok {
			return nil, false
		}
		return big.NewRat(1, 1), true
	}
	f, ok := per(from)
	if !ok {
		return nil, false
	}
	t, ok := per(to)
	if !ok {
		return nil, false
	}
	return new(big.Rat).Quo(t, f), true
}

// ExchangeRates reads the ECB's euro reference rates in force at an instant: the newest set published at or before it
// (a weekend or a bank holiday reads the working day before). No capability is needed — it is public reference data
// Core reads from Nilda Central, holds by the hour and keeps through an outage; with no Central it is an error, and an
// invoice that needs a rate says so rather than guessing one.
func (r *Reference) ExchangeRates(ctx context.Context, at time.Time) (ExchangeRateSet, error) {
	var out struct {
		Data ExchangeRateSet `json:"data"`
	}
	q := url.Values{"at": {at.UTC().Format(time.RFC3339)}}
	if err := r.api.Get(ctx, "/reference/exchange-rates", q, &out); err != nil {
		return ExchangeRateSet{}, err
	}
	return out.Data, nil
}

// Seller is who sells on this site, from its imprint (the owner's Legal settings) — the supplier an invoice names
// (Directive 2006/112/EC Art. 226(5)). Read it at the moment an invoice is made, never keep a copy: the imprint is the
// one place the site keeps it, so an invoice and the published imprint cannot disagree. A field the owner left empty is
// "".
type Seller struct {
	Name          string `json:"name"`
	LegalForm     string `json:"legal_form,omitempty"`
	RegisterNo    string `json:"register_number,omitempty"`
	RegisterCourt string `json:"register_court,omitempty"`
	// VATID is the seller's VAT identification number as the owner entered it ("ATU12345678"); "" when it has none.
	VATID    string `json:"vat_id,omitempty"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Street   string `json:"street,omitempty"`
	Locality string `json:"locality,omitempty"`
	Region   string `json:"region,omitempty"`
	Postcode string `json:"postal_code,omitempty"`
	// Country is ISO 3166-1 alpha-2.
	Country string `json:"country,omitempty"`
}

// Seller reads who sells on this site.
func (r *Reference) Seller(ctx context.Context) (Seller, error) {
	var out struct {
		Data Seller `json:"data"`
	}
	if err := r.api.Get(ctx, "/reference/seller", nil, &out); err != nil {
		return Seller{}, err
	}
	return out.Data, nil
}
