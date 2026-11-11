package nilda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A paid order of two lines, as the shop's event says it.
func paidEvent() ShopOrderEvent {
	return ShopOrderEvent{EventID: "8d0f6b6e-6a4b-5d0c-9d7e-1f2a3b4c5d6e", Sequence: 7, Type: EventShopOrderPaid,
		OrderID: "0b9a", Ref: "NLD-1042", OccurredAt: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Currency: "EUR",
		NetMinor: 2500, ShippingMinor: 490, TaxMinor: 568, TotalMinor: 3558,
		Lines: []ShopEventLine{{SKU: "MUG-RED", ProductSKU: "MUG", Quantity: 2, NetMinor: 1700, TaxMinor: 323},
			{SKU: "CARD", Quantity: 1, NetMinor: 800, TaxMinor: 152}},
		Consent: ConsentSnapshot{AdStorage: ConsentUnknown, AdUserData: ConsentUnknown, AdPersonalization: ConsentUnknown,
			AnalyticsStorage: ConsentGranted, Source: ConsentFromFirstTouch, RecordedAt: time.Date(2026, 11, 9, 9, 58, 0, 0, time.UTC)}}
}

// An order event is held to its rules before it leaves the shop and before a subscriber acts on it: a field that does not
// add up is refused naming itself, never passed on.
func TestAShopOrderEventIsHeldToItsRules(t *testing.T) {
	if err := paidEvent().Validate(); err != nil {
		t.Fatalf("a well-formed event was refused: %v", err)
	}
	for name, breakIt := range map[string]func(e *ShopOrderEvent){
		"no event id":                       func(e *ShopOrderEvent) { e.EventID = "" },
		"no sequence":                       func(e *ShopOrderEvent) { e.Sequence = 0 },
		"an unknown type":                   func(e *ShopOrderEvent) { e.Type = "ecommerce.order_paid" },
		"no reference":                      func(e *ShopOrderEvent) { e.Ref = "" },
		"no currency":                       func(e *ShopOrderEvent) { e.Currency = "euro" },
		"a negative amount":                 func(e *ShopOrderEvent) { e.TaxMinor = -1 },
		"more refunded than paid":           func(e *ShopOrderEvent) { e.RefundedMinor = e.TotalMinor + 1 },
		"a refund on a paid event":          func(e *ShopOrderEvent) { e.RefundMinor = 100 },
		"a shipment on a paid event":        func(e *ShopOrderEvent) { e.FulfillmentID = "f1" },
		"a line with no SKU":                func(e *ShopOrderEvent) { e.Lines[0].SKU = "" },
		"a line refunded past what it sold": func(e *ShopOrderEvent) { e.Lines[0].QuantityRefunded, e.Lines[0].QuantityCancelled = 1, 2 },
		"a line shipped past what it sold":  func(e *ShopOrderEvent) { e.Lines[0].QuantityFulfilled = 3 },
		"a consent word that is not one":    func(e *ShopOrderEvent) { e.Consent.AdUserData = "yes" },
		"a consent with no source":          func(e *ShopOrderEvent) { e.Consent.Source = "" },
		"lines that are not the net":        func(e *ShopOrderEvent) { e.NetMinor = 2400 },
	} {
		e := paidEvent()
		e.Lines = append([]ShopEventLine(nil), e.Lines...)
		breakIt(&e)
		if err := e.Validate(); err == nil {
			t.Errorf("%s: the event was accepted", name)
		}
	}
	refunded := paidEvent()
	refunded.Type, refunded.RefundMinor, refunded.RefundedMinor = EventShopOrderRefunded, 1000, 1000
	if err := refunded.Validate(); err != nil {
		t.Errorf("a refund event was refused: %v", err)
	}
	refunded.RefundMinor = 0
	if err := refunded.Validate(); err == nil {
		t.Error("a refund event that says no refund was accepted")
	}
	shipped := paidEvent()
	shipped.Type = EventShopOrderFulfilled
	if err := shipped.Validate(); err == nil {
		t.Error("a fulfilled event naming no shipment was accepted")
	}
	shipped.FulfillmentID = "f1"
	if err := shipped.Validate(); err != nil {
		t.Errorf("a fulfilled event was refused: %v", err)
	}
}

