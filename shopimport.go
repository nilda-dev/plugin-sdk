package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// AN OLD SHOP, WRITTEN INTO THIS SHOP BY AN IMPORT PLUGIN (docs/SHOP_IMPORT.md; Core's D10, 2026-11-18). EXPERIMENTAL.
//
// The import plugin (site_import) reads an old shop — WooCommerce now, Shopify and Wix later — turns it into the NEUTRAL
// records below, the shop's own vocabulary and no platform's, and writes them through the shop's provider of
// shop.import/v1, one kind a batch, through Core's service desk. The shop keeps only this door; what a WooCommerce field
// means lives in the plugin.
//
//	shop.import/v1  the shop PROVIDES, an import plugin holding the `import` capability USES: settings.answer, records.put
//	                (an import call: Core marks it so the shop's own callbacks are quiet), records.list, publish.plan,
//	                publish.prices, publish.apply, publish.undo.
//
// Phase A only: the catalogue, the owner's act and the read. Customers, orders, gift cards and store credit (phase B)
// and subscriptions (phase C) are added to v1 additively, each when the shop's code that writes it lands: the contract
// never names a kind nobody writes.
//
// The bounds are the contract file's own (each field's `contract` tag, generated into the schema), so Core checks them
// before the shop is asked, the plugin's client before the call leaves, and Serve before the shop's method runs.

// ContractShopImport is the shop's import door.
const ContractShopImport = "shop.import/v1"

// Its methods.
const (
	ShopImportSettingsAnswer = "settings.answer"
	ShopImportRecordsPut     = "records.put"
	ShopImportRecordsList    = "records.list"
	ShopImportPublishPlan    = "publish.plan"
	ShopImportPublishPrices  = "publish.prices"
	ShopImportPublishApply   = "publish.apply"
	ShopImportPublishUndo    = "publish.undo"
)

// The kinds of record a batch carries (ShopImportBatch.Kind), one kind a batch.
const (
	ShopImportCategory  = "category"
	ShopImportTag       = "tag"
	ShopImportAttribute = "attribute"
	ShopImportProduct   = "product"
	ShopImportVariant   = "variant"
	ShopImportLinks     = "links"
	ShopImportReview    = "review"
	ShopImportCoupon    = "coupon"
)

var shopImportKinds = []string{ShopImportCategory, ShopImportTag, ShopImportAttribute, ShopImportProduct, ShopImportVariant,
	ShopImportLinks, ShopImportReview, ShopImportCoupon}

// ShopImportKinds returns every kind a batch may carry, in the order an import sends them: what a product names comes
// before it, a variant after its product, links, reviews and coupons after the products they name.
func ShopImportKinds() []string { return slices.Clone(shopImportKinds) }

// The platforms an old shop is read from (ShopImportBatch.Platform): what the losses' sentences name.
const (
	ShopImportWooCommerce = "woocommerce"
	ShopImportShopify     = "shopify"
	ShopImportWix         = "wix"
	ShopImportSquarespace = "squarespace"
	ShopImportCSV         = "csv"
)

// What became of one record (ShopImportResult.Outcome).
const (
	ShopImportCreated = "created"
	ShopImportUpdated = "updated"
	ShopImportKept    = "kept"    // changed here since the import wrote it: never overwritten
	ShopImportAlready = "already" // the same record, the same money: nothing to do
	ShopImportRefused = "refused" // written not at all; its losses say why
)

// Why a record waits (ShopImportResult.Hold): a held product or coupon is never published or switched on by the act.
const (
	ShopImportHoldNoPrice          = "no_price"
	ShopImportHoldCurrency         = "currency"
	ShopImportHoldDownloadMissing  = "download_missing"
	ShopImportHoldHidden           = "hidden"
	ShopImportHoldPassword         = "password"
	ShopImportHoldExternalURL      = "external_url"
	ShopImportHoldPageMissing      = "page_missing"
	ShopImportHoldCouponRestricted = "coupon_restricted"
	ShopImportHoldSKUTaken         = "sku_taken" // published only when the owner chooses keep_both for its pair
)

// A record's status at the old shop (ShopImportRecord.Status), in the shop's words: the plugin maps its platform's.
const (
	ShopImportPublished = "published"
	ShopImportDraft     = "draft"
	ShopImportPending   = "pending"
	ShopImportPrivate   = "private"
	ShopImportScheduled = "scheduled"
)

// The owner's money answer (ShopImportSettings.Money; D-M3-8), and the shop's decision (ShopImportSettingsResult.Decision).
const (
	ShopImportMoneySame     = "same"      // the two shops sell in one currency
	ShopImportMoneyAdopt    = "adopt"     // this shop is empty and takes the old currency at the act
	ShopImportMoneyConvert  = "convert"   // converted at the envelope's rate, rounded once
	ShopImportMoneyNoPrices = "no_prices" // also what an unanswered question means
	ShopImportMoneyAsk      = "ask"       // the shop's decision only: the currencies differ and the owner has not answered
)

// What records.list lists (ShopImportListRequest.Filter).
const (
	ShopImportListHeld      = "held"
	ShopImportListPairs     = "pairs"
	ShopImportListConverted = "converted"
	ShopImportListAll       = "all"
)

// The owner's choice for a product whose SKU met one of the owner's (ShopImportChoices.SKUPairs).
const (
	ShopImportKeepMine = "keep_mine" // the default: the old one stays a draft
	ShopImportKeepBoth = "keep_both"
)

// ShopImportUnmappedField is the loss code under which the shop counts the fields a record's Unmapped names.
const ShopImportUnmappedField = "unmapped_field"

// The contract's bounds (each also a field's `contract` tag).
const (
	MaxShopImportRecords  = 800   // records in one batch: a preview's batch is four times an apply's (the shop's N per kind)
	MaxShopImportUnmapped = 200   // source fields one record names as not carried
	MaxShopImportName     = 500   // characters of a name, a title, an alt text
	MaxShopImportText     = 65536 // characters of plain text: a description, an excerpt, a review, a note
	MaxShopImportRefs     = 10000 // records one list names: a coupon's products, a product's upsells
	MaxShopImportListPage = 100   // rows of records.list and publish.prices
	MaxShopImportPairs    = 1000  // SKU pairs and soon-starting sales publish.plan lists (its counts hold the totals)
)

