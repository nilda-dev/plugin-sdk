package nilda

import (
	"strings"
	"testing"
)

// coffee is mug with two purchase options besides the one-time purchase, priced per choice.
func coffee() CommerceProduct {
	p := mug()
	p.Plans = []CommercePlan{
		{ID: "sp-monthly", Name: "Every month, 10% off", Interval: IntervalMonth, IntervalCount: 1, Price: "€17.91",
			OldPrice: "€19.90", SubscriptionCycles: SubscriptionCycles{MinCycles: 3}},
		{ID: "sp-fortnight", Name: "Every 2 weeks, 15% off the first 3", Interval: IntervalWeek, IntervalCount: 2,
			Price: "€16.92", LaterPrice: "€19.90", SubscriptionCycles: SubscriptionCycles{DiscountCycles: 3, MaxCycles: 12},
			Cancel: "Cancel online in your account, or by email."},
	}
	for i := range p.Choices {
		p.Choices[i].Plans = []CommerceChoicePlan{{ID: "sp-monthly", Price: "€18.00"}}
	}
	p.Choices[3].Plans = append(p.Choices[3].Plans, CommerceChoicePlan{ID: "sp-fortnight", Price: "€18.60", LaterPrice: "€21.90"})
	return p
}

// A PRODUCT'S PLANS CROSS THE WIRE UNDER THE NAMES CORE READS — the terms inline, as plan.set writes them — and every
// rule that keeps the page's price the checkout's is held before the answer leaves.
func TestAProductsPlansHoldTheirRules(t *testing.T) {
	got, err := dispatchShop(t, oneProductShop{p: coffee()}, HookCommerceProduct, `{"id":"mug"}`)
	if err != nil {
		t.Fatalf("a product with plans within every rule was refused: %v", err)
	}
	for _, want := range []string{`"plans":[{"id":"sp-monthly","name":"Every month, 10% off","interval":"month",` +
		`"interval_count":1,"price":"€17.91","old_price":"€19.90","min_cycles":3}`,
		`"later_price":"€19.90","discount_cycles":3,"max_cycles":12,"cancel":"Cancel online in your account, or by email."`,
		`"plans":[{"id":"sp-monthly","price":"€18.00"},{"id":"sp-fortnight","price":"€18.60","later_price":"€21.90"}]`} {
		if !strings.Contains(got, want) {
			t.Errorf("the answer lacks %s:\n%s", want, got)
		}
	}

	bad := map[string]func(*CommerceProduct){
		"too many plans": func(p *CommerceProduct) {
			for len(p.Plans) <= MaxCommercePlans {
				pl := p.Plans[0]
				pl.ID += strings.Repeat("x", len(p.Plans))
				p.Plans = append(p.Plans, pl)
			}
		},
		"a pre-order with plans": func(p *CommerceProduct) { p.PreOrder = &CommercePreOrder{ShipsBy: "Ships by May"} },
		"no id":                  func(p *CommerceProduct) { p.Plans[0].ID = " " },
		"an id past its bound":   func(p *CommerceProduct) { p.Plans[0].ID = strings.Repeat("a", MaxCommercePlanID+1) },
		"an id twice":            func(p *CommerceProduct) { p.Plans[1].ID = p.Plans[0].ID },
		"no name":                func(p *CommerceProduct) { p.Plans[0].Name = "" },
		"a bidi override name":   func(p *CommerceProduct) { p.Plans[0].Name = "Every month \u202e" },
		"no rhythm":              func(p *CommerceProduct) { p.Plans[0].Interval = "fortnight" },
		"a zero interval count":  func(p *CommerceProduct) { p.Plans[0].IntervalCount = 0 },
		"terms past max":         func(p *CommerceProduct) { p.Plans[1].MinCycles = 13 },
		"no price":               func(p *CommerceProduct) { p.Plans[0].Price = "" },
		"a price on two lines":   func(p *CommerceProduct) { p.Plans[0].UnitPrice = "€1\n/ kg" },
		"a discount with no later price": func(p *CommerceProduct) {
			p.Plans[1].LaterPrice = ""
		},
		"a later price with no end to the discount": func(p *CommerceProduct) { p.Plans[0].LaterPrice = "€19.90" },
		"a long cancel": func(p *CommerceProduct) { p.Plans[0].Cancel = strings.Repeat("x", MaxCommerceNote+1) },
		"a choice naming a plan not offered": func(p *CommerceProduct) {
			p.Choices[0].Plans = append(p.Choices[0].Plans, CommerceChoicePlan{ID: "sp-yearly", Price: "€150"})
		},
		"a choice naming a plan twice": func(p *CommerceProduct) {
			p.Choices[0].Plans = append(p.Choices[0].Plans, p.Choices[0].Plans[0])
		},
		"a choice's plan with no price": func(p *CommerceProduct) { p.Choices[0].Plans[0].Price = "" },
		"a choice's discounted plan with no later price": func(p *CommerceProduct) {
			p.Choices[3].Plans[1].LaterPrice = ""
		},
		"choice plans with no product plans": func(p *CommerceProduct) { p.Plans = nil },
	}
	for name, breakIt := range bad {
		p := coffee()
		breakIt(&p)
		err := p.Validate()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), `product "mug"`) {
			t.Errorf("%s: the refusal does not name the product: %v", name, err)
		}
		if _, err := dispatchShop(t, oneProductShop{p: p}, HookCommerceProduct, `{"id":"mug"}`); err == nil {
			t.Errorf("%s: the answer left the plugin", name)
		}
	}

	// A product with plans and no choices is offered its plans at the product's prices.
	p := coffee()
	p.Variations, p.Choices = nil, nil
	if err := p.Validate(); err != nil {
		t.Errorf("plans on a product with no choices were refused: %v", err)
	}
	// And a choice may offer none of them: that combination is sold once only.
	p = coffee()
	p.Choices[2].Plans = nil
	if err := p.Validate(); err != nil {
		t.Errorf("a choice offering no plan was refused: %v", err)
	}
}
