package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// mug is a product with every storefront field of v0.58.0 set within its rules: two axes, four choices, facts.
func mug() CommerceProduct {
	facts := func(amount, gtin string) *CommerceFacts {
		return &CommerceFacts{Amount: amount, Currency: "EUR", Availability: SchemaInStock, Condition: SchemaNewCondition,
			GTIN: gtin, MPN: "MUG-1", Brand: "Kiln Co"}
	}
	choice := func(colour, size, sku, price string, inStock bool) CommerceChoice {
		return CommerceChoice{Selection: map[string]string{"colour": colour, "size": size}, SKU: sku, Price: "€" + price,
			OldPrice: "€25.00", UnitPrice: "€" + price + " / item", InStock: inStock, StockNote: "Only 2 left",
			MediaID: "0b8c7c3e-6a0e-4d3e-9a64-2f8a1f6a9c11", Facts: facts(price, "4006381333931")}
	}
	return CommerceProduct{
		ID: "mug", Title: "Mug", Price: "€19.90", PriceFrom: true, UnitPrice: "€19.90 / item", StockNote: "Only 3 left",
		InStock: true,
		Variations: []CommerceVariation{
			{Name: "Colour", Key: "colour", Options: []string{"Black", "White"}},
			{Name: "Size", Key: "size", Options: []string{"M", "L"}},
		},
		Choices: []CommerceChoice{
			choice("Black", "M", "MUG-BM", "19.90", true), choice("Black", "L", "MUG-BL", "21.90", true),
			choice("White", "M", "MUG-WM", "19.90", false), choice("White", "L", "MUG-WL", "21.90", true),
		},
		Facts: facts("19.90", "96385074"),
	}
}

// oneProductShop answers one product to every question that asks for products.
type oneProductShop struct {
	catalogueOnly
	p CommerceProduct
}

func (s oneProductShop) Products(context.Context, CommerceQuery) ([]CommerceProduct, error) {
	return []CommerceProduct{s.p}, nil
}
func (s oneProductShop) Product(context.Context, string) (CommerceProduct, bool) { return s.p, true }
func (s oneProductShop) ProductForContent(context.Context, string) (CommerceProduct, bool) {
	return s.p, true
}

