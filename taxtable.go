package nilda

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// The tax table (v0.34.0): the reference figures Nilda Central keeps for the places beyond the EU — VAT and GST rates,
// the registration lines for sellers based elsewhere, import lines for low-value goods, a note where a country taxes
// sales in another way (the United States). Every row names its official source and the day it was read; Central
// refuses a row without them. The EU's own rates stay on EUVATRates (the Commission's TEDB).
//
// A row is a starting point the merchant checks, never tax advice: a shop copies the rows into a table its owner sees
// and can change, and charges a country's rates only where the owner says they are registered there.

// The kinds of row the table holds. A reader skips a kind it does not know: the table may gain kinds.
const (
	TaxRowRate         = "rate"          // a VAT/GST rate: TaxRateValue
	TaxRowThreshold    = "threshold"     // a registration line: TaxThresholdValue
	TaxRowImportRegime = "import_regime" // a low-value import line: TaxImportRegimeValue
	TaxRowRegistration = "registration"  // a scheme a seller registers for: TaxRegistrationValue
	TaxRowNotice       = "notice"        // words a merchant must read, no figure: TaxNoticeValue
)

// TaxTable is the reference rows that apply on a day.
type TaxTable struct {
	// Version changes exactly when a row of the table changes (a hash of the rows): a reader that kept a version has
	// nothing new to read while it is the same.
	Version string `json:"version"`
	// On is the day the rows were chosen for, yyyy-mm-dd.
	On   string            `json:"on"`
	Rows []TaxReferenceRow `json:"rows"`
}

// TaxReferenceRow is one figure of the table.
type TaxReferenceRow struct {
	// Key is the row's stable name — "GB:rate:standard", "CA-QC:rate:qst", "NO:threshold:voec" — the same across
	// every day and every change of its value.
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Country is ISO 3166-1 alpha-2; Subdivision the ISO 3166-2 part without the country ("QC"), "" for the whole
	// country.
	Country     string `json:"country"`
	Subdivision string `json:"subdivision,omitempty"`
	// Label is how the row reads to a person: "VAT standard rate", "QST".
	Label string `json:"label"`
	// Value is the kind's own value; read it with Rate, Threshold, ImportRegime, Registration or Notice.
	Value json.RawMessage `json:"value"`
	// ValidFrom is the first day the value applies (the day its source states, else the day it was read); ValidTo
	// the last, "" while open.
	ValidFrom string `json:"valid_from"`
	ValidTo   string `json:"valid_to,omitempty"`
	// SourceURL is the official page the value was read on, SourceReadOn the day; SourceNote what a reader should
	// know about the source ("the page gives no start date").
	SourceURL    string `json:"source_url"`
	SourceReadOn string `json:"source_read_on"`
	SourceNote   string `json:"source_note,omitempty"`
	// Next is the row's value from the day after On when it changes then; nil when it does not.
	Next *TaxReferenceNext `json:"next,omitempty"`
}

// TaxReferenceNext is a value announced for the next day.
type TaxReferenceNext struct {
	ValidFrom string          `json:"valid_from"`
	Value     json.RawMessage `json:"value"`
}

// TaxRateValue is a rate row's value.
type TaxRateValue struct {
	// RatePercent is the rate: 20 is 20%, 9.975 is Québec's QST.
	RatePercent float64 `json:"rate_percent"`
	// Scheme is the Key-less name of the registration the rate is charged under ("GB:vat", "CA:gst", "CA-QC:qst"): a
	// seller charges it only when registered for that scheme. The table holds a registration row for it.
	Scheme string `json:"scheme"`
	// Priority orders the rates of one place (ascending); Compound taxes the base plus the rates before it. Canada's
	// QST is priority 2 and NOT compound: it is worked on the price without the GST.
	Priority int  `json:"priority"`
	Compound bool `json:"compound,omitempty"`
}

// TaxThresholdValue is a registration line's value: the sales at which a seller must register for Scheme.
type TaxThresholdValue struct {
	// AmountMinor is the line in Currency's minor units (MinorUnits): 3000000 CAD is CAD 30,000. 0 means a seller the
	// line is for registers from the first sale.
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// Period is how the sales are counted: "rolling_12m", "calendar_year", "annual" (the source says a year and no
	// more), "none" (no period: from the first sale).
	Period string `json:"period"`
	// Counts is which sales count: "b2c_digital", "b2c_all", "b2c_small_consignments", "worldwide_all", "all_supplies".
	Counts string `json:"counts"`
	Scheme string `json:"scheme"`
}

// TaxImportRegimeValue is a low-value import line: up to it a registered seller charges the tax at checkout; above it
// the parcel is taxed at the border.
type TaxImportRegimeValue struct {
	ThresholdMinor int64  `json:"threshold_minor"`
	Currency       string `json:"currency"`
	// Basis is what the line is measured on: "consignment", "item", or "" where the source does not say.
	Basis  string `json:"basis"`
	Scheme string `json:"scheme"`
}

// TaxRegistrationValue names a scheme a seller registers for.
type TaxRegistrationValue struct {
	Scheme string `json:"scheme"`
}

// TaxNoticeValue is words with no figure.
type TaxNoticeValue struct {
	Text string `json:"text"`
}

func taxValue[T any](r TaxReferenceRow, kind string) (T, bool) {
	var v T
	if r.Kind != kind || json.Unmarshal(r.Value, &v) != nil {
		return v, false
	}
	return v, true
}

// Rate is a rate row's value; ok is false for a row of another kind.
func (r TaxReferenceRow) Rate() (TaxRateValue, bool) { return taxValue[TaxRateValue](r, TaxRowRate) }

// Threshold is a registration line's value; ok is false for a row of another kind.
func (r TaxReferenceRow) Threshold() (TaxThresholdValue, bool) {
	return taxValue[TaxThresholdValue](r, TaxRowThreshold)
}

// ImportRegime is an import line's value; ok is false for a row of another kind.
func (r TaxReferenceRow) ImportRegime() (TaxImportRegimeValue, bool) {
	return taxValue[TaxImportRegimeValue](r, TaxRowImportRegime)
}

// Registration is a registration row's value; ok is false for a row of another kind.
func (r TaxReferenceRow) Registration() (TaxRegistrationValue, bool) {
	return taxValue[TaxRegistrationValue](r, TaxRowRegistration)
}

// Notice is a notice row's value; ok is false for a row of another kind.
func (r TaxReferenceRow) Notice() (TaxNoticeValue, bool) {
	return taxValue[TaxNoticeValue](r, TaxRowNotice)
}

// Of is the table's rows for a country (any case), its subdivisions' with them.
func (t TaxTable) Of(country string) []TaxReferenceRow {
	country = strings.ToUpper(strings.TrimSpace(country))
	var out []TaxReferenceRow
	for _, r := range t.Rows {
		if r.Country == country {
			out = append(out, r)
		}
	}
	return out
}

// TaxTable reads the reference rows applying on day (its calendar day in Brussels, as EUVATRates counts it). Core
// keeps what it read through an outage of Nilda Central; an install with no Central, or one that never read the table,
// answers an error.
func (r *Reference) TaxTable(ctx context.Context, day time.Time) (TaxTable, error) {
	var out struct {
		Data TaxTable `json:"data"`
	}
	brussels, err := time.LoadLocation("Europe/Brussels")
	if err != nil {
		brussels = time.FixedZone("CET", 3600)
	}
	q := url.Values{"date": {day.In(brussels).Format(time.DateOnly)}}
	if err := r.api.Get(ctx, "/reference/tax-table", q, &out); err != nil {
		return TaxTable{}, err
	}
	return out.Data, nil
}
