package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// slots is a delivery-slot plugin: a date and a time at checkout, an engraving on the ring's product page, a customer's
// Peppol id, a product's "fragile" flag, and a reference only the plugin sets.
type slots struct {
	validates string // "" does not validate; block or allow otherwise
	full      string // a date the plugin says is full
	down      bool   // the validator does not answer
	asked     int
}

func (p *slots) DescribeAttributes(context.Context) (nilda.AttributeDescription, error) {
	d := nilda.AttributeDescription{Fields: []nilda.AttributeField{
		{Key: "delivery_date", Target: nilda.AttributeOrder, Type: nilda.AttributeDate, Required: true, MinDate: "P1D",
			Label: map[string]string{"en": "Delivery date", "de": "Liefertag"},
			Shown: []string{nilda.AttributeShownCheckout, nilda.AttributeShownOrder}},
		{Key: "slot", Target: nilda.AttributeOrder, Type: nilda.AttributeSelect, Label: map[string]string{"en": "Time"},
			Shown: []string{nilda.AttributeShownCheckout, nilda.AttributeShownOrder},
			Choices: []nilda.AttributeChoice{{Value: "am", Label: map[string]string{"en": "Morning"}},
				{Value: "pm", Label: map[string]string{"en": "Afternoon"}}}},
		{Key: "engraving", Target: nilda.AttributeLine, Type: nilda.AttributeText, MaxLength: 20, Required: true,
			Label: map[string]string{"en": "Engraving"}, SKUs: []string{"RING-1"},
			Shown: []string{nilda.AttributeShownProductPage, nilda.AttributeShownOrder}},
		{Key: "peppol_id", Target: nilda.AttributeCustomer, Type: nilda.AttributeText, Label: map[string]string{"en": "Peppol id"},
			Shown: []string{nilda.AttributeShownCheckout, nilda.AttributeShownOrder}},
		{Key: "fragile", Target: nilda.AttributeProduct, Type: nilda.AttributeBoolean, Label: map[string]string{"en": "Fragile"},
			Shown: []string{nilda.AttributeShownProductForm, nilda.AttributeShownOrder}},
		{Key: "slot_ref", Target: nilda.AttributeOrder, Type: nilda.AttributeText, Label: map[string]string{"en": "Slot reference"},
			Shown: []string{nilda.AttributeShownOrder}},
	}}
	if p.validates != "" {
		d.Validates, d.WhenUnanswered = true, p.validates
	}
	return d, nil
}

func (p *slots) ValidateAttributes(_ context.Context, req nilda.AttributeValidateRequest) (nilda.AttributeValidateAnswer, error) {
	p.asked++
	if p.down {
		return nilda.AttributeValidateAnswer{}, errors.New("the slot calendar is down")
	}
	for _, v := range req.Values {
		if v.Field == "delivery_date" && string(v.Value) == `"`+p.full+`"` {
			return nilda.AttributeValidateAnswer{Errors: []nilda.AttributeError{{Field: "delivery_date", Message: "That day is full."}}}, nil
		}
	}
	return nilda.AttributeValidateAnswer{}, nil
}

func (p *slots) ServiceMethods() []nilda.ServiceMethod { return nilda.AttributeMethods(p) }

var nov5 = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func shopWithSlots(t *testing.T, p *slots) (*Services, *AttributeShop) {
	t.Helper()
	desk := NewServices()
	shop := NewAttributeShop(desk)
	t.Cleanup(shop.Close)
	shop.SetToday(nov5)
	desk.Provider("slots", p)
	return desk, shop
}

func val(plugin, f, line, v string) nilda.AttributeValue {
	return nilda.AttributeValue{Plugin: plugin, Field: f, Line: line, Value: json.RawMessage(v)}
}

func ringOrder(values ...nilda.AttributeValue) AttributeOrderDraft {
	return AttributeOrderDraft{Currency: "EUR", Country: "DE", Customer: "u1", Locale: "de",
		Lines: []AttributeDraftLine{{ID: "l1", SKU: "RING-1", Product: "ring", Quantity: 1},
			{ID: "l2", SKU: "MUG-1", Product: "mug", Quantity: 2}},
		Values: values}
}

func snapshot(o AttributeOrder, field, line string) (AttributeSnapshot, bool) {
	for _, a := range o.Attributes {
		if a.Field == field && a.Line == line {
			return a, true
		}
	}
	return AttributeSnapshot{}, false
}