// A PRODUCT'S STOREFRONT FIELDS CROSS THE WIRE UNDER THE NAMES CORE READS, and a product that breaks one of their rules
// fails the call and names itself — on the grid, the product page and the page a product is.
func TestAProductsStorefrontFieldsHoldTheirRules(t *testing.T) {
	got, err := dispatchShop(t, oneProductShop{p: mug()}, HookCommerceProduct, `{"id":"mug"}`)
	if err != nil {
		t.Fatalf("a product within every rule was refused: %v", err)
	}
	for _, want := range []string{`"price_from":true`, `"unit_price":"€19.90 / item"`, `"stock_note":"Only 3 left"`,
		`"choices":[{"selection":{"colour":"Black","size":"M"},"sku":"MUG-BM","price":"€19.90","old_price":"€25.00","unit_price":"€19.90 / item","in_stock":true,"stock_note":"Only 2 left","media_id":"0b8c7c3e-6a0e-4d3e-9a64-2f8a1f6a9c11","facts":{`,
		`"facts":{"amount":"19.90","currency":"EUR","availability":"InStock","condition":"NewCondition","gtin":"96385074","mpn":"MUG-1","brand":"Kiln Co"}`} {
		if !strings.Contains(got, want) {
			t.Errorf("the product's answer lacks %s:\n%s", want, got)
		}
	}

	bad := map[string]func(*CommerceProduct){
		"From with no price":               func(p *CommerceProduct) { p.Price = "" },
		"a unit price on two lines":        func(p *CommerceProduct) { p.UnitPrice = "€19.90\n/ item" },
		"a stock note past 200":            func(p *CommerceProduct) { p.StockNote = strings.Repeat("é", MaxCommerceNote+1) },
		"a bidi override in a stock note":  func(p *CommerceProduct) { p.StockNote = "Only \u202e3 left" },
		"choices with no variations":       func(p *CommerceProduct) { p.Variations = nil },
		"a choice missing an axis":         func(p *CommerceProduct) { delete(p.Choices[0].Selection, "size") },
		"a choice naming no axis":          func(p *CommerceProduct) { p.Choices[0].Selection["material"] = "Clay" },
		"a choice's value is no option":    func(p *CommerceProduct) { p.Choices[0].Selection["size"] = "XL" },
		"two choices of one combination":   func(p *CommerceProduct) { p.Choices[1].Selection = p.Choices[0].Selection },
		"two choices with one code":        func(p *CommerceProduct) { p.Choices[1].SKU = p.Choices[0].SKU },
		"a choice's code past 127":         func(p *CommerceProduct) { p.Choices[0].SKU = strings.Repeat("S", MaxPaymentLineSKU+1) },
		"a choice's price on two lines":    func(p *CommerceProduct) { p.Choices[0].Price = "€19\n.90" },
		"a choice's media id with a space": func(p *CommerceProduct) { p.Choices[0].MediaID = "media 1" },
		"a choice in another currency":     func(p *CommerceProduct) { p.Choices[0].Facts.Currency = "USD" },
		"a choice's facts break a rule":    func(p *CommerceProduct) { p.Choices[0].Facts.GTIN = "4006381333932" },
		"too many choices": func(p *CommerceProduct) {
			p.Variations = []CommerceVariation{{Key: "n", Name: "N"}}
			p.Choices = nil
			for i := 0; i <= MaxCommerceChoices; i++ {
				v := fmt.Sprint(i)
				p.Variations[0].Options = append(p.Variations[0].Options, v)
				p.Choices = append(p.Choices, CommerceChoice{Selection: map[string]string{"n": v}})
			}
		},
		"an amount with a comma":             func(p *CommerceProduct) { p.Facts.Amount = "19,90" },
		"an amount with a symbol":            func(p *CommerceProduct) { p.Facts.Amount = "€19.90" },
		"an amount past the currency's":      func(p *CommerceProduct) { p.Facts.Amount = "19.901" },
		"an amount with a space":             func(p *CommerceProduct) { p.Facts.Amount = " 19.90" },
		"a negative amount":                  func(p *CommerceProduct) { p.Facts.Amount = "-19.90" },
		"an amount with no currency":         func(p *CommerceProduct) { p.Facts.Currency = "" },
		"a currency with no amount":          func(p *CommerceProduct) { p.Facts.Amount = "" },
		"a currency in lower case":           func(p *CommerceProduct) { p.Facts.Currency = "eur" },
		"a currency ISO 4217 does not price": func(p *CommerceProduct) { p.Facts.Currency = "EUX" },
		"the catalogue's availability word":  func(p *CommerceProduct) { p.Facts.Availability = AvailabilityInStock },
		"a condition Google does not take":   func(p *CommerceProduct) { p.Facts.Condition = "DamagedCondition" },
		"a GTIN with a wrong check digit":    func(p *CommerceProduct) { p.Facts.GTIN = "96385075" },
		"a GTIN of eleven digits":            func(p *CommerceProduct) { p.Facts.GTIN = "03600029145" },
		"a GTIN with a dash":                 func(p *CommerceProduct) { p.Facts.GTIN = "4006381-333931" },
		"an MPN past 70":                     func(p *CommerceProduct) { p.Facts.MPN = strings.Repeat("M", MaxCommerceFactText+1) },
		"a brand with a newline":             func(p *CommerceProduct) { p.Facts.Brand = "Kiln\nCo" },
	}
	for name, edit := range bad {
		p := mug()
		// A fresh selection per choice, so an edit of one map does not reach the others.
		for i := range p.Choices {
			sel := map[string]string{}
			for k, v := range p.Choices[i].Selection {
				sel[k] = v
			}
			p.Choices[i].Selection = sel
		}
		edit(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: Validate passed it", name)
			continue
		}
		for _, hook := range []string{HookCommerceProduct, HookCommerceProducts, HookCommerceProductForContent} {
			if _, err := dispatchShop(t, oneProductShop{p: p}, hook, `{"id":"mug","content_id":"c1"}`); err == nil ||
				!strings.Contains(err.Error(), `"mug"`) {
				t.Errorf("%s: %s answered it (%v); the call must fail and name the product", name, hook, err)
			}
		}
	}

	// What is not set is not checked: a product of the fields before v0.58.0 passes as it always did.
	if err := (CommerceProduct{ID: "old", Title: "Old", Price: "£4"}).Validate(); err != nil {
		t.Errorf("a product with no storefront field: %v", err)
	}
	// A yen amount has no decimals, a dinar's three; an axis with no key is answered by its name.
	yen := CommerceProduct{ID: "y", Facts: &CommerceFacts{Amount: "1500", Currency: "JPY", GTIN: "036000291452"},
		Variations: []CommerceVariation{{Name: "Size", Options: []string{"S"}}},
		Choices:    []CommerceChoice{{Selection: map[string]string{"Size": "S"}, Facts: &CommerceFacts{Amount: "1500", Currency: "JPY"}}}}
	dinar := CommerceProduct{ID: "k", Facts: &CommerceFacts{Amount: "1.250", Currency: "KWD", GTIN: "00012345600012"}}
	for _, p := range []CommerceProduct{yen, dinar, {ID: "g14", Facts: &CommerceFacts{GTIN: "10012345678902"}}} {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", p.ID, err)
		}
	}
	if err := (CommerceProduct{ID: "y", Facts: &CommerceFacts{Amount: "1500.5", Currency: "JPY"}}).Validate(); err == nil {
		t.Error("a yen amount with a decimal passed")
	}
}