// ---- the envelope --------------------------------------------------------------------------------------------------

// ShopImportSettings are the old shop's settings and the owner's answers, frozen in the plugin's job and sent unchanged
// in every batch of it.
type ShopImportSettings struct {
	// Currency is the old shop's, ISO 4217; empty when unstated.
	Currency string `json:"currency" contract:"pattern=currency_or_none"`
	// PricesIncludeTax is whether the old shop's prices included tax; null when unstated.
	PricesIncludeTax *bool  `json:"prices_include_tax"`
	WeightUnit       string `json:"weight_unit" contract:"enum=|kg|g|lbs|oz"`
	DimensionUnit    string `json:"dimension_unit" contract:"enum=|m|cm|mm|in|yd"`
	// From is "site" (read from the old shop) or "owner" (the owner typed them).
	From string `json:"from" contract:"enum=|site|owner"`
	// Money is the owner's answer (ShopImportMoney…); empty is unanswered, which means no_prices.
	Money string `json:"money" contract:"enum=|same|adopt|convert|no_prices"`
	// Convert is present only with Money convert.
	Convert *ShopImportConvert `json:"convert,omitempty"`
}

// ShopImportConvert is how the owner chose to convert the old prices. The shop applies ONLY this rate; it never reads one
// during an import.
type ShopImportConvert struct {
	// To is this shop's currency.
	To string `json:"to" contract:"pattern=currency"`
	// Rate is units of To per one unit of the old currency, an exact decimal; RateDay the ECB's day or the day the owner
	// typed it; RateSource ecb or owner.
	Rate       string `json:"rate" contract:"pattern=rate"`
	RateDay    string `json:"rate_day" contract:"pattern=date"`
	RateSource string `json:"rate_source" contract:"enum=ecb|owner"`
	// Ending is the owner's rounding: exact, or the price ending in .99, .95 or .00.
	Ending string `json:"ending" contract:"enum=exact|99|95|00"`
	// TaxClassRates is one VAT percent per tax class of the old catalogue ("" is the standard class), every figure the
	// owner's — only when the two shops' prices_include_tax differ.
	TaxClassRates map[string]string `json:"tax_class_rates,omitempty" contract:"props=100,pattern=percent"`
}

