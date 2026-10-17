package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// THE SHOP'S CATALOGUE, READ BY OTHER PLUGINS (CATALOG.md; commerce unit 9.2, report 06 K2).
//
// A shopping channel — Google Merchant Center, a price-comparison site, a marketplace, an AI agent's catalogue — needs
// the shop's products in a feed-grade shape: a number for the price and a currency, availability per variant, the
// identifiers every channel matches a product by (brand, GTIN, MPN), the page and the pictures, and what changed since
// it last asked. The shop PROVIDES shop.catalog/v1; a channel plugin REQUIRES it and reads it through Core's service
// desk. Channels are plugins and the shop owns the data (owner decision D7): a channel's API changing is a channel
// plugin's release, never the shop's.

// ContractShopCatalog is the contract's name.
const ContractShopCatalog = "shop.catalog/v1"

// The contract's methods.
const (
	CatalogDescribe     = "describe"
	CatalogProductsPage = "products.page"
	CatalogProductGet   = "product.get"
)

// CatalogPageMax is the most products one page holds.
const CatalogPageMax = 200

// A variant's availability, as every feed spec names it.
const (
	AvailabilityInStock    = "in_stock"
	AvailabilityOutOfStock = "out_of_stock"
	AvailabilityBackorder  = "backorder"
	// AvailabilityPreOrder is an item sold before it exists, shipping by the variant's AvailabilityDate (v0.30.0).
	AvailabilityPreOrder = "preorder"
)

// CatalogDescription is the shop as a channel needs to know it.
type CatalogDescription struct {
	// Name is the shop's name (the seller); Origin the site's address, https://…
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// Currency is the shop's currency (ISO 4217); every price in the catalogue is in it.
	Currency string `json:"currency"`
	// PricesIncludeTax says a price is what the buyer pays, tax inside it.
	PricesIncludeTax bool `json:"prices_include_tax"`
	// Country is the shop's own (ISO 3166-1 alpha-2), "" when it has not said; ShipsTo the countries its delivery
	// zones reach ("*" for everywhere).
	Country string   `json:"country,omitempty"`
	ShipsTo []string `json:"ships_to"`
	// ReturnDays is the right of withdrawal a consumer has, in days (14 in the EU).
	ReturnDays int `json:"return_days"`
	// Locale is the language the catalogue's text is written in.
	Locale string `json:"locale"`
}

// Validate refuses a description a channel could not use.
func (d CatalogDescription) Validate() error {
	if strings.TrimSpace(d.Name) == "" || !strings.HasPrefix(d.Origin, "https://") && !strings.HasPrefix(d.Origin, "http://") {
		return errors.New("nilda: a catalogue description needs the shop's name and the site's address")
	}
	if err := validCurrency("the catalogue's currency", d.Currency); err != nil {
		return err
	}
	return nil
}

// CatalogPageRequest asks for one page of the published products.
type CatalogPageRequest struct {
	// Cursor is the NextCursor of the page before; "" for the first.
	Cursor string `json:"cursor,omitempty"`
	// Limit is how many products, at most CatalogPageMax; 0 is 50.
	Limit int `json:"limit,omitempty"`
	// UpdatedSince keeps the products changed at or after it — the price, the stock, the text, a variant — for a
	// channel that already holds the rest. A product taken off sale since is listed with Removed set.
	UpdatedSince *time.Time `json:"updated_since,omitempty"`
}

// Validate refuses a request the shop cannot answer.
func (r CatalogPageRequest) Validate() error {
	if r.Limit < 0 || r.Limit > CatalogPageMax {
		return fmt.Errorf("nilda: a catalogue page holds 1 to %d products, not %d", CatalogPageMax, r.Limit)
	}
	return nil
}

// CatalogPage is one page of products, oldest change first.
type CatalogPage struct {
	Products []CatalogProduct `json:"products"`
	// NextCursor reads the next page; "" when this was the last.
	NextCursor string `json:"next_cursor,omitempty"`
}

