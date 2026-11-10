package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// THE SHOP'S ORDER EVENTS, WITH THEIR MONEY AND THE BUYER'S CONSENT (door D1, the shop's plan 2026-11-05 §1J;
// SHOP_EVENTS.md). EXPERIMENTAL.
//
// The shop's first events (ecommerce.order_paid and its siblings, EVENTS.md) carry an order's id and little else: a
// plugin that delivers a licence key reads the order afterwards through shop.orders/v1. A plugin that MEASURES — an ad
// network's conversions, an analytics feed, a tax provider reversing its answer on a refund — needs more, and needs it
// as it was: the order's money net of tax, line by line, in its currency, each event once, in order, and the consent the
// visitor gave when the order was placed. That is this contract:
//
//   - five events, emitted by the shop once each committed change: placed, paid, cancelled, refunded (with what came
//     back, line by line), fulfilled (one per shipment). Their data is a ShopOrderEvent;
//   - event_id is the shop's, kept by every redelivery and every re-emission (Core's own CloudEvent id is per emission);
//     sequence rises with every event the shop records, so a subscriber orders them and sees what it missed;
//   - events.read, the one method: the events after a sequence, a page at a time — how a subscriber installed later, or
//     one whose delivery was dead-lettered, catches up;
//   - the consent is a SNAPSHOT copied onto the order when it was placed: a later change of mind does not rewrite what an
//     event of that order says, and an order carries the words, never a guess.
//
// No event carries the buyer's email, name or address. A subscriber that needs one (an enhanced conversion) reads it
// through shop.orders/v1, and only when the snapshot says the visitor allowed it.

// ContractShopEvents is the shop's order events and their read.
const ContractShopEvents = "shop.events/v1"

// ShopEventsRead is its one method.
const ShopEventsRead = "events.read"

// The events of shop.events/v1, in the shop's namespace. The shop's older names (ecommerce.order_paid …) stay as they
// are, for the owner's automations; these are the contract's.
const (
	EventShopOrderPlaced    = "ecommerce.order.placed"
	EventShopOrderPaid      = "ecommerce.order.paid"
	EventShopOrderCancelled = "ecommerce.order.cancelled"
	EventShopOrderRefunded  = "ecommerce.order.refunded"
	EventShopOrderFulfilled = "ecommerce.order.fulfilled"
)

// ShopEventTypes is every event of the contract.
func ShopEventTypes() []string {
	return []string{EventShopOrderPlaced, EventShopOrderPaid, EventShopOrderCancelled, EventShopOrderRefunded, EventShopOrderFulfilled}
}

// A consent signal's word. Unknown is the shop not having been told — never a yes.
const (
	ConsentGranted = "granted"
	ConsentDenied  = "denied"
	ConsentUnknown = "unknown"
)

// Where a consent snapshot's words came from.
const (
	// ConsentFromFirstTouch: analytics_storage is granted because the basket carried the visit's first touch, which
	// Core's page sends only once the visitor allowed analytics; the ad signals are unknown.
	ConsentFromFirstTouch = "first_touch"
	// ConsentNotTold: the shop was told nothing; every signal is unknown.
	ConsentNotTold = "not_told"
	// ConsentFromCore: Core told the shop the visitor's recorded consent with the request that placed the order
	// (VisitorConsent); every signal is granted or denied.
	ConsentFromCore = "core"
)

// ConsentSnapshot is the visitor's consent when the order was placed, in Google Consent Mode v2's four signals (the
// words Core's privacy runtime maps its categories to).
type ConsentSnapshot struct {
	AdStorage         string `json:"ad_storage"`
	AdUserData        string `json:"ad_user_data"`
	AdPersonalization string `json:"ad_personalization"`
	AnalyticsStorage  string `json:"analytics_storage"`
	// Source is where the words came from: ConsentFromCore, ConsentFromFirstTouch or ConsentNotTold.
	Source string `json:"source"`
	// RecordedAt is when the shop copied it onto the order.
	RecordedAt time.Time `json:"recorded_at"`
}