// ShopImportBatch is records.put: one batch of ONE kind. Apply false is the preview: the shop maps and looks up in a
// read-only transaction, rolled back, and writes nothing.
type ShopImportBatch struct {
	Import ImportCall `json:"import"`
	// Source is "<platform>:<host>"; source|kind|source_id keys what the shop keeps, so a batch sent again is harmless.
	Source   string             `json:"source" contract:"len=1-300,pattern=source"`
	Platform string             `json:"platform" contract:"enum=woocommerce|shopify|wix|squarespace|csv"`
	Apply    bool               `json:"apply,omitempty"`
	Locale   string             `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
	Settings ShopImportSettings `json:"settings"`
	Kind     string             `json:"kind" contract:"enum=category|tag|attribute|product|variant|links|review|coupon"`
	Records  []ShopImportRecord `json:"records" contract:"items=0-800"`
}

// ShopImportRecord is one record of a batch.
type ShopImportRecord struct {
	// SourceID is the record's id at the old shop, as the plugin names it ("shop.example.com#123"); every reference to it
	// from another record is this.
	SourceID string `json:"source_id" contract:"len=1-300,pattern=ref"`
	// ParentSourceID is a variant's product; empty for every other kind.
	ParentSourceID string `json:"parent_source_id,omitempty" contract:"len=0-300,pattern=ref"`
	// Status is the record's status at the old shop (ShopImportPublished…); empty where the kind has none.
	Status string `json:"status,omitempty" contract:"enum=|published|draft|pending|private|scheduled"`
	// Data is the kind's record: ShopImportCategoryData, …, ShopImportCouponData. The contract checks it against its kind.
	Data json.RawMessage `json:"data"`
	// Unmapped names every source field the plugin read and could not carry; the shop counts them into the answer's
	// losses as unmapped_field, so the report's field counts balance.
	Unmapped []string `json:"unmapped,omitempty" contract:"items=0-200,len=1-200,pattern=line"`
}

// Decode reads the record's data into out, one of the kinds' data types.
func (r ShopImportRecord) Decode(out any) error { return json.Unmarshal(r.Data, out) }

// ShopImportRecordOf is a record carrying data, one of the kinds' data types.
func ShopImportRecordOf(sourceID, status string, data any) (ShopImportRecord, error) {
	raw, err := encodeWire(data)
	if err != nil {
		return ShopImportRecord{}, fmt.Errorf("nilda: the data of %s does not encode: %w", sourceID, err)
	}
	return ShopImportRecord{SourceID: sourceID, Status: status, Data: raw}, nil
}

// Validate refuses what the contract's schema cannot say: a platform the source does not name, a source id twice in a
// batch, a variant without its product or another kind with one, a batch of the settings' convert without its money.
func (b ShopImportBatch) Validate() error {
	if err := b.Import.Validate(); err != nil {
		return err
	}
	if platform, _, _ := strings.Cut(b.Source, ":"); platform != b.Platform {
		return fmt.Errorf("nilda: a batch's source %q is not of its platform %q", b.Source, b.Platform)
	}
	if err := b.Settings.Validate(); err != nil {
		return err
	}
	seen := make(map[string]bool, len(b.Records))
	for _, r := range b.Records {
		switch {
		case seen[r.SourceID]:
			return fmt.Errorf("nilda: a batch names %s twice", r.SourceID)
		case b.Kind == ShopImportVariant && r.ParentSourceID == "":
			return fmt.Errorf("nilda: the variant %s names no parent product", r.SourceID)
		case b.Kind != ShopImportVariant && r.ParentSourceID != "":
			return fmt.Errorf("nilda: only a variant names a parent; %s is a %s", r.SourceID, b.Kind)
		}
		seen[r.SourceID] = true
	}
	return nil
}

// Validate refuses settings whose conversion and money answer disagree.
func (s ShopImportSettings) Validate() error {
	if (s.Money == ShopImportMoneyConvert) != (s.Convert != nil) {
		return errors.New("nilda: an import's settings carry convert exactly when its money answer is convert")
	}
	if s.Convert != nil && strings.Trim(strings.ReplaceAll(s.Convert.Rate, ".", ""), "0") == "" {
		return errors.New("nilda: an import's conversion rate is above zero")
	}
	return nil
}

// ---- the records: the shop's own vocabulary (money a decimal string in the settings' currency, "" none; times RFC 3339
// UTC, "" none; references to other records their source ids; text plain, except a product's body) ----------------------

// ShopImportCategoryData is a category: Parent the parent category's source id, "" at the top.
type ShopImportCategoryData struct {
	Name         string `json:"name" contract:"len=1-500,pattern=line"`
	Slug         string `json:"slug" contract:"len=0-200,pattern=line"`
	Parent       string `json:"parent,omitempty" contract:"len=0-300,pattern=ref"`
	Description  string `json:"description,omitempty" contract:"len=0-65536"`
	ImageMediaID string `json:"image_media_id,omitempty" contract:"len=0-100,pattern=media"`
	Position     int    `json:"position,omitempty" contract:"min=-1000000000,max=1000000000"`
}

// ShopImportTagData is a tag.
type ShopImportTagData struct {
	Name        string `json:"name" contract:"len=1-500,pattern=line"`
	Slug        string `json:"slug" contract:"len=0-200,pattern=line"`
	Description string `json:"description,omitempty" contract:"len=0-65536"`
}

// ShopImportAttributeData is a shared axis many products choose from (WooCommerce's global pa_* attributes).
type ShopImportAttributeData struct {
	Name  string                    `json:"name" contract:"len=1-500,pattern=line"`
	Slug  string                    `json:"slug" contract:"len=0-200,pattern=line"`
	Terms []ShopImportAttributeTerm `json:"terms" contract:"items=0-1000"`
}

// ShopImportAttributeTerm is one value of a shared axis.
type ShopImportAttributeTerm struct {
	Name     string `json:"name" contract:"len=1-500,pattern=line"`
	Slug     string `json:"slug" contract:"len=0-200,pattern=line"`
	Position int    `json:"position,omitempty" contract:"min=-1000000000,max=1000000000"`
}

// ShopImportImage is a picture: a Core media id the plugin made before the call (the shop never fetches an address).
type ShopImportImage struct {
	MediaID string `json:"media_id" contract:"len=1-100,pattern=media"`
	Alt     string `json:"alt,omitempty" contract:"len=0-500,pattern=line"`
}

// ShopImportDownload is a file a buyer downloads: a Core media id the plugin uploaded born private.
type ShopImportDownload struct {
	MediaID string `json:"media_id" contract:"len=1-100,pattern=media"`
	Name    string `json:"name,omitempty" contract:"len=0-500,pattern=line"`
}

// ShopImportAxis is one of a product's choice axes; Attribute is the shared attribute's source id, "" for the product's
// own.
type ShopImportAxis struct {
	Name      string   `json:"name" contract:"len=1-500,pattern=line"`
	Slug      string   `json:"slug,omitempty" contract:"len=0-200,pattern=line"`
	Attribute string   `json:"attribute,omitempty" contract:"len=0-300,pattern=ref"`
	Position  int      `json:"position,omitempty" contract:"min=-1000000000,max=1000000000"`
	Visible   bool     `json:"visible,omitempty"`
	Values    []string `json:"values" contract:"items=0-1000,len=1-500,pattern=line"`
}

// ShopImportChoice is one axis's value: a product's default choice, or a variant's ("" = any).
type ShopImportChoice struct {
	Axis  string `json:"axis" contract:"len=1-500,pattern=line"`
	Value string `json:"value" contract:"len=0-500,pattern=line"`
}

// ShopImportProductData is a product. Weight and dimensions are already in the settings' units; Body is a Core editor
// document the plugin made, Excerpt plain text; the old address is Permalink (for its redirect).
type ShopImportProductData struct {
	Name              string `json:"name" contract:"len=0-500,pattern=line"`
	Slug              string `json:"slug" contract:"len=0-200,pattern=line"`
	Kind              string `json:"kind" contract:"enum=simple|variable|digital|external|grouped"`
	Featured          bool   `json:"featured,omitempty"`
	Visibility        string `json:"visibility" contract:"enum=everywhere|catalog|search|hidden"`
	PasswordProtected bool   `json:"password_protected,omitempty"`
	// Body is the description as a Core editor document (an object); absent when the product has none.
	Body    json.RawMessage `json:"body,omitempty"`
	Excerpt string          `json:"excerpt,omitempty" contract:"len=0-65536"`
	SKU     string          `json:"sku,omitempty" contract:"len=0-100,pattern=line"`
	GTIN    string          `json:"gtin,omitempty" contract:"len=0-100,pattern=line"`
	MPN     string          `json:"mpn,omitempty" contract:"len=0-100,pattern=line"`
	ShopImportPricing
	ShopImportStock
	ShopImportParcel
	SoldIndividually bool     `json:"sold_individually,omitempty"`
	ReviewsAllowed   bool     `json:"reviews_allowed,omitempty"`
	PurchaseNote     string   `json:"purchase_note,omitempty" contract:"len=0-65536"`
	Position         int      `json:"position,omitempty" contract:"min=-1000000000,max=1000000000"`
	Categories       []string `json:"categories,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	Tags             []string `json:"tags,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	// Brands are brand names: the shop keeps one and names the rest (no brand is a record of its own).
	Brands        []string             `json:"brands,omitempty" contract:"items=0-100,len=1-500,pattern=line"`
	Images        []ShopImportImage    `json:"images,omitempty" contract:"items=0-100"`
	Axes          []ShopImportAxis     `json:"axes,omitempty" contract:"items=0-100"`
	DefaultChoice []ShopImportChoice   `json:"default_choice,omitempty" contract:"items=0-100"`
	Downloads     []ShopImportDownload `json:"downloads,omitempty" contract:"items=0-100"`
	DownloadLimit int                  `json:"download_limit,omitempty" contract:"min=-1,max=1000000000"`
	DownloadDays  int                  `json:"download_days,omitempty" contract:"min=-1,max=1000000000"`
	ExternalURL   string               `json:"external_url,omitempty" contract:"len=0-2048,pattern=line"`
	ButtonText    string               `json:"button_text,omitempty" contract:"len=0-500,pattern=line"`
	Permalink     string               `json:"permalink,omitempty" contract:"len=0-2048,pattern=line"`
	CreatedAt     string               `json:"created_at,omitempty" contract:"pattern=time"`
}

