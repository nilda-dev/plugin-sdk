package nildatest

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// item is one product of the mug shop, as its catalogue keeps it.
type item struct {
	sku, colour, brand, term string
	rating                   int
	inStock                  bool
}

// mugShop counts its facets as a shop must: each facet's counts under every filter except its own. conjunctive counts
// them under its own selection too — the mistake that makes a second colour read zero.
type mugShop struct {
	items       []item
	conjunctive bool
	bad         bool // answers a product that breaks a rule
}

func (s mugShop) match(it item, f *nilda.CommerceFilters, skip string) bool {
	if f == nil {
		return true
	}
	in := func(v string, list []string) bool { return len(list) == 0 || slices.Contains(list, v) }
	return (skip == "option:colour" || in(it.colour, f.Options["colour"])) &&
		(skip == "brand" || in(it.brand, f.Brands)) &&
		(skip == "category" || in(it.term, f.Terms)) &&
		(skip == "rating" || it.rating >= f.MinRating) &&
		(skip == "stock" || !f.InStock || it.inStock)
}

func (s mugShop) Products(_ context.Context, q nilda.CommerceQuery) ([]nilda.CommerceProduct, error) {
	var out []nilda.CommerceProduct
	for _, it := range s.items {
		if s.match(it, q.Filters, "") {
			out = append(out, nilda.CommerceProduct{ID: it.sku, Title: it.sku, Price: "€10.00", InStock: it.inStock,
				Facts: &nilda.CommerceFacts{Amount: "10.00", Currency: "EUR", Brand: it.brand}})
		}
	}
	return out, nil
}

func (s mugShop) Product(_ context.Context, id string) (nilda.CommerceProduct, bool) {
	if id != "mug" {
		return nilda.CommerceProduct{}, false
	}
	p := nilda.CommerceProduct{ID: "mug", Title: "Mug", Price: "€10.00", PriceFrom: true, UnitPrice: "€10.00 / item",
		Variations: []nilda.CommerceVariation{{Name: "Colour", Key: "colour", Options: []string{"Black", "White"}}},
		Choices: []nilda.CommerceChoice{
			{Selection: map[string]string{"colour": "Black"}, SKU: "MUG-B", Price: "€10.00", InStock: true},
			{Selection: map[string]string{"colour": "White"}, SKU: "MUG-W", Price: "€12.00"},
		},
		Facts: &nilda.CommerceFacts{Amount: "10.00", Currency: "EUR", Availability: nilda.SchemaInStock}}
	if s.bad {
		p.Choices[1].SKU = "MUG-B"
	}
	return p, true
}

func (s mugShop) Endpoints(context.Context) nilda.CommerceEndpoints {
	return nilda.CommerceEndpoints{Cart: "/shop/cart", PaymentMarks: []string{nilda.PaymentMarkVisa, nilda.PaymentMarkBankTransfer}}
}

func (s mugShop) CountProducts(_ context.Context, q nilda.CommerceQuery) (int, bool) {
	n := 0
	for _, it := range s.items {
		if s.match(it, q.Filters, "") {
			n++
		}
	}
	return n, true
}

func (s mugShop) Facets(_ context.Context, q nilda.CommerceQuery, want []string) ([]nilda.CommerceFacet, error) {
	var out []nilda.CommerceFacet
	count := func(key string, keep func(item) bool) int {
		skip := key
		if s.conjunctive {
			skip = ""
		}
		n := 0
		for _, it := range s.items {
			if s.match(it, q.Filters, skip) && keep(it) {
				n++
			}
		}
		return n
	}
	values := func(key string, of func(item) string) []nilda.CommerceFacetValue {
		var vals []string
		for _, it := range s.items {
			if v := of(it); !slices.Contains(vals, v) {
				vals = append(vals, v)
			}
		}
		var opts []nilda.CommerceFacetValue
		for _, v := range vals {
			opts = append(opts, nilda.CommerceFacetValue{Value: v, Count: count(key, func(it item) bool { return of(it) == v })})
		}
		return opts
	}
	for _, k := range want {
		switch k {
		case "price":
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetPrice, Label: "Price", Min: 10, Max: 10})
		case "option:colour":
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetOption, Label: "Colour",
				Options: values(k, func(it item) string { return it.colour })})
		case "brand":
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetBrand, Label: "Brand",
				Options: values(k, func(it item) string { return it.brand })})
		case "category":
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetCategory, Label: "Category",
				Options: values(k, func(it item) string { return it.term })})
		case "rating":
			var opts []nilda.CommerceFacetValue
			for r := 1; r <= 5; r++ {
				opts = append(opts, nilda.CommerceFacetValue{Value: strconv.Itoa(r),
					Count: count(k, func(it item) bool { return it.rating >= r })})
			}
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetRating, Label: "Rating", Options: opts})
		case "stock":
			out = append(out, nilda.CommerceFacet{Key: k, Kind: nilda.CommerceFacetStock, Label: "Availability",
				Options: []nilda.CommerceFacetValue{{Value: nilda.CommerceFacetInStock, Count: count(k, func(it item) bool { return it.inStock })}}})
		}
	}
	return out, nil
}

var mugs = []item{
	{"m1", "Black", "kiln", "mugs", 5, true}, {"m2", "White", "kiln", "mugs", 3, false},
	{"m3", "Black", "clay", "mugs", 4, true}, {"m4", "Blue", "clay", "cups", 2, true},
}