// Only a word the visitor said is consent: unknown is not granted, so a subscriber that sends a person's data to an ad
// network asks AdUserDataGranted and gets false for everything but "granted" (Consent Mode v2's ad_user_data).
func TestUnknownConsentIsNotConsent(t *testing.T) {
	c := paidEvent().Consent
	if c.AdUserDataGranted() || c.AdStorageGranted() {
		t.Fatal("an unknown ad consent reads as granted")
	}
	c.AdUserData, c.AdStorage = ConsentGranted, ConsentDenied
	if !c.AdUserDataGranted() || c.AdStorageGranted() {
		t.Fatal("the granted and denied words are read the wrong way round")
	}
	none := ConsentSnapshot{}
	if err := none.Validate(); err == nil {
		t.Fatal("an empty consent snapshot was accepted: an order placed before the snapshot existed says not_told, never nothing")
	}
	if got := NotToldConsent(time.Now()); got.Validate() != nil || got.AdUserDataGranted() || got.AnalyticsStorage != ConsentUnknown {
		t.Fatalf("NotToldConsent is not all unknown: %+v", got)
	}
}

// A subscriber decodes what HandleEvent hands it through DecodeShopOrderEvent and is refused a payload that is not a whole
// event of this contract — an event of the old thin names, a broken one.
func TestASubscriberDecodesOnlyAWholeEvent(t *testing.T) {
	raw, _ := json.Marshal(paidEvent())
	e, err := DecodeShopOrderEvent(EventShopOrderPaid, raw)
	if err != nil || e.Ref != "NLD-1042" || len(e.Lines) != 2 {
		t.Fatalf("a whole event did not decode: %+v %v", e, err)
	}
	if _, err := DecodeShopOrderEvent(EventShopOrderRefunded, raw); err == nil {
		t.Error("an event delivered under another name than its type decoded")
	}
	if _, err := DecodeShopOrderEvent("ecommerce.order_paid", []byte(`{"order_id":"x","total_minor":1}`)); err == nil {
		t.Error("the old thin event decoded as a shop.events/v1 event")
	}
}

// The read is bounded, in order, and checked on both sides: a subscriber that missed events (installed later, a dead
// letter, a restart) catches up from the last sequence it handled, a page at a time.
func TestTheEventsReadIsBoundedAndInOrder(t *testing.T) {
	for _, r := range []ShopEventsRequest{{AfterSequence: -1}, {Limit: MaxShopEventsRead + 1}, {Limit: -1}} {
		if r.Validate() == nil {
			t.Errorf("%+v was accepted", r)
		}
	}
	req := ShopEventsRequest{AfterSequence: 6, Limit: 2}
	a, b := paidEvent(), paidEvent()
	b.Sequence, b.EventID = 9, "9"
	if err := (ShopEventsAnswer{Events: []ShopOrderEvent{a, b}}).ValidateFor(req); err != nil {
		t.Fatalf("an ordered page was refused: %v", err)
	}
	for name, ans := range map[string]ShopEventsAnswer{
		"out of order":       {Events: []ShopOrderEvent{b, a}},
		"at or before after": {Events: []ShopOrderEvent{func() ShopOrderEvent { e := a; e.Sequence = 6; return e }()}},
		"past the limit":     {Events: []ShopOrderEvent{a, b, func() ShopOrderEvent { e := b; e.Sequence = 10; return e }()}},
	} {
		if ans.ValidateFor(req) == nil {
			t.Errorf("%s: the page was accepted", name)
		}
	}
	m := ShopEventsMethods(fakeEventsShop{a, b})
	if len(m) != 1 || m[0].Contract != ContractShopEvents || m[0].Method != ShopEventsRead {
		t.Fatalf("the shop's methods are %+v", m)
	}
	out, err := m[0].Handle(context.Background(), json.RawMessage(`{"after_sequence":6,"limit":5}`))
	if err != nil || len(out.(ShopEventsAnswer).Events) != 2 {
		t.Fatalf("the read answered %+v %v", out, err)
	}
	if _, err := m[0].Handle(context.Background(), json.RawMessage(`{"after_sequence":-4}`)); err == nil {
		t.Error("a request the contract refuses reached the shop")
	}
}

type fakeEventsShop []ShopOrderEvent

func (f fakeEventsShop) ShopEvents(_ context.Context, req ShopEventsRequest) (ShopEventsAnswer, error) {
	var out ShopEventsAnswer
	for _, e := range f {
		if e.Sequence > req.AfterSequence && len(out.Events) < req.limit() {
			out.Events = append(out.Events, e)
		}
	}
	return out, nil
}

