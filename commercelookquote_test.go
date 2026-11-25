package nilda

import (
	"encoding/json"
	"strings"
	"testing"
)

func goodLookQuote() (LookQuoteRequest, LookQuoteAnswer) {
	req := LookQuoteRequest{Lines: []LookQuoteLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 2}}}
	ans := LookQuoteAnswer{Currency: "EUR", PricesIncludeTax: true,
		Lines: []LookQuoteAnswerLine{
			{SKU: "COAT-M", Qty: 1, UnitMinor: 20000, LineMinor: 20000, Availability: AvailabilityInStock},
			{SKU: "scarf", Qty: 2, UnitMinor: 3000, LineMinor: 6000, Availability: AvailabilityShort},
		},
		SubtotalMinor: 26000,
		Discounts:     []LookQuoteDiscount{{Label: "Winter set −10%", AmountMinor: 2600}},
		TotalMinor:    23400,
	}
	return req, ans
}

// A LOOK QUOTE'S REQUEST IS THE SHOP'S DOOR'S: 1 to 12 lines, 1 to 10 of each, a SKU a payment line carries, none twice.
func TestALookQuoteRequestIsBounded(t *testing.T) {
	req, _ := goodLookQuote()
	if err := req.Validate(); err != nil {
		t.Fatalf("a good request refused: %v", err)
	}
	many := LookQuoteRequest{}
	for i := 0; i <= MaxLookQuoteLines; i++ {
		many.Lines = append(many.Lines, LookQuoteLine{SKU: strings.Repeat("x", i+1), Qty: 1})
	}
	for name, bad := range map[string]LookQuoteRequest{
		"no lines":     {},
		"13 lines":     many,
		"no sku":       {Lines: []LookQuoteLine{{SKU: " ", Qty: 1}}},
		"spaced sku":   {Lines: []LookQuoteLine{{SKU: " MUG", Qty: 1}}},
		"long sku":     {Lines: []LookQuoteLine{{SKU: strings.Repeat("s", MaxPaymentLineSKU+1), Qty: 1}}},
		"control":      {Lines: []LookQuoteLine{{SKU: "MU\nG", Qty: 1}}},
		"zero qty":     {Lines: []LookQuoteLine{{SKU: "MUG", Qty: 0}}},
		"11 of one":    {Lines: []LookQuoteLine{{SKU: "MUG", Qty: MaxLookQuoteQty + 1}}},
		"sku twice":    {Lines: []LookQuoteLine{{SKU: "MUG", Qty: 1}, {SKU: "mug", Qty: 1}}},
		"negative qty": {Lines: []LookQuoteLine{{SKU: "MUG", Qty: -1}}},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// THE SHOP READS THE BODY STRICTLY, as its own door does: at most MaxLookQuoteBody, no field it does not know, nothing
// after the object; a SKU is trimmed.
func TestDecodeLookQuoteRequestIsStrict(t *testing.T) {
	r, err := DecodeLookQuoteRequest(strings.NewReader(`{"lines":[{"sku":" MUG ","qty":2}]}`))
	if err != nil || len(r.Lines) != 1 || r.Lines[0].SKU != "MUG" || r.Lines[0].Qty != 2 {
		t.Fatalf("decoded %+v, %v", r, err)
	}
	for name, body := range map[string]string{
		"unknown field": `{"lines":[{"sku":"MUG","qty":1,"price":1}]}`,
		"trailing":      `{"lines":[{"sku":"MUG","qty":1}]}{}`,
		"not json":      `lines=MUG`,
		"empty":         `{"lines":[]}`,
		"too big":       `{"lines":[{"sku":"` + strings.Repeat("a", MaxLookQuoteBody) + `","qty":1}]}`,
	} {
		if _, err := DecodeLookQuoteRequest(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// AN ANSWER IS ONLY WHAT A CALLER MAY DRAW: an ISO 4217 currency, the SDK's availability words, non-negative figures that
// add up (subtotal = the lines, total = subtotal − the discounts), plain-text labels.
func TestALookQuoteAnswerAddsUp(t *testing.T) {
	req, good := goodLookQuote()
	if err := good.Validate(); err != nil {
		t.Fatalf("a good answer refused: %v", err)
	}
	if err := good.ValidateFor(req); err != nil {
		t.Fatalf("a good answer refused for its request: %v", err)
	}
	mutate := func(f func(a *LookQuoteAnswer)) LookQuoteAnswer {
		_, a := goodLookQuote()
		f(&a)
		return a
	}
	for name, bad := range map[string]LookQuoteAnswer{
		"no currency":      mutate(func(a *LookQuoteAnswer) { a.Currency = "" }),
		"not iso 4217":     mutate(func(a *LookQuoteAnswer) { a.Currency = "eur" }),
		"unknown currency": mutate(func(a *LookQuoteAnswer) { a.Currency = "XYZ" }),
		"no lines":         mutate(func(a *LookQuoteAnswer) { a.Lines, a.SubtotalMinor, a.Discounts, a.TotalMinor = nil, 0, nil, 0 }),
		"availability":     mutate(func(a *LookQuoteAnswer) { a.Lines[0].Availability = "soon" }),
		"no availability":  mutate(func(a *LookQuoteAnswer) { a.Lines[0].Availability = "" }),
		"negative unit":    mutate(func(a *LookQuoteAnswer) { a.Lines[0].UnitMinor = -1 }),
		"negative line": mutate(func(a *LookQuoteAnswer) {
			a.Lines[0].LineMinor, a.SubtotalMinor, a.TotalMinor = -1, 5999, 3399
		}),
		"subtotal off":      mutate(func(a *LookQuoteAnswer) { a.SubtotalMinor++ }),
		"total off":         mutate(func(a *LookQuoteAnswer) { a.TotalMinor-- }),
		"negative discount": mutate(func(a *LookQuoteAnswer) { a.Discounts[0].AmountMinor, a.TotalMinor = -1, 26001 }),
		"negative total":    mutate(func(a *LookQuoteAnswer) { a.Discounts[0].AmountMinor, a.TotalMinor = 26001, -1 }),
		"no label":          mutate(func(a *LookQuoteAnswer) { a.Discounts[0].Label = " " }),
		"bidi label":        mutate(func(a *LookQuoteAnswer) { a.Discounts[0].Label = "Sale‮" }),
		"long label": mutate(func(a *LookQuoteAnswer) {
			a.Discounts[0].Label = strings.Repeat("é", MaxLookQuoteLabel+1)
		}),
		"sku twice": mutate(func(a *LookQuoteAnswer) { a.Lines[1].SKU = "coat-m" }),
		"qty":       mutate(func(a *LookQuoteAnswer) { a.Lines[0].Qty = 0 }),
		"overflow": mutate(func(a *LookQuoteAnswer) {
			a.Lines[0].LineMinor, a.Lines[1].LineMinor = 1<<62, 1<<62
			a.SubtotalMinor, a.Discounts, a.TotalMinor = -(1 << 63), nil, -(1 << 63)
		}),
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	tooMany := mutate(func(a *LookQuoteAnswer) {
		for i := 0; i <= MaxLookQuoteDiscounts; i++ {
			a.Discounts = append(a.Discounts, LookQuoteDiscount{Label: "x"})
		}
	})
	if tooMany.Validate() == nil {
		t.Error("an answer past MaxLookQuoteDiscounts accepted")
	}

	// ValidateFor: the answer prices exactly the lines asked, each once, at the quantity asked.
	for name, r := range map[string]LookQuoteRequest{
		"a line not answered": {Lines: []LookQuoteLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 2}, {SKU: "HAT", Qty: 1}}},
		"a line not asked":    {Lines: []LookQuoteLine{{SKU: "COAT-M", Qty: 1}}},
		"another quantity":    {Lines: []LookQuoteLine{{SKU: "COAT-M", Qty: 1}, {SKU: "SCARF", Qty: 3}}},
	} {
		if good.ValidateFor(r) == nil {
			t.Errorf("ValidateFor %s: accepted", name)
		}
	}
}

// A REFUSAL NAMES ONE OF THE DOOR'S CODES, and the SKU it is about when there is one.
func TestALookQuoteRefusalIsOneOfTheCodes(t *testing.T) {
	for _, c := range []string{LookQuoteUnavailable, LookQuoteChoiceRequired, LookQuoteMixedCurrency, LookQuoteBadRequest,
		LookQuoteTooManyRequests, LookQuoteError} {
		if err := (LookQuoteRefusal{Code: c}).Validate(); err != nil {
			t.Errorf("%s refused: %v", c, err)
		}
	}
	if err := (LookQuoteRefusal{Code: LookQuoteUnavailable, SKU: "MUG"}).Validate(); err != nil {
		t.Errorf("a refusal naming its SKU refused: %v", err)
	}
	for name, bad := range map[string]LookQuoteRefusal{
		"no code":    {},
		"other code": {Code: "sold_out"},
		"long sku":   {Code: LookQuoteUnavailable, SKU: strings.Repeat("s", MaxPaymentLineSKU+1)},
		"control":    {Code: LookQuoteUnavailable, SKU: "a\x00b"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (LookQuoteRefusal{Code: LookQuoteUnavailable}).Status() != 409 || (LookQuoteRefusal{Code: LookQuoteBadRequest}).Status() != 400 ||
		(LookQuoteRefusal{Code: LookQuoteTooManyRequests}).Status() != 429 || (LookQuoteRefusal{Code: LookQuoteError}).Status() != 500 {
		t.Error("a refusal's status is not the door's")
	}
}

// THE WIRE NAMES: the shop's answer, the endpoint's key, and the shop's doors on a widget's render request.
func TestTheLookQuoteWireNames(t *testing.T) {
	_, a := goodLookQuote()
	b, _ := json.Marshal(a)
	for _, k := range []string{`"currency":"EUR"`, `"prices_include_tax":true`, `"lines":[{"sku":"COAT-M","qty":1,"unit_minor":20000,` +
		`"line_minor":20000,"availability":"in_stock"}`, `"subtotal_minor":26000`, `"discounts":[{"label":"Winter set −10%","amount_minor":2600}]`,
		`"total_minor":23400`, `"availability":"short"`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("the answer %s lacks %s", b, k)
		}
	}
	b, _ = json.Marshal(CommerceEndpoints{LookQuote: "/shop/look/quote"})
	if string(b) != `{"look_quote":"/shop/look/quote"}` {
		t.Errorf("endpoints = %s", b)
	}
	b, _ = json.Marshal(LookQuoteRefusal{Code: LookQuoteUnavailable, SKU: "MUG"})
	if string(b) != `{"code":"unavailable","sku":"MUG"}` {
		t.Errorf("refusal = %s", b)
	}
}

// A WIDGET LEARNS THE SHOP'S DOORS FROM CORE (shop, the active shop's endpoints as Core kept them), and nothing when no
// shop is active — so a widget from another plugin calls the shop without hard-coding a private path.
func TestAWidgetRenderCarriesTheShopsEndpoints(t *testing.T) {
	var req WidgetRenderRequest
	raw := `{"type":"look","kind":"single","shop":{"cart":"/shop/cart","add_to_cart":"/shop/cart/add","look_quote":"/shop/look/quote"}}`
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatal(err)
	}
	if req.Shop == nil || req.Shop.LookQuote != "/shop/look/quote" || req.Shop.AddToCart != "/shop/cart/add" || req.Shop.Cart != "/shop/cart" {
		t.Fatalf("shop = %+v", req.Shop)
	}
	b, _ := json.Marshal(WidgetRenderRequest{Type: "look", Kind: "home"})
	if strings.Contains(string(b), "shop") {
		t.Errorf("a render with no shop says one: %s", b)
	}
}