// THE STOREFRONT ASKS AS CORE ASKS: a product page's choices and facts, a grid's narrowing, the endpoints' marks — and a
// product that breaks a rule fails the ask, as it fails Core's.
func TestTheStorefrontAsksTheShopAsCoreDoes(t *testing.T) {
	ctx := context.Background()
	sf := NewStorefront(mugShop{items: mugs})
	p, found, err := sf.Product(ctx, "mug")
	if err != nil || !found || !p.PriceFrom || len(p.Choices) != 2 || p.Choices[1].Price != "€12.00" ||
		p.Facts == nil || p.Facts.Availability != nilda.SchemaInStock {
		t.Fatalf("the product page: %+v %v %v", p, found, err)
	}
	if _, found, err := sf.Product(ctx, "nope"); found || err != nil {
		t.Errorf("no such product: %v %v", found, err)
	}
	if _, _, err := NewStorefront(mugShop{items: mugs, bad: true}).Product(ctx, "mug"); err == nil ||
		!strings.Contains(err.Error(), "MUG-B") {
		t.Errorf("a product with two choices of one code was drawn: %v", err)
	}
	q := nilda.CommerceQuery{Filters: &nilda.CommerceFilters{Options: map[string][]string{"colour": {"Black"}}, InStock: true}}
	list, err := sf.Products(ctx, q)
	if err != nil || len(list) != 2 || list[0].ID != "m1" || list[1].ID != "m3" {
		t.Fatalf("the grid under filters: %+v %v", list, err)
	}
	if n, ok, err := sf.Count(ctx, q); err != nil || !ok || n != 2 {
		t.Errorf("the count under filters: %d %v %v", n, ok, err)
	}
	if _, err := sf.Products(ctx, nilda.CommerceQuery{Filters: &nilda.CommerceFilters{MinRating: 9}}); err == nil {
		t.Error("a query Core never sends was answered")
	}
	ep, err := sf.Endpoints(ctx)
	if err != nil || !slices.Equal(ep.PaymentMarks, []string{"visa", "bank-transfer"}) {
		t.Errorf("the endpoints' marks: %+v %v", ep, err)
	}
	facets, err := sf.Facets(ctx, q, []string{"option:colour", "price", "stock"})
	if err != nil || len(facets) != 3 || facets[0].Options[0].Value != "Black" || facets[0].Options[0].Count != 2 ||
		facets[0].Options[2].Value != "Blue" || facets[0].Options[2].Count != 1 {
		t.Fatalf("the facets: %+v %v", facets, err)
	}
}

// THE FACETS' COUNTS ARE HELD TO THE SHOP'S OWN COUNT, disjunctively: a shop that counts each facet under its own
// selection is named, option by option.
func TestFacetCountsAreHeldToTheShopsCount(t *testing.T) {
	ctx := context.Background()
	q := nilda.CommerceQuery{Filters: &nilda.CommerceFilters{Options: map[string][]string{"colour": {"Black"}},
		Brands: []string{"kiln"}, MinRating: 3, InStock: true}}
	want := []string{"option:colour", "brand", "category", "rating", "stock", "price"}
	if err := NewStorefront(mugShop{items: mugs}).CheckFacetCounts(ctx, q, want); err != nil {
		t.Fatalf("a shop counting disjunctively: %v", err)
	}
	err := NewStorefront(mugShop{items: mugs, conjunctive: true}).CheckFacetCounts(ctx, q, want)
	// With Black, kiln, 3+ and in stock chosen, clay's mug (black, 4 stars, in stock) is what picking clay instead of
	// kiln finds: 1, which a count under brand's own selection reads as 0.
	if err == nil || !strings.Contains(err.Error(), `brand = "clay" counts 0 and the shop finds 1`) {
		t.Fatalf("a shop counting each facet under its own selection passed: %v", err)
	}
	if err := NewStorefront(noCount{mugShop{items: mugs}}).CheckFacetCounts(ctx, q, want); err == nil {
		t.Error("a shop that does not count was checked")
	}
}

// noCount is the mug shop without its counter.
type noCount struct{ s mugShop }

func (n noCount) Products(ctx context.Context, q nilda.CommerceQuery) ([]nilda.CommerceProduct, error) {
	return n.s.Products(ctx, q)
}
func (n noCount) Product(ctx context.Context, id string) (nilda.CommerceProduct, bool) {
	return n.s.Product(ctx, id)
}
func (n noCount) Endpoints(ctx context.Context) nilda.CommerceEndpoints { return n.s.Endpoints(ctx) }
func (n noCount) Facets(ctx context.Context, q nilda.CommerceQuery, want []string) ([]nilda.CommerceFacet, error) {
	return n.s.Facets(ctx, q, want)
}

// A METHOD'S MARKS REACH THE CONSUMER, as Core passes them on; a describe naming a mark Core cannot draw offers nothing.
func TestAMethodsMarksReachTheConsumer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Marks: []string{nilda.PaymentMarkVisa, nilda.PaymentMarkMaestro}}}
	opts, err := w.shopAPI.Methods(ctx, "EUR")
	if err != nil || len(opts) != 1 || !slices.Equal(opts[0].Marks, []string{"visa", "maestro"}) {
		t.Fatalf("the option's marks: %+v %v", opts, err)
	}
	w.gw.Methods[0].Marks = []string{"diners"}
	if opts, err := w.shopAPI.Methods(ctx, "EUR"); err != nil || len(opts) != 0 {
		t.Errorf("a describe naming a mark Core cannot draw offered %+v (%v)", opts, err)
	}
}