// FILTERS ARE BOUNDED, and a query whose filters break a bound is refused before the shop is asked; one within them
// reaches the shop's Products and CountProducts as it was sent.
func TestFiltersAreBoundedAndReachTheShop(t *testing.T) {
	ten, five := int64(10), int64(5)
	good := CommerceFilters{PriceMin: &five, PriceMax: &ten, Options: map[string][]string{"colour": {"Black", "White"}},
		Brands: []string{"kiln"}, MinRating: 4, InStock: true, Terms: []string{"mugs"}}
	if err := good.Validate(); err != nil {
		t.Fatalf("filters within every bound: %v", err)
	}
	many := func(n int) []string {
		var out []string
		for i := 0; i < n; i++ {
			out = append(out, fmt.Sprint("v", i))
		}
		return out
	}
	neg := int64(-1)
	for name, f := range map[string]CommerceFilters{
		"a negative minimum":         {PriceMin: &neg},
		"a minimum past the maximum": {PriceMin: &ten, PriceMax: &five},
		"nine axes": {Options: map[string][]string{"a": {"1"}, "b": {"1"}, "c": {"1"}, "d": {"1"}, "e": {"1"},
			"f": {"1"}, "g": {"1"}, "h": {"1"}, "i": {"1"}}},
		"an axis with no key":         {Options: map[string][]string{"": {"1"}}},
		"an axis with no value":       {Options: map[string][]string{"colour": {}}},
		"twenty-one colours":          {Options: map[string][]string{"colour": many(MaxCommerceFilterValues + 1)}},
		"twenty-one brands":           {Brands: many(MaxCommerceFilterValues + 1)},
		"twenty-one terms":            {Terms: many(MaxCommerceFilterValues + 1)},
		"an empty brand":              {Brands: []string{""}},
		"a term past 128 bytes":       {Terms: []string{strings.Repeat("t", MaxCommerceFilterValueBytes+1)}},
		"a value with a newline":      {Options: map[string][]string{"colour": {"Bla\nck"}}},
		"one brand twice":             {Brands: []string{"kiln", "kiln"}},
		"six stars":                   {MinRating: MaxCommerceRating + 1},
		"a negative rating":           {MinRating: -1},
		"an axis key past 128 bytes":  {Options: map[string][]string{strings.Repeat("k", MaxCommerceFilterValueBytes+1): {"1"}}},
		"one colour twice in an axis": {Options: map[string][]string{"colour": {"Black", "Black"}}},
	} {
		if err := f.Validate(); err == nil {
			t.Errorf("%s: Validate passed it", name)
		}
		payload, _ := json.Marshal(CommerceQuery{Filters: &f})
		for _, hook := range []string{HookCommerceProducts, HookCommerceCount, HookCommerceFacets} {
			if _, err := dispatchShop(t, filteredShop{}, hook, string(payload)); err == nil {
				t.Errorf("%s: %s took it", name, hook)
			}
		}
	}

	payload, _ := json.Marshal(CommerceQuery{Term: "mugs", Sort: "best-selling", Filters: &good})
	if got, err := dispatchShop(t, filteredShop{}, HookCommerceProducts, string(payload)); err != nil ||
		!strings.Contains(got, `"title":"best-selling Black/White kiln 4+ in stock mugs 5-10"`) {
		t.Errorf("the shop's list did not see the filters: %s %v", got, err)
	}
	if got, err := dispatchShop(t, filteredShop{}, HookCommerceCount, string(payload)); err != nil || got != `{"total":7}` {
		t.Errorf("the shop's count did not see the filters: %s %v", got, err)
	}
}

