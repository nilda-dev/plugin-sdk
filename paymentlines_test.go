package nilda

import (
	"strings"
	"testing"
)

// A basket of 84.90 €: two candles at 12.50, a vase at 49.90 with a 5.00 code off, delivery 4.90, VAT included.
func candleSession() PaymentSessionParams {
	return PaymentSessionParams{Gateway: "stripe", Method: "card", Reference: "NLD-1042", AmountMinor: 7480, Currency: "EUR",
		ReturnURL: "https://shop.example/thanks",
		Lines: []PaymentLine{
			{Name: "Candle", Quantity: 2, UnitAmountMinor: 1250, TotalMinor: 2500, TaxMinor: 399, SKU: "CANDLE-1",
				URL: "https://shop.example/p/candle", ImageURL: "https://shop.example/m/candle.jpg", Fulfilment: FulfilmentPhysical},
			{Name: "Vase", Quantity: 1, UnitAmountMinor: 4990, TotalMinor: 4990, TaxMinor: 797},
		},
		Breakdown: &PaymentBreakdown{ShippingMinor: 490, DiscountMinor: 500, TaxMinor: 1274, TaxIncluded: true}}
}

// THE LINES DESCRIBE THE AMOUNT EXACTLY (10.15): what a gateway shows on the processor's page adds up to what is charged,
// each line is its unit price times its quantity, and every text and count is within what Stripe and PayPal take.
func TestThePaymentLinesAddUpToTheAmount(t *testing.T) {
	if err := candleSession().Validate(); err != nil {
		t.Fatalf("the candle basket is refused: %v", err)
	}
	// Tax on top: 100.00 of goods, 19.00 tax, 5.00 delivery = 124.00.
	onTop := PaymentSessionParams{Gateway: "paypal", Method: "paypal", Reference: "US-1", AmountMinor: 12400, Currency: "USD",
		ReturnURL: "https://shop.example/thanks", Lines: []PaymentLine{{Name: "Lamp", Quantity: 4, UnitAmountMinor: 2500, TotalMinor: 10000, TaxMinor: 1900}},
		Breakdown: &PaymentBreakdown{ShippingMinor: 500, TaxMinor: 1900}}
	if err := onTop.Validate(); err != nil {
		t.Fatalf("a tax-on-top basket is refused: %v", err)
	}
	// None at all: a consumer built before sends neither.
	plain := candleSession()
	plain.Lines, plain.Breakdown = nil, nil
	if err := plain.Validate(); err != nil {
		t.Fatalf("a session without lines is refused: %v", err)
	}
	many := make([]PaymentLine, MaxPaymentLines+1)
	for i := range many {
		many[i] = PaymentLine{Name: "x", Quantity: 1, UnitAmountMinor: 1, TotalMinor: 1}
	}
	for name, tc := range map[string]struct {
		edit func(p *PaymentSessionParams)
		want string
	}{
		"the lines do not add up":       {func(p *PaymentSessionParams) { p.AmountMinor = 7481 }, "add up"},
		"tax added twice":               {func(p *PaymentSessionParams) { p.Breakdown.TaxIncluded = false }, "add up"},
		"a total that is not unit×qty":  {func(p *PaymentSessionParams) { p.Lines[0].TotalMinor = 2400; p.Breakdown.DiscountMinor = 400 }, "quantity"},
		"no breakdown":                  {func(p *PaymentSessionParams) { p.Breakdown = nil }, "breakdown"},
		"a breakdown without lines":     {func(p *PaymentSessionParams) { p.Lines = nil }, "breakdown"},
		"no name":                       {func(p *PaymentSessionParams) { p.Lines[0].Name = " " }, "name"},
		"a name of two lines":           {func(p *PaymentSessionParams) { p.Lines[0].Name = "Candle\nfree" }, "name"},
		"a long name":                   {func(p *PaymentSessionParams) { p.Lines[0].Name = strings.Repeat("ä", MaxPaymentLineName+1) }, "name"},
		"a NUL in a name":               {func(p *PaymentSessionParams) { p.Lines[0].Name = "Can\x00dle" }, "NUL"},
		"no quantity":                   {func(p *PaymentSessionParams) { p.Lines[0].Quantity = 0 }, "quantity"},
		"a huge quantity":               {func(p *PaymentSessionParams) { p.Lines[0].Quantity = MaxPaymentLineQuantity + 1 }, "quantity"},
		"a negative price":              {func(p *PaymentSessionParams) { p.Lines[1].UnitAmountMinor = -1 }, "negative"},
		"a negative delivery":           {func(p *PaymentSessionParams) { p.Breakdown.ShippingMinor = -490; p.Breakdown.DiscountMinor = -480 }, "negative"},
		"a line's tax past its total":   {func(p *PaymentSessionParams) { p.Lines[1].TaxMinor = 5000 }, "tax"},
		"the lines' tax past the total": {func(p *PaymentSessionParams) { p.Breakdown.TaxMinor = 1000 }, "tax"},
		"a long SKU":                    {func(p *PaymentSessionParams) { p.Lines[0].SKU = strings.Repeat("s", MaxPaymentLineSKU+1) }, "sku"},
		"a page that is no URL":         {func(p *PaymentSessionParams) { p.Lines[0].URL = "/p/candle" }, "url"},
		"a picture that is no URL":      {func(p *PaymentSessionParams) { p.Lines[0].ImageURL = "candle.jpg" }, "image_url"},
		"an unknown fulfilment":         {func(p *PaymentSessionParams) { p.Lines[0].Fulfilment = "pickup" }, "fulfilment"},
		"too many lines": {func(p *PaymentSessionParams) {
			p.Lines = many
			p.AmountMinor = int64(len(many))
			p.Breakdown = &PaymentBreakdown{}
		}, "at most"},
		"lines on a setup": {func(p *PaymentSessionParams) { p.AmountMinor = 0; p.Save = &PaymentMandate{} }, ""},
		"a total that overflows": {func(p *PaymentSessionParams) {
			p.Lines[0].UnitAmountMinor = 1 << 62
			p.Lines[0].Quantity = 4
			p.Lines[0].TotalMinor = 0
		}, ""},
	} {
		p := candleSession()
		tc.edit(&p)
		err := p.Validate()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refused for another reason: %v", name, err)
		}
	}
}