// NotToldConsent is the snapshot of an order whose shop was told nothing: every signal unknown.
func NotToldConsent(at time.Time) ConsentSnapshot {
	return ConsentSnapshot{AdStorage: ConsentUnknown, AdUserData: ConsentUnknown, AdPersonalization: ConsentUnknown,
		AnalyticsStorage: ConsentUnknown, Source: ConsentNotTold, RecordedAt: at.UTC()}
}

// VisitorConsent is the visitor's consent as Core recorded it, for a request your resident HTTP server received: the
// four Consent Mode signals, each granted or denied, Source ConsentFromCore, RecordedAt now. Core sends it on a request
// that changes something (not GET, HEAD or OPTIONS) — read it in the handler that places the order, and copy it onto
// the order. A member's is their account's, a visitor's their browser's; one who never decided is denied in every
// signal. Core strips any value a client sent. ok is false with no header (an older Core, a page view, a declared
// machine path, a consent Core could not read) or one that is not four known signals once each.
func VisitorConsent(r *http.Request) (ConsentSnapshot, bool) {
	raw := r.Header.Get(visitorConsentHeader)
	if raw == "" {
		return ConsentSnapshot{}, false
	}
	signals := map[string]*string{}
	c := ConsentSnapshot{Source: ConsentFromCore, RecordedAt: time.Now().UTC()}
	for name, field := range map[string]*string{"ad_storage": &c.AdStorage, "ad_user_data": &c.AdUserData,
		"ad_personalization": &c.AdPersonalization, "analytics_storage": &c.AnalyticsStorage} {
		signals[name] = field
	}
	for _, part := range strings.Split(raw, ";") {
		name, word, _ := strings.Cut(strings.TrimSpace(part), "=")
		field, known := signals[name]
		if !known || *field != "" || (word != ConsentGranted && word != ConsentDenied) {
			return ConsentSnapshot{}, false
		}
		*field = word
	}
	if c.Validate() != nil {
		return ConsentSnapshot{}, false
	}
	return c, true
}

// AdUserDataGranted says the visitor allowed their data to be sent to an ad network (ad_user_data granted) — the one
// question to ask before a hashed email leaves. Unknown and denied are both no.
func (c ConsentSnapshot) AdUserDataGranted() bool { return c.AdUserData == ConsentGranted }

// AdStorageGranted says the visitor allowed advertising measurement (ad_storage granted).
func (c ConsentSnapshot) AdStorageGranted() bool { return c.AdStorage == ConsentGranted }

var consentWords = []string{ConsentGranted, ConsentDenied, ConsentUnknown}

// Validate refuses a snapshot with a word that is not one of the three, or without its source.
func (c ConsentSnapshot) Validate() error {
	for name, v := range map[string]string{"ad_storage": c.AdStorage, "ad_user_data": c.AdUserData,
		"ad_personalization": c.AdPersonalization, "analytics_storage": c.AnalyticsStorage} {
		if !slices.Contains(consentWords, v) {
			return fmt.Errorf("nilda: consent %s is %q; it is granted, denied or unknown", name, v)
		}
	}
	if c.Source != ConsentFromCore && c.Source != ConsentFromFirstTouch && c.Source != ConsentNotTold {
		return fmt.Errorf("nilda: a consent snapshot's source is %q", c.Source)
	}
	if c.Source == ConsentFromCore && slices.Contains([]string{c.AdStorage, c.AdUserData, c.AdPersonalization, c.AnalyticsStorage}, ConsentUnknown) {
		return errors.New("nilda: a consent Core told is granted or denied in every signal")
	}
	if c.Source == ConsentNotTold && c != NotToldConsent(c.RecordedAt) {
		return errors.New("nilda: a consent the shop was not told is unknown in every signal")
	}
	if c.RecordedAt.IsZero() {
		return errors.New("nilda: a consent snapshot says when it was copied onto the order")
	}
	return nil
}

// ShopEventLine is one line of the order as the event saw it: its quantities so far and its money net of tax.
type ShopEventLine struct {
	SKU        string `json:"sku"`
	ProductSKU string `json:"product_sku,omitempty"`
	// Quantity is how many were bought; the others how many of them were refunded, called off before shipping, shipped.
	Quantity          int64 `json:"quantity"`
	QuantityRefunded  int64 `json:"quantity_refunded,omitempty"`
	QuantityCancelled int64 `json:"quantity_cancelled,omitempty"`
	QuantityFulfilled int64 `json:"quantity_fulfilled,omitempty"`
	// NetMinor is the line's goods after its share of the discounts, tax excluded, for Quantity; TaxMinor its tax.
	NetMinor int64 `json:"net_minor"`
	TaxMinor int64 `json:"tax_minor"`
}

