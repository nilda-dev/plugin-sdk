package nilda

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// SHIPPING CARRIERS, ON THE SERVICE DESK (docs/SHIPPING.md; commerce's plans-2026-10-12/03 report, unit SH1).
//
// A carrier — an aggregator such as Sendcloud or Shippo, or one carrier's own API — is a plugin that `provides`
// shipping.carrier/v1; the shop `uses` it. The merchant connects their OWN carrier account to the carrier plugin; Nilda
// takes no fee and resells nothing. Core relays every call and checks it against the contract file; the shop keeps
// every record (quotes, labels, tracking) — the carrier plugin keeps none it needs to answer again.
//
// THE RULE EVERY METHOD KEEPS: a carrier saying no is a RESULT, never an error. "No service to that address",
// "the address is invalid", "the carrier is down" are statuses with a failure code the shop can act on — show the
// backup rate, ask for a phone number — while an error is the plugin failing, which Core counts against the plugin's
// circuit for this contract. CarrierMethods turns a rates call that runs out of its time into an "unavailable" result
// for the same reason: a slow carrier is not a broken plugin.

// ContractShippingCarrier is the contract's name.
const ContractShippingCarrier = "shipping.carrier/v1"

// The contract's methods.
const (
	CarrierDescribe     = "describe"
	CarrierRates        = "rates"
	CarrierLabelCreate  = "label.create"
	CarrierLabelVoid    = "label.void"
	CarrierTrack        = "track"
	CarrierDocumentsGet = "documents"
)

// What a carrier service can do (CarrierService.Supports).
const (
	CarrierSupportRates     = "rates"
	CarrierSupportLabels    = "labels"
	CarrierSupportVoid      = "void"
	CarrierSupportTrack     = "track"
	CarrierSupportCustoms   = "customs"
	CarrierSupportReturns   = "returns"
	CarrierSupportSignature = "signature"
	CarrierSupportInsurance = "insurance"
)

var carrierSupports = map[string]bool{CarrierSupportRates: true, CarrierSupportLabels: true, CarrierSupportVoid: true,
	CarrierSupportTrack: true, CarrierSupportCustoms: true, CarrierSupportReturns: true, CarrierSupportSignature: true,
	CarrierSupportInsurance: true}

// The failure codes a carrier result may carry — a closed set, so the shop can act on each.
const (
	CarrierUnavailable        = "unavailable"         // the carrier could not be reached, or did not answer in time
	CarrierInvalidAddress     = "invalid_address"     // an address the carrier will not ship to or from
	CarrierInvalidParcel      = "invalid_parcel"      // too heavy, too large, or missing a size the service needs
	CarrierNoService          = "no_service"          // no switched-on service covers this route
	CarrierCustomsMissing     = "customs_missing"     // a customs declaration this route needs is missing or incomplete
	CarrierCredentialsRefused = "credentials_refused" // the merchant's carrier account refused the plugin's keys
	CarrierNotVoidable        = "not_voidable"        // the label is past the point the carrier can cancel it
	CarrierDeclined           = "declined"            // the carrier said no for a reason of its own (FailureMessage says it)
	CarrierUnsupported        = "unsupported"         // this carrier does not do what was asked (it tracks nothing, say)
)

var carrierFailures = map[string]bool{CarrierUnavailable: true, CarrierInvalidAddress: true, CarrierInvalidParcel: true,
	CarrierNoService: true, CarrierCustomsMissing: true, CarrierCredentialsRefused: true, CarrierNotVoidable: true,
	CarrierDeclined: true, CarrierUnsupported: true}

// The tracking statuses, normalised: the shop's own six and four more a carrier reports.
const (
	TrackPreTransit         = "pre_transit"
	TrackLabelPrinted       = "label_printed"
	TrackInTransit          = "in_transit"
	TrackOutForDelivery     = "out_for_delivery"
	TrackAvailableForPickup = "available_for_pickup"
	TrackAttemptedDelivery  = "attempted_delivery"
	TrackDelivered          = "delivered"
	TrackFailure            = "failure"
	TrackReturnedToSender   = "returned_to_sender"
	TrackCancelled          = "cancelled"
)

var trackStatuses = map[string]bool{TrackPreTransit: true, TrackLabelPrinted: true, TrackInTransit: true,
	TrackOutForDelivery: true, TrackAvailableForPickup: true, TrackAttemptedDelivery: true, TrackDelivered: true,
	TrackFailure: true, TrackReturnedToSender: true, TrackCancelled: true}

