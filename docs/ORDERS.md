# Orders and what a plugin delivers for them — `shop.orders/v1`, `shop.deliverables/v1`

A plugin that delivers what an order bought — a licence key, a gift card's code, an event ticket — needs two things
from the shop: to **read the order** it was told was paid, and to **hand back what it delivered** so the buyer finds it
where they look for their purchase. The shop provides the first and uses the second.

## `shop.orders/v1` — the shop provides, a delivering plugin requires

The shop's events carry only the order's id: `ecommerce.order_paid` (`order_id`, `total_minor`),
`ecommerce.order_refunded` (`order_id`, `amount_minor`, `partial`) and `ecommerce.order_cancelled` (`order_id`).
The plugin reads the rest here.

| Method | Asks | Answers |
|---|---|---|
| `order.get` | `ShopOrderRequest`: the order's id | `ShopOrderResult`: `found`, and the `ShopOrder` — reference, status, `paid`, the buyer's email and the language the shop writes to them in, currency, total, and each line's SKU, title, quantity and `quantity_refunded` |

One shop answers; nobody answering is an error (fallback `block`), and the plugin tries again at its next pass. The
buyer's email is personal data: a plugin that requires this contract says in its manifest's `processing` what it does
with it.

## `shop.deliverables/v1` — a delivering plugin provides, the shop uses

| Method | Asks | Answers |
|---|---|---|
| `describe` | nothing | `DeliverablesDescription`: the SKUs this plugin delivers. The shop sells them as **digital content**: the buyer is asked the EU waiver of the right of withdrawal at checkout, as for a download. |
| `order.items` | `DeliverablesRequest`: the order's id, and the language of the page | `DeliverablesResult`: the `DeliveredItem`s — SKU, title, `kind` (`key` or `link`), a label, the value, a one-sentence note, or `pending` with no value when it is owed and not delivered yet |

Several plugins may provide it, and every one is asked. `Services.DeliveredSKUs` returns an error when any provider
that is there did not answer — a checkout must not skip the waiver because one plugin was slow.
`Services.OrderDeliverables` returns what the others gave and counts the one it could not ask, so a page can say so.

The shop shows the items on its thank-you page and in the buyer's account. Values are drawn as text, never markup; a
`link` is an absolute address. Neither contract is a tool of the site's assistant: the owner reads orders through the
shop's abilities, and reads, resends and disables what was delivered through the delivering plugin's.

```go
// The delivering plugin: "requires": ["shop.orders/v1"], "provides": ["shop.deliverables/v1"].
res, err := core.API().Services().ShopOrder(ctx, orderID)
func (p *Keys) DescribeDeliverables(ctx context.Context) (nilda.DeliverablesDescription, error) { … }
func (p *Keys) OrderDeliverables(ctx context.Context, req nilda.DeliverablesRequest) (nilda.DeliverablesResult, error) { … }

// The shop: "provides": ["shop.orders/v1"], "uses": ["shop.deliverables/v1"].
func (s *Shop) ShopOrder(ctx context.Context, req nilda.ShopOrderRequest) (nilda.ShopOrderResult, error) { … }
items, missed, err := core.API().Services().OrderDeliverables(ctx, nilda.DeliverablesRequest{OrderID: id, Locale: "de"})
```
