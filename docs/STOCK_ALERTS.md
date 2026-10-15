# Back-in-stock requests — `stock.alerts/v1`

A shopper on a sold-out item leaves an email and is told, once, when it can be bought again. The **form** is the
shop's product page: Core's add-to-cart widget draws "Notify me when available" beside a sold-out item's button while
the shop sets `CommerceEndpoints.StockAlert`. **Keeping the requests** — confirming each to the shopper, watching the
stock, sending the news, the owner's list — is a plugin's, the way Shopify, BigCommerce and WooCommerce ship it, as an
app or an extension beside the shop. The plugin **provides** `stock.alerts/v1`; the shop **uses** it.

| Method | Asks | Answers |
|---|---|---|
| `describe` | nothing | `StockAlertsDescription`: `collecting`, and the sentence the form shows beside the email box. Nothing answering is no form (fallback `skip`). |
| `subscribe` | `StockAlertRequest`: the variant's SKU, its product's, the title and page the mails name, the email, the shopper's language | `StockAlertResult`: `subscribed`, `already` or `refused`, with the sentence the shopper reads |

A request the checks refuse — not an email address, no page — is answered `refused` with the reason, never an error.
Neither method is a tool of the site's assistant: the owner reads and removes requests through the plugin's own
abilities.

```go
// The plugin: Serve finds a StockAlertProvider by itself.
func (p *Alerts) DescribeStockAlerts(ctx context.Context) (nilda.StockAlertsDescription, error) { … }
func (p *Alerts) SubscribeStockAlert(ctx context.Context, req nilda.StockAlertRequest) (nilda.StockAlertResult, error) { … }

// The shop: "uses": ["stock.alerts/v1"], with service_consumer.
d, ok, err := core.API().Services().StockAlertsDescription(ctx)
res, err := core.API().Services().SubscribeStockAlert(ctx, nilda.StockAlertRequest{…})
```