// The bounds. Core holds the same file, and the shop the same numbers.
const (
	maxParcels         = 50
	maxParcelGrams     = 1_000_000 // a tonne: past any parcel service, short of freight
	maxParcelMM        = 10_000    // ten metres
	maxCustomsItems    = 200
	maxRates           = 100
	maxCarrierServices = 200
	maxTrackEvents     = 500
	maxDocuments       = 10
	maxDocumentBytes   = 1 << 20 // a label PDF; a bigger one goes by URL
	maxCarrierText     = 500
	maxCarrierKey      = 128
	maxContactField    = 200
	// CarrierRatesBudget is the time CarrierMethods gives a carrier's Rates, inside Core's ordinary five seconds, so a
	// slow carrier answers "unavailable" rather than nothing.
	CarrierRatesBudget = 4 * time.Second
)

// ---- shapes ----------------------------------------------------------------------------------------------

// CarrierService is one service a carrier offers — settings, never a live call.
type CarrierService struct {
	// Key names the service within its carrier: "dhl_paket", "ups_express_saver".
	Key   string `json:"key"`
	Label string `json:"label"`
	// Description is the line under the label at checkout.
	Description string `json:"description,omitempty"`
	// Countries it delivers to, ISO 3166-1 alpha-2; empty means any the account allows.
	Countries []string `json:"countries,omitempty"`
	// Currencies it quotes in, ISO 4217; empty means any.
	Currencies []string `json:"currencies,omitempty"`
	// Supports is what it can do (CarrierSupport*).
	Supports []string `json:"supports,omitempty"`
	// RequiresPhone: the courier needs the receiver's phone — express, most international services.
	RequiresPhone bool `json:"requires_phone,omitempty"`
	// Test is true while the plugin holds the carrier's TEST credentials: nothing is billed and tracking numbers are
	// samples. The shop stamps it on every label, so a test label is never shipped or told to a customer.
	Test bool `json:"test,omitempty"`
	// TestHint says what test mode means for this carrier, by language ("en" to fall back on; TestHintFor).
	TestHint map[string]string `json:"test_hint,omitempty"`
}

// CarrierDescription is describe's answer.
type CarrierDescription struct {
	// Name is the carrier or aggregator a merchant recognises: "Sendcloud", "DHL Express".
	Name     string           `json:"name"`
	Services []CarrierService `json:"services"`
}

// Parcel is one box. Weights are grams and lengths millimetres, whole numbers.
type Parcel struct {
	WeightGrams int64 `json:"weight_grams"`
	LengthMM    int64 `json:"length_mm,omitempty"`
	WidthMM     int64 `json:"width_mm,omitempty"`
	HeightMM    int64 `json:"height_mm,omitempty"`
}

// ShipAddress is a sender's or a receiver's address.
type ShipAddress struct {
	Name       string `json:"name,omitempty"`
	Company    string `json:"company,omitempty"`
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	// Country is ISO 3166-1 alpha-2 and always present.
	Country string `json:"country"`
	Phone   string `json:"phone,omitempty"`
	Email   string `json:"email,omitempty"`
}

// CustomsItem is one line of a customs declaration.
type CustomsItem struct {
	Description string `json:"description"`
	// HSCode is the Harmonized System code, 6 to 10 digits.
	HSCode string `json:"hs_code,omitempty"`
	// OriginCountry is where the goods were made, ISO 3166-1 alpha-2.
	OriginCountry  string `json:"origin_country,omitempty"`
	Quantity       int64  `json:"quantity"`
	UnitValueMinor int64  `json:"unit_value_minor"`
	WeightGrams    int64  `json:"weight_grams,omitempty"`
}

// Customs is a shipment's customs declaration. Incoterm is "DAP" — the receiver pays duties — until the shop handles
// duties itself.
type Customs struct {
	Currency     string        `json:"currency"`
	ContentsType string        `json:"contents_type,omitempty"` // merchandise, gift, documents, sample, return
	Incoterm     string        `json:"incoterm,omitempty"`
	Items        []CustomsItem `json:"items"`
}

// RatesRequest is rates' request: what a basket would cost to send.
type RatesRequest struct {
	// Quote is the shop's id for this question, echoed in the carrier's logs.
	Quote string `json:"quote,omitempty"`
	// Services are the service keys the merchant switched on; empty asks for every one.
	Services           []string    `json:"services,omitempty"`
	Origin             ShipAddress `json:"origin"`
	Destination        ShipAddress `json:"destination"`
	Parcels            []Parcel    `json:"parcels"`
	Currency           string      `json:"currency"`
	DeclaredValueMinor int64       `json:"declared_value_minor,omitempty"`
	Customs            *Customs    `json:"customs,omitempty"`
	Locale             string      `json:"locale,omitempty"`
}

