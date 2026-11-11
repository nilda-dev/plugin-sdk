# Shop events — the shop's orders, as they happen, with their money and the buyer's consent (door D1)

Status: EXPERIMENTAL (`"status": "experimental"`, `WRITE_COMMANDS.md` §5) from v0.61.0. The shop's door D1
(plan 2026-11-05 §1J). Go: `shopevents.go`; test kit: `nildatest.ShopEvents`.

## 1. What it is for

The shop's first events (`ecommerce.order_paid` and its siblings, `EVENTS.md`) carry an order's id: a plugin that delivers
something reads the order afterwards (`ORDERS.md`). A plugin that MEASURES — an ad network's conversions, an analytics feed,
a tax provider reversing its answer on a refund — needs the order as it was at that moment, each change once, in order,
and the consent the visitor gave. That is `shop.events/v1`.

## 2. The five events

| name (`nilda.Event…`) | emitted once | carries beside the common fields |
|---|---|---|
| `ecommerce.order.placed` (`EventShopOrderPlaced`) | when the order is committed | — |
| `ecommerce.order.paid` (`EventShopOrderPaid`) | when the money is in | — |
| `ecommerce.order.cancelled` (`EventShopOrderCancelled`) | when the order is called off | — |
| `ecommerce.order.refunded` (`EventShopOrderRefunded`) | per refund | `refund_minor`: what this refund returned; the lines' `quantity_refunded` so far |
| `ecommerce.order.fulfilled` (`EventShopOrderFulfilled`) | per shipment | `fulfillment_id`; the lines' `quantity_fulfilled` so far |

Every event's data is a `ShopOrderEvent`: `event_id` (the shop's — the same on every redelivery and every re-emission;
Core's CloudEvent id is per emission), `sequence` (rises with every event the shop records; it may skip a number, never
go back), `order_id`, `ref`, `occurred_at`, `currency`, `net_minor` (the goods after discounts, tax and delivery
excluded: the sum of the lines'), `shipping_minor`, `tax_minor`, `total_minor`, `refunded_minor` (so far), `lines` (SKU,
quantities so far, `net_minor` and `tax_minor` each) and `consent`. No event carries the buyer's email, name or address:
read them through `shop.orders/v1`, and only when the consent says you may.

Decode with `nilda.DecodeShopOrderEvent(eventType, data)`: it refuses a payload that is not a whole event of this contract,
or one delivered under another name than its own. Subscribe with the `events` capability and the five names.

## 3. The consent snapshot

`consent` is copied onto the order WHEN IT IS PLACED and never rewritten: an event about a refund a month later says what
the visitor allowed when they bought. Its four signals are Google Consent Mode v2's — `ad_storage`, `ad_user_data`,
`ad_personalization`, `analytics_storage` — each `granted`, `denied` or `unknown`, with `source` and `recorded_at`.

- `unknown` is not a yes. `AdUserDataGranted()` is the one question to ask before a person's (hashed) data leaves for an
  ad network; `AdStorageGranted()` before advertising measurement.
- `source: core`: Core told the shop the visitor's recorded consent with the request that placed the order —
  `nilda.VisitorConsent(r)`, read from `X-Nilda-Visitor-Consent`, which Core's proxy sets on a request that changes
  something (not GET, HEAD or OPTIONS) and strips from any client. Every signal is `granted` or `denied`, mapped from
  Core's categories as the public page maps them for Google's tags; a visitor who never decided is `denied`.
- `source: first_touch`: Core did not tell (an older Core, a consent it could not read), and the basket carried the
  visit's first touch, which Core's page sends only after the visitor allowed analytics: `analytics_storage` is
  `granted`, the ad signals `unknown`.
- `source: not_told` (`NotToldConsent`): every signal `unknown` — an order placed before the snapshot existed, or by a
  door with no visitor's browser behind it (a visitor's agent).

## 4. Catching up: `events.read`

`ServiceCallOptions`-free read, answered by the one shop: `{"after_sequence": n, "limit": ≤100}` → `{"events": [...],
"more": bool}`, oldest first. A subscriber keeps the last sequence it handled; after a restart, a dead letter or on first
install it reads from there until `more` is false. `Services.ShopEvents` asks; `ShopEventsMethods` answers (Serve finds a
`ShopEventsProvider` by itself).

## 5. What a subscriber does

- Drop repeats by `event_id`, not by Core's CloudEvent id.
- Order by `sequence`; an event older than one handled for the same order is history, not news.
- Never treat `unknown` consent as granted. Never read the buyer's address book for an event the consent does not allow.
