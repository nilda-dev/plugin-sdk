package nilda

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// THE STOREFRONT'S RELEASE 1 (commerce plan 1C, v0.58.0): what a product says beyond its tile, what a catalogue may be
// narrowed by, and the facets a filter draws. Every field is OPTIONAL — a shop that answers none of them draws exactly
// what it drew before — and each one is checked here before it leaves your plugin (CommerceProduct.Validate,
// CommerceFilters.Validate, validFacets), because Core draws them into a page that is cached and shared, and a GTIN
// with a wrong check digit or a price in a currency nobody prices is a page Google refuses for everybody.
//
// Read 2026-11-06: Google Search Central's merchant listing structured data (Offer price, priceCurrency in ISO 4217,
// availability and itemCondition from closed lists, gtin "in the numerical form", at most one brand —
// developers.google.com/search/docs/appearance/structured-data/merchant-listing) and its product variants
// (ProductGroup + hasVariant, each variant with a unique id — …/structured-data/product-variants); Google Merchant
// Center's product data specification (brand and mpn at most 70 characters, a GTIN of at most 14 digits whose check
// digit "is present and correct", a price's decimal point a period — support.google.com/merchants/answer/7052112);
// Shopify's Liquid product object (`variants` "returns a maximum of 250 variants when unpaginated", `price_varies`,
// `price_min` — shopify.dev/docs/api/liquid/objects/product) and its 2,048-variant product limit (shopify.dev
// changelog, 2025-10-15); WooCommerce's variation threshold (30 by default, beyond which a product page stops
// checking combinations live — woocommerce.com/document/change-limit-on-number-of-variations-for-dynamic-variable-
// product-dropdowns).

// MaxCommerceChoices bounds CommerceProduct.Choices: the combinations a product page carries as data so it can follow
// the shopper's choice. 250 is what Shopify's storefront hands a theme unpaginated; a product with more is a product
// whose page asks its question and lets the add-to-cart answer it (Core draws no live update and says so).
const MaxCommerceChoices = 250

// MaxCommerceNote bounds a short formatted line the storefront draws beside a price — UnitPrice, StockNote, a choice's
// Price and OldPrice — in characters: the same 200 Core trims an Extra value to.
const MaxCommerceNote = 200

// MaxCommerceFactText bounds CommerceFacts.MPN and Brand, in characters (Merchant Center's 70 for both).
const MaxCommerceFactText = 70

// The availability of an offer, in schema.org's words — the ten Google's merchant listings accept
// (CommerceFacts.Availability, SchemaAvailabilities). Not the catalogue's in_stock words (AvailabilityInStock): these
// are written into a page's structured data as they are.
const (
	SchemaInStock             = "InStock"
	SchemaOutOfStock          = "OutOfStock"
	SchemaPreOrder            = "PreOrder"
	SchemaPreSale             = "PreSale"
	SchemaBackOrder           = "BackOrder"
	SchemaSoldOut             = "SoldOut"
	SchemaDiscontinued        = "Discontinued"
	SchemaLimitedAvailability = "LimitedAvailability"
	SchemaInStoreOnly         = "InStoreOnly"
	SchemaOnlineOnly          = "OnlineOnly"
)

// The condition of an item, in schema.org's words — the three Google's merchant listings accept
// (CommerceFacts.Condition, SchemaConditions).
const (
	SchemaNewCondition         = "NewCondition"
	SchemaRefurbishedCondition = "RefurbishedCondition"
	SchemaUsedCondition        = "UsedCondition"
)

// SchemaAvailabilities is every word CommerceFacts.Availability may hold.
func SchemaAvailabilities() []string {
	return []string{SchemaInStock, SchemaOutOfStock, SchemaPreOrder, SchemaPreSale, SchemaBackOrder, SchemaSoldOut,
		SchemaDiscontinued, SchemaLimitedAvailability, SchemaInStoreOnly, SchemaOnlineOnly}
}

// SchemaConditions is every word CommerceFacts.Condition may hold.
func SchemaConditions() []string {
	return []string{SchemaNewCondition, SchemaRefurbishedCondition, SchemaUsedCondition}
}

