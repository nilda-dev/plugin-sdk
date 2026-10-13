// Command tablepost is a shipping carrier plugin with no carrier behind it: the template a real one — Sendcloud,
// Shippo, a carrier's own API — starts from (docs/SHIPPING.md).
//
// It answers every method of shipping.carrier/v1 in the same places a real one does, with the carrier played by a
// price table and a label printer that prints nothing real:
//
//	DescribeCarrier  two services, "standard" and "express", test or live from the owner's setting
//	Rates            a price from weight bands and the route (domestic, EU, the world) — CARRIER: ask its rates API
//	CreateLabel      a label per request Key, found again by the same Key — CARRIER: look it up first, then buy
//	VoidLabel        cancels a label it made — CARRIER: its cancel call
//	Track            "label_printed" until a real carrier says more — CARRIER: its tracking call
//
// To write a real one, copy this directory, replace the four places marked CARRIER, and keep the rule the contract
// keeps: a carrier saying no is a RESULT with a failure code, never an error.
package main

import (
	"context"
	"fmt"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func main() { nilda.Serve(&TablePost{}) }

// TablePost is the plugin: a Handler, and a nilda.Carrier behind nilda.CarrierMethods.
type TablePost struct {
	live bool

	mu     sync.Mutex
	labels map[string]nilda.LabelResult // by request Key — a real carrier keeps them; this one keeps them here
	next   int
}

// Init reads the owner's setting. A real carrier also checks its API key here and says so in the log when it is
// missing, rather than failing every call later.
func (p *TablePost) Init(_ context.Context, core *nilda.Core) (nilda.InitResult, error) {
	if !core.HasCapability("service_provider") {
		return nilda.InitResult{}, fmt.Errorf("tablepost needs the service_provider capability and \"provides\": " +
			"[\"shipping.carrier/v1\"] in plugin.json")
	}
	p.live = core.SettingBool("live")
	p.labels = map[string]nilda.LabelResult{}
	return nilda.InitResult{}, nil
}

// HandleHook answers nothing: every call this plugin gets is a contract method, which Serve hands to ServiceMethods.
func (p *TablePost) HandleHook(_ context.Context, hook string, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("tablepost does not handle %s", hook)
}

// HandleEvent: it subscribes to none.
func (p *TablePost) HandleEvent(context.Context, string, []byte) error { return nil }

// ServiceMethods makes it a provider of shipping.carrier/v1: every request checked before it is answered, every
// answer before it leaves.
func (p *TablePost) ServiceMethods() []nilda.ServiceMethod { return nilda.CarrierMethods(p) }

// DescribeCarrier is settings only, never a call to the carrier.
func (p *TablePost) DescribeCarrier(context.Context) (nilda.CarrierDescription, error) {
	supports := []string{nilda.CarrierSupportRates, nilda.CarrierSupportLabels, nilda.CarrierSupportVoid, nilda.CarrierSupportTrack}
	return nilda.CarrierDescription{Name: "Table Post", Services: []nilda.CarrierService{
		{Key: "standard", Label: "Standard", Description: "2–5 working days", Supports: supports, Test: !p.live,
			TestHint: map[string]string{"en": "Test mode: no label is billed and tracking numbers are samples."}},
		{Key: "express", Label: "Express", Description: "Next working day", Supports: supports, RequiresPhone: true,
			Test: !p.live, TestHint: map[string]string{"en": "Test mode: no label is billed and tracking numbers are samples."}},
	}}, nil
}

// eu is the European Union, for the middle price band.
var eu = map[string]bool{"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true, "EE": true,
	"FI": true, "FR": true, "DE": true, "GR": true, "HU": true, "IE": true, "IT": true, "LV": true, "LT": true, "LU": true,
	"MT": true, "NL": true, "PL": true, "PT": true, "RO": true, "SK": true, "SI": true, "ES": true, "SE": true}

// Rates prices the parcels from a table — CARRIER: ask its rates API, and turn "no route" into RatesNoService.
func (p *TablePost) Rates(_ context.Context, req nilda.RatesRequest) (nilda.RatesResult, error) {
	if req.Currency != "EUR" {
		return nilda.RatesResult{Status: nilda.RatesNoService, FailureCode: nilda.CarrierNoService,
			FailureMessage: "Table Post quotes in EUR"}, nil
	}
	var grams int64
	for _, parcel := range req.Parcels {
		if parcel.WeightGrams > 31_500 {
			return nilda.RatesResult{Status: nilda.RatesInvalid, FailureCode: nilda.CarrierInvalidParcel,
				FailureMessage: "a parcel is at most 31.5 kg"}, nil
		}
		grams += parcel.WeightGrams
	}
	base := int64(490) // domestic, first kilogram
	switch {
	case req.Origin.Country == req.Destination.Country:
	case eu[req.Origin.Country] && eu[req.Destination.Country]:
		base = 990
	default:
		base = 2490
		if req.Customs == nil {
			return nilda.RatesResult{Status: nilda.RatesInvalid, FailureCode: nilda.CarrierCustomsMissing,
				FailureMessage: "outside the EU a parcel needs its customs declaration"}, nil
		}
	}
	perKg := base / 5
	extra := (grams - 1) / 1000 * perKg
	on := map[string]bool{}
	for _, s := range req.Services {
		on[s] = true
	}
	var rates []nilda.CarrierRate
	for _, r := range []nilda.CarrierRate{
		{Service: "standard", Title: "Table Post Standard", AmountMinor: base + extra, Currency: "EUR", DaysMin: 2, DaysMax: 5},
		{Service: "express", Title: "Table Post Express", AmountMinor: 2*(base+extra) + 500, Currency: "EUR", DaysMin: 1, DaysMax: 1},
	} {
		if len(on) == 0 || on[r.Service] {
			rates = append(rates, r)
		}
	}
	if len(rates) == 0 {
		return nilda.RatesResult{Status: nilda.RatesNoService, FailureCode: nilda.CarrierNoService}, nil
	}
	return nilda.RatesResult{Status: nilda.RatesOK, Rates: rates}, nil
}

// CreateLabel buys one label per Key — CARRIER: look the Key up at the carrier FIRST, and buy only if it finds none,
// so an answer lost on the way back never buys a second label.
func (p *TablePost) CreateLabel(_ context.Context, req nilda.LabelRequest) (nilda.LabelResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := p.labels[req.Key]; ok {
		return l, nil
	}
	if req.Service == "express" && req.Destination.Phone == "" {
		return nilda.LabelResult{Status: nilda.LabelRejected, FailureCode: nilda.CarrierInvalidAddress,
			FailureMessage: "express needs the receiver's phone number"}, nil
	}
	p.next++
	ref := fmt.Sprintf("tp_%d", p.next)
	l := nilda.LabelResult{Status: nilda.LabelPurchased, ProviderRef: ref, TrackingNumber: fmt.Sprintf("TP%010d", p.next),
		CarrierName: "Table Post", Service: req.Service, CostMinor: 0, Currency: "EUR",
		Documents: []nilda.DocumentRef{nilda.DocumentData("label", "pdf", labelPDF(ref, req))}}
	p.labels[req.Key] = l
	return l, nil
}

// VoidLabel cancels a label — CARRIER: its cancel call; past the carrier's cut-off it answers not_voidable.
func (p *TablePost) VoidLabel(_ context.Context, req nilda.VoidRequest) (nilda.VoidResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, l := range p.labels {
		if l.ProviderRef == req.ProviderRef {
			delete(p.labels, key)
			return nilda.VoidResult{Status: nilda.VoidVoided}, nil
		}
	}
	return nilda.VoidResult{Status: nilda.VoidRejected, FailureCode: nilda.CarrierNotVoidable, FailureMessage: "no such label"}, nil
}

// Track — CARRIER: its tracking call, each scan mapped to the contract's statuses.
func (p *TablePost) Track(context.Context, nilda.TrackRequest) (nilda.TrackResult, error) {
	return nilda.TrackResult{Status: nilda.TrackLabelPrinted}, nil
}

// labelPDF is a one-page PDF naming the shipment — what a real carrier sends as its label.
func labelPDF(ref string, req nilda.LabelRequest) []byte {
	text := fmt.Sprintf("Table Post %s %s to %s %s", ref, req.Service, req.Destination.PostalCode, req.Destination.Country)
	stream := fmt.Sprintf("BT /F1 14 Tf 40 760 Td (%s) Tj ET", text)
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	out := []byte("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = len(out)
		out = append(out, fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o)...)
	}
	xref := len(out)
	out = append(out, fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)...)
	for _, off := range offsets {
		out = append(out, fmt.Sprintf("%010d 00000 n \n", off)...)
	}
	return append(out, fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)...)
}
