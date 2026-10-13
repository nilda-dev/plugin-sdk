package nilda

import (
	"context"
	"net/url"
	"strings"
	"time"
)

// Reference is public reference data Core holds for every plugin: no capability is needed to read it, only Core's API.
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