// ShopImportPricing is a product's or a variant's prices and tax.
type ShopImportPricing struct {
	RegularPrice string `json:"regular_price,omitempty" contract:"pattern=money"`
	SalePrice    string `json:"sale_price,omitempty" contract:"pattern=money"`
	SaleFrom     string `json:"sale_from,omitempty" contract:"pattern=time"`
	SaleTo       string `json:"sale_to,omitempty" contract:"pattern=time"`
	// TaxStatus is what the tax is charged on: taxable (the item; "" is taxable too, so a record that does not say is
	// never sold untaxed), shipping_only (only its delivery) or none.
	TaxStatus string `json:"tax_status,omitempty" contract:"enum=|taxable|shipping_only|none"`
	TaxClass  string `json:"tax_class,omitempty" contract:"len=0-200,pattern=line"`
}

// ShopImportStock is a product's or a variant's stock.
type ShopImportStock struct {
	ManageStock   bool   `json:"manage_stock,omitempty"`
	StockQuantity int64  `json:"stock_quantity,omitempty" contract:"min=-1000000000,max=1000000000"`
	StockStatus   string `json:"stock_status,omitempty" contract:"enum=|in|out|backorder"`
	Backorders    string `json:"backorders,omitempty" contract:"enum=|no|notify|yes"`
	LowStockAt    int64  `json:"low_stock_at,omitempty" contract:"min=0,max=1000000000"`
}

// ShopImportParcel is a product's or a variant's weight, dimensions and shipping class: decimals in the settings' units.
// Virtual is an item nothing is delivered for (a service, a booking, a ticket): it asks no address and no shipping.
type ShopImportParcel struct {
	Virtual       bool   `json:"virtual,omitempty"`
	Weight        string `json:"weight,omitempty" contract:"pattern=measure"`
	Length        string `json:"length,omitempty" contract:"pattern=measure"`
	Width         string `json:"width,omitempty" contract:"pattern=measure"`
	Height        string `json:"height,omitempty" contract:"pattern=measure"`
	ShippingClass string `json:"shipping_class,omitempty" contract:"len=0-500,pattern=line"`
}

// ShopImportVariantData is one of a variable product's variants (the record's ParentSourceID is the product). The plugin
// RESOLVED what the variant inherits from its product: the shop never sees "same as the parent".
type ShopImportVariantData struct {
	SKU         string `json:"sku,omitempty" contract:"len=0-100,pattern=line"`
	GTIN        string `json:"gtin,omitempty" contract:"len=0-100,pattern=line"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty" contract:"len=0-65536"`
	ShopImportPricing
	ShopImportStock
	ShopImportParcel
	ImageMediaID string `json:"image_media_id,omitempty" contract:"len=0-100,pattern=media"`
	// Choice names a value of EVERY axis of the product; "" is any.
	Choice        []ShopImportChoice   `json:"choice" contract:"items=0-100"`
	Downloads     []ShopImportDownload `json:"downloads,omitempty" contract:"items=0-100"`
	DownloadLimit int                  `json:"download_limit,omitempty" contract:"min=-1,max=1000000000"`
	DownloadDays  int                  `json:"download_days,omitempty" contract:"min=-1,max=1000000000"`
	Position      int                  `json:"position,omitempty" contract:"min=-1000000000,max=1000000000"`
}

// ShopImportLinksData is what a product (the record's source id) leads to: other products' source ids.
type ShopImportLinksData struct {
	Upsells    []string `json:"upsells,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	CrossSells []string `json:"cross_sells,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	Grouped    []string `json:"grouped,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
}

// ShopImportReviewData is a review, which arrives unverified and is labelled "From our previous shop". It NEVER carries
// an e-mail, an avatar, an IP address, a user id or "verified".
type ShopImportReviewData struct {
	Product string `json:"product" contract:"len=1-300,pattern=ref"`
	Status  string `json:"status" contract:"enum=approved|pending"`
	Author  string `json:"author,omitempty" contract:"len=0-500,pattern=line"`
	Body    string `json:"body,omitempty" contract:"len=0-65536"`
	// Rating is 1 to 5, 0 for none.
	Rating    int    `json:"rating,omitempty" contract:"min=0,max=5"`
	CreatedAt string `json:"created_at,omitempty" contract:"pattern=time"`
	// Replies is how many replies it had (not carried, counted), -1 when the old shop could not say.
	Replies int `json:"replies,omitempty" contract:"min=-1,max=1000000000"`
}

