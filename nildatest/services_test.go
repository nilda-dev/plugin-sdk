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

func useQuoteContract(t *testing.T, budgetMS int, fallback string) {
	t.Helper()
	c, err := nilda.ParseContract([]byte(`{"name": "test.rates/v1", "description": "Rates.", "methods": [
		{"name": "quote", "answered_by": "provider", "description": "A price.", "budget_ms": ` + itoa(budgetMS) + `,
		 "retry_class": "failure", "fallback": "` + fallback + `", "idempotent": true, "agent": "read",
		 "input": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
		 "output": {"type": "object", "properties": {"price": {"type": "integer"}}, "required": ["price"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nilda.RegisterContractForTest(c))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

type carrier struct{ wait time.Duration }

func (c carrier) ServiceMethods() []nilda.ServiceMethod {
	type in struct {
		N int `json:"n"`
	}
	return []nilda.ServiceMethod{nilda.Serves("test.rates/v1", "quote", func(ctx context.Context, q in) (map[string]int, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.wait):
		}
		return map[string]int{"price": q.N * 10}, nil
	})}
}

// A consumer and a provider, with the desk between them and no Core running.
func TestServeServiceAnswersThroughTheFakeHost(t *testing.T) {
	useQuoteContract(t, 2000, "skip")
	desk := NewServices()
	desk.Provider("dhl", carrier{})
	core, _, srv := desk.Core("shop", "test.rates/v1")
	defer srv.Close()
	res, err := core.API().Services().Call(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 3},
		nilda.ServiceCallOptions{IdempotencyKey: "order-1-rates"})
	if err != nil || len(res.Answers) != 1 || string(res.Answers[0].Output) != `{"price":30}` {
		t.Fatalf("result = %+v, err %v", res, err)
	}
	calls := desk.Calls()
	if len(calls) != 1 || calls[0].Caller != "shop" || calls[0].IdempotencyKey != "order-1-rates" {
		t.Fatalf("the provider was asked %+v", calls)
	}

	stranger, _, srv2 := desk.Core("blog")
	defer srv2.Close()
	if _, err := stranger.API().Services().Call(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1},
		nilda.ServiceCallOptions{}); err == nil || !strings.Contains(err.Error(), "does not require or use") {
		t.Fatalf("a plugin that did not declare the contract was answered: %v", err)
	}
}

// A provider that runs out of the method's budget is a Miss in the result, not the call failing: the shop shows the
// rates it has.
func TestADeadlineIsAResultNotAnError(t *testing.T) {
	useQuoteContract(t, 100, "skip")
	desk := NewServices()
	desk.Provider("dhl", carrier{})
	desk.Provider("slow", carrier{wait: time.Second})
	res, err := desk.Ask(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1}, nilda.ServiceCallOptions{})
	if err != nil {
		t.Fatalf("a slow provider failed the call: %v", err)
	}
	if len(res.Answers) != 1 || res.Answers[0].Provider != "dhl" || len(res.Missed) != 1 || res.Missed[0].Reason != "timeout" {
		t.Fatalf("result = %+v", res)
	}

	useQuoteContract(t, 100, "block")
	only := NewServices()
	only.Provider("slow", carrier{wait: time.Second})
	if _, err := only.Ask(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1}, nilda.ServiceCallOptions{}); !errors.Is(err, ErrServiceBlocked) {
		t.Fatalf("a blocking method with nothing answered: %v", err)
	}
}

// alertsKeeper is a stock-alerts plugin whose describe answers, or fails.
type alertsKeeper struct{ fail bool }

func (a alertsKeeper) DescribeStockAlerts(context.Context) (nilda.StockAlertsDescription, error) {
	if a.fail {
		return nilda.StockAlertsDescription{}, errors.New("database down")
	}
	return nilda.StockAlertsDescription{Collecting: true}, nil
}

func (a alertsKeeper) SubscribeStockAlert(context.Context, nilda.StockAlertRequest) (nilda.StockAlertResult, error) {
	return nilda.StockAlertResult{Status: nilda.StockAlertSubscribed}, nil
}

func (a alertsKeeper) ServiceMethods() []nilda.ServiceMethod { return nilda.StockAlertMethods(a) }

// A PLUGIN THAT IS THERE AND DID NOT ANSWER IS NOT "NOBODY" (the 2026-10-15 review, plan 13 C7): the shop's answer is
// kept until the next plugin event, so a describe that failed once must not read as "no plugin collects".
func TestAStockAlertsPluginThatFailsIsNotNobody(t *testing.T) {
	ctx := context.Background()
	ask := func(keeper *alertsKeeper) (nilda.StockAlertsDescription, bool, error) {
		desk := NewServices()
		if keeper != nil {
			desk.Provider("stock_alerts", *keeper)
		}
		core, _, srv := desk.Core("shop", nilda.ContractStockAlerts)
		defer srv.Close()
		return core.API().Services().StockAlertsDescription(ctx)
	}
	if _, ok, err := ask(nil); ok || err != nil {
		t.Errorf("no plugin: ok %v, err %v — want nobody, no error", ok, err)
	}
	if _, ok, err := ask(&alertsKeeper{fail: true}); ok || err == nil || !strings.Contains(err.Error(), "stock_alerts") {
		t.Errorf("a plugin whose describe failed: ok %v, err %v — want an error naming it", ok, err)
	}
	if d, ok, err := ask(&alertsKeeper{}); !ok || err != nil || !d.Collecting {
		t.Errorf("a collecting plugin: %+v %v %v", d, ok, err)
	}
}

// keyDelivery is a delivering plugin whose describe answers, or fails.
type keyDelivery struct {
	skus []string
	fail bool
}

func (k keyDelivery) DescribeDeliverables(context.Context) (nilda.DeliverablesDescription, error) {
	if k.fail {
		return nilda.DeliverablesDescription{}, errors.New("database down")
	}
	return nilda.DeliverablesDescription{SKUs: k.skus}, nil
}

func (k keyDelivery) OrderDeliverables(context.Context, nilda.DeliverablesRequest) (nilda.DeliverablesResult, error) {
	if k.fail {
		return nilda.DeliverablesResult{}, errors.New("database down")
	}
	return nilda.DeliverablesResult{Items: []nilda.DeliveredItem{{SKU: k.skus[0], Kind: nilda.DeliverableKey, Label: "Key", Value: "K-" + k.skus[0]}}}, nil
}

func (k keyDelivery) ServiceMethods() []nilda.ServiceMethod { return nilda.DeliverablesMethods(k) }

// EVERY DELIVERING PLUGIN IS ASKED, AND ONE THAT FAILS IS NOT "NOTHING DELIVERED": the SKUs come from all of them, a
// checkout cannot sell a key without its waiver because one plugin was slow, and a page shows what the others gave
// and counts the one it could not ask.
func TestEveryDeliveringPluginIsAskedAndAFailureIsNotNone(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	desk.Provider("license_keys", keyDelivery{skus: []string{"pe-pro"}})
	desk.Provider("tickets", keyDelivery{skus: []string{"gig-1"}})
	core, _, srv := desk.Core("shop", nilda.ContractShopDeliverables)
	defer srv.Close()
	skus, err := core.API().Services().DeliveredSKUs(ctx)
	if err != nil || !skus["pe-pro"] || !skus["gig-1"] {
		t.Fatalf("the SKUs of two plugins: %v %v", skus, err)
	}
	items, missed, err := core.API().Services().OrderDeliverables(ctx, nilda.DeliverablesRequest{OrderID: "o1"})
	if err != nil || missed != 0 || len(items) != 2 {
		t.Fatalf("two plugins' items: %+v %d %v", items, missed, err)
	}

	desk = NewServices()
	desk.Provider("license_keys", keyDelivery{skus: []string{"pe-pro"}})
	desk.Provider("tickets", keyDelivery{skus: []string{"gig-1"}, fail: true})
	core, _, srv2 := desk.Core("shop", nilda.ContractShopDeliverables)
	defer srv2.Close()
	if _, err := core.API().Services().DeliveredSKUs(ctx); err == nil || !strings.Contains(err.Error(), "tickets") {
		t.Errorf("a plugin whose describe failed: %v — want an error naming it", err)
	}
	items, missed, err = core.API().Services().OrderDeliverables(ctx, nilda.DeliverablesRequest{OrderID: "o1"})
	if err != nil || missed != 1 || len(items) != 1 {
		t.Errorf("one plugin failed: %+v missed %d %v — want the other's item and one missed", items, missed, err)
	}
}

// offersKeeper is an offers plugin whose answers come, or fail.
type offersKeeper struct {
	id   string
	fail bool
}

func (o offersKeeper) OffersFor(context.Context, nilda.OffersRequest) (nilda.OffersResult, error) {
	if o.fail {
		return nilda.OffersResult{}, errors.New("database down")
	}
	return nilda.OffersResult{Offers: []nilda.Offer{{ID: o.id, SKU: "cap", PercentOff: 10, MaxUnits: 1}}}, nil
}

func (o offersKeeper) ConfirmOffers(context.Context, nilda.OffersConfirmRequest) (nilda.OffersConfirmResult, error) {
	if o.fail {
		return nilda.OffersConfirmResult{}, errors.New("database down")
	}
	return nilda.OffersConfirmResult{Accepted: []nilda.Offer{{ID: o.id, SKU: "cap", PercentOff: 10, MaxUnits: 1}}}, nil
}

func (o offersKeeper) ServiceMethods() []nilda.ServiceMethod { return nilda.OffersMethods(o) }

// AN OFFERS PLUGIN THAT FAILS IS LEFT OUT, AND ITS OFFERS DO NOT STAND: the page shows the other plugin's offers, and
// the basket is priced without the silent one's discount.
func TestAnOffersPluginThatFailsIsLeftOutAndItsOffersDoNotStand(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	desk.Provider("offers", offersKeeper{id: "a"})
	desk.Provider("bumps", offersKeeper{id: "b", fail: true})
	core, _, srv := desk.Core("shop", nilda.ContractShopOffers)
	defer srv.Close()
	basket := []nilda.OfferLine{{SKU: "lens", Qty: 1, UnitPriceMinor: 2000}}
	offers, err := core.API().Services().Offers(ctx, nilda.OffersRequest{Stage: nilda.OfferStageCart, Currency: "EUR", Lines: basket})
	if err != nil || len(offers) != 1 || offers[0].ID != "a" {
		t.Fatalf("offers: %+v %v", offers, err)
	}
	stand, err := core.API().Services().ConfirmOffers(ctx, nilda.OffersConfirmRequest{Currency: "EUR",
		Lines: append(basket, nilda.OfferLine{SKU: "cap", Qty: 1, UnitPriceMinor: 500, OfferID: "b"})})
	if err != nil || len(stand) != 1 || stand["b"].ID != "" || stand["a"].ID != "a" {
		t.Errorf("confirmed: %+v %v", stand, err)
	}
}