// CommerceFacts are a product's MACHINE values: never drawn as text, written by Core into the page's structured data —
// the Product's Offer, and a ProductGroup's variant per choice — so a search engine can list it with its price. Price
// is a string you formatted for a person ("€19.90", "19,90 €") and Core never parses it; Amount is the same price as a
// machine reads it. Every field is optional; Core leaves out what you leave out, and makes no Offer without an Amount.
type CommerceFacts struct {
	// Amount is the price a buyer pays now, as a plain decimal with a period — "19.90", "1500" for yen — no symbol, no
	// grouping, no sign, and no more decimals than Currency has (ParseMinor's rule). With ShowPricesIncludingTax set, the
	// gross figure, as Price is. Set with Currency or not at all. Google lists a merchant offer only above zero.
	Amount string `json:"amount,omitempty"`
	// Currency is Amount's currency, three upper-case ISO 4217 letters ("EUR").
	Currency string `json:"currency,omitempty"`
	// Availability is one of SchemaAvailabilities ("InStock").
	Availability string `json:"availability,omitempty"`
	// Condition is one of SchemaConditions ("NewCondition").
	Condition string `json:"condition,omitempty"`
	// GTIN is the item's global trade item number — 8, 12, 13 or 14 digits, its check digit correct (EAN, UPC, ISBN-13).
	GTIN string `json:"gtin,omitempty"`
	// MPN is the manufacturer's part number; Brand the one brand name. At most MaxCommerceFactText characters each.
	MPN   string `json:"mpn,omitempty"`
	Brand string `json:"brand,omitempty"`
}

// CommerceChoice is one sellable combination of a product's Variations — the black one in size M — as the product page
// carries it, so the page follows the shopper's choice: its price, its code, its stock, its photo. FORMATTED BY YOU,
// as the product's own fields are.
type CommerceChoice struct {
	// Selection is the combination: every axis of the product's Variations (its Key, or its Name when it has no Key)
	// answered with one of its Options — {"colour": "Black", "size": "M"}. No two choices have the same.
	Selection map[string]string `json:"selection"`
	// SKU is the choice's own code (unique among the product's choices); empty, the product's.
	SKU string `json:"sku,omitempty"`
	// Price, OldPrice and UnitPrice are as the product's; empty, the product's.
	Price     string `json:"price,omitempty"`
	OldPrice  string `json:"old_price,omitempty"`
	UnitPrice string `json:"unit_price,omitempty"`
	// InStock is whether this combination can be bought now.
	InStock bool `json:"in_stock,omitempty"`
	// StockNote is the product's StockNote for this combination ("Only 2 left").
	StockNote string `json:"stock_note,omitempty"`
	// MediaID is the photo of this combination in the site's media library; empty, the gallery stays where it is.
	MediaID string `json:"media_id,omitempty"`
	// Facts are this combination's machine values (its Offer); nil, none. Its Currency, when set, is the product's.
	Facts *CommerceFacts `json:"facts,omitempty"`
}