// ShopImportCouponData is a discount code. It NEVER carries the e-mails it was limited to or who used it; a code limited
// to e-mails (HadEmailRestriction) or of a kind the plugin could not be sure of (KindUncertain) is held
// coupon_restricted.
type ShopImportCouponData struct {
	Code    string `json:"code" contract:"len=1-200,pattern=line"`
	Enabled bool   `json:"enabled"`
	// Kind is percent (off the goods), fixed_order (off the basket) or fixed_unit (off each item); Amount its figure.
	Kind                  string   `json:"kind" contract:"enum=percent|fixed_order|fixed_unit"`
	Amount                string   `json:"amount" contract:"pattern=money"`
	FreeShipping          bool     `json:"free_shipping,omitempty"`
	ExpiresAt             string   `json:"expires_at,omitempty" contract:"pattern=time"`
	Used                  int      `json:"used,omitempty" contract:"min=0,max=1000000000"`
	UsageLimit            int      `json:"usage_limit,omitempty" contract:"min=0,max=1000000000"`
	UsageLimitPerCustomer int      `json:"usage_limit_per_customer,omitempty" contract:"min=0,max=1000000000"`
	MaxItems              int      `json:"max_items,omitempty" contract:"min=0,max=1000000000"`
	Combinable            bool     `json:"combinable,omitempty"`
	Products              []string `json:"products,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	ExcludedProducts      []string `json:"excluded_products,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	Categories            []string `json:"categories,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	ExcludedCategories    []string `json:"excluded_categories,omitempty" contract:"items=0-10000,len=1-300,pattern=ref"`
	ExcludeSaleItems      bool     `json:"exclude_sale_items,omitempty"`
	MinSpend              string   `json:"min_spend,omitempty" contract:"pattern=money"`
	MaxSpend              string   `json:"max_spend,omitempty" contract:"pattern=money"`
	Description           string   `json:"description,omitempty" contract:"len=0-65536"`
	HadEmailRestriction   bool     `json:"had_email_restriction,omitempty"`
	KindUncertain         bool     `json:"kind_uncertain,omitempty"`
}

// ---- the answers ---------------------------------------------------------------------------------------------------

// ShopImportLoss is one thing a record, the settings or the act lost, in the batch's locale; Fields names the source
// fields of an unmapped_field loss.
type ShopImportLoss struct {
	Code     string   `json:"code" contract:"len=1-100,pattern=line"`
	Count    int      `json:"count" contract:"min=0,max=1000000000"`
	Sentence string   `json:"sentence" contract:"len=0-2000"`
	Fields   []string `json:"fields,omitempty" contract:"items=0-200,len=1-200,pattern=line"`
}

// ShopImportRefs are what the shop made for a record: a product's page, a term, and with hold sku_taken the owner's
// product whose SKU it met.
type ShopImportRefs struct {
	ContentID     string `json:"content_id,omitempty" contract:"len=0-100,pattern=media"`
	ContentType   string `json:"content_type,omitempty" contract:"len=0-100,pattern=line"`
	Slug          string `json:"slug,omitempty" contract:"len=0-200,pattern=line"`
	TermID        string `json:"term_id,omitempty" contract:"len=0-100,pattern=media"`
	Taxonomy      string `json:"taxonomy,omitempty" contract:"len=0-100,pattern=line"`
	PairContentID string `json:"pair_content_id,omitempty" contract:"len=0-100,pattern=media"`
}

// ShopImportResult is one record's answer.
type ShopImportResult struct {
	SourceID string           `json:"source_id" contract:"len=1-300,pattern=ref"`
	Outcome  string           `json:"outcome" contract:"enum=created|updated|kept|already|refused"`
	Refs     ShopImportRefs   `json:"refs"`
	Hold     string           `json:"hold,omitempty" contract:"enum=|no_price|currency|download_missing|hidden|password|external_url|page_missing|coupon_restricted|sku_taken"`
	Losses   []ShopImportLoss `json:"losses" contract:"items=0-1000"`
}

// ShopImportDebug is what a batch cost: the Core requests its records made.
type ShopImportDebug struct {
	CoreRequests int `json:"core_requests" contract:"min=0,max=1000000000"`
	Records      int `json:"records" contract:"min=0,max=1000000000"`
}

// ShopImportAnswer is records.put's answer: a result per record of the batch, in its order.
type ShopImportAnswer struct {
	Kind    string             `json:"kind" contract:"enum=category|tag|attribute|product|variant|links|review|coupon"`
	Results []ShopImportResult `json:"results" contract:"items=0-800"`
	Debug   *ShopImportDebug   `json:"debug,omitempty"`
}

// ValidateFor refuses an answer that is not about the batch sent: another kind, a result for a record it did not carry,
// one twice, or one missing — a result the plugin cannot pin to a record is a record it cannot report.
func (a ShopImportAnswer) ValidateFor(b ShopImportBatch) error {
	if a.Kind != b.Kind {
		return fmt.Errorf("nilda: the shop answered a batch of %s with %s", b.Kind, a.Kind)
	}
	sent := make(map[string]bool, len(b.Records))
	for _, r := range b.Records {
		sent[r.SourceID] = true
	}
	for _, r := range a.Results {
		if !sent[r.SourceID] {
			return fmt.Errorf("nilda: the shop answered %s, which the batch did not carry or named twice", r.SourceID)
		}
		delete(sent, r.SourceID)
	}
	if len(sent) > 0 {
		return fmt.Errorf("nilda: the shop answered %d of the batch's %d records", len(a.Results), len(b.Records))
	}
	return nil
}