// filteredShop says back what filters it was asked with.
type filteredShop struct{ catalogueOnly }

func (filteredShop) Products(_ context.Context, q CommerceQuery) ([]CommerceProduct, error) {
	f := q.Filters
	if f == nil || f.PriceMin == nil || f.PriceMax == nil {
		return nil, errors.New("no filters with a price")
	}
	stock := ""
	if f.InStock {
		stock = " in stock"
	}
	title := fmt.Sprintf("%s %s %s %d+%s %s %d-%d", q.Sort, strings.Join(f.Options["colour"], "/"),
		strings.Join(f.Brands, ","), f.MinRating, stock, strings.Join(f.Terms, ","), *f.PriceMin, *f.PriceMax)
	return []CommerceProduct{{ID: "x", Title: title}}, nil
}

func (filteredShop) CountProducts(_ context.Context, q CommerceQuery) (int, bool) {
	if q.Filters == nil || !q.Filters.InStock {
		return 0, false
	}
	return 7, true
}

func (filteredShop) Facets(context.Context, CommerceQuery, []string) ([]CommerceFacet, error) {
	return nil, nil
}

// facetedShop answers the facets it is asked for, from what it was given.
type facetedShop struct {
	catalogueOnly
	answer func(q CommerceQuery, want []string) []CommerceFacet
	err    error
}

func (s facetedShop) Facets(_ context.Context, q CommerceQuery, want []string) ([]CommerceFacet, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.answer(q, want), nil
}

func colourFacets(q CommerceQuery, want []string) []CommerceFacet {
	var out []CommerceFacet
	for _, k := range want {
		switch k {
		case "price":
			out = append(out, CommerceFacet{Key: k, Kind: CommerceFacetPrice, Label: "Price", Min: 5, Max: 40})
		case "option:colour":
			black := 3
			if q.Filters != nil && q.Filters.InStock {
				black = 2
			}
			out = append(out, CommerceFacet{Key: k, Kind: CommerceFacetOption, Label: "Colour",
				Options: []CommerceFacetValue{{Value: "Black", Count: black}, {Value: "White", Label: "Weiß", Count: 0}}})
		case "rating":
			out = append(out, CommerceFacet{Key: k, Kind: CommerceFacetRating, Label: "Rating",
				Options: []CommerceFacetValue{{Value: "4", Count: 2}}})
		case "stock":
			out = append(out, CommerceFacet{Key: k, Kind: CommerceFacetStock, Label: "Availability",
				Options: []CommerceFacetValue{{Value: CommerceFacetInStock, Label: "In stock", Count: 2}}})
		}
	}
	return out
}