// ShopOrderEvent is the data of every shop.events/v1 event.
type ShopOrderEvent struct {
	EventID  string `json:"event_id"`
	Sequence int64  `json:"sequence"`
	// Type is the event's name, one of ShopEventTypes.
	Type       string    `json:"type"`
	OrderID    string    `json:"order_id"`
	Ref        string    `json:"ref"`
	OccurredAt time.Time `json:"occurred_at"`
	Currency   string    `json:"currency"`
	// NetMinor is the goods after discounts, tax and delivery excluded — the sum of the lines' NetMinor.
	NetMinor      int64 `json:"net_minor"`
	ShippingMinor int64 `json:"shipping_minor"`
	TaxMinor      int64 `json:"tax_minor"`
	TotalMinor    int64 `json:"total_minor"`
	// RefundedMinor is everything refunded on the order so far; RefundMinor what THIS refund returned (refunded only).
	RefundedMinor int64 `json:"refunded_minor"`
	RefundMinor   int64 `json:"refund_minor,omitempty"`
	// FulfillmentID is the shipment a fulfilled event is about (fulfilled only).
	FulfillmentID string          `json:"fulfillment_id,omitempty"`
	Lines         []ShopEventLine `json:"lines"`
	Consent       ConsentSnapshot `json:"consent"`
}

// Validate refuses an event that does not add up.
func (e ShopOrderEvent) Validate() error {
	switch {
	case strings.TrimSpace(e.EventID) == "" || len(e.EventID) > 64:
		return errors.New("nilda: a shop event has its event_id")
	case e.Sequence <= 0:
		return errors.New("nilda: a shop event has its sequence")
	case !slices.Contains(ShopEventTypes(), e.Type):
		return fmt.Errorf("nilda: %q is not an event of %s", e.Type, ContractShopEvents)
	case strings.TrimSpace(e.OrderID) == "" || strings.TrimSpace(e.Ref) == "":
		return errors.New("nilda: a shop event names its order and reference")
	case e.OccurredAt.IsZero():
		return errors.New("nilda: a shop event says when it happened")
	}
	if err := validCurrency("the event's currency", e.Currency); err != nil {
		return err
	}
	if e.NetMinor < 0 || e.ShippingMinor < 0 || e.TaxMinor < 0 || e.TotalMinor < 0 || e.RefundedMinor < 0 || e.RefundMinor < 0 {
		return fmt.Errorf("nilda: order %s's event has a negative amount", e.Ref)
	}
	if e.RefundedMinor > e.TotalMinor || e.RefundMinor > e.RefundedMinor {
		return fmt.Errorf("nilda: order %s's event refunds more than was paid", e.Ref)
	}
	if (e.Type == EventShopOrderRefunded) != (e.RefundMinor > 0) {
		return fmt.Errorf("nilda: order %s: a refund amount rides a refunded event, and every refunded event has one", e.Ref)
	}
	if (e.Type == EventShopOrderFulfilled) != (e.FulfillmentID != "") {
		return fmt.Errorf("nilda: order %s: a shipment rides a fulfilled event, and every fulfilled event names one", e.Ref)
	}
	var net int64
	for _, l := range e.Lines {
		if strings.TrimSpace(l.SKU) == "" || l.Quantity < 0 || l.QuantityRefunded < 0 || l.QuantityCancelled < 0 ||
			l.QuantityFulfilled < 0 || l.QuantityRefunded+l.QuantityCancelled > l.Quantity || l.QuantityFulfilled > l.Quantity ||
			l.NetMinor < 0 || l.TaxMinor < 0 {
			return fmt.Errorf("nilda: order %s's event has a line with no SKU or impossible quantities", e.Ref)
		}
		net += l.NetMinor
	}
	if net != e.NetMinor {
		return fmt.Errorf("nilda: order %s's event says net %d and its lines %d", e.Ref, e.NetMinor, net)
	}
	return e.Consent.Validate()
}