// Validate holds a product's storefront fields to what Core can draw and a search engine will read: PriceFrom only
// beside a Price; UnitPrice and StockNote one plain line of at most MaxCommerceNote characters; Facts by
// CommerceFacts.Validate; and at most MaxCommerceChoices Choices, only on a product with Variations, each a complete
// and distinct Selection of them, each SKU distinct, in the product's one currency. The fields before v0.58.0 are not
// checked here. DispatchCommerceHook runs it on every product it answers, so a product that breaks a rule fails the
// call and names itself, in your own tests first.
func (p CommerceProduct) Validate() error {
	fail := func(format string, a ...any) error {
		return fmt.Errorf("nilda: product %q: "+format, append([]any{p.ID}, a...)...)
	}
	if p.PriceFrom && strings.TrimSpace(p.Price) == "" {
		return fail("price_from says its price is the lowest of several, and it has no price")
	}
	if !commerceLine(p.UnitPrice, MaxCommerceNote) || !commerceLine(p.StockNote, MaxCommerceNote) {
		return fail("unit_price and stock_note are one plain line of at most %d characters each", MaxCommerceNote)
	}
	currency := ""
	if p.Facts != nil {
		if err := p.Facts.Validate(); err != nil {
			return fail("facts: %v", err)
		}
		currency = p.Facts.Currency
	}
	if p.ChoicesComplete && len(p.Choices) == 0 {
		return fail("choices_complete says every combination is named, and it names none")
	}
	if len(p.Choices) == 0 {
		return nil
	}
	if len(p.Choices) > MaxCommerceChoices {
		return fail("%d choices, past the %d a product page carries", len(p.Choices), MaxCommerceChoices)
	}
	// The axes a choice answers: each by its Key, or its Name when it has none; only an axis with options is asked.
	axes := map[string]map[string]bool{}
	for _, v := range p.Variations {
		key := v.Key
		if key == "" {
			key = v.Name
		}
		if strings.TrimSpace(key) == "" || len(v.Options) == 0 {
			continue
		}
		axes[key] = map[string]bool{}
		for _, o := range v.Options {
			axes[key][o] = true
		}
	}
	if len(axes) == 0 {
		return fail("choices answer its variations, and it has none")
	}
	combos, skus := map[string]bool{}, map[string]bool{}
	for i, c := range p.Choices {
		if len(c.Selection) != len(axes) {
			return fail("choice %d answers %d of its %d axes; a choice is one whole combination", i, len(c.Selection), len(axes))
		}
		parts := make([]string, 0, len(axes))
		for k, v := range c.Selection {
			opts, ok := axes[k]
			if !ok {
				return fail("choice %d names %q, which is none of its variations' keys", i, k)
			}
			if !opts[v] {
				return fail("choice %d answers %q with %q, which is none of its options", i, k, v)
			}
			parts = append(parts, k+"\x00"+v)
		}
		slices.Sort(parts)
		combo := strings.Join(parts, "\x01")
		if combos[combo] {
			return fail("choice %d is a combination an earlier choice already is", i)
		}
		combos[combo] = true
		if c.SKU != "" {
			if len(c.SKU) > MaxPaymentLineSKU || !commerceLine(c.SKU, MaxPaymentLineSKU) || strings.TrimSpace(c.SKU) == "" {
				return fail("choice %d's sku is not one plain line of at most %d bytes", i, MaxPaymentLineSKU)
			}
			if skus[c.SKU] {
				return fail("choice %d's sku %q is an earlier choice's; each combination has its own code", i, c.SKU)
			}
			skus[c.SKU] = true
		}
		for _, line := range []string{c.Price, c.OldPrice, c.UnitPrice, c.StockNote} {
			if !commerceLine(line, MaxCommerceNote) {
				return fail("choice %d's price, old_price, unit_price and stock_note are one plain line of at most %d "+
					"characters each", i, MaxCommerceNote)
			}
		}
		if len(c.MediaID) > maxCommerceMediaID || strings.ContainsFunc(c.MediaID, func(r rune) bool { return r <= ' ' || r > '~' }) {
			return fail("choice %d's media_id is not a media library id", i)
		}
		if c.Facts != nil {
			if err := c.Facts.Validate(); err != nil {
				return fail("choice %d's facts: %v", i, err)
			}
			if c.Facts.Currency != "" && currency != "" && c.Facts.Currency != currency {
				return fail("choice %d is priced in %s and the product in %s; one product, one currency", i, c.Facts.Currency, currency)
			}
			if currency == "" {
				currency = c.Facts.Currency
			}
		}
	}
	return nil
}

// maxCommerceMediaID bounds a choice's media id: a media library id, printable ASCII.
const maxCommerceMediaID = 64

// Validate holds machine facts to schema.org's and Google's words (see each field).
func (f CommerceFacts) Validate() error {
	if (f.Amount == "") != (f.Currency == "") {
		return fmt.Errorf("amount and currency are set together or not at all")
	}
	if f.Currency != "" {
		if _, ok := MinorUnits(f.Currency); !ok || len(f.Currency) != 3 || strings.ToUpper(f.Currency) != f.Currency {
			return fmt.Errorf("currency %q is not three upper-case ISO 4217 letters with a minor unit", f.Currency)
		}
		if strings.TrimSpace(f.Amount) != f.Amount {
			return fmt.Errorf("amount %q is not a plain decimal", f.Amount)
		}
		if _, err := ParseMinor(f.Amount, f.Currency); err != nil {
			return fmt.Errorf("amount: %w", err)
		}
	}
	if f.Availability != "" && !slices.Contains(SchemaAvailabilities(), f.Availability) {
		return fmt.Errorf("availability %q is none of schema.org's words Google takes: %s", f.Availability,
			strings.Join(SchemaAvailabilities(), ", "))
	}
	if f.Condition != "" && !slices.Contains(SchemaConditions(), f.Condition) {
		return fmt.Errorf("condition %q is none of %s", f.Condition, strings.Join(SchemaConditions(), ", "))
	}
	if f.GTIN != "" && !validGTIN(f.GTIN) {
		return fmt.Errorf("gtin %q is not 8, 12, 13 or 14 digits with a correct check digit", f.GTIN)
	}
	if !commerceLine(f.MPN, MaxCommerceFactText) || !commerceLine(f.Brand, MaxCommerceFactText) {
		return fmt.Errorf("mpn and brand are one plain line of at most %d characters each", MaxCommerceFactText)
	}
	return nil
}