// THE ORDER KEEPS WHAT WAS ASKED: the buyer's values, the customer's and the product's copied in at placement, each with
// the label in the order's language — and the order still says so after the plugin is gone.
func TestTheShopPlacesAnOrderWithAPluginsFieldsAndKeepsThem(t *testing.T) {
	ctx := context.Background()
	desk, shop := shopWithSlots(t, &slots{})
	if err := shop.SetProductValue(ctx, "mug", "slots", "fragile", json.RawMessage(`true`)); err != nil {
		t.Fatalf("the owner's product form: %v", err)
	}
	o, err := shop.PlaceOrder(ctx, ringOrder(
		val("slots", "delivery_date", "", `"2026-11-06"`),
		val("slots", "engraving", "l1", `"Für Sara"`),
		val("slots", "peppol_id", "", `"0208:1234"`)))
	if err != nil {
		t.Fatalf("a good order was refused: %v", err)
	}
	date, ok := snapshot(o, "delivery_date", "")
	if !ok || date.Label != "Liefertag" || date.Plugin != "slots" || date.Kind != nilda.AttributeOrder || !date.OnOrder || string(date.Value) != `"2026-11-06"` {
		t.Errorf("the delivery date on the order: %+v", date)
	}
	if e, ok := snapshot(o, "engraving", "l1"); !ok || e.Kind != nilda.AttributeLine {
		t.Errorf("the engraving on the ring's line: %+v", e)
	}
	if c, ok := snapshot(o, "peppol_id", ""); !ok || c.Kind != nilda.AttributeCustomer {
		t.Errorf("the customer's Peppol id copied into the order: %+v", c)
	}
	if f, ok := snapshot(o, "fragile", "l2"); !ok || f.Kind != nilda.AttributeProduct || string(f.Value) != "true" {
		t.Errorf("the mug's fragile flag copied onto its line: %+v", f)
	}
	// The plugin is switched off: nothing answers its describe, and the order says what it said.
	desk.Provider("slots", nil)
	kept, ok := shop.Order(o.ID)
	if !ok {
		t.Fatal("the order is gone")
	}
	if d, ok := snapshot(kept, "delivery_date", ""); !ok || d.Label != "Liefertag" {
		t.Errorf("the order forgot its delivery date with the plugin: %+v", d)
	}
	// A later change to the product does not reach a placed order.
	if err := shop.SetProductValue(ctx, "mug", "slots", "fragile", json.RawMessage(`false`)); err == nil {
		t.Log("the product form saved without the plugin; the order must still say true")
	}
	if f, _ := snapshot(kept, "fragile", "l2"); string(f.Value) != "true" {
		t.Errorf("a placed order's copy moved with the product: %s", f.Value)
	}
}

// What the buyer may not send, and what they must: every refusal names the plugin, the field, the line and a code.
func TestAnOrderIsRefusedForAValueItsFieldRefuses(t *testing.T) {
	ctx := context.Background()
	_, shop := shopWithSlots(t, &slots{})
	date := val("slots", "delivery_date", "", `"2026-11-06"`)
	ring := val("slots", "engraving", "l1", `"Sara"`)
	for name, tc := range map[string]struct {
		values []nilda.AttributeValue
		field  string
		line   string
		code   string
	}{
		"no delivery date":             {[]nilda.AttributeValue{ring}, "delivery_date", "", nilda.AttributeRequired},
		"no engraving on the ring":     {[]nilda.AttributeValue{date}, "engraving", "l1", nilda.AttributeRequired},
		"a date today":                 {[]nilda.AttributeValue{ring, val("slots", "delivery_date", "", `"2026-11-05"`)}, "delivery_date", "", nilda.AttributeTooEarly},
		"a slot nobody offers":         {[]nilda.AttributeValue{date, ring, val("slots", "slot", "", `"night"`)}, "slot", "", nilda.AttributeNotAChoice},
		"an engraving on the mug":      {[]nilda.AttributeValue{date, ring, val("slots", "engraving", "l2", `"x"`)}, "engraving", "l2", nilda.AttributeNotOffered},
		"a value only the plugin sets": {[]nilda.AttributeValue{date, ring, val("slots", "slot_ref", "", `"R1"`)}, "slot_ref", "", nilda.AttributeNotOffered},
		"the owner's product field":    {[]nilda.AttributeValue{date, ring, val("slots", "fragile", "l2", `true`)}, "fragile", "l2", nilda.AttributeNotOffered},
		"another plugin's field":       {[]nilda.AttributeValue{date, ring, val("gifts", "note", "", `"hi"`)}, "note", "", nilda.AttributeNotOffered},
		"an engraving on no line":      {[]nilda.AttributeValue{date, ring, val("slots", "engraving", "l9", `"x"`)}, "engraving", "l9", nilda.AttributeNotOffered},
	} {
		_, err := shop.PlaceOrder(ctx, ringOrder(tc.values...))
		var r *AttributeRefusal
		if !errors.As(err, &r) {
			t.Errorf("%s: want a refusal, got %v", name, err)
			continue
		}
		found := false
		for _, e := range r.Errors {
			found = found || (e.Field == tc.field && e.Line == tc.line && e.Code == tc.code)
		}
		if !found {
			t.Errorf("%s: want %s on %s/%s, got %+v", name, tc.code, tc.field, tc.line, r.Errors)
		}
	}
}

