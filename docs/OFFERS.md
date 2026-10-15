# Offers at the cart, the checkout and after the purchase — `shop.offers/v1`

The conversion layer the leading shops sell as apps: a suggestion beside the basket, an **order bump** — one more item,
often at a discount, added with one tap on the checkout — and an offer on the page after the purchase. The owner's
offers and their numbers are a plugin's; the **shop** draws them, adds the item to the basket and applies the discount
**through its own pricing**, so tax, invoices and refunds treat it as any other discount. The plugin **provides**
`shop.offers/v1`; the shop **uses** it.

| Method | Asks | Answers |
|---|---|---|
| `offers.for` | `OffersRequest`: the stage (`OfferStageCart`, `OfferStageCheckout`, `OfferStageAfterPurchase`), the basket's lines (`OfferLine`: SKU, quantity, unit price, and the offer a line was added through), its currency and subtotal, the page's language, and for after the purchase the order's id | `OffersResult`: the `Offer`s to show, the first first — an id, the SKU offered, the owner's headline, `PercentOff` (0 to `OfferPercentMax`, 90) and `MaxUnits` (how many units get it) |
| `offers.confirm` | `OffersConfirmRequest`: the basket's lines, the offers on them, its currency and subtotal | `OffersConfirmResult`: the offers that still stand, with their terms now |

The shop asks `offers.confirm` whenever it prices a basket that holds an offer's line, so an offer the owner switched
off — or whose trigger the shopper took back out of the basket — gives no discount at the checkout. Several plugins may
provide the contract: `Services.Offers` returns every answer and leaves out a plugin that did not answer (an offer is a
suggestion); `Services.ConfirmOffers` returns the offers that stand by id, and an error when a plugin that is there did
not answer — the shop then waits rather than take the discount away. A provider that returns a nil list answers an empty one.

The shop never shows an offer for an item it cannot sell now, or one already in the basket. It records the offer on the
order line, and `shop.orders/v1` returns it (`ShopOrderLine.OfferID`), so an offers plugin counts what its offers sold.
Neither method is a tool of the site's assistant: the owner sets and reads offers through the offers plugin's own
abilities.

```go
// The plugin: Serve finds an OffersProvider by itself.
func (p *Offers) OffersFor(ctx context.Context, req nilda.OffersRequest) (nilda.OffersResult, error) { … }
func (p *Offers) ConfirmOffers(ctx context.Context, req nilda.OffersConfirmRequest) (nilda.OffersConfirmResult, error) { … }

// The shop: "uses": ["shop.offers/v1"], with service_consumer.
offers, err := core.API().Services().Offers(ctx, nilda.OffersRequest{Stage: nilda.OfferStageCheckout, …})
stand, err := core.API().Services().ConfirmOffers(ctx, nilda.OffersConfirmRequest{…})
```