// ShopImportSettingsRequest is settings.answer: what the shop would do with an old shop's settings.
type ShopImportSettingsRequest struct {
	Source   string             `json:"source" contract:"len=1-300,pattern=source"`
	Platform string             `json:"platform" contract:"enum=woocommerce|shopify|wix|squarespace|csv"`
	Locale   string             `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
	Settings ShopImportSettings `json:"settings"`
}

// ShopImportRate is the European Central Bank's reference rate of the last published day, read by the shop.
type ShopImportRate struct {
	Rate   string `json:"rate" contract:"pattern=rate"`
	Day    string `json:"day" contract:"pattern=date"`
	Source string `json:"source" contract:"enum=ecb"`
}

// ShopImportSettingsResult is settings.answer's answer: the money decision (ask when the currencies differ and the owner
// has not answered), the ECB's rate for the pair when it has one, whether the two shops' prices_include_tax differ,
// this shop's currency and whether it is empty (only an empty shop may adopt the old currency).
type ShopImportSettingsResult struct {
	Decision          string          `json:"decision" contract:"enum=same|adopt|convert|no_prices|ask"`
	ECBRate           *ShopImportRate `json:"ecb_rate,omitempty"`
	TaxIncludeDiffers bool            `json:"tax_include_differs"`
	// Currency is this shop's, empty while it has not chosen one (a new shop, which may adopt the old one).
	Currency string           `json:"currency" contract:"pattern=currency_or_none"`
	Empty    bool             `json:"empty"`
	Losses   []ShopImportLoss `json:"losses" contract:"items=0-100"`
}

// ShopImportListRequest is records.list: what imports brought, held, paired and converted, newest first, a page at a time.
type ShopImportListRequest struct {
	// Source is one old shop; empty is every import.
	Source   string `json:"source,omitempty" contract:"len=0-300,pattern=source_or_none"`
	ImportID string `json:"import_id,omitempty" contract:"len=0-200,pattern=importid_or_none"`
	Filter   string `json:"filter" contract:"enum=held|pairs|converted|all"`
	Cursor   string `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
	Limit    int    `json:"limit,omitempty" contract:"min=0,max=100"`
	Locale   string `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
}

// ShopImportProductRef names a product on one side of a SKU pair.
type ShopImportProductRef struct {
	Name      string `json:"name" contract:"len=0-500,pattern=line"`
	SKU       string `json:"sku" contract:"len=0-100,pattern=line"`
	ContentID string `json:"content_id" contract:"len=0-100,pattern=media"`
}

// ShopImportPair is a product of the old shop whose SKU met one of the owner's.
type ShopImportPair struct {
	Theirs ShopImportProductRef `json:"theirs"`
	Ours   ShopImportProductRef `json:"ours"`
}

// ShopImportSourceMoney is the old shop's money AS READ, kept so a later run never converts twice: the currency, every
// price as typed by field name, and the conversion when there was one.
type ShopImportSourceMoney struct {
	Currency   string            `json:"currency" contract:"pattern=currency_or_none"`
	Prices     map[string]string `json:"prices" contract:"props=16,pattern=money"`
	Rate       string            `json:"rate,omitempty" contract:"pattern=rate_or_none"`
	RateDay    string            `json:"rate_day,omitempty" contract:"pattern=date_or_none"`
	RateSource string            `json:"rate_source,omitempty" contract:"enum=|ecb|owner"`
	Ending     string            `json:"ending,omitempty" contract:"enum=|exact|99|95|00"`
}

// ShopImportRow is one record an import brought.
type ShopImportRow struct {
	Kind     string `json:"kind" contract:"enum=category|tag|attribute|product|variant|links|review|coupon"`
	Source   string `json:"source" contract:"len=1-300,pattern=source"`
	SourceID string `json:"source_id" contract:"len=1-300,pattern=ref"`
	Outcome  string `json:"outcome" contract:"enum=created|updated|kept|already|refused"`
	Hold     string `json:"hold,omitempty" contract:"enum=|no_price|currency|download_missing|hidden|password|external_url|page_missing|coupon_restricted|sku_taken"`
	// Sentence says, in the request's locale, why it is held or what was lost.
	Sentence    string                 `json:"sentence,omitempty" contract:"len=0-2000"`
	Pair        *ShopImportPair        `json:"pair,omitempty"`
	SourceMoney *ShopImportSourceMoney `json:"source_money,omitempty"`
	// PublishedBy is the act's marker that published (a product) or switched on (a coupon) it; empty while it is not.
	PublishedBy string `json:"published_by,omitempty" contract:"len=0-200,pattern=importid_or_none"`
}

// ShopImportListAnswer is a page of records.list; Cursor asks the next, empty after the last.
type ShopImportListAnswer struct {
	Rows   []ShopImportRow `json:"rows" contract:"items=0-100"`
	Cursor string          `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
}

// ShopImportActRequest is publish.plan and publish.prices: the act of one old shop's import, described before the owner
// approves it.
type ShopImportActRequest struct {
	Source string `json:"source" contract:"len=1-300,pattern=source"`
	Locale string `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
	// Cursor pages publish.prices; publish.plan ignores it.
	Cursor string `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
}

// ShopImportCounts are what the act would publish, hold and keep.
type ShopImportCounts struct {
	Products  int `json:"products" contract:"min=0,max=1000000000"`
	Coupons   int `json:"coupons" contract:"min=0,max=1000000000"`
	Held      int `json:"held" contract:"min=0,max=1000000000"`
	Kept      int `json:"kept" contract:"min=0,max=1000000000"`
	Converted int `json:"converted" contract:"min=0,max=1000000000"`
	PerPerson int `json:"per_person" contract:"min=0,max=1000000000"`
	SKUPairs  int `json:"sku_pairs" contract:"min=0,max=1000000000"`
}

// ShopImportSKUPair is one pair the owner chooses for at the act.
type ShopImportSKUPair struct {
	SourceID string `json:"source_id" contract:"len=1-300,pattern=ref"`
	ShopImportPair
	// LooksSame is the shop's hint that the two are one product (the same barcode, or the same name once case, width and
	// spacing are set aside); the owner chooses, nothing is merged on it.
	LooksSame bool `json:"looks_same"`
}

// ShopImportSaleSoon is an imported sale that starts less than 30 days after the act: kept and named, never dropped.
type ShopImportSaleSoon struct {
	SourceID string `json:"source_id" contract:"len=1-300,pattern=ref"`
	Name     string `json:"name" contract:"len=0-500,pattern=line"`
	Starts   string `json:"starts" contract:"pattern=time"`
}

// ShopImportGroups are the act's groups: converted products (published only on the owner's tick, unticked by default),
// used once-per-customer coupons (switched on unless the owner unticks them), SKU pairs, and sales starting soon.
type ShopImportGroups struct {
	Converted     int                  `json:"converted" contract:"min=0,max=1000000000"`
	PerPerson     int                  `json:"per_person" contract:"min=0,max=1000000000"`
	PerPersonUses int                  `json:"per_person_uses" contract:"min=0,max=1000000000"`
	SKUPairs      []ShopImportSKUPair  `json:"sku_pairs" contract:"items=0-1000"`
	SaleSoon      []ShopImportSaleSoon `json:"sale_soon" contract:"items=0-1000"`
}