// A SHOP'S FACETS: asked with the query and the keys the widget wants, answered as the shop counts them; a shop without
// facets answers none and is not subscribed; an answer Core could not draw, or a want Core never sends, fails the call.
func TestAShopAnswersTheFacetsItIsAsked(t *testing.T) {
	shop := facetedShop{answer: colourFacets}
	got, err := dispatchShop(t, shop, HookCommerceFacets,
		`{"term":"mugs","filters":{"in_stock":true},"want":["option:colour","price","brand","rating","stock"]}`)
	if err != nil {
		t.Fatalf("facets: %v", err)
	}
	var ans struct {
		Facets []CommerceFacet `json:"facets"`
	}
	if err := json.Unmarshal([]byte(got), &ans); err != nil || len(ans.Facets) != 4 ||
		ans.Facets[0].Key != "option:colour" || ans.Facets[0].Options[0].Count != 2 || ans.Facets[1].Max != 40 ||
		!strings.Contains(got, `{"value":"White","label":"Weiß","count":0}`) {
		t.Fatalf("the shop's facets did not come back as it answered them: %s (%v)", got, err)
	}
	if got, err := dispatchShop(t, catalogueOnly{}, HookCommerceFacets, `{"want":["price"]}`); err != nil || got != `{"facets":[]}` {
		t.Errorf("a shop without facets: %s %v", got, err)
	}
	if got, err := dispatchShop(t, facetedShop{answer: func(CommerceQuery, []string) []CommerceFacet { return nil }},
		HookCommerceFacets, `{"want":["price"]}`); err != nil || got != `{"facets":[]}` {
		t.Errorf("a shop with nothing to offer: %s %v", got, err)
	}
	if _, err := dispatchShop(t, facetedShop{err: errors.New("index down")}, HookCommerceFacets, `{"want":["price"]}`); err == nil {
		t.Error("a shop's error was not the call's")
	}
	subscribed := func(c Commerce) bool {
		res, err := (&commerceOnlyHandler{c: c}).Init(context.Background(), &Core{PluginKey: "shop"})
		if err != nil {
			t.Fatal(err)
		}
		return slices.Contains(res.Hooks, HookCommerceFacets)
	}
	if !subscribed(shop) || subscribed(catalogueOnly{}) {
		t.Error("commerce.facets is subscribed by the wrong shops (ServeCommerce)")
	}
	if !slices.Contains(withProvidedHooks(facetedHandler{}, nil), HookCommerceFacets) ||
		slices.Contains(withProvidedHooks(commerceOnly{}, nil), HookCommerceFacets) {
		t.Error("commerce.facets is subscribed by the wrong shops (Serve)")
	}

	for name, want := range map[string]string{
		"a key that is no facet":      `["colour"]`,
		"an option with no axis":      `["option:"]`,
		"a key asked twice":           `["price","price"]`,
		"an axis past 128 bytes":      `["option:` + strings.Repeat("k", MaxCommerceFilterValueBytes+1) + `"]`,
		"more than sixteen":           `["option:a","option:b","option:c","option:d","option:e","option:f","option:g","option:h","option:i","option:j","option:k","option:l","option:m","option:n","option:o","option:p","option:q"]`,
		"an axis with a control char": "[\"option:co\\u0007lour\"]",
	} {
		if _, err := dispatchShop(t, shop, HookCommerceFacets, `{"want":`+want+`}`); err == nil {
			t.Errorf("%s: the want was taken", name)
		}
	}

	wrong := map[string]func([]CommerceFacet) []CommerceFacet{
		"a facet nobody asked for": func(f []CommerceFacet) []CommerceFacet {
			return append(f, CommerceFacet{Key: "brand", Kind: CommerceFacetBrand, Label: "Brand"})
		},
		"one facet twice":          func(f []CommerceFacet) []CommerceFacet { return append(f, f[0]) },
		"a kind not its key's":     func(f []CommerceFacet) []CommerceFacet { f[0].Kind = CommerceFacetBrand; return f },
		"a facet with no label":    func(f []CommerceFacet) []CommerceFacet { f[0].Label = " "; return f },
		"a price with options":     func(f []CommerceFacet) []CommerceFacet { f[1].Options = f[0].Options; return f },
		"a price's min past max":   func(f []CommerceFacet) []CommerceFacet { f[1].Min = 50; return f },
		"a negative price bound":   func(f []CommerceFacet) []CommerceFacet { f[1].Min = -1; return f },
		"an option with a bound":   func(f []CommerceFacet) []CommerceFacet { f[0].Max = 3; return f },
		"a negative count":         func(f []CommerceFacet) []CommerceFacet { f[0].Options[0].Count = -1; return f },
		"one value twice":          func(f []CommerceFacet) []CommerceFacet { f[0].Options[1].Value = "Black"; return f },
		"an empty value":           func(f []CommerceFacet) []CommerceFacet { f[0].Options[0].Value = ""; return f },
		"a label on two lines":     func(f []CommerceFacet) []CommerceFacet { f[0].Options[0].Label = "Bl\nack"; return f },
		"a rating of six stars":    func(f []CommerceFacet) []CommerceFacet { f[2].Options[0].Value = "6"; return f },
		"a stock value of its own": func(f []CommerceFacet) []CommerceFacet { f[3].Options[0].Value = "yes"; return f },
		"sixty-one options": func(f []CommerceFacet) []CommerceFacet {
			f[0].Options = nil
			for i := 0; i <= MaxCommerceFacetOptions; i++ {
				f[0].Options = append(f[0].Options, CommerceFacetValue{Value: fmt.Sprint(i)})
			}
			return f
		},
	}
	for name, edit := range wrong {
		bad := facetedShop{answer: func(q CommerceQuery, want []string) []CommerceFacet { return edit(colourFacets(q, want)) }}
		if _, err := dispatchShop(t, bad, HookCommerceFacets, `{"want":["option:colour","price","rating","stock"]}`); err == nil {
			t.Errorf("%s: the answer was passed on", name)
		}
	}
}