// validGTIN reports whether s is 8, 12, 13 or 14 digits whose last is the GS1 check digit of the others.
func validGTIN(s string) bool {
	switch len(s) {
	case 8, 12, 13, 14:
	default:
		return false
	}
	if !allDigits(s) {
		return false
	}
	// GS1's mod-10: from the digit left of the check digit, weights 3, 1, 3, …
	sum := 0
	for i := len(s) - 2; i >= 0; i-- {
		d := int(s[i] - '0')
		if (len(s)-2-i)%2 == 0 {
			d *= 3
		}
		sum += d
	}
	return int(s[len(s)-1]-'0') == (10-sum%10)%10
}

// commerceLine is a short line the storefront draws: at most max characters, valid UTF-8, no control character and
// no bidirectional override (plainLine).
func commerceLine(v string, max int) bool {
	return utf8.ValidString(v) && utf8.RuneCountInString(v) <= max && plainLine(v)
}

// FILTERS — what a shopper narrowed a catalogue by (EXPERIMENTAL, v0.58.0).

// Bounds on CommerceFilters, the same Core puts on its filter URL (commerce plan ST25): at most MaxCommerceFilterAxes
// option axes, MaxCommerceFilterValues values in any one list, MaxCommerceFilterValueBytes bytes in any one value.
const (
	MaxCommerceFilterAxes       = 8
	MaxCommerceFilterValues     = 20
	MaxCommerceFilterValueBytes = 128
	// MaxCommerceRating is the most stars a MinRating may ask for.
	MaxCommerceRating = 5
)

// CommerceFilters are what a shopper chose in Core's product filter, on CommerceQuery.Filters (EXPERIMENTAL, v0.58.0).
// Every list is an OR within itself and an AND with the others: two colours are either colour, a colour and a brand
// are both. Nil filters, or an empty one, narrow nothing. Values are the ones your facets answered (CommerceFacet) —
// Core sends back what the shopper picked from them and invents none.
type CommerceFilters struct {
	// PriceMin and PriceMax bound the price, both inclusive, in the unit your price facet's Min and Max are in: Core
	// sends back what the shopper chose between the bounds you gave and never converts. Nil is no bound.
	PriceMin *int64 `json:"price_min,omitempty"`
	PriceMax *int64 `json:"price_max,omitempty"`
	// Options are the chosen values per variation axis, by the axis key your option facet answered ("option:<key>").
	Options map[string][]string `json:"options,omitempty"`
	// Brands are the chosen brands, as your brand facet's option values.
	Brands []string `json:"brands,omitempty"`
	// MinRating is "this many stars and up", 1 to MaxCommerceRating; 0 is no bound.
	MinRating int `json:"min_rating,omitempty"`
	// InStock keeps only what can be bought now.
	InStock bool `json:"in_stock,omitempty"`
	// Terms are the chosen categories, as your category facet's option values (a term's slug).
	Terms []string `json:"terms,omitempty"`
}

// Validate holds filters to the bounds above: prices not negative and the minimum not past the maximum; at most
// MaxCommerceFilterAxes axes and MaxCommerceFilterValues values in a list, each value one plain line of 1 to
// MaxCommerceFilterValueBytes bytes, no value twice; MinRating 0 to MaxCommerceRating. Core sends nothing outside them;
// DispatchCommerceHook refuses a payload that does.
func (f CommerceFilters) Validate() error {
	if (f.PriceMin != nil && *f.PriceMin < 0) || (f.PriceMax != nil && *f.PriceMax < 0) {
		return fmt.Errorf("filters: a price bound is not negative")
	}
	if f.PriceMin != nil && f.PriceMax != nil && *f.PriceMin > *f.PriceMax {
		return fmt.Errorf("filters: price_min %d is past price_max %d", *f.PriceMin, *f.PriceMax)
	}
	if f.MinRating < 0 || f.MinRating > MaxCommerceRating {
		return fmt.Errorf("filters: min_rating is 0 to %d", MaxCommerceRating)
	}
	if len(f.Options) > MaxCommerceFilterAxes {
		return fmt.Errorf("filters: %d option axes, past %d", len(f.Options), MaxCommerceFilterAxes)
	}
	list := func(name string, vals []string, required bool) error {
		if required && len(vals) == 0 {
			return fmt.Errorf("filters: %s chooses no value", name)
		}
		if len(vals) > MaxCommerceFilterValues {
			return fmt.Errorf("filters: %s holds %d values, past %d", name, len(vals), MaxCommerceFilterValues)
		}
		seen := map[string]bool{}
		for _, v := range vals {
			if !commerceValue(v) {
				return fmt.Errorf("filters: %s holds a value that is not one plain line of 1 to %d bytes", name,
					MaxCommerceFilterValueBytes)
			}
			if seen[v] {
				return fmt.Errorf("filters: %s holds %q twice", name, v)
			}
			seen[v] = true
		}
		return nil
	}
	for axis, vals := range f.Options {
		if !commerceValue(axis) {
			return fmt.Errorf("filters: an option axis key is not one plain line of 1 to %d bytes", MaxCommerceFilterValueBytes)
		}
		if err := list("options."+axis, vals, true); err != nil {
			return err
		}
	}
	if err := list("brands", f.Brands, false); err != nil {
		return err
	}
	return list("terms", f.Terms, false)
}