// VALIDATE AT PLACE ORDER: the plugin's "no" refuses the order in its words; a plugin that does not answer blocks or lets
// the order through, as it declared — and a let-through order says which plugin did not check it.
func TestAPluginChecksItsValuesBeforeTheOrderIsPlaced(t *testing.T) {
	ctx := context.Background()
	good := []nilda.AttributeValue{val("slots", "delivery_date", "", `"2026-11-07"`), val("slots", "engraving", "l1", `"Sara"`)}

	p := &slots{validates: nilda.AttributeBlock, full: "2026-11-07"}
	_, shop := shopWithSlots(t, p)
	_, err := shop.PlaceOrder(ctx, ringOrder(good...))
	var r *AttributeRefusal
	if !errors.As(err, &r) || len(r.Errors) != 1 || r.Errors[0].Message != "That day is full." || r.Errors[0].Plugin != "slots" {
		t.Fatalf("a full day was not refused in the plugin's words: %v %+v", err, r)
	}
	p.full = ""
	if _, err := shop.PlaceOrder(ctx, ringOrder(good...)); err != nil {
		t.Fatalf("a free day was refused: %v", err)
	}

	p.down = true
	_, err = shop.PlaceOrder(ctx, ringOrder(good...))
	if !errors.As(err, &r) || len(r.Unanswered) != 1 || r.Unanswered[0] != "slots" {
		t.Fatalf("a validator that did not answer let a blocking plugin's order through: %v", err)
	}

	p2 := &slots{validates: nilda.AttributeAllow, down: true}
	_, shop2 := shopWithSlots(t, p2)
	o, err := shop2.PlaceOrder(ctx, ringOrder(good...))
	if err != nil {
		t.Fatalf("an allowing plugin that did not answer refused the order: %v", err)
	}
	if len(o.Unchecked) != 1 || o.Unchecked[0] != "slots" || p2.asked != 1 {
		t.Errorf("the order does not say which plugin did not check it: %+v (asked %d)", o.Unchecked, p2.asked)
	}

	p3 := &slots{}
	_, shop3 := shopWithSlots(t, p3)
	if _, err := shop3.PlaceOrder(ctx, ringOrder(good...)); err != nil || p3.asked != 0 {
		t.Errorf("a plugin that does not validate was asked %d times (%v)", p3.asked, err)
	}
}