// ShopImportShopSettings are the shop's currency and whether its prices include tax: what the act sets on an empty shop
// that adopts the old currency (publish.apply names a currency), and what it replaced (publish.plan's settings_before,
// whose currency is empty when the shop had not chosen one).
type ShopImportShopSettings struct {
	Currency         string `json:"currency" contract:"pattern=currency_or_none"`
	PricesIncludeTax bool   `json:"prices_include_tax"`
}

// ShopImportPlan is publish.plan's answer: what publish.apply would do now.
type ShopImportPlan struct {
	Counts         ShopImportCounts        `json:"counts"`
	Groups         ShopImportGroups        `json:"groups"`
	SettingsBefore *ShopImportShopSettings `json:"settings_before,omitempty"`
	Losses         []ShopImportLoss        `json:"losses" contract:"items=0-100"`
}

// ShopImportPriceRow is one converted product's old and new price, written with their currencies ("EUR 19.99").
type ShopImportPriceRow struct {
	SourceID string `json:"source_id" contract:"len=1-300,pattern=ref"`
	Name     string `json:"name" contract:"len=0-500,pattern=line"`
	SKU      string `json:"sku,omitempty" contract:"len=0-100,pattern=line"`
	Old      string `json:"old" contract:"len=0-40,pattern=line"`
	New      string `json:"new" contract:"len=0-40,pattern=line"`
	SaleOld  string `json:"sale_old,omitempty" contract:"len=0-40,pattern=line"`
	SaleNew  string `json:"sale_new,omitempty" contract:"len=0-40,pattern=line"`
}

// ShopImportPrices is a page of publish.prices; Cursor asks the next, empty after the last.
type ShopImportPrices struct {
	Rows   []ShopImportPriceRow `json:"rows" contract:"items=0-100"`
	Cursor string               `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
}

// ShopImportChoices are the owner's choices at the act: PublishConverted (default false: converted products stay drafts
// without "I checked the converted prices"), SwitchOnPerPerson (default true), and per SKU pair keep_mine (the default
// for a pair not named) or keep_both. A source id that is not one of the plan's pairs is refused by name.
type ShopImportChoices struct {
	PublishConverted  bool              `json:"publish_converted"`
	SwitchOnPerPerson bool              `json:"switch_on_per_person"`
	SKUPairs          map[string]string `json:"sku_pairs,omitempty" contract:"props=1000,enum=keep_mine|keep_both"`
}

// ShopImportApplyRequest is publish.apply: the owner's act, one page at a time under the act's marker.
type ShopImportApplyRequest struct {
	Source string `json:"source" contract:"len=1-300,pattern=source"`
	// Marker is the act's, chosen by the caller once for the whole act and sent with every page and with the undo.
	Marker string `json:"marker" contract:"pattern=importid"`
	// Settings are set on an empty shop that adopts the old currency; absent otherwise.
	Settings *ShopImportShopSettings `json:"settings,omitempty"`
	Choices  ShopImportChoices       `json:"choices"`
	Cursor   string                  `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
	Locale   string                  `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
}

// ShopImportUndoRequest is publish.undo: takes back exactly what an act published, from what each record held before it.
type ShopImportUndoRequest struct {
	Source string `json:"source" contract:"len=1-300,pattern=source"`
	Marker string `json:"marker" contract:"pattern=importid"`
	Cursor string `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
	Locale string `json:"locale,omitempty" contract:"len=0-35,pattern=locale"`
}

// ShopImportActPage is one page of publish.apply or publish.undo: what it published (or took back), held, kept and
// failed; Done after the last page, else Cursor asks the next.
type ShopImportActPage struct {
	Done     bool             `json:"done"`
	Cursor   string           `json:"cursor,omitempty" contract:"len=0-200,pattern=line"`
	Products int              `json:"products" contract:"min=0,max=1000000000"`
	Coupons  int              `json:"coupons" contract:"min=0,max=1000000000"`
	Held     int              `json:"held" contract:"min=0,max=1000000000"`
	Kept     int              `json:"kept" contract:"min=0,max=1000000000"`
	Failed   int              `json:"failed" contract:"min=0,max=1000000000"`
	Losses   []ShopImportLoss `json:"losses" contract:"items=0-100"`
}

// Validate refuses an act whose settings name no currency: an act sets the old currency on an empty shop, or none.
func (r ShopImportApplyRequest) Validate() error {
	if r.Settings != nil && r.Settings.Currency == "" {
		return errors.New("nilda: an act's settings name the currency it sets")
	}
	return nil
}

// Validate refuses a page that is done and asks for another, or is not done and cannot.
func (p ShopImportActPage) Validate() error {
	if p.Done == (p.Cursor != "") {
		return errors.New("nilda: an act's page names the next page's cursor exactly when it is not done")
	}
	return nil
}

// ---- the shop's side -----------------------------------------------------------------------------------------------

// ShopImportProvider is the shop. Every method runs after Serve checked the request against the contract, and its answer
// is checked before it leaves.
type ShopImportProvider interface {
	ShopImportSettings(ctx context.Context, req ShopImportSettingsRequest) (ShopImportSettingsResult, error)
	PutShopImportRecords(ctx context.Context, batch ShopImportBatch) (ShopImportAnswer, error)
	ShopImportRecords(ctx context.Context, req ShopImportListRequest) (ShopImportListAnswer, error)
	PlanShopImport(ctx context.Context, req ShopImportActRequest) (ShopImportPlan, error)
	ShopImportPrices(ctx context.Context, req ShopImportActRequest) (ShopImportPrices, error)
	ApplyShopImport(ctx context.Context, req ShopImportApplyRequest) (ShopImportActPage, error)
	UndoShopImport(ctx context.Context, req ShopImportUndoRequest) (ShopImportActPage, error)
}

