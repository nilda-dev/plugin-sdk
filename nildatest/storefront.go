package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Storefront is Core's storefront asking your shop what its widgets draw (v0.58.0): over the same JSON the commerce
// hooks carry and through the same dispatch Serve uses, so a product or a facet your shop answers is held to every rule
// Core's side holds it to — CommerceProduct.Validate, CommerceFilters.Validate, the facets' rules — in `go test`,
// before a page drew it. Facets are EXPERIMENTAL, as nilda.CommerceFacets is.
//
//	sf := nildatest.NewStorefront(myShop)
//	p, found, err := sf.Product(ctx, "mug")
//	facets, err := sf.Facets(ctx, nilda.CommerceQuery{Term: "mugs"}, []string{"price", "option:colour"})
//	err = sf.CheckFacetCounts(ctx, nilda.CommerceQuery{Term: "mugs"}, []string{"option:colour", "brand"})
type Storefront struct {
	shop nilda.Commerce
}

// NewStorefront is the storefront of shop.
func NewStorefront(shop nilda.Commerce) *Storefront { return &Storefront{shop: shop} }

// ask sends one hook's payload as Core sends it and decodes the answer into out.
func (s *Storefront) ask(ctx context.Context, hook string, payload any, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	ans, handled, err := nilda.DispatchCommerceHook(ctx, nil, s.shop, hook, b)
	if !handled {
		return fmt.Errorf("nildatest: %s was not handled", hook)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(ans, out)
}

// Products is what a catalogue widget is answered for q.
func (s *Storefront) Products(ctx context.Context, q nilda.CommerceQuery) ([]nilda.CommerceProduct, error) {
	var out struct {
		Products []nilda.CommerceProduct `json:"products"`
	}
	err := s.ask(ctx, nilda.HookCommerceProducts, q, &out)
	return out.Products, err
}

// Product is what a product page is answered for id; found=false is an explicit "no such product".
func (s *Storefront) Product(ctx context.Context, id string) (nilda.CommerceProduct, bool, error) {
	var out struct {
		Product *nilda.CommerceProduct `json:"product"`
	}
	if err := s.ask(ctx, nilda.HookCommerceProduct, map[string]string{"id": id}, &out); err != nil || out.Product == nil {
		return nilda.CommerceProduct{}, false, err
	}
	return *out.Product, true, nil
}

// Endpoints is the shop's endpoints answer, its payment marks included.
func (s *Storefront) Endpoints(ctx context.Context) (nilda.CommerceEndpoints, error) {
	var out nilda.CommerceEndpoints
	err := s.ask(ctx, nilda.HookCommerceEndpoints, struct{}{}, &out)
	return out, err
}

// Count is the shop's CountProducts for q through commerce.count; ok=false when the shop does not count.
func (s *Storefront) Count(ctx context.Context, q nilda.CommerceQuery) (total int, ok bool, err error) {
	q.Limit, q.Offset = 0, 0 // the count is about the whole result, as Core asks it
	var out struct {
		Total *int `json:"total"`
	}
	if err := s.ask(ctx, nilda.HookCommerceCount, q, &out); err != nil || out.Total == nil {
		return 0, false, err
	}
	return *out.Total, true, nil
}

// Facets is what a product filter is answered for q and the facet keys it wants.
func (s *Storefront) Facets(ctx context.Context, q nilda.CommerceQuery, want []string) ([]nilda.CommerceFacet, error) {
	q.Limit, q.Offset = 0, 0 // a filter narrows the whole result, as Core asks it
	payload := struct {
		nilda.CommerceQuery
		Want []string `json:"want"`
	}{q, want}
	var out struct {
		Facets []nilda.CommerceFacet `json:"facets"`
	}
	err := s.ask(ctx, nilda.HookCommerceFacets, payload, &out)
	return out.Facets, err
}

// CheckFacetCounts holds your facets' counts to your own count, the way a shopper reads them: every option of every
// facet answered for q and want (a price facet has none) must count what CountProducts finds with q's filters and that
// option picked IN PLACE OF the facet's own selection — disjunctively, so a second colour shows what it adds. It needs
// the shop to count (nilda.CommerceCounter), and names each option whose count disagrees.
func (s *Storefront) CheckFacetCounts(ctx context.Context, q nilda.CommerceQuery, want []string) error {
	if _, ok := s.shop.(nilda.CommerceCounter); !ok {
		return fmt.Errorf("nildatest: the shop does not count (nilda.CommerceCounter), so its facets' counts cannot be checked")
	}
	facets, err := s.Facets(ctx, q, want)
	if err != nil {
		return err
	}
	var wrong []string
	for _, f := range facets {
		if f.Kind == nilda.CommerceFacetPrice {
			continue
		}
		for _, o := range f.Options {
			picked, err := pickOnly(q, f, o.Value)
			if err != nil {
				return err
			}
			n, ok, err := s.Count(ctx, picked)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("nildatest: the shop did not count the query with %s = %q picked", f.Key, o.Value)
			}
			if n != o.Count {
				wrong = append(wrong, fmt.Sprintf("%s = %q counts %d and the shop finds %d", f.Key, o.Value, o.Count, n))
			}
		}
	}
	if len(wrong) > 0 {
		return fmt.Errorf("nildatest: facet counts disagree with the shop's own count — each is counted under every "+
			"filter but its own facet's: %v", wrong)
	}
	return nil
}

// pickOnly is q with v picked in facet f in place of whatever f's own filter held, the rest of q's filters kept.
func pickOnly(q nilda.CommerceQuery, f nilda.CommerceFacet, v string) (nilda.CommerceQuery, error) {
	var fl nilda.CommerceFilters
	if q.Filters != nil {
		fl = *q.Filters
	}
	opts := map[string][]string{}
	for k, vals := range fl.Options {
		opts[k] = vals
	}
	fl.Options = opts
	switch f.Kind {
	case nilda.CommerceFacetOption:
		fl.Options[f.Key[len(nilda.CommerceFacetOption)+1:]] = []string{v}
	case nilda.CommerceFacetBrand:
		fl.Brands = []string{v}
	case nilda.CommerceFacetCategory:
		fl.Terms = []string{v}
	case nilda.CommerceFacetRating:
		r, err := strconv.Atoi(v)
		if err != nil {
			return q, fmt.Errorf("nildatest: rating option %q is not a number", v)
		}
		fl.MinRating = r
	case nilda.CommerceFacetStock:
		fl.InStock = true
	default:
		return q, fmt.Errorf("nildatest: facet %q is of no kind the kit can pick", f.Key)
	}
	q.Filters = &fl
	return q, nil
}