// SETTING A VALUE IS A WRITE COMMAND (D0): a key is required and makes a repeat the same write; the plugin is Core's word
// and reaches only its own fields; an undo brings the replaced value back, once, by the plugin that wrote it, and not
// past a later write; each committed write is announced.
func TestAPluginSetsItsOwnValuesThroughWriteCommands(t *testing.T) {
	ctx := context.Background()
	desk, shop := shopWithSlots(t, &slots{})
	o, err := shop.PlaceOrder(ctx, ringOrder(val("slots", "delivery_date", "", `"2026-11-06"`), val("slots", "engraving", "l1", `"Sara"`)))
	if err != nil {
		t.Fatal(err)
	}
	core, _, srv := desk.Core("slots", nilda.ContractShopAttributeValues)
	defer srv.Close()
	api := core.API().Services()
	set := nilda.AttributeSetRequest{Target: nilda.AttributeTarget{Kind: nilda.AttributeOrder, ID: o.ID}, Field: "slot_ref", Value: json.RawMessage(`"R-1"`)}

	if _, err := api.SetAttribute(ctx, "", set); err == nil {
		t.Fatal("a write without a key left the plugin")
	}
	// The shop refuses one without a key too — a plugin not written in Go reaches it without the SDK's check.
	if _, err := desk.Ask(ctx, nilda.ContractShopAttributeValues, nilda.AttributeValueSet, set, nilda.ServiceCallOptions{IdempotencyKey: "k"}); err == nil {
		t.Fatal("a write with no calling plugin was answered")
	}
	first, err := api.SetAttribute(ctx, "o1-slot-ref", set)
	if err != nil || first.RecordID == "" || first.Already {
		t.Fatalf("the first write: %+v %v", first, err)
	}
	again, err := api.SetAttribute(ctx, "o1-slot-ref", set)
	if err != nil || again.RecordID != first.RecordID || !again.Already {
		t.Fatalf("the repeat made a second write: %+v %v", again, err)
	}
	vs, err := api.AttributeValuesOf(ctx, set.Target)
	if err != nil {
		t.Fatal(err)
	}
	gotRef := false
	for _, v := range vs {
		if v.Plugin != "slots" {
			t.Errorf("values.get answered another plugin's value: %+v", v)
		}
		gotRef = gotRef || (v.Field == "slot_ref" && string(v.Value) == `"R-1"`)
	}
	if !gotRef {
		t.Fatalf("the written value is not on the order: %+v", vs)
	}
	if kept, _ := shop.Order(o.ID); func() bool { s, ok := snapshot(kept, "slot_ref", ""); return !ok || s.Label != "Slot reference" }() {
		t.Error("the order screen does not show the plugin's value with its label")
	}

	// A refusal is a RESULT, never an error: Core's desk relays an error as the provider failing, which blocks and is
	// retried, so a plugin told "no" by an error would wait out the retries and read "unavailable".
	refused := func(name string, a nilda.AttributeWriteAnswer, err error, code string) {
		t.Helper()
		if err != nil || a.Refused != code || a.RecordID != "" || a.Already {
			t.Errorf("%s: want refused %q, got %+v %v", name, code, a, err)
		}
	}
	// Another plugin cannot write the slot plugin's field: in its namespace there is no such field.
	other, _, srv2 := desk.Core("gifts", nilda.ContractShopAttributeValues)
	defer srv2.Close()
	a, err := other.API().Services().SetAttribute(ctx, "steal", set)
	refused("another plugin's field", a, err, nilda.AttributeNotOffered)
	asked := 0
	for _, c := range desk.Calls() {
		if c.IdempotencyKey == "steal" {
			asked++
		}
	}
	if asked != 1 {
		t.Errorf("a refused write reached the shop %d times: it was retried as a failure", asked)
	}
	// A field on the wrong kind of record, a value its field refuses, a record that does not exist.
	wrong := set
	wrong.Target = nilda.AttributeTarget{Kind: nilda.AttributeCustomer, ID: "u1"}
	a, err = api.SetAttribute(ctx, "wrong-kind", wrong)
	refused("an order field on a customer", a, err, nilda.AttributeNotOffered)
	bad := set
	bad.Value = json.RawMessage(`5`)
	a, err = api.SetAttribute(ctx, "bad-type", bad)
	refused("a number in a text field", a, err, nilda.AttributeWrongType)
	missing := set
	missing.Target = nilda.AttributeTarget{Kind: nilda.AttributeOrder, ID: "no-such-order"}
	a, err = api.SetAttribute(ctx, "no-order", missing)
	refused("an order that does not exist", a, err, nilda.AttributeNoRecord)

	// A second write replaces the first; the first cannot be undone past it; the second can, and brings R-1 back.
	set2 := set
	set2.Value = json.RawMessage(`"R-2"`)
	second, err := api.SetAttribute(ctx, "o1-slot-ref-2", set2)
	if err != nil || second.Refused != "" {
		t.Fatal(second, err)
	}
	a, err = api.UndoAttribute(ctx, "undo-1", first.RecordID)
	refused("an undo past a later write", a, err, nilda.AttributeReplaced)
	a, err = other.API().Services().UndoAttribute(ctx, "undo-steal", second.RecordID)
	refused("another plugin's undo", a, err, nilda.AttributeUnknownWrite)
	u, err := api.UndoAttribute(ctx, "undo-2", second.RecordID)
	if err != nil || u.RecordID != second.RecordID || u.Already || u.Refused != "" {
		t.Fatalf("the undo: %+v %v", u, err)
	}
	if u2, err := api.UndoAttribute(ctx, "undo-2", second.RecordID); err != nil || !u2.Already {
		t.Errorf("a repeated undo with its key: %+v %v", u2, err)
	}
	a, err = api.UndoAttribute(ctx, "undo-2b", second.RecordID)
	refused("a second undo", a, err, nilda.AttributeUndoneAlready)
	vs, _ = api.AttributeValuesOf(ctx, set.Target)
	back := false
	for _, v := range vs {
		back = back || (v.Field == "slot_ref" && string(v.Value) == `"R-1"`)
	}
	if !back {
		t.Errorf("the undo did not bring the replaced value back: %+v", vs)
	}

	var types []string
	for _, e := range shop.Events() {
		types = append(types, e.Type)
		if e.Data["plugin"] != "slots" || e.Data["record_id"] == "" {
			t.Errorf("an event does not say whose record: %+v", e)
		}
	}
	if got := strings.Join(types, ","); got != "ecommerce.attribute_set,ecommerce.attribute_set,ecommerce.attribute_undone" {
		t.Errorf("the events: %s (a repeat or a refusal announced nothing)", got)
	}
}

