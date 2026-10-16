package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeOrderShop answers one order.
type fakeOrderShop struct{ order ShopOrder }

func (f *fakeOrderShop) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f *fakeOrderShop) HandleEvent(context.Context, string, []byte) error          { return nil }
func (f *fakeOrderShop) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }

func (f *fakeOrderShop) ShopOrder(_ context.Context, req ShopOrderRequest) (ShopOrderResult, error) {
	if req.OrderID != f.order.ID {
		return ShopOrderResult{}, nil
	}
	o := f.order
	return ShopOrderResult{Found: true, Order: &o}, nil
}

// THE SHOP IS FOUND BY ITSELF, a request with no id never reaches it, and an answer the contract refuses — a line
// refunded more than it was bought — fails in the shop's own process instead of reaching a plugin.
func TestServeAnswersAShopThatProvidesItsOrders(t *testing.T) {
	ctx := context.Background()
	f := &fakeOrderShop{order: ShopOrder{ID: "o1", Ref: "NLD-1", Status: OrderStatusProcessing, Paid: true, Email: "eva@example.com",
		Locale: "en", Currency: "EUR", TotalMinor: 2900, PlacedAt: time.Now(),
		Lines: []ShopOrderLine{{SKU: "pe-pro", Title: "Photo Editor Pro", Quantity: 2, UnitPriceMinor: 1450}}}}
	sp := serviceProviderOf(f)
	if sp == nil {
		t.Fatal("an OrdersProvider is not found")
	}
	ask := func(in string) (ShopOrderResult, error) {
		out, ok, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopOrders, OrderGet), []byte(in))
		if !ok {
			t.Fatalf("order.get %s was not dispatched", in)
		}
		var r ShopOrderResult
		if err == nil {
			_ = json.Unmarshal(out, &r)
		}
		return r, err
	}
	if r, err := ask(`{"order_id":"o1"}`); err != nil || !r.Found || r.Order.Lines[0].Quantity != 2 {
		t.Fatalf("the order: %+v %v", r, err)
	}
	if r, err := ask(`{"order_id":"o2"}`); err != nil || r.Found {
		t.Errorf("an order the shop does not have: %+v %v", r, err)
	}
	if _, err := ask(`{"order_id":" "}`); err == nil {
		t.Error("a request with no id reached the shop")
	}
	f.order.Lines[0].QuantityRefunded = 3
	if _, err := ask(`{"order_id":"o1"}`); err == nil {
		t.Error("a line refunded more than it was bought left the shop")
	}
	f.order.Lines[0].QuantityRefunded = 0
	f.order.Status = "shipped"
	if _, err := ask(`{"order_id":"o1"}`); err == nil {
		t.Error("an unknown status left the shop")
	}
}

// fakeKeyDelivery delivers keys.
type fakeKeyDelivery struct{ items []DeliveredItem }

func (f *fakeKeyDelivery) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, nil
}
func (f *fakeKeyDelivery) HandleEvent(context.Context, string, []byte) error { return nil }
func (f *fakeKeyDelivery) Init(context.Context, *Core) (InitResult, error)   { return InitResult{}, nil }

func (f *fakeKeyDelivery) DescribeDeliverables(context.Context) (DeliverablesDescription, error) {
	return DeliverablesDescription{SKUs: []string{"pe-pro"}}, nil
}

func (f *fakeKeyDelivery) OrderDeliverables(context.Context, DeliverablesRequest) (DeliverablesResult, error) {
	return DeliverablesResult{Items: f.items}, nil
}

// A DELIVERED ITEM THE SHOP COULD NOT DRAW SAFELY NEVER LEAVES THE PLUGIN: a link that is not an address, a pending
// item that carries a value, a kind nobody draws.
func TestADeliveredItemIsCheckedBeforeItLeaves(t *testing.T) {
	ctx := context.Background()
	f := &fakeKeyDelivery{}
	sp := serviceProviderOf(f)
	if sp == nil {
		t.Fatal("a DeliverablesProvider is not found")
	}
	ask := func() error {
		_, ok, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopDeliverables, DeliverablesOrderItems), []byte(`{"order_id":"o1"}`))
		if !ok {
			t.Fatal("order.items was not dispatched")
		}
		return err
	}
	good := DeliveredItem{SKU: "pe-pro", Title: "Photo Editor Pro", Kind: DeliverableKey, Label: "Licence key", Value: "ABCDE-12345"}
	f.items = []DeliveredItem{good, {SKU: "pe-pro", Kind: DeliverableKey, Label: "Licence key", Pending: true, Note: "Coming soon."}}
	if err := ask(); err != nil {
		t.Fatalf("a key and a pending one: %v", err)
	}
	for name, it := range map[string]DeliveredItem{
		"a link that is no address": {SKU: "t", Kind: DeliverableLink, Label: "Ticket", Value: "javascript:alert(1)"},
		"pending with a value":      {SKU: "t", Kind: DeliverableKey, Label: "Key", Value: "X", Pending: true},
		"no value, not pending":     {SKU: "t", Kind: DeliverableKey, Label: "Key"},
		"an unknown kind":           {SKU: "t", Kind: "qr", Label: "Key", Value: "X"},
		"no label":                  {SKU: "t", Kind: DeliverableKey, Value: "X"},
		"a value too long":          {SKU: "t", Kind: DeliverableKey, Label: "Key", Value: strings.Repeat("x", 3000)},
	} {
		f.items = []DeliveredItem{good, it}
		if err := ask(); err == nil {
			t.Errorf("%s left the plugin", name)
		}
	}
}