// FACETS — what a filter offers, with how many each would find (EXPERIMENTAL, v0.58.0).

// HookCommerceFacets asks a shop which filters a catalogue offers — see CommerceFacets.
const HookCommerceFacets = "commerce.facets"

// The kinds of facet, and each one's key: a facet is keyed by its kind, except an option facet, keyed
// "option:<axis key>" (CommerceFacetKey).
const (
	// CommerceFacetPrice is a range: Min and Max, no options.
	CommerceFacetPrice = "price"
	// CommerceFacetOption is one variation axis ("option:colour"); its options are the axis's values.
	CommerceFacetOption = "option"
	// CommerceFacetBrand's options are brands.
	CommerceFacetBrand = "brand"
	// CommerceFacetRating's options are "1" to "5": that many stars and up.
	CommerceFacetRating = "rating"
	// CommerceFacetStock has one option, CommerceFacetInStock: what can be bought now.
	CommerceFacetStock = "stock"
	// CommerceFacetCategory's options are categories, each value a term's slug.
	CommerceFacetCategory = "category"
)

// CommerceFacetInStock is the value of a stock facet's one option.
const CommerceFacetInStock = "in_stock"

// Bounds on a facets answer: at most MaxCommerceFacets facets asked or answered, MaxCommerceFacetOptions options in one
// (Core's filter widget draws no more).
const (
	MaxCommerceFacets       = 16
	MaxCommerceFacetOptions = 60
)

// CommerceFacetKey is the key of the facet of kind for an option axis ("option:colour"), or of any other kind (axis
// ignored, "brand").
func CommerceFacetKey(kind, axis string) string {
	if kind == CommerceFacetOption {
		return CommerceFacetOption + ":" + axis
	}
	return kind
}

// CommerceFacet is one filter a catalogue offers: what a shopper may narrow it by, in your words.
type CommerceFacet struct {
	// Key is the key Core asked for (CommerceFacetKey); Kind its kind.
	Key  string `json:"key"`
	Kind string `json:"kind"`
	// Label is what the filter is called, in the shop's language ("Colour").
	Label string `json:"label"`
	// Options are the values to pick, in the order you want them offered, each with how many products picking it would
	// find. Empty for a price facet.
	Options []CommerceFacetValue `json:"options,omitempty"`
	// Min and Max are a price facet's bounds, in the unit CommerceFilters.PriceMin and PriceMax come back in — the
	// lowest and highest price in the narrowed catalogue, rounded outwards as you like. Zero for any other kind.
	Min int64 `json:"min,omitempty"`
	Max int64 `json:"max,omitempty"`
}

// CommerceFacetValue is one value of a facet: an option to pick.
type CommerceFacetValue struct {
	// Value is what comes back in CommerceFilters when the shopper picks it.
	Value string `json:"value"`
	// Label is what the shopper reads; empty, Value.
	Label string `json:"label,omitempty"`
	// Count is how many products the query would find with this value picked — DISJUNCTIVELY: counted under every
	// filter of the query except this facet's own, so picking a second colour shows what it ADDS (what WooCommerce's,
	// Shopify's and BigCommerce's filters show). Never negative.
	Count int `json:"count"`
}