// facetedHandler is a Handler that is a shop with facets, for Serve's subscriptions.
type facetedHandler struct{ commerceOnly }

func (facetedHandler) Facets(context.Context, CommerceQuery, []string) ([]CommerceFacet, error) {
	return nil, nil
}

// PAYMENT MARKS ARE A CLOSED LIST: a gateway's method names only marks Core can draw, none twice; the shop's strip the
// same; and the marks travel under the names Core reads.
func TestPaymentMarksAreAClosedList(t *testing.T) {
	for _, m := range PaymentMarks() {
		if !ValidPaymentMark(m) {
			t.Errorf("%q is in PaymentMarks and not valid", m)
		}
	}
	if len(PaymentMarks()) != 14 {
		t.Errorf("PaymentMarks lists %d marks; the list is the fourteen of commerce plan ST15", len(PaymentMarks()))
	}
	for _, m := range []string{"", "Visa", "applepay", "diners", "visa "} {
		if ValidPaymentMark(m) {
			t.Errorf("%q passed as a mark", m)
		}
	}
	card := PaymentMethod{Key: "card", Label: "Card", Marks: []string{PaymentMarkVisa, PaymentMarkMastercard}}
	if err := validMethods([]PaymentMethod{card}); err != nil {
		t.Fatalf("a card method with two marks: %v", err)
	}
	if b, _ := json.Marshal(card); !strings.Contains(string(b), `"marks":["visa","mastercard"]`) {
		t.Errorf("a method's marks on the wire: %s", b)
	}
	for name, marks := range map[string][]string{"a mark Core cannot draw": {"diners"}, "one mark twice": {"visa", "visa"}} {
		m := card
		m.Marks = marks
		if err := validMethods([]PaymentMethod{m}); err == nil {
			t.Errorf("%s: payment.describe passed it", name)
		}
	}
	shop := marksShop{marks: []string{PaymentMarkVisa, PaymentMarkBankTransfer, PaymentMarkCashOnDelivery}}
	if got, err := dispatchShop(t, shop, HookCommerceEndpoints, `{}`); err != nil ||
		!strings.Contains(got, `"payment_marks":["visa","bank-transfer","cash-on-delivery"]`) {
		t.Errorf("the shop's strip: %s %v", got, err)
	}
	for _, marks := range [][]string{{"bank_transfer"}, {"visa", "visa"}} {
		if _, err := dispatchShop(t, marksShop{marks: marks}, HookCommerceEndpoints, `{}`); err == nil {
			t.Errorf("the shop's strip %v was passed on", marks)
		}
	}
}

type marksShop struct {
	catalogueOnly
	marks []string
}

func (s marksShop) Endpoints(ctx context.Context) CommerceEndpoints {
	e := s.catalogueOnly.Endpoints(ctx)
	e.PaymentMarks = s.marks
	return e
}

func TestChoicesCompleteNeedsChoices(t *testing.T) {
	p := mug()
	p.ChoicesComplete = true
	if err := p.Validate(); err != nil {
		t.Errorf("complete with choices: %v", err)
	}
	p.Choices = nil
	if err := p.Validate(); err == nil {
		t.Error("complete with no choices must be refused")
	}
	p.ChoicesComplete = false
	if err := p.Validate(); err != nil {
		t.Errorf("neither: %v", err)
	}
}
