package nilda

import (
	"encoding/json"
	"strings"
	"testing"
)

// THE BASKET DOOR (v0.75.0): CommerceEndpoints.Basket is a GET answering the visitor's basket as lines of {sku, qty}.
func TestCommerceEndpointsCarryTheBasket(t *testing.T) {
	b, err := json.Marshal(CommerceEndpoints{Basket: "/shop/api/cart"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"basket":"/shop/api/cart"}` {
		t.Errorf("marshalled %s, want the basket door under \"basket\"", b)
	}
	if b, _ := json.Marshal(CommerceEndpoints{}); string(b) != `{}` {
		t.Errorf("a shop with no basket door marshals %s, want {}", b)
	}
}

// A BASKET ANSWER IS THE SHOP'S OWN BOUNDS: at most MaxBasketLines lines, a plain SKU, 1 to MaxBasketLineQty of it. A SKU
// may stand on two lines (a one-time line and a subscription line of the same variant), and an empty basket is an answer.
func TestABasketAnswerIsBounded(t *testing.T) {
	for name, good := range map[string]BasketAnswer{
		"empty":     {Lines: []BasketLine{}},
		"nil":       {},
		"two":       {Lines: []BasketLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: MaxBasketLineQty}}},
		"sku twice": {Lines: []BasketLine{{SKU: "BEANS", Qty: 1}, {SKU: "BEANS", Qty: 2}}},
	} {
		if err := good.Validate(); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	full := BasketAnswer{}
	for i := 0; i <= MaxBasketLines; i++ {
		full.Lines = append(full.Lines, BasketLine{SKU: "S" + strings.Repeat("x", i), Qty: 1})
	}
	for name, bad := range map[string]BasketAnswer{
		"101 lines":  full,
		"no sku":     {Lines: []BasketLine{{SKU: "", Qty: 1}}},
		"spaced sku": {Lines: []BasketLine{{SKU: "MUG ", Qty: 1}}},
		"long sku":   {Lines: []BasketLine{{SKU: strings.Repeat("s", MaxPaymentLineSKU+1), Qty: 1}}},
		"control":    {Lines: []BasketLine{{SKU: "MU\x1bG", Qty: 1}}},
		"markup":     {Lines: []BasketLine{{SKU: "MU‮G", Qty: 1}}},
		"zero qty":   {Lines: []BasketLine{{SKU: "MUG", Qty: 0}}},
		"too many":   {Lines: []BasketLine{{SKU: "MUG", Qty: MaxBasketLineQty + 1}}},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// A WIDGET'S SCRIPT READS line.sku AND line.qty, AND JAVASCRIPT IS CASE-SENSITIVE: DecodeBasketAnswer takes the keys as
// spelled, so a shop answering Go's default "SKU"/"Qty" fails here rather than in every visitor's browser. Keys a shop
// adds beside them (a title, a price) are the shop's and are ignored.
func TestDecodeBasketAnswerHoldsTheKeysAsABrowserReadsThem(t *testing.T) {
	a, err := DecodeBasketAnswer(strings.NewReader(
		`{"lines":[{"sku":"COAT-M","qty":1,"Title":"Coat"},{"sku":"SCARF","qty":2}],"subtotal_minor":26000}`))
	if err != nil || len(a.Lines) != 2 || a.Lines[1].SKU != "SCARF" || a.Lines[1].Qty != 2 {
		t.Fatalf("decoded %+v, %v", a, err)
	}
	if a, err := DecodeBasketAnswer(strings.NewReader(`{"lines":[]}`)); err != nil || len(a.Lines) != 0 {
		t.Fatalf("an empty basket: %+v, %v", a, err)
	}
	for name, body := range map[string]string{
		"Go's default keys": `{"lines":[{"SKU":"COAT-M","Qty":1}]}`,
		"Lines":             `{"Lines":[{"sku":"COAT-M","qty":1}]}`,
		"no lines":          `{}`,
		"null lines":        `{"lines":null}`,
		"no qty":            `{"lines":[{"sku":"COAT-M"}]}`,
		"string qty":        `{"lines":[{"sku":"COAT-M","qty":"1"}]}`,
		"fractional qty":    `{"lines":[{"sku":"COAT-M","qty":1.5}]}`,
		"a bad line":        `{"lines":[{"sku":"COAT-M","qty":0}]}`,
		"not an object":     `[]`,
		"trailing":          `{"lines":[]}{}`,
		"too long":          `{"lines":[],"pad":"` + strings.Repeat("x", MaxBasketBody) + `"}`,
	} {
		if _, err := DecodeBasketAnswer(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// WHETHER THE BASKET HOLDS ONLY A LOOK: a widget shows the look's saving only then (LookQuoteAnswer's rule). Every line
// is one of the look's SKUs (letter case aside) and each SKU's units, over its lines, are the look's.
func TestABasketHoldsOnlyALook(t *testing.T) {
	look := LookQuoteRequest{Lines: []LookQuoteLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 2}}}
	for name, c := range map[string]struct {
		lines []BasketLine
		want  bool
	}{
		"exactly":         {[]BasketLine{{SKU: "scarf", Qty: 2}, {SKU: "COAT-M", Qty: 1}}, true},
		"split over two":  {[]BasketLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 1}, {SKU: "SCARF", Qty: 1}}, true},
		"empty":           {nil, false},
		"a piece missing": {[]BasketLine{{SKU: "COAT-M", Qty: 1}}, false},
		"one more":        {[]BasketLine{{SKU: "COAT-M", Qty: 2}, {SKU: "SCARF", Qty: 2}}, false},
		"something else":  {[]BasketLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 2}, {SKU: "MUG", Qty: 1}}, false},
	} {
		if got := (BasketAnswer{Lines: c.lines}).HoldsOnly(look); got != c.want {
			t.Errorf("%s: HoldsOnly = %v, want %v", name, got, c.want)
		}
	}
}
