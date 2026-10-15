# Business buyers: prices, terms and quotes — `shop.buyers/v1`

The wholesale starter the market ships (Shopify B2B, BigCommerce B2B Edition, the WooCommerce wholesale suites):
customer **groups** with their own **prices** — a percent off the catalogue, or a fixed price per item — **minimum
quantities** and a minimum order, **paying by invoice** on net terms, and **quotes** the owner prices. The groups,
members, prices and quotes are a plugin's; the **shop** asks for a signed-in buyer's terms and applies them through its
own pricing, so tax, invoices and refunds treat a group price as any other discount. The plugin **provides**
`shop.buyers/v1`; the shop **uses** it.

| Method | Asks | Answers |
|---|---|---|
| `buyer.terms` | `BuyerTermsRequest`: Core's id for the signed-in buyer, and the items whose prices are wanted | `BuyerTerms`: `member`; the group; `percent_off` on every item without a price of its own; `prices` (`BuyerPrice`, each in its currency); `min_qty` (`BuyerMinimum`) and `min_order_minor`; `pay_by_invoice` and `net_days`; `quotes` |
| `quote.request` | `QuoteRequestInput`: the buyer, their email and language, the basket's lines at list price (`QuoteLine`), a note, and the shop's page that opens a priced quote | `QuoteRequestResult`: the quote's id and the sentence the buyer reads |
| `quote.open` | `QuoteOpenRequest`: the token from the quote's link, and the signed-in buyer | `QuoteOpenResult`: the `Quote` — its lines at the quoted prices and when it expires — or not found |

No plugin keeping buyers is an ordinary shopper: `Services.BuyerTerms` answers `member: false` without an error. A
plugin that is there and did not answer IS an error — a member's basket priced as a stranger's would be the wrong
price. Prices are a member's for their own basket and checkout; a product page is drawn once for everyone and shows
the public price. A quote's prices reach the basket through `buyer.terms` too (a quote the buyer accepted is their price
for its items until it expires). Neither method is a tool of the site's assistant: the owner manages groups, prices
and quotes through the B2B plugin's own abilities.

```go
// The plugin: Serve finds a BuyersProvider by itself.
func (p *B2B) BuyerTerms(ctx context.Context, req nilda.BuyerTermsRequest) (nilda.BuyerTerms, error) { … }
func (p *B2B) RequestQuote(ctx context.Context, req nilda.QuoteRequestInput) (nilda.QuoteRequestResult, error) { … }
func (p *B2B) OpenQuote(ctx context.Context, req nilda.QuoteOpenRequest) (nilda.QuoteOpenResult, error) { … }

// The shop: "uses": ["shop.buyers/v1"], with service_consumer.
terms, err := core.API().Services().BuyerTerms(ctx, nilda.BuyerTermsRequest{UserID: viewer.UserID, SKUs: skus})
```