// ShopImportMethods are the shop's import methods, each holding the request to the rules the schema cannot say before the
// shop sees it, and the answer after.
func ShopImportMethods(p ShopImportProvider) []ServiceMethod {
	act := func(fn func(context.Context, ShopImportApplyRequest) (ShopImportActPage, error)) func(context.Context, ShopImportApplyRequest) (ShopImportActPage, error) {
		return func(ctx context.Context, req ShopImportApplyRequest) (ShopImportActPage, error) {
			a, err := fn(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}
	}
	return []ServiceMethod{
		Serves(ContractShopImport, ShopImportSettingsAnswer, func(ctx context.Context, req ShopImportSettingsRequest) (ShopImportSettingsResult, error) {
			if err := sourceOfPlatform(req.Source, req.Platform); err != nil {
				return ShopImportSettingsResult{}, err
			}
			if err := req.Settings.Validate(); err != nil {
				return ShopImportSettingsResult{}, err
			}
			return p.ShopImportSettings(ctx, req)
		}),
		Serves(ContractShopImport, ShopImportRecordsPut, func(ctx context.Context, b ShopImportBatch) (ShopImportAnswer, error) {
			if err := b.Validate(); err != nil {
				return ShopImportAnswer{}, err
			}
			a, err := p.PutShopImportRecords(ctx, b)
			if err != nil {
				return a, err
			}
			return a, a.ValidateFor(b)
		}),
		Serves(ContractShopImport, ShopImportRecordsList, p.ShopImportRecords),
		Serves(ContractShopImport, ShopImportPublishPlan, p.PlanShopImport),
		Serves(ContractShopImport, ShopImportPublishPrices, p.ShopImportPrices),
		Serves(ContractShopImport, ShopImportPublishApply, act(func(ctx context.Context, req ShopImportApplyRequest) (ShopImportActPage, error) {
			if err := req.Validate(); err != nil {
				return ShopImportActPage{}, err
			}
			return p.ApplyShopImport(ctx, req)
		})),
		Serves(ContractShopImport, ShopImportPublishUndo, func(ctx context.Context, req ShopImportUndoRequest) (ShopImportActPage, error) {
			a, err := p.UndoShopImport(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}),
	}
}

func sourceOfPlatform(source, platform string) error {
	if p, _, _ := strings.Cut(source, ":"); p != platform {
		return fmt.Errorf("nilda: the source %q is not of the platform %q", source, platform)
	}
	return nil
}

// ---- an import plugin's side ---------------------------------------------------------------------------------------

// ShopImportSettings asks the shop what it would do with an old shop's settings. provider names the shop, "" the only
// one.
func (s *Services) ShopImportSettings(ctx context.Context, provider string, req ShopImportSettingsRequest) (ShopImportSettingsResult, error) {
	var a ShopImportSettingsResult
	if err := sourceOfPlatform(req.Source, req.Platform); err != nil {
		return a, err
	}
	if err := req.Settings.Validate(); err != nil {
		return a, err
	}
	return a, shopImportCall(ctx, s, ShopImportSettingsAnswer, provider, req, &a)
}

// PutShopImportRecords writes one batch into the shop — an import call: the plugin must hold the `import` capability,
// and Core relays it to one shop only. Sending a batch again is harmless: the shop answers already or kept.
func (s *Services) PutShopImportRecords(ctx context.Context, provider string, b ShopImportBatch) (ShopImportAnswer, error) {
	var a ShopImportAnswer
	if err := b.Validate(); err != nil {
		return a, err
	}
	if err := shopImportCall(ctx, s, ShopImportRecordsPut, provider, b, &a); err != nil {
		return a, err
	}
	return a, a.ValidateFor(b)
}

// ShopImportRecords reads a page of what imports brought.
func (s *Services) ShopImportRecords(ctx context.Context, provider string, req ShopImportListRequest) (ShopImportListAnswer, error) {
	var a ShopImportListAnswer
	return a, shopImportCall(ctx, s, ShopImportRecordsList, provider, req, &a)
}

// PlanShopImport asks what the act would do now, for the owner to approve.
func (s *Services) PlanShopImport(ctx context.Context, provider string, req ShopImportActRequest) (ShopImportPlan, error) {
	var a ShopImportPlan
	return a, shopImportCall(ctx, s, ShopImportPublishPlan, provider, req, &a)
}

// ShopImportPrices reads a page of the converted products' old and new prices.
func (s *Services) ShopImportPrices(ctx context.Context, provider string, req ShopImportActRequest) (ShopImportPrices, error) {
	var a ShopImportPrices
	return a, shopImportCall(ctx, s, ShopImportPublishPrices, provider, req, &a)
}

// ApplyShopImport runs one page of the owner's act; ask again with the page's cursor until it is done.
func (s *Services) ApplyShopImport(ctx context.Context, provider string, req ShopImportApplyRequest) (ShopImportActPage, error) {
	var a ShopImportActPage
	if err := req.Validate(); err != nil {
		return a, err
	}
	if err := shopImportCall(ctx, s, ShopImportPublishApply, provider, req, &a); err != nil {
		return a, err
	}
	return a, a.Validate()
}

// UndoShopImport takes back one page of an act; ask again with the page's cursor until it is done.
func (s *Services) UndoShopImport(ctx context.Context, provider string, req ShopImportUndoRequest) (ShopImportActPage, error) {
	var a ShopImportActPage
	if err := shopImportCall(ctx, s, ShopImportPublishUndo, provider, req, &a); err != nil {
		return a, err
	}
	return a, a.Validate()
}

// shopImportCall asks one shop and reads its one answer: an import writes into one shop, never into two.
func shopImportCall(ctx context.Context, s *Services, method, provider string, in, out any) error {
	res, err := s.Call(ctx, ContractShopImport, method, in, ServiceCallOptions{Provider: provider})
	if err != nil {
		return err
	}
	if len(res.Answers) != 1 {
		return fmt.Errorf("nilda: %d shops answered %s %s; name the shop that takes the import", len(res.Answers), ContractShopImport, method)
	}
	return res.Answers[0].Decode(out)
}
