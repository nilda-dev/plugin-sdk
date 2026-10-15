package nilda

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeAlerts keeps requests in memory.
type fakeAlerts struct{ kept []StockAlertRequest }

func (f *fakeAlerts) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f *fakeAlerts) HandleEvent(context.Context, string, []byte) error          { return nil }
func (f *fakeAlerts) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }

func (f *fakeAlerts) DescribeStockAlerts(context.Context) (StockAlertsDescription, error) {
	return StockAlertsDescription{Collecting: true}, nil
}

func (f *fakeAlerts) SubscribeStockAlert(_ context.Context, req StockAlertRequest) (StockAlertResult, error) {
	for _, k := range f.kept {
		if k.SKU == req.SKU && k.Email == req.Email {
			return StockAlertResult{Status: StockAlertAlready}, nil
		}
	}
	f.kept = append(f.kept, req)
	return StockAlertResult{Status: StockAlertSubscribed}, nil
}

// THE PLUGIN IS FOUND BY ITSELF, and a request the checks refuse comes back refused with the reason a shopper reads —
// the plugin never sees it.
func TestServeAnswersAPluginThatKeepsStockAlerts(t *testing.T) {
	ctx := context.Background()
	f := &fakeAlerts{}
	sp := serviceProviderOf(f)
	if sp == nil {
		t.Fatal("a StockAlertProvider is not found")
	}
	ask := func(in string) StockAlertResult {
		out, ok, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractStockAlerts, StockAlertSubscribe), []byte(in))
		if err != nil || !ok {
			t.Fatalf("subscribe %s: %v %v", in, ok, err)
		}
		var r StockAlertResult
		_ = json.Unmarshal(out, &r)
		return r
	}
	good := `{"sku":"mug-blue","title":"Mug — Blue","url":"https://kari.example/shop/mug","email":"eva@example.com"}`
	if r := ask(good); r.Status != StockAlertSubscribed {
		t.Fatalf("a request: %+v", r)
	}
	if r := ask(good); r.Status != StockAlertAlready {
		t.Errorf("the same request again: %+v", r)
	}
	for name, in := range map[string]string{
		"not an email":   `{"sku":"a","title":"A","url":"https://kari.example/a","email":"eva"}`,
		"a display name": `{"sku":"a","title":"A","url":"https://kari.example/a","email":"Eva <eva@example.com>"}`,
		"no domain dot":  `{"sku":"a","title":"A","url":"https://kari.example/a","email":"eva@localhost"}`,
		"no page":        `{"sku":"a","title":"A","url":"/a","email":"eva@example.com"}`,
		"a blank sku":    `{"sku":" ","title":"A","url":"https://kari.example/a","email":"eva@example.com"}`,
	} {
		if r := ask(in); r.Status != StockAlertRefused || r.Message == "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if len(f.kept) != 1 {
		t.Errorf("the plugin saw %d requests, want the one good one", len(f.kept))
	}
	if (StockAlertResult{Status: "maybe"}).Validate() == nil {
		t.Error("an unknown status was accepted")
	}
}