// DecodeShopOrderEvent is a subscriber's HandleEvent data as an event of this contract, checked: refused when it is not
// one, or not the event it was delivered as.
func DecodeShopOrderEvent(eventType string, data []byte) (ShopOrderEvent, error) {
	var e ShopOrderEvent
	if err := json.Unmarshal(data, &e); err != nil {
		return ShopOrderEvent{}, fmt.Errorf("nilda: %s does not decode as a shop event: %w", eventType, err)
	}
	if e.Type != eventType {
		return ShopOrderEvent{}, fmt.Errorf("nilda: an event delivered as %q says it is %q", eventType, e.Type)
	}
	return e, e.Validate()
}

// MaxShopEventsRead is the most events one read answers; DefaultShopEventsRead the page a request naming none gets.
const (
	MaxShopEventsRead     = 100
	DefaultShopEventsRead = 50
)

// ShopEventsRequest asks for the events after a sequence (0: from the first).
type ShopEventsRequest struct {
	AfterSequence int64 `json:"after_sequence"`
	Limit         int   `json:"limit,omitempty"`
}

func (r ShopEventsRequest) limit() int {
	if r.Limit == 0 {
		return DefaultShopEventsRead
	}
	return r.Limit
}

// Validate refuses a negative sequence or a page outside the bound.
func (r ShopEventsRequest) Validate() error {
	if r.AfterSequence < 0 || r.Limit < 0 || r.Limit > MaxShopEventsRead {
		return fmt.Errorf("nilda: an events read starts at a sequence of 0 or more and asks for at most %d", MaxShopEventsRead)
	}
	return nil
}

// ShopEventsAnswer is one page, oldest first; More says another page follows.
type ShopEventsAnswer struct {
	Events []ShopOrderEvent `json:"events"`
	More   bool             `json:"more"`
}

// ValidateFor checks a page against its request: in rising order after its sequence, within its limit, each whole.
func (a ShopEventsAnswer) ValidateFor(r ShopEventsRequest) error {
	if len(a.Events) > r.limit() {
		return fmt.Errorf("nilda: an events read answered %d events where %d were asked for", len(a.Events), r.limit())
	}
	last := r.AfterSequence
	for _, e := range a.Events {
		if e.Sequence <= last {
			return errors.New("nilda: an events read answered events out of order or at or before its sequence")
		}
		last = e.Sequence
		if err := e.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// ShopEventsProvider is the shop. Declare "provides": ["shop.events/v1"] with service_provider.
type ShopEventsProvider interface {
	ShopEvents(ctx context.Context, req ShopEventsRequest) (ShopEventsAnswer, error)
}

// ShopEventsMethods are the shop's methods, checking the request before the shop sees it and the page before it leaves.
func ShopEventsMethods(p ShopEventsProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopEvents, ShopEventsRead, func(ctx context.Context, req ShopEventsRequest) (ShopEventsAnswer, error) {
			if err := req.Validate(); err != nil {
				return ShopEventsAnswer{}, err
			}
			a, err := p.ShopEvents(ctx, req)
			if err != nil {
				return ShopEventsAnswer{}, err
			}
			if a.Events == nil {
				a.Events = []ShopOrderEvent{} // none is an empty list on the wire, never null
			}
			return a, a.ValidateFor(req)
		}),
	}
}

// ---- a subscriber's side -----------------------------------------------------------------------------------

// ShopEvents reads the events after a sequence from the shop. One shop answers, so none answering is an error.
func (s *Services) ShopEvents(ctx context.Context, req ShopEventsRequest) (ShopEventsAnswer, error) {
	if err := req.Validate(); err != nil {
		return ShopEventsAnswer{}, err
	}
	res, err := s.Call(ctx, ContractShopEvents, ShopEventsRead, req, ServiceCallOptions{})
	if err != nil {
		return ShopEventsAnswer{}, err
	}
	if len(res.Answers) == 0 {
		return ShopEventsAnswer{}, fmt.Errorf("nilda: no shop answered %s %s", ContractShopEvents, ShopEventsRead)
	}
	var a ShopEventsAnswer
	if err := res.Answers[0].Decode(&a); err != nil {
		return ShopEventsAnswer{}, err
	}
	return a, a.ValidateFor(req)
}
