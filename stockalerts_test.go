package nilda

import (
	"encoding/json"
	"testing"
)

// The form's request is checked as the shop needs it, and an answer carries one of the three statuses the form reads.
func TestAStockAlertRequestIsCheckedAsTheShopNeedsIt(t *testing.T) {
	parse := func(in string) StockAlertRequest {
		var r StockAlertRequest
		if err := json.Unmarshal([]byte(in), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if err := parse(`{"sku":"mug-blue","title":"Mug — Blue","url":"https://kari.example/shop/mug","email":"eva@example.com"}`).Validate(); err != nil {
		t.Fatalf("a good request: %v", err)
	}
	for name, in := range map[string]string{
		"not an email":   `{"sku":"a","title":"A","url":"https://kari.example/a","email":"eva"}`,
		"a display name": `{"sku":"a","title":"A","url":"https://kari.example/a","email":"Eva <eva@example.com>"}`,
		"no domain dot":  `{"sku":"a","title":"A","url":"https://kari.example/a","email":"eva@localhost"}`,
		"no page":        `{"sku":"a","title":"A","url":"/a","email":"eva@example.com"}`,
		"a blank sku":    `{"sku":" ","title":"A","url":"https://kari.example/a","email":"eva@example.com"}`,
	} {
		if parse(in).Validate() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	for _, s := range []string{StockAlertSubscribed, StockAlertAlready, StockAlertRefused} {
		if (StockAlertResult{Status: s}).Validate() != nil {
			t.Errorf("status %q was refused", s)
		}
	}
	if (StockAlertResult{Status: "maybe"}).Validate() == nil {
		t.Error("an unknown status was accepted")
	}
}