// CommerceFacets is an OPTIONAL extra (EXPERIMENTAL, v0.58.0): the filters a catalogue offers. Core asks with the query a
// product filter narrows — its Term, Search and Filters, no Limit or Offset — and want, the facet keys the owner chose
// for the widget, in their order; answer the ones you can, each at most once, in any order — a key you cannot answer is
// left out and the widget draws no such filter. Answered by the hook commerce.facets, which Serve subscribes only for a
// shop that implements this. An error draws no filter at all.
type CommerceFacets interface {
	Facets(ctx context.Context, q CommerceQuery, want []string) ([]CommerceFacet, error)
}

// validFacetWant refuses a want list Core never sends.
func validFacetWant(want []string) error {
	if len(want) > MaxCommerceFacets {
		return fmt.Errorf("%d facets asked, past %d", len(want), MaxCommerceFacets)
	}
	seen := map[string]bool{}
	for _, k := range want {
		if commerceFacetKind(k) == "" {
			return fmt.Errorf("%q is no facet key: price, brand, rating, stock, category or option:<axis key>", k)
		}
		if seen[k] {
			return fmt.Errorf("facet %q asked twice", k)
		}
		seen[k] = true
	}
	return nil
}

// commerceFacetKind is the kind of a facet key, or "" for a key that is none.
func commerceFacetKind(key string) string {
	switch key {
	case CommerceFacetPrice, CommerceFacetBrand, CommerceFacetRating, CommerceFacetStock, CommerceFacetCategory:
		return key
	}
	if axis, ok := strings.CutPrefix(key, CommerceFacetOption+":"); ok && commerceValue(axis) {
		return CommerceFacetOption
	}
	return ""
}

// validFacets refuses a facets answer Core could not draw.
func validFacets(want []string, facets []CommerceFacet) error {
	asked := map[string]bool{}
	for _, k := range want {
		asked[k] = true
	}
	answered := map[string]bool{}
	for _, f := range facets {
		switch {
		case !asked[f.Key]:
			return fmt.Errorf("facet %q was not asked for", f.Key)
		case answered[f.Key]:
			return fmt.Errorf("facet %q answered twice", f.Key)
		case f.Kind != commerceFacetKind(f.Key):
			return fmt.Errorf("facet %q is of kind %q, not %q", f.Key, commerceFacetKind(f.Key), f.Kind)
		case strings.TrimSpace(f.Label) == "" || !commerceLine(f.Label, MaxCommerceNote):
			return fmt.Errorf("facet %q's label is not one plain line of 1 to %d characters", f.Key, MaxCommerceNote)
		case len(f.Options) > MaxCommerceFacetOptions:
			return fmt.Errorf("facet %q offers %d options, past %d", f.Key, len(f.Options), MaxCommerceFacetOptions)
		}
		answered[f.Key] = true
		if f.Kind == CommerceFacetPrice {
			if len(f.Options) > 0 || f.Min < 0 || f.Max < f.Min {
				return fmt.Errorf("facet %q is a range: no options, and 0 <= min <= max", f.Key)
			}
			continue
		}
		if f.Min != 0 || f.Max != 0 {
			return fmt.Errorf("facet %q is not a range and has a min or a max", f.Key)
		}
		seen := map[string]bool{}
		for _, o := range f.Options {
			switch {
			case !commerceValue(o.Value):
				return fmt.Errorf("facet %q has a value that is not one plain line of 1 to %d bytes", f.Key, MaxCommerceFilterValueBytes)
			case seen[o.Value]:
				return fmt.Errorf("facet %q offers %q twice", f.Key, o.Value)
			case !commerceLine(o.Label, MaxCommerceNote):
				return fmt.Errorf("facet %q's option %q has a label that is not one plain line", f.Key, o.Value)
			case o.Count < 0:
				return fmt.Errorf("facet %q's option %q counts %d", f.Key, o.Value, o.Count)
			case f.Kind == CommerceFacetRating && (len(o.Value) != 1 || o.Value < "1" || o.Value > "5"):
				return fmt.Errorf("facet %q's option %q is not 1 to %d stars", f.Key, o.Value, MaxCommerceRating)
			case f.Kind == CommerceFacetStock && o.Value != CommerceFacetInStock:
				return fmt.Errorf("facet %q's option is %q, and a stock facet's one option is %q", f.Key, o.Value, CommerceFacetInStock)
			}
			seen[o.Value] = true
		}
	}
	return nil
}

// commerceValue is one filter or facet value: one plain line of 1 to MaxCommerceFilterValueBytes bytes.
func commerceValue(v string) bool {
	return v != "" && strings.TrimSpace(v) != "" && len(v) <= MaxCommerceFilterValueBytes && commerceLine(v, MaxCommerceFilterValueBytes)
}