// addresses is an address plugin: it owns no field and suggests.
type addresses struct {
	down  bool
	calls int
}

func (a *addresses) DescribeAttributes(context.Context) (nilda.AttributeDescription, error) {
	return nilda.AttributeDescription{AddressSuggestions: true}, nil
}

func (a *addresses) SuggestAddresses(_ context.Context, req nilda.AddressSuggestRequest) (nilda.AddressSuggestAnswer, error) {
	a.calls++
	if a.down {
		return nilda.AddressSuggestAnswer{}, errors.New("provider down")
	}
	return nilda.AddressSuggestAnswer{Suggestions: []nilda.AddressSuggestion{{ID: "p1", Label: req.Query + " 1, Berlin"}}}, nil
}

func (a *addresses) ResolveAddress(_ context.Context, req nilda.AddressResolveRequest) (nilda.AddressResolveAnswer, error) {
	a.calls++
	return nilda.AddressResolveAnswer{Found: true, Address: &nilda.ShipAddress{Line1: "Hauptstr. 1", City: "Berlin", PostalCode: "10115", Country: "DE"}}, nil
}

func (a *addresses) ServiceMethods() []nilda.ServiceMethod { return nilda.AttributeMethods(a) }

// THE ADDRESS PROXY: the shop asks only a plugin that offers suggestions, bounds each visitor, and a plugin that does not
// answer is no suggestions — the visitor types.
func TestTheShopProxiesAddressSuggestionsBounded(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	shop := NewAttributeShop(desk)
	defer shop.Close()
	a := &addresses{}
	desk.Provider("places", a)
	desk.Provider("slots", &slots{})
	req := nilda.AddressSuggestRequest{Query: "Hauptstr", Country: "DE"}

	got, err := shop.SuggestAddresses(ctx, "v1", "places", req)
	if err != nil || len(got.Suggestions) != 1 {
		t.Fatalf("suggestions: %+v %v", got, err)
	}
	r, err := shop.ResolveAddress(ctx, "v1", "places", nilda.AddressResolveRequest{ID: "p1"})
	if err != nil || !r.Found || r.Address.City != "Berlin" {
		t.Fatalf("resolve: %+v %v", r, err)
	}
	if _, err := shop.SuggestAddresses(ctx, "v1", "slots", req); err == nil {
		t.Error("the shop asked a plugin that offers no suggestions")
	}
	if _, err := shop.SuggestAddresses(ctx, "v1", "places", nilda.AddressSuggestRequest{Query: "Ha"}); err == nil {
		t.Error("a query of two letters reached the plugin")
	}
	a.down = true
	if got, err := shop.SuggestAddresses(ctx, "v1", "places", req); err != nil || len(got.Suggestions) != 0 {
		t.Errorf("a plugin that did not answer: %+v %v — want no suggestions and no error", got, err)
	}
	a.down = false
	for i := 0; i < nilda.AddressSuggestPerMinute; i++ {
		_, _ = shop.SuggestAddresses(ctx, "v2", "places", req)
	}
	before := a.calls
	if _, err := shop.SuggestAddresses(ctx, "v2", "places", req); !errors.Is(err, nilda.ErrAddressSuggestLimited) {
		t.Errorf("past the bound: %v", err)
	}
	if a.calls != before {
		t.Error("a call past the bound reached the plugin")
	}
	if _, err := shop.SuggestAddresses(ctx, "v3", "places", req); err != nil {
		t.Errorf("another visitor was refused: %v", err)
	}
}