// The contract is door D1 as the plan draws it: experimental, one read answered by the one shop, and no assistant door —
// the owner reads orders through the shop's own abilities.
func TestTheEventsContractIsDoorD1(t *testing.T) {
	c, ok := ContractNamed(ContractShopEvents)
	if !ok {
		t.Fatal("shop.events/v1 is not published")
	}
	if !c.Experimental() {
		t.Error("shop.events/v1 is not experimental")
	}
	if len(c.Methods) != 1 || c.Methods[0].Name != ShopEventsRead || c.Methods[0].Fallback != "block" || c.Methods[0].Write != nil {
		t.Errorf("the contract's methods are %+v", c.Methods)
	}
	for _, typ := range ShopEventTypes() {
		if !strings.HasPrefix(typ, "ecommerce.order.") {
			t.Errorf("%s is outside the shop's namespace", typ)
		}
	}
}

// THE SHOP'S SIDE, HELD TO ITS SOURCE: the shop emits every event of this contract through the SDK's names, checks each
// with Validate before it keeps it, answers the read as a ShopEventsProvider (Serve then answers it through
// ShopEventsMethods), and copies the consent at placement in the SDK's words — read from the shop's checkout beside
// plugin-sdk (skipped when it is not there).
func TestTheShopEmitsTheEventsContract(t *testing.T) {
	src := siblingSource(t, "commerce", "shopevents.go")
	for _, want := range []string{
		"ShopEvents(ctx context.Context, req nilda.ShopEventsRequest) (nilda.ShopEventsAnswer, error)",
		"nilda.ConsentFromFirstTouch", "nilda.NotToldConsent", "e.Validate()",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the shop's shopevents.go no longer uses %s", want)
		}
	}
	for _, typ := range []string{"EventShopOrderPlaced", "EventShopOrderPaid", "EventShopOrderCancelled", "EventShopOrderRefunded", "EventShopOrderFulfilled"} {
		if !strings.Contains(src, "nilda."+typ) {
			t.Errorf("the shop does not emit nilda.%s", typ)
		}
	}
	if !strings.Contains(siblingSource(t, "commerce", "plugin.json"), `"`+ContractShopEvents+`"`) {
		t.Error("the shop's plugin.json does not provide shop.events/v1")
	}
}

// THE CONSENT CORE TELLS THE SHOP, READ AS CORE SENDS IT (core internal/plugin/visitorconsent.go): the four Consent Mode
// signals, each granted or denied, on a request that changes something. Anything else — no header (an older Core, a
// page view, a machine path), a missing signal, a word that is not one, unknown — is not a consent and reads false, so
// the shop falls back to what it can prove itself.
func TestVisitorConsentIsReadAsCoreSendsIt(t *testing.T) {
	req := func(v string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/shop/checkout", nil)
		if v != "" {
			r.Header.Set("X-Nilda-Visitor-Consent", v)
		}
		return r
	}
	c, ok := VisitorConsent(req("ad_storage=granted;ad_user_data=granted;ad_personalization=denied;analytics_storage=denied"))
	if !ok || c.Source != ConsentFromCore || !c.AdUserDataGranted() || !c.AdStorageGranted() ||
		c.AdPersonalization != ConsentDenied || c.AnalyticsStorage != ConsentDenied || c.Validate() != nil {
		t.Fatalf("read %+v %v", c, ok)
	}
	for _, bad := range []string{"", "ad_storage=granted", "ad_storage=yes;ad_user_data=granted;ad_personalization=granted;analytics_storage=granted",
		"ad_storage=unknown;ad_user_data=unknown;ad_personalization=unknown;analytics_storage=unknown",
		"ad_storage=granted;ad_user_data=granted;ad_personalization=granted;analytics_storage=granted;extra=granted",
		"ad_storage=granted;ad_storage=denied;ad_personalization=granted;analytics_storage=granted"} {
		if c, ok := VisitorConsent(req(bad)); ok {
			t.Errorf("%q read as %+v", bad, c)
		}
	}
	told := c
	told.AdUserData = ConsentUnknown
	if told.Validate() == nil {
		t.Error("a consent Core told with an unknown signal was accepted: Core knows every signal")
	}
	src := coreSource(t, "internal", "plugin", "visitorconsent.go")
	for _, want := range []string{`visitorConsentHeader = "X-Nilda-Visitor-Consent"`,
		`"ad_storage=" + a + ";ad_user_data=" + a + ";ad_personalization=" + a + ";analytics_storage=" + an`} {
		if !strings.Contains(src, want) {
			t.Errorf("Core's visitorconsent.go no longer says %s", want)
		}
	}
}