// Validate checks every product of the page.
func (p CatalogPage) Validate() error {
	for _, pr := range p.Products {
		if err := pr.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CatalogProductRequest asks for one product by its SKU or one of its variants'.
type CatalogProductRequest struct {
	SKU string `json:"sku"`
}

// Validate refuses a request with no SKU.
func (r CatalogProductRequest) Validate() error {
	if strings.TrimSpace(r.SKU) == "" {
		return errors.New("nilda: a catalogue product is asked for by its SKU")
	}
	return nil
}

// CatalogProductResult is one product, or Found false.
type CatalogProductResult struct {
	Found   bool            `json:"found"`
	Product *CatalogProduct `json:"product,omitempty"`
}

// Validate checks the product.
func (r CatalogProductResult) Validate() error {
	if r.Found != (r.Product != nil) {
		return errors.New("nilda: a found catalogue product carries the product, and only then")
	}
	if r.Product != nil {
		return r.Product.Validate()
	}
	return nil
}

// CatalogProduct is one product as a channel lists it.
type CatalogProduct struct {
	// SKU is the product's own; a channel's item id is a variant's SKU (CatalogVariant.SKU).
	SKU string `json:"sku"`
	// Removed: the product was taken off sale or deleted since UpdatedSince — a channel withdraws it. Nothing else is
	// filled.
	Removed     bool   `json:"removed,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// URL is the product's page, absolute.
	URL string `json:"url,omitempty"`
	// ImageURLs are its pictures, absolute, the main one first.
	ImageURLs []string `json:"image_urls,omitempty"`
	Brand     string   `json:"brand,omitempty"`
	// Condition is new, refurbished or used.
	Condition string `json:"condition,omitempty"`
	// GoogleCategory is Google's product category, as the merchant gave it (an id or a path).
	GoogleCategory string `json:"google_category,omitempty"`
	// UpdatedAt is when anything a channel shows last changed.
	UpdatedAt time.Time        `json:"updated_at"`
	Variants  []CatalogVariant `json:"variants,omitempty"`
}

// Validate refuses a product a channel could not list.
func (p CatalogProduct) Validate() error {
	if strings.TrimSpace(p.SKU) == "" {
		return errors.New("nilda: a catalogue product has a SKU")
	}
	if p.Removed {
		return nil
	}
	if strings.TrimSpace(p.Title) == "" || !strings.HasPrefix(p.URL, "http") {
		return fmt.Errorf("nilda: catalogue product %q needs a title and an absolute URL", p.SKU)
	}
	if len(p.Variants) == 0 {
		return fmt.Errorf("nilda: catalogue product %q has no variant to sell", p.SKU)
	}
	switch p.Condition {
	case "", "new", "refurbished", "used":
	default:
		return fmt.Errorf("nilda: catalogue product %q has condition %q", p.SKU, p.Condition)
	}
	for _, v := range p.Variants {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("nilda: catalogue product %q: %w", p.SKU, err)
		}
	}
	return nil
}

// CatalogVariant is one purchasable variant: the item a channel lists.
type CatalogVariant struct {
	SKU string `json:"sku"`
	// Title is the variant's own ("Blue / M"); "" for a product with one.
	Title string `json:"title,omitempty"`
	// Options are its answers to the product's axes, e.g. {"Color": "Blue"}.
	Options map[string]string `json:"options,omitempty"`
	// PriceMinor is the regular price in minor units of Currency; SalePriceMinor the price now when it is on sale,
	// until SaleEnds when the sale has an end.
	PriceMinor     int64      `json:"price_minor"`
	SalePriceMinor *int64     `json:"sale_price_minor,omitempty"`
	SaleEnds       *time.Time `json:"sale_ends,omitempty"`
	Currency       string     `json:"currency"`
	// ShownPriceMinor and ShownSalePriceMinor are the same two prices as a buyer pays them — with the shop's own tax
	// when its prices are entered without it (CatalogDescription.PricesIncludeTax false), as its storefront shows
	// them. A channel that lists prices with tax (Google and Meta in the EU) lists these. Zero from a provider built
	// before v0.19.0: PriceMinor is then the best a channel has.
	ShownPriceMinor     int64  `json:"shown_price_minor,omitempty"`
	ShownSalePriceMinor *int64 `json:"shown_sale_price_minor,omitempty"`
	// PriceWithoutTax: the shown prices hold no tax although the shop's prices are entered without it — no tax rate
	// matches the variant's tax class — so the price with VAT is unknown. A channel that must show the price with VAT
	// (Directive 98/6/EC Art. 2(a); Google and Meta in the EU) leaves the variant out and says why, rather than
	// publishing a net price as the gross one. From v0.23.0.
	PriceWithoutTax bool `json:"price_without_tax,omitempty"`
	// ShownPriorPriceMinor is, while the variant is on sale, the lowest price (as a buyer pays it) in the 30 days
	// before the reduction began — the only price an EU shop may announce a reduction against (Directive 98/6/EC Art.
	// 6a; Austria's PrAG §9a, in the same sales channel). nil when there is none to compare with. A channel showing a
	// struck-through price shows THIS one; with none, it shows the sale price as the price and no reduction. From
	// v0.23.0.
	ShownPriorPriceMinor *int64 `json:"shown_prior_price_minor,omitempty"`
	// UnitPricing is how much one holds and the base its unit price is stated in, for goods sold by weight, volume,
	// length or area (Directive 98/6/EC Art. 3(4): an advertisement naming the price names the unit price too). nil
	// for anything else. From v0.23.0.
	UnitPricing *CatalogUnitPricing `json:"unit_pricing,omitempty"`
	// Availability is in_stock, out_of_stock, backorder or preorder; Quantity how many are available, nil when not
	// counted. AvailabilityDate is the day a preorder ships by (v0.30.0) — what every channel asks a pre-order for.
	Availability     string     `json:"availability"`
	AvailabilityDate *time.Time `json:"availability_date,omitempty"`
	Quantity     *int64 `json:"quantity,omitempty"`
	GTIN         string `json:"gtin,omitempty"`
	MPN          string `json:"mpn,omitempty"`
	WeightGrams  *int64 `json:"weight_grams,omitempty"`
	// RequiresShipping is false for a download or a service.
	RequiresShipping bool `json:"requires_shipping"`
	// ImageURL is the variant's own picture, absolute; "" uses the product's.
	ImageURL string `json:"image_url,omitempty"`
}

// CatalogUnitPricing is a variant's measure: Quantity of Measure in one item (250 g), the unit price stated per Base
// ("100 g"), and that unit price as a buyer pays it, in minor units.
type CatalogUnitPricing struct {
	Quantity            float64 `json:"quantity"`
	Measure             string  `json:"measure"`
	Base                string  `json:"base"`
	ShownUnitPriceMinor int64   `json:"shown_unit_price_minor"`
}

// Validate refuses a variant a channel could not list.
func (v CatalogVariant) Validate() error {
	if strings.TrimSpace(v.SKU) == "" {
		return errors.New("a variant has a SKU")
	}
	if v.PriceMinor < 0 || v.SalePriceMinor != nil && (*v.SalePriceMinor < 0 || *v.SalePriceMinor > v.PriceMinor) {
		return fmt.Errorf("variant %q has a price below zero or a sale price above its price", v.SKU)
	}
	if v.ShownPriceMinor < 0 || v.ShownSalePriceMinor != nil && (*v.ShownSalePriceMinor < 0 || *v.ShownSalePriceMinor > v.ShownPriceMinor) {
		return fmt.Errorf("variant %q has a shown price below zero or a shown sale price above its shown price", v.SKU)
	}
	if v.ShownPriorPriceMinor != nil && (v.ShownSalePriceMinor == nil || *v.ShownPriorPriceMinor < *v.ShownSalePriceMinor) {
		return fmt.Errorf("variant %q has a prior price without a sale, or below its sale price", v.SKU)
	}
	if u := v.UnitPricing; u != nil && (u.Quantity <= 0 || u.Measure == "" || u.Base == "" || u.ShownUnitPriceMinor < 0) {
		return fmt.Errorf("variant %q has a unit pricing missing its quantity, measure or base", v.SKU)
	}
	if err := validCurrency(fmt.Sprintf("variant %q's currency", v.SKU), v.Currency); err != nil {
		return err
	}
	switch v.Availability {
	case AvailabilityInStock, AvailabilityOutOfStock, AvailabilityBackorder:
	case AvailabilityPreOrder:
		if v.AvailabilityDate == nil {
			return fmt.Errorf("variant %q is a preorder with no date it ships by", v.SKU)
		}
	default:
		return fmt.Errorf("variant %q has availability %q", v.SKU, v.Availability)
	}
	return nil
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// CatalogProvider is the shop. Return CatalogMethods(c) from your ServiceMethods and declare
// "provides": ["shop.catalog/v1"] with service_provider.
type CatalogProvider interface {
	DescribeCatalog(ctx context.Context) (CatalogDescription, error)
	CatalogPage(ctx context.Context, req CatalogPageRequest) (CatalogPage, error)
	CatalogProduct(ctx context.Context, req CatalogProductRequest) (CatalogProductResult, error)
}

// CatalogMethods are the shop's contract methods, each checking the request before the shop sees it and the answer
// before it leaves.
func CatalogMethods(c CatalogProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopCatalog, CatalogDescribe, func(ctx context.Context, _ struct{}) (CatalogDescription, error) {
			return checked(c.DescribeCatalog(ctx))
		}),
		Serves(ContractShopCatalog, CatalogProductsPage, func(ctx context.Context, req CatalogPageRequest) (CatalogPage, error) {
			if err := req.Validate(); err != nil {
				return CatalogPage{}, err
			}
			return checked(c.CatalogPage(ctx, req))
		}),
		Serves(ContractShopCatalog, CatalogProductGet, func(ctx context.Context, req CatalogProductRequest) (CatalogProductResult, error) {
			if err := req.Validate(); err != nil {
				return CatalogProductResult{}, err
			}
			return checked(c.CatalogProduct(ctx, req))
		}),
	}
}

// ---- a channel's side --------------------------------------------------------------------------------------

// catalogAnswer asks the shop one catalogue question: one shop, so the first answer is the answer and none is an
// error.
func (s *Services) catalogAnswer(ctx context.Context, method string, req any, into validator) error {
	res, err := s.Call(ctx, ContractShopCatalog, method, req, ServiceCallOptions{})
	if err != nil {
		return err
	}
	if len(res.Answers) == 0 {
		return fmt.Errorf("nilda: no shop answered %s %s", ContractShopCatalog, method)
	}
	if err := res.Answers[0].Decode(into); err != nil {
		return err
	}
	return into.Validate()
}

// CatalogDescription reads the shop as a channel needs to know it.
func (s *Services) CatalogDescription(ctx context.Context) (CatalogDescription, error) {
	var d CatalogDescription
	return d, s.catalogAnswer(ctx, CatalogDescribe, struct{}{}, &d)
}

// CatalogPage reads one page of the shop's published products.
func (s *Services) CatalogPage(ctx context.Context, req CatalogPageRequest) (CatalogPage, error) {
	if err := req.Validate(); err != nil {
		return CatalogPage{}, err
	}
	var p CatalogPage
	return p, s.catalogAnswer(ctx, CatalogProductsPage, req, &p)
}

// CatalogProduct reads one product.
func (s *Services) CatalogProduct(ctx context.Context, sku string) (CatalogProductResult, error) {
	req := CatalogProductRequest{SKU: sku}
	if err := req.Validate(); err != nil {
		return CatalogProductResult{}, err
	}
	var r CatalogProductResult
	return r, s.catalogAnswer(ctx, CatalogProductGet, req, &r)
}
