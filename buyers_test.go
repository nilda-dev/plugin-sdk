package nilda

import (
	"context"
	"testing"
	"time"
)

// fakeBuyers answers with the terms the test sets.
type fakeBuyers struct {
	terms BuyerTerms
	quote *Quote
}

func (f *fakeBuyers) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f *fakeBuyers) HandleEvent(context.Context, string, []byte) error          { return nil }
func (f *fakeBuyers) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }

func (f *fakeBuyers) BuyerTerms(context.Context, BuyerTermsRequest) (BuyerTerms, error) { return f.terms, nil }

func (f *fakeBuyers) RequestQuote(context.Context, QuoteRequestInput) (QuoteRequestResult, error) {
	return QuoteRequestResult{QuoteID: "q1"}, nil
}

func (f *fakeBuyers) OpenQuote(context.Context, QuoteOpenRequest) (QuoteOpenResult, error) {
	return QuoteOpenResult{Found: f.quote != nil, Quote: f.quote}, nil
}

// TERMS THE SHOP COULD NOT APPLY NEVER LEAVE THE PLUGIN — a discount over 100%, a price in no currency, a minimum of
// none — and a quote request without the buyer's address or the shop's page never reaches it.
func TestBuyerTermsAreCheckedBothWays(t *testing.T) {
	ctx := context.Background()
	f := &fakeBuyers{}
	sp := serviceProviderOf(f)
	if sp == nil {
		t.Fatal("a BuyersProvider is not found")
	}
	terms := func() error {
		_, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopBuyers, BuyerTermsGet), []byte(`{"user_id":"u1","skus":["a"]}`))
		return err
	}
	f.terms = BuyerTerms{Member: true, Group: "g", PercentOff: 15, Prices: []BuyerPrice{{SKU: "a", PriceMinor: 800, Currency: "EUR"}},
		MinQty: []BuyerMinimum{{SKU: "a", Qty: 6}}, PayByInvoice: true, NetDays: 30}
	if err := terms(); err != nil {
		t.Fatalf("good terms: %v", err)
	}
	for name, bad := range map[string]BuyerTerms{
		"120% off":       {Member: true, PercentOff: 120},
		"no currency":    {Member: true, Prices: []BuyerPrice{{SKU: "a", PriceMinor: 1}}},
		"a minimum of 0": {Member: true, MinQty: []BuyerMinimum{{SKU: "a"}}},
		"net 400":        {Member: true, NetDays: 400},
	} {
		f.terms = bad
		if err := terms(); err == nil {
			t.Errorf("%s left the plugin", name)
		}
	}
	for name, in := range map[string]string{
		"no email":    `{"user_id":"u1","email":"","currency":"EUR","accept_url":"https://s/shop/quote","lines":[{"sku":"a","qty":1,"unit_price_minor":1}]}`,
		"no page":     `{"user_id":"u1","email":"eva@x.com","currency":"EUR","accept_url":"/shop/quote","lines":[{"sku":"a","qty":1,"unit_price_minor":1}]}`,
		"no lines":    `{"user_id":"u1","email":"eva@x.com","currency":"EUR","accept_url":"https://s/shop/quote","lines":[]}`,
		"no buyer id": `{"user_id":"","email":"eva@x.com","currency":"EUR","accept_url":"https://s/shop/quote","lines":[{"sku":"a","qty":1,"unit_price_minor":1}]}`,
	} {
		if _, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopBuyers, QuoteRequest), []byte(in)); err == nil {
			t.Errorf("a quote request with %s reached the plugin", name)
		}
	}
	f.quote = &Quote{ID: "q1", Currency: "EUR", ExpiresAt: time.Now(), Lines: []QuoteLine{{SKU: "a", Qty: 10, UnitPriceMinor: 700}}}
	if _, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopBuyers, QuoteOpen), []byte(`{"token":"t","user_id":"u1"}`)); err != nil {
		t.Errorf("a priced quote: %v", err)
	}
}