// CarrierRate is one price for one service.
type CarrierRate struct {
	Service     string     `json:"service"`
	Title       string     `json:"title"`
	AmountMinor int64      `json:"amount_minor"`
	Currency    string     `json:"currency"`
	DaysMin     int        `json:"days_min,omitempty"`
	DaysMax     int        `json:"days_max,omitempty"`
	DeliveryBy  *time.Time `json:"delivery_by,omitempty"`
	// ProviderRef is the carrier's id for this rate, needed to buy this very rate.
	ProviderRef    string     `json:"provider_ref,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	PickupRequired bool       `json:"pickup_required,omitempty"`
	Warnings       []string   `json:"warnings,omitempty"`
}

// The statuses of a rates answer.
const (
	RatesOK          = "ok"
	RatesNoService   = "no_service"
	RatesUnavailable = "unavailable"
	RatesInvalid     = "invalid_request"
)

// RatesResult is rates' answer.
type RatesResult struct {
	Status         string        `json:"status"`
	Rates          []CarrierRate `json:"rates,omitempty"`
	FailureCode    string        `json:"failure_code,omitempty"`
	FailureMessage string        `json:"failure_message,omitempty"`
}

// LabelRequest is label.create's request. Key is the idempotency key AND what the carrier looks a label up by: a
// label.create repeated with one Key buys one label, which is what makes a lost answer safe to ask again.
type LabelRequest struct {
	Key         string      `json:"key"`
	Service     string      `json:"service"`
	RateRef     string      `json:"rate_ref,omitempty"`
	Origin      ShipAddress `json:"origin"`
	Destination ShipAddress `json:"destination"`
	Parcels     []Parcel    `json:"parcels"`
	Customs     *Customs    `json:"customs,omitempty"`
	// Reference is the order's number, printed on the label for the merchant.
	Reference string `json:"reference,omitempty"`
	// Return asks for a return label: the receiver sends to the origin.
	Return  bool              `json:"return,omitempty"`
	Options map[string]string `json:"options,omitempty"` // "signature": "true", "insurance_minor": "5000"
	Test    bool              `json:"test,omitempty"`
}

// DocumentRef is one document of a shipment — by URL, or its bytes (at most 1 MiB) in standard base64.
type DocumentRef struct {
	Kind   string `json:"kind"`   // label, customs_invoice, cn23, return_label
	Format string `json:"format"` // pdf, png, zpl
	URL    string `json:"url,omitempty"`
	// Data is the document itself, base64 (RFC 4648 standard encoding) — a string in the contract's schema, which is
	// what a []byte is in JSON anyway; DocumentData makes one, Bytes reads it.
	Data string `json:"data,omitempty"`
}

// DocumentData is a document carried by its bytes.
func DocumentData(kind, format string, b []byte) DocumentRef {
	return DocumentRef{Kind: kind, Format: format, Data: base64.StdEncoding.EncodeToString(b)}
}

// Bytes is the document's data, decoded; nil for a document carried by URL.
func (d DocumentRef) Bytes() ([]byte, error) {
	if d.Data == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(d.Data)
}

var documentKinds = map[string]bool{"label": true, "customs_invoice": true, "cn23": true, "return_label": true}
var documentFormats = map[string]bool{"pdf": true, "png": true, "zpl": true}

// The statuses of a label.
const (
	LabelPurchased = "purchased"
	LabelPending   = "pending"
	LabelRejected  = "rejected"
)

// LabelResult is label.create's answer. "pending" means the carrier is still making it: ask again with the same Key.
type LabelResult struct {
	Status         string        `json:"status"`
	ProviderRef    string        `json:"provider_ref,omitempty"`
	TrackingNumber string        `json:"tracking_number,omitempty"`
	TrackingURL    string        `json:"tracking_url,omitempty"`
	CarrierName    string        `json:"carrier_name,omitempty"`
	Service        string        `json:"service,omitempty"`
	CostMinor      int64         `json:"cost_minor,omitempty"`
	Currency       string        `json:"currency,omitempty"`
	Documents      []DocumentRef `json:"documents,omitempty"`
	DeliveryBy     *time.Time    `json:"delivery_by,omitempty"`
	FailureCode    string        `json:"failure_code,omitempty"`
	FailureMessage string        `json:"failure_message,omitempty"`
}

// VoidRequest is label.void's request.
type VoidRequest struct {
	ProviderRef string `json:"provider_ref"`
	Key         string `json:"key,omitempty"`
}

// The statuses of a void.
const (
	VoidVoided   = "voided"
	VoidPending  = "pending"
	VoidRejected = "rejected"
)

// VoidResult is label.void's answer.
type VoidResult struct {
	Status         string `json:"status"`
	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
}

// TrackRequest is track's request.
type TrackRequest struct {
	ProviderRef    string `json:"provider_ref,omitempty"`
	TrackingNumber string `json:"tracking_number"`
}

// TrackEvent is one scan.
type TrackEvent struct {
	Status   string    `json:"status"`
	Detail   string    `json:"detail,omitempty"`
	Location string    `json:"location,omitempty"`
	At       time.Time `json:"at"`
	// Dedupe is the carrier's id for the event, so the same scan reported twice is stored once.
	Dedupe string `json:"dedupe,omitempty"`
}

// TrackResult is track's answer. Status is the shipment's latest normalised status; empty with a failure code when
// the carrier could not say.
type TrackResult struct {
	Status         string       `json:"status,omitempty"`
	Events         []TrackEvent `json:"events,omitempty"`
	FailureCode    string       `json:"failure_code,omitempty"`
	FailureMessage string       `json:"failure_message,omitempty"`
}

// DocumentsRequest is documents' request: a label's documents, fetched again.
type DocumentsRequest struct {
	ProviderRef string `json:"provider_ref"`
}

// DocumentsResult is documents' answer.
type DocumentsResult struct {
	Documents      []DocumentRef `json:"documents,omitempty"`
	FailureCode    string        `json:"failure_code,omitempty"`
	FailureMessage string        `json:"failure_message,omitempty"`
}

// ---- validation: the same rules on both sides ------------------------------------------------------------

func carrierText(field, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return fmt.Errorf("nilda: %s is at most %d characters", field, max)
	}
	return nil
}

func validCountry(field, c string) error {
	if !countryCode.MatchString(c) {
		return fmt.Errorf("nilda: %s is an ISO 3166-1 alpha-2 code (\"AT\"), got %q", field, c)
	}
	return nil
}

func validCurrency(field, c string) error {
	if _, ok := MinorUnits(c); !ok {
		return fmt.Errorf("nilda: %s %q is not a currency ISO 4217 gives a minor unit to", field, c)
	}
	return nil
}

func validFailure(code, message string, required bool) error {
	switch {
	case code == "" && required:
		return errors.New("nilda: a result that is not a success names its failure_code")
	case code != "" && !carrierFailures[code]:
		return fmt.Errorf("nilda: failure_code %q is not one the contract knows (unavailable, invalid_address, "+
			"invalid_parcel, no_service, customs_missing, credentials_refused, not_voidable, declined, unsupported)", code)
	}
	return carrierText("failure_message", message, maxCarrierText)
}

// Validate refuses an address with no country, or past its bounds.
func (a ShipAddress) Validate() error {
	if err := validCountry("an address's country", a.Country); err != nil {
		return err
	}
	for name, v := range map[string]string{"name": a.Name, "company": a.Company, "line1": a.Line1, "line2": a.Line2,
		"phone": a.Phone, "email": a.Email} {
		if err := carrierText("an address's "+name, v, maxAddressLine); err != nil {
			return err
		}
	}
	for name, v := range map[string]string{"city": a.City, "region": a.Region} {
		if err := carrierText("an address's "+name, v, maxAddressCity); err != nil {
			return err
		}
	}
	return carrierText("an address's postal_code", a.PostalCode, maxAddressPostal)
}

// Validate refuses a parcel with no weight, or sizes past a parcel service.
func (p Parcel) Validate() error {
	if p.WeightGrams <= 0 || p.WeightGrams > maxParcelGrams {
		return fmt.Errorf("nilda: a parcel weighs 1 to %d grams, got %d", maxParcelGrams, p.WeightGrams)
	}
	for name, v := range map[string]int64{"length_mm": p.LengthMM, "width_mm": p.WidthMM, "height_mm": p.HeightMM} {
		if v < 0 || v > maxParcelMM {
			return fmt.Errorf("nilda: a parcel's %s is 0 to %d, got %d", name, maxParcelMM, v)
		}
	}
	return nil
}

func validParcels(ps []Parcel) error {
	if len(ps) == 0 || len(ps) > maxParcels {
		return fmt.Errorf("nilda: a shipment has 1 to %d parcels, got %d", maxParcels, len(ps))
	}
	for i, p := range ps {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("parcel %d: %w", i+1, err)
		}
	}
	return nil
}

// Validate refuses a customs declaration a carrier could not file.
func (c Customs) Validate() error {
	if err := validCurrency("a customs declaration's currency", c.Currency); err != nil {
		return err
	}
	if c.Incoterm != "" && c.Incoterm != "DAP" {
		return fmt.Errorf("nilda: the incoterm is DAP — the receiver pays duties — got %q", c.Incoterm)
	}
	if len(c.Items) == 0 || len(c.Items) > maxCustomsItems {
		return fmt.Errorf("nilda: a customs declaration has 1 to %d items, got %d", maxCustomsItems, len(c.Items))
	}
	for i, it := range c.Items {
		switch {
		case strings.TrimSpace(it.Description) == "":
			return fmt.Errorf("nilda: customs item %d has no description", i+1)
		case it.HSCode != "" && !hsCode(it.HSCode):
			return fmt.Errorf("nilda: customs item %d's hs_code is 6 to 10 digits, got %q", i+1, it.HSCode)
		case it.OriginCountry != "" && !countryCode.MatchString(it.OriginCountry):
			return fmt.Errorf("nilda: customs item %d's origin_country is an ISO 3166-1 alpha-2 code, got %q", i+1, it.OriginCountry)
		case it.Quantity <= 0 || it.UnitValueMinor < 0 || it.WeightGrams < 0:
			return fmt.Errorf("nilda: customs item %d needs a quantity above 0 and no negative value or weight", i+1)
		}
		if err := carrierText(fmt.Sprintf("customs item %d's description", i+1), it.Description, maxCarrierText); err != nil {
			return err
		}
	}
	return nil
}

func hsCode(s string) bool {
	if len(s) < 6 || len(s) > 10 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validShipment(origin, destination ShipAddress, parcels []Parcel, customs *Customs) error {
	if err := origin.Validate(); err != nil {
		return fmt.Errorf("origin: %w", err)
	}
	if err := destination.Validate(); err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	if err := validParcels(parcels); err != nil {
		return err
	}
	if customs != nil {
		return customs.Validate()
	}
	return nil
}

// Validate refuses a rates question no carrier could answer.
func (r RatesRequest) Validate() error {
	if err := validCurrency("currency", r.Currency); err != nil {
		return err
	}
	if r.DeclaredValueMinor < 0 {
		return errors.New("nilda: declared_value_minor is not negative")
	}
	return validShipment(r.Origin, r.Destination, r.Parcels, r.Customs)
}

// Validate refuses a rates answer the shop could not use: a rate with no price, a currency ISO 4217 does not price,
// two rates for one service, or an "ok" with no rate.
func (r RatesResult) Validate() error {
	switch r.Status {
	case RatesOK:
		if len(r.Rates) == 0 {
			return errors.New("nilda: an ok rates answer has at least one rate; no rate is no_service")
		}
	case RatesNoService, RatesUnavailable, RatesInvalid:
		if err := validFailure(r.FailureCode, r.FailureMessage, false); err != nil {
			return err
		}
	default:
		return fmt.Errorf("nilda: a rates status is ok, no_service, unavailable or invalid_request, got %q", r.Status)
	}
	if len(r.Rates) > maxRates {
		return fmt.Errorf("nilda: a rates answer has at most %d rates", maxRates)
	}
	seen := map[string]bool{}
	for i, rate := range r.Rates {
		switch {
		case strings.TrimSpace(rate.Service) == "" || len(rate.Service) > maxCarrierKey:
			return fmt.Errorf("nilda: rate %d names no service", i+1)
		case seen[rate.Service]:
			return fmt.Errorf("nilda: two rates for the service %q — one price per service", rate.Service)
		case rate.AmountMinor <= 0:
			return fmt.Errorf("nilda: rate %q has no price (%d); a carrier rate costs something", rate.Service, rate.AmountMinor)
		case rate.DaysMin < 0 || rate.DaysMax < 0 || (rate.DaysMax != 0 && rate.DaysMax < rate.DaysMin):
			return fmt.Errorf("nilda: rate %q has days_min %d and days_max %d", rate.Service, rate.DaysMin, rate.DaysMax)
		}
		seen[rate.Service] = true
		if err := validCurrency(fmt.Sprintf("rate %q's currency", rate.Service), rate.Currency); err != nil {
			return err
		}
		if err := carrierText(fmt.Sprintf("rate %q's title", rate.Service), rate.Title, maxCarrierText); err != nil {
			return err
		}
	}
	return nil
}

// Validate refuses a label request a carrier could not fill — and one without the Key that makes it safe to repeat.
func (r LabelRequest) Validate() error {
	switch {
	case strings.TrimSpace(r.Key) == "" || len(r.Key) > maxCarrierKey:
		return fmt.Errorf("nilda: a label request has a key of 1 to %d characters — it is what makes asking again buy one label", maxCarrierKey)
	case strings.TrimSpace(r.Service) == "":
		return errors.New("nilda: a label request names the service")
	}
	return validShipment(r.Origin, r.Destination, r.Parcels, r.Customs)
}

func validDocuments(ds []DocumentRef) error {
	if len(ds) > maxDocuments {
		return fmt.Errorf("nilda: at most %d documents", maxDocuments)
	}
	for i, d := range ds {
		switch {
		case !documentKinds[d.Kind]:
			return fmt.Errorf("nilda: document %d's kind is label, customs_invoice, cn23 or return_label, got %q", i+1, d.Kind)
		case !documentFormats[d.Format]:
			return fmt.Errorf("nilda: document %d's format is pdf, png or zpl, got %q", i+1, d.Format)
		case (d.URL == "") == (d.Data == ""):
			return fmt.Errorf("nilda: document %d carries a url or its data, exactly one", i+1)
		case len(d.Data) > base64.StdEncoding.EncodedLen(maxDocumentBytes):
			return fmt.Errorf("nilda: document %d is over 1 MiB; send it by url", i+1)
		case d.URL != "" && !strings.HasPrefix(d.URL, "https://"):
			return fmt.Errorf("nilda: document %d's url is https", i+1)
		}
		if _, err := d.Bytes(); err != nil {
			return fmt.Errorf("nilda: document %d's data is not standard base64: %w", i+1, err)
		}
	}
	return nil
}

// Validate refuses a label answer the shop could not record: a purchase with no tracking number or no reference to
// void it by, a rejection with no reason.
func (r LabelResult) Validate() error {
	switch r.Status {
	case LabelPurchased:
		if r.ProviderRef == "" || r.TrackingNumber == "" {
			return errors.New("nilda: a purchased label has a provider_ref and a tracking_number")
		}
		if r.Currency != "" {
			if err := validCurrency("the label's currency", r.Currency); err != nil {
				return err
			}
		}
		if r.CostMinor < 0 {
			return errors.New("nilda: a label's cost_minor is not negative")
		}
	case LabelPending:
	case LabelRejected:
		if err := validFailure(r.FailureCode, r.FailureMessage, true); err != nil {
			return err
		}
	default:
		return fmt.Errorf("nilda: a label's status is purchased, pending or rejected, got %q", r.Status)
	}
	if r.TrackingURL != "" && !strings.HasPrefix(r.TrackingURL, "https://") {
		return errors.New("nilda: a tracking_url is https")
	}
	return validDocuments(r.Documents)
}

// Validate refuses a void with nothing to void.
func (r VoidRequest) Validate() error {
	if strings.TrimSpace(r.ProviderRef) == "" {
		return errors.New("nilda: a void names the label's provider_ref")
	}
	return nil
}

// Validate refuses a void answer the shop could not act on.
func (r VoidResult) Validate() error {
	switch r.Status {
	case VoidVoided, VoidPending:
		return nil
	case VoidRejected:
		return validFailure(r.FailureCode, r.FailureMessage, true)
	}
	return fmt.Errorf("nilda: a void's status is voided, pending or rejected, got %q", r.Status)
}

// Validate refuses a track question with nothing to track.
func (r TrackRequest) Validate() error {
	if strings.TrimSpace(r.TrackingNumber) == "" {
		return errors.New("nilda: tracking names the tracking_number")
	}
	return nil
}

// Validate refuses a tracking answer with a status the shop does not know.
func (r TrackResult) Validate() error {
	if r.Status == "" {
		if err := validFailure(r.FailureCode, r.FailureMessage, true); err != nil {
			return err
		}
	} else if !trackStatuses[r.Status] {
		return fmt.Errorf("nilda: tracking status %q is not one the contract knows", r.Status)
	}
	if len(r.Events) > maxTrackEvents {
		return fmt.Errorf("nilda: at most %d tracking events", maxTrackEvents)
	}
	for i, e := range r.Events {
		if !trackStatuses[e.Status] {
			return fmt.Errorf("nilda: tracking event %d's status %q is not one the contract knows", i+1, e.Status)
		}
		if e.At.IsZero() {
			return fmt.Errorf("nilda: tracking event %d has no time", i+1)
		}
	}
	return nil
}

// Validate refuses a documents question with no label.
func (r DocumentsRequest) Validate() error {
	if strings.TrimSpace(r.ProviderRef) == "" {
		return errors.New("nilda: documents names the label's provider_ref")
	}
	return nil
}

// Validate refuses documents the shop could not store.
func (r DocumentsResult) Validate() error {
	if len(r.Documents) == 0 {
		if err := validFailure(r.FailureCode, r.FailureMessage, true); err != nil {
			return err
		}
	}
	return validDocuments(r.Documents)
}

// Validate refuses a carrier description with no service, a service twice, or a code the contract does not know.
func (d CarrierDescription) Validate() error {
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("nilda: a carrier names itself")
	}
	if len(d.Services) > maxCarrierServices {
		return fmt.Errorf("nilda: at most %d services", maxCarrierServices)
	}
	seen := map[string]bool{}
	for i, s := range d.Services {
		switch {
		case strings.TrimSpace(s.Key) == "" || len(s.Key) > maxCarrierKey:
			return fmt.Errorf("nilda: service %d has no key", i+1)
		case seen[s.Key]:
			return fmt.Errorf("nilda: the service %q is described twice", s.Key)
		case strings.TrimSpace(s.Label) == "":
			return fmt.Errorf("nilda: service %q has no label", s.Key)
		}
		seen[s.Key] = true
		for _, c := range s.Countries {
			if err := validCountry(fmt.Sprintf("service %q's country", s.Key), c); err != nil {
				return err
			}
		}
		for _, c := range s.Currencies {
			if err := validCurrency(fmt.Sprintf("service %q's currency", s.Key), c); err != nil {
				return err
			}
		}
		for _, sup := range s.Supports {
			if !carrierSupports[sup] {
				return fmt.Errorf("nilda: service %q supports %q, which the contract does not know", s.Key, sup)
			}
		}
	}
	return nil
}

// ---- the carrier plugin's side ---------------------------------------------------------------------------

// Carrier is a shipping carrier plugin. Return CarrierMethods(c) from your ServiceMethods and declare
// "provides": ["shipping.carrier/v1"] with service_provider.
type Carrier interface {
	DescribeCarrier(ctx context.Context) (CarrierDescription, error)
	Rates(ctx context.Context, req RatesRequest) (RatesResult, error)
	CreateLabel(ctx context.Context, req LabelRequest) (LabelResult, error)
	VoidLabel(ctx context.Context, req VoidRequest) (VoidResult, error)
}

// CarrierTracker is a carrier that tracks; without it track answers "unsupported".
type CarrierTracker interface {
	Track(ctx context.Context, req TrackRequest) (TrackResult, error)
}

// CarrierDocuments is a carrier that fetches a label's documents again; without it documents answers "unsupported".
type CarrierDocuments interface {
	Documents(ctx context.Context, req DocumentsRequest) (DocumentsResult, error)
}

// CarrierMethods are a Carrier's contract methods, each checking the request before the carrier sees it and the
// answer before it leaves — an answer the shop could not use is an error in the plugin's own test, not a surprise in
// production. Rates gets CarrierRatesBudget, and a carrier that runs out of it answers "unavailable".
func CarrierMethods(c Carrier) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShippingCarrier, CarrierDescribe, func(ctx context.Context, _ struct{}) (CarrierDescription, error) {
			return checked(c.DescribeCarrier(ctx))
		}),
		Serves(ContractShippingCarrier, CarrierRates, func(ctx context.Context, req RatesRequest) (RatesResult, error) {
			if err := req.Validate(); err != nil {
				return RatesResult{}, err
			}
			rctx, cancel := context.WithTimeout(ctx, carrierRatesBudget)
			defer cancel()
			res, err := c.Rates(rctx, req)
			if err != nil && rctx.Err() != nil && ctx.Err() == nil {
				return RatesResult{Status: RatesUnavailable, FailureCode: CarrierUnavailable,
					FailureMessage: "the carrier did not answer in time"}, nil
			}
			if err != nil {
				return RatesResult{}, err
			}
			return checked(res, nil)
		}),
		Serves(ContractShippingCarrier, CarrierLabelCreate, func(ctx context.Context, req LabelRequest) (LabelResult, error) {
			if err := req.Validate(); err != nil {
				return LabelResult{}, err
			}
			return checked(c.CreateLabel(ctx, req))
		}),
		Serves(ContractShippingCarrier, CarrierLabelVoid, func(ctx context.Context, req VoidRequest) (VoidResult, error) {
			if err := req.Validate(); err != nil {
				return VoidResult{}, err
			}
			return checked(c.VoidLabel(ctx, req))
		}),
		Serves(ContractShippingCarrier, CarrierTrack, func(ctx context.Context, req TrackRequest) (TrackResult, error) {
			if err := req.Validate(); err != nil {
				return TrackResult{}, err
			}
			t, ok := c.(CarrierTracker)
			if !ok {
				return TrackResult{FailureCode: CarrierUnsupported, FailureMessage: "this carrier does not track"}, nil
			}
			return checked(t.Track(ctx, req))
		}),
		Serves(ContractShippingCarrier, CarrierDocumentsGet, func(ctx context.Context, req DocumentsRequest) (DocumentsResult, error) {
			if err := req.Validate(); err != nil {
				return DocumentsResult{}, err
			}
			d, ok := c.(CarrierDocuments)
			if !ok {
				return DocumentsResult{FailureCode: CarrierUnsupported, FailureMessage: "this carrier does not fetch documents again"}, nil
			}
			return checked(d.Documents(ctx, req))
		}),
	}
}

// carrierRatesBudget is CarrierRatesBudget, shortened by a test that watches it run out.
var carrierRatesBudget = CarrierRatesBudget

// validator is an answer that checks itself.
type validator interface{ Validate() error }

func checked[T validator](v T, err error) (T, error) {
	if err != nil {
		return v, err
	}
	if verr := v.Validate(); verr != nil {
		var zero T
		return zero, verr
	}
	return v, nil
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// CarrierRatesAnswer is one carrier plugin's answer to a rates question.
type CarrierRatesAnswer struct {
	// Provider is the carrier plugin's key — what a label for one of these rates is bought from.
	Provider string
	Result   RatesResult
	// Cached marks the carrier's last good answer to the same question, given because it did not answer now.
	Cached bool
}

// ShippingRates asks every active carrier (or opts.Provider) what req would cost. A carrier that failed or ran out of
// time is in the misses, not an error: the shop shows the rates it has, and its backup rate when it has none.
func (s *Services) ShippingRates(ctx context.Context, req RatesRequest, opts ServiceCallOptions) ([]CarrierRatesAnswer, []ServiceMiss, error) {
	if err := req.Validate(); err != nil {
		return nil, nil, err
	}
	res, err := s.Call(ctx, ContractShippingCarrier, CarrierRates, req, opts)
	if err != nil {
		return nil, nil, err
	}
	out := make([]CarrierRatesAnswer, 0, len(res.Answers))
	missed := res.Missed
	for _, a := range res.Answers {
		var r RatesResult
		if err := a.Decode(&r); err != nil || r.Validate() != nil {
			missed = append(missed, ServiceMiss{Provider: a.Provider, Reason: "invalid_answer"})
			continue
		}
		out = append(out, CarrierRatesAnswer{Provider: a.Provider, Result: r, Cached: a.Cached})
	}
	return out, missed, nil
}

// CreateShippingLabel asks one carrier to buy a label. req.Key is sent as the idempotency key too, so a call whose
// answer was lost is safe to repeat: Core replays it for 24 hours, and the carrier finds the label by the same key.
func (s *Services) CreateShippingLabel(ctx context.Context, provider string, req LabelRequest) (LabelResult, error) {
	if err := req.Validate(); err != nil {
		return LabelResult{}, err
	}
	var out LabelResult
	err := s.callOne(ctx, provider, CarrierLabelCreate, req, req.Key, &out)
	return out, err
}

// VoidShippingLabel asks the carrier that bought a label to cancel it.
func (s *Services) VoidShippingLabel(ctx context.Context, provider string, req VoidRequest) (VoidResult, error) {
	if err := req.Validate(); err != nil {
		return VoidResult{}, err
	}
	var out VoidResult
	key := ""
	if req.Key != "" {
		key = "void-" + req.Key
	}
	err := s.callOne(ctx, provider, CarrierLabelVoid, req, key, &out)
	return out, err
}

// TrackShipment asks the carrier that bought a label where the parcel is.
func (s *Services) TrackShipment(ctx context.Context, provider string, req TrackRequest) (TrackResult, error) {
	if err := req.Validate(); err != nil {
		return TrackResult{}, err
	}
	var out TrackResult
	err := s.callOne(ctx, provider, CarrierTrack, req, "", &out)
	return out, err
}

// ShippingDocuments fetches a label's documents again from the carrier that bought it.
func (s *Services) ShippingDocuments(ctx context.Context, provider string, req DocumentsRequest) (DocumentsResult, error) {
	if err := req.Validate(); err != nil {
		return DocumentsResult{}, err
	}
	var out DocumentsResult
	err := s.callOne(ctx, provider, CarrierDocumentsGet, req, "", &out)
	return out, err
}

// errNoCarrierAnswer is a call to one carrier that gave no usable answer.
var errNoCarrierAnswer = errors.New("nilda: the carrier gave no answer")

// callOne asks one carrier one method and decodes its checked answer into out.
func (s *Services) callOne(ctx context.Context, provider, method string, req any, key string, out validator) error {
	if provider == "" {
		return errors.New("nilda: name the carrier plugin that bought the label")
	}
	res, err := s.Call(ctx, ContractShippingCarrier, method, req, ServiceCallOptions{Provider: provider, IdempotencyKey: key})
	if err != nil {
		return err
	}
	if len(res.Answers) == 0 {
		reason := "no answer"
		if len(res.Missed) > 0 {
			reason = res.Missed[0].Reason
		}
		return fmt.Errorf("%w (%s %s: %s)", errNoCarrierAnswer, provider, method, reason)
	}
	if err := json.Unmarshal(res.Answers[0].Output, out); err != nil {
		return fmt.Errorf("nilda: the answer of %s %s does not decode: %w", provider, method, err)
	}
	return out.Validate()
}
