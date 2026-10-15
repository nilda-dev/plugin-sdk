# The shop's catalogue — `shop.catalog/v1`

A shopping channel needs the shop's products in a shape a channel can list: a number for the price and a currency,
availability per variant, the identifiers every channel matches a product by, the page and the pictures, and what
changed since it last asked. The shop **provides** `shop.catalog/v1`; a channel plugin **requires** it and reads it
through Core's service desk. A channel's own API — Google's Merchant API, a marketplace's, an AI agent platform's —
stays in the channel's plugin, so the channel changing is the channel plugin's release, never the shop's.

## The methods

| Method | Asks | Answers |
|---|---|---|
| `describe` | nothing | `CatalogDescription`: the shop's name and address, its currency, whether prices include tax, its country, the countries it ships to (`*` for everywhere), the return window in days, the language its text is in |
| `products.page` | `CatalogPageRequest`: `cursor`, `limit` (1–200, default 50), `updated_since` | `CatalogPage`: products, the oldest change first, and `next_cursor` ("" on the last page) |
| `product.get` | `CatalogProductRequest`: a product's SKU or one of its variants' | `CatalogProductResult`: `found`, and the product |

Every method is relayed by Core's service desk, checked against the contract's schema both ways, and fails the call
when the shop does not answer — a feed built without the catalogue would empty a channel. None is a tool of the site's
assistant: the owner reads and changes the catalogue through the shop's own abilities.

## A product

`CatalogProduct` carries the product's SKU, title, description, absolute page URL, picture URLs (the main one first),
brand, condition (`new`, `refurbished`, `used`), Google's product category as the merchant gave it, and `updated_at`;
and one `CatalogVariant` per purchasable variant — the item a channel lists: its SKU, title and options, the regular
price and the sale price now with the sale's end, the same two as a buyer pays them (`shown_price_minor`,
`shown_sale_price_minor`: with the shop's tax when its prices are entered without it — what a channel showing prices
with tax lists; from v0.19.0), the currency, the availability (`in_stock`, `out_of_stock`,
`backorder`) and the quantity when it is counted, GTIN and MPN, the weight, whether it ships, its own picture.

A variant on sale carries `shown_prior_price_minor` (from v0.23.0): the lowest price, as a buyer pays it, in the 30
days before the reduction began — the only price an EU channel may strike through (Directive 98/6/EC Art. 6a; Austria's
PrAG §9a). With none, a channel shows the sale price as the price and no reduction. Goods sold by weight, volume, length
or area carry `unit_pricing` — quantity, measure, the base the unit price is stated in, and that unit price as paid —
which an advertisement naming the price names too (Art. 3(4)).

With `updated_since`, only products that changed since — the text, a price, the stock, a variant — and products taken
off sale since, listed with `removed: true` and nothing else, so the channel withdraws them.

## In Go

```go
// The shop: Serve finds a CatalogProvider by itself.
func (s *Shop) DescribeCatalog(ctx context.Context) (nilda.CatalogDescription, error) { … }
func (s *Shop) CatalogPage(ctx context.Context, req nilda.CatalogPageRequest) (nilda.CatalogPage, error) { … }
func (s *Shop) CatalogProduct(ctx context.Context, req nilda.CatalogProductRequest) (nilda.CatalogProductResult, error) { … }

// A channel: "requires": ["shop.catalog/v1"], with service_consumer.
page, err := core.API().Services().CatalogPage(ctx, nilda.CatalogPageRequest{UpdatedSince: &lastRun})
```
