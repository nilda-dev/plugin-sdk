package nilda

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeOffers answers with the offers the test sets.
type fakeOffers struct {
	offers []Offer
	asked  []OffersRequest
}

func (f *fakeOffers) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f *fakeOffers) HandleEvent(context.Context, string, []byte) error          { return nil }
func (f *fakeOffers) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }

func (f *fakeOffers) OffersFor(_ context.Context, req OffersRequest) (OffersResult, error) {
	f.asked = append(f.asked, req)
	return OffersResult{Offers: f.offers}, nil
}

func (f *fakeOffers) ConfirmOffers(context.Context, OffersConfirmRequest) (OffersConfirmResult, error) {
	return OffersConfirmResult{Accepted: f.offers}, nil
}

// AN OFFER THE SHOP COULD NOT HONOUR NEVER LEAVES THE PLUGIN — a discount over 90%, none of the units, a headline
// that is a paragraph — and a request for a stage nobody draws never reaches it.
func TestAnOfferIsCheckedBothWays(t *testing.T) {
	ctx := context.Background()
	f := &fakeOffers{}
	sp := serviceProviderOf(f)
	if sp == nil {
		t.Fatal("an OffersProvider is not found")
	}
	ask := func(in string) error {
		_, ok, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopOffers, OffersFor), []byte(in))
		if !ok {
			t.Fatal("offers.for was not dispatched")
		}
		return err
	}
	basket := `{"stage":"checkout","currency":"EUR","subtotal_minor":2000,"lines":[{"sku":"lens","qty":1,"unit_price_minor":2000}]}`
	f.offers = []Offer{{ID: "o1", SKU: "cap", PercentOff: 20, MaxUnits: 1, Headline: "Protect it"}}
	if err := ask(basket); err != nil || len(f.asked) != 1 {
		t.Fatalf("a good offer: %v", err)
	}
	for name, o := range map[string]Offer{
		"95% off":     {ID: "o", SKU: "cap", PercentOff: 95, MaxUnits: 1},
		"no units":    {ID: "o", SKU: "cap", PercentOff: 10},
		"no SKU":      {ID: "o", PercentOff: 10, MaxUnits: 1},
		"a paragraph": {ID: "o", SKU: "cap", PercentOff: 10, MaxUnits: 1, Headline: string(make([]byte, 300))},
	} {
		f.offers = []Offer{o}
		if err := ask(basket); err == nil {
			t.Errorf("%s left the plugin", name)
		}
	}
	f.offers = nil
	asked := len(f.asked)
	if err := ask(`{"stage":"homepage","currency":"EUR","lines":[]}`); err == nil || len(f.asked) != asked {
		t.Error("a stage nobody draws reached the plugin")
	}
	out, _, _ := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopOffers, OffersConfirm),
		[]byte(`{"currency":"EUR","subtotal_minor":500,"lines":[{"sku":"cap","qty":1,"unit_price_minor":500,"offer_id":"o1"}]}`))
	var r OffersConfirmResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
}

// NONE IS AN EMPTY LIST: a plugin that returns a nil slice answers [] — the contract's schema refuses null.
func TestNoOffersIsAnEmptyList(t *testing.T) {
	ctx := context.Background()
	sp := serviceProviderOf(&fakeOffers{})
	for _, m := range []string{OffersFor, OffersConfirm} {
		out, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopOffers, m),
			[]byte(`{"stage":"cart","currency":"EUR","subtotal_minor":1,"lines":[{"sku":"a","qty":1,"unit_price_minor":1}]}`))
		if err != nil || len(out) == 0 {
			t.Errorf("%s with nothing to offer: %q %v", m, out, err)
		}
	}
	sp = serviceProviderOf(&fakeKeyDelivery{})
	for _, m := range []string{DeliverablesDescribe, DeliverablesOrderItems} {
		out, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopDeliverables, m), []byte(`{"order_id":"o1"}`))
		if err != nil || len(out) == 0 {
			t.Errorf("%s with nothing delivered: %q %v", m, out, err)
		}
	}
}
