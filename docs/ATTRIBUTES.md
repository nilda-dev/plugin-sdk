# A plugin's own fields in the shop — `shop.attributes/v1` and `shop.attribute.values/v1` (door D2, EXPERIMENTAL)

Status: v0.58.0, experimental (`docs/WRITE_COMMANDS.md` §5: the shape may still change in a minor release until the first
plugin built by Nilda ships green on it). The shop's plan: `plugin-commerce/docs/plans-2026-11-05/01-COMMERCE.md` §1J, D2,
landing with row 10.5. Built on door D0 (`docs/WRITE_COMMANDS.md`). Go: `shopattributes.go`; the kit: `nildatest.AttributeShop`.

A delivery-slot plugin asks the buyer for a date at checkout (P10); a personalisation plugin asks for the engraving on the
product page (P07); an e-invoicing plugin keeps a buyer's routing id (P13); an address plugin suggests addresses while the
buyer types (P14). Each is a **field the plugin declares and the shop draws, checks, keeps and copies into the order when it
is placed** — so the order still says what was asked after the plugin is gone (D11's snapshot rule).

## 1. Two contracts, one per direction

A contract has one provider side (`SERVICES.md` §1: Core's desk asks providers on a consumer's behalf), and D2 runs both
ways — the shop asks the plugin, the plugin writes into the shop — so it is two contracts, as the shop's orders
(`shop.orders/v1`, the shop provides) and deliverables (`shop.deliverables/v1`, the plugin provides) are.

| contract | provided by | used by | methods |
|---|---|---|---|
| `shop.attributes/v1` (`nilda.ContractShopAttributes`) | the field-owning plugin | the shop | `describe` (fallback `cached`), `validate` (`block`), `address.suggest` (`skip`), `address.resolve` (`skip`) |
| `shop.attribute.values/v1` (`nilda.ContractShopAttributeValues`) | the shop | the field-owning plugin | `values.get` (read), `value.set` (write command), `value.undo` (its undo) |

Every method is agent class `none`: the owner reads and edits the values on the shop's own screens and abilities, a
visitor's address query is nobody's tool, and a model writing a plugin's field would write under no plugin's key.

A plugin's manifest: `"capabilities": ["service_provider", "service_consumer"], "provides": ["shop.attributes/v1"],
"requires": ["shop.attribute.values/v1"]` (or `uses`, when it never writes). An address plugin only provides.

## 2. Declaring fields — `describe`

`AttributeMethods(p)` answers the contract for a plugin implementing `AttributeProvider` (`DescribeAttributes`), plus
`AttributeValidator` when it validates and `AddressSuggester` when it suggests; `Serve` finds them by itself. The
description (`AttributeDescription`) is checked before it leaves the plugin (`Validate`), so a field the shop could not
draw fails the plugin's own test, not a checkout.

| `AttributeField` | rule |
|---|---|
| `key` | `^[a-z][a-z0-9_]{0,39}$`, unique in the plugin; the shop keeps `<plugin key>.<key>` — the plugin key is Core's (the provider that answered, the caller that wrote), never a request's |
| `target` | `order`, `line`, `product`, `customer` (`AttributeOrder` …) |
| `type` | `AttributeFieldTypes()`: `text`, `textarea`, `email`, `phone`, `url`, `number`, `boolean`, `select`, `radio`, `date` — every one a form type Nilda's screens and the owner's app already draw (`FormFieldTypes`; the app's `AppFormType`, held by `TestTheOwnersAppDrawsEveryAttributeType`) |
| `label`, `help` | per language tag (`{"en": "Delivery date", "de": "Liefertag"}`), one plain line, at most 100 / 300 characters, at most 20 languages; `LabelIn(labels, locale)` picks: the tag, its base language, `en`, the first tag in order |
| `shown` | at least one of `checkout` (an order, line or customer field), `product_page` (a line field), `product_form` (a product field, the owner fills it), `order` (shown wherever the shop shows the order: its order screen, the buyer's account, the order mails, the packing slip). A field shown only on `order` is set by its plugin (`value.set`), never by a buyer |
| `required` | refuses an order (or a product form's save) without a value; a required `boolean` must be `true` — a tick. Only on a field that is collected somewhere |
| `choices` | a `select`'s or `radio`'s, and only theirs: 1 to 100, each a value and a label per language |
| `min_length`, `max_length` | a text's characters; never past the type's own (`text` 255, `textarea` 2,000, `email` 254, `url` 2,048, `phone` 32) |
| `min`, `max` | a number's |
| `min_date`, `max_date` | a date's: `2026-12-24`, or days from the shop's today, `P1D` (WooCommerce's date field takes the same) |
| `skus` | limits a line field to the lines of these SKUs (at most 500); empty is every line |

`validates` asks the shop to call `validate`; it then needs `when_unanswered`, `block` or `allow`. `address_suggestions`
says the plugin answers the two address methods. At most 20 fields; a description with no field and no suggestions is
refused. `describe` falls back to the plugin's last good answer (a day, Core's cache), so a plugin that hiccups does not
take its fields off the checkout.

## 3. Drawing and collecting them — the slot

The shop draws each field **with its own form helpers** (a visible label around the control, the value drawn back
escaped, the theme's input look), after its own fields at `checkout` and above the basket button on the `product_page`:

| type | control | value on the wire |
|---|---|---|
| `text`, `email`, `phone`, `url` | `<input type="text|email|tel|url">`, `maxlength` | a string |
| `textarea` | `<textarea>` | a string (newlines kept) |
| `number` | `<input type="number">`, `min`, `max` | a JSON number |
| `boolean` | a checkbox | `true` / `false` |
| `select` / `radio` | a `<select>` / a radio group in a `<fieldset>`, the labels in the buyer's language | the choice's `value` |
| `date` | `<input type="date">`, `min`, `max` resolved against today | `"2026-11-06"` |

The input's name is `AttributeInputName(plugin, key, line)`: `attr[<plugin>.<key>]`, and `attr[<plugin>.<key>][<line>]` for a
line's. In the shop's product form and the owner's app a field is `AttributeField.FormField(locale)` — the `PageForm`
vocabulary they already draw. **No plugin sends markup or script**: the slot is a declaration the shop draws, as Core's
own field contributions are (`field.go`), so nothing new must be taught to the mobile app.

## 4. Placing the order — the rules (`nildatest.AttributeShop` holds each)

1. **Only what was offered.** A value for another plugin's field, an unknown key, a field the buyer is never asked for, a
   line field on a line of another SKU or on no line is refused `not_offered` (`AttributeNotOffered`).
2. **The one rule per value**: `AttributeField.CheckValue(raw, today)` — the shop runs it at checkout, on the product page, in
   the product form and on every `value.set`. A refusal is an `*AttributeValueError` with a code (`required`, `type`,
   `malformed`, `too_short`, `too_long`, `too_small`, `too_large`, `not_a_choice`, `too_early`, `too_late`); **the shop
   writes the sentence in the buyer's language** — the SDK holds no human words.
3. **Required** fields are held on the order and on each line they apply to; a customer field the signed-in buyer already
   holds counts.
4. **Validate at place order.** Each plugin whose description `validates` and that has a field this order offers is asked
   `validate` — one call to that provider (`Services.ValidateAttributes`), with its own values only (`AttributeValidateRequest`:
   currency, country, customer, locale, the basket lines, the values). No `errors` places the order; each `AttributeError`
   names its field (and line) and the sentence the buyer reads, in the request's language. An answer naming a field the
   plugin never declared is refused as the plugin failing. **A plugin that does not answer** (an error, a timeout, a
   refused answer) gets its own `when_unanswered`: `block` refuses the order ("this cannot be checked now"), `allow`
   places it and marks the order unchecked for that plugin — Shopify's `blockOnFailure`, said by the plugin that knows
   whether an unchecked value is harmless.
5. **The snapshot.** The order keeps a copy of every value — the buyer's order and line values, the customer's (kept on
   the account too) and each line's product values — with the label in the order's language and the type, at that
   moment. A later change to the product or the customer, or the plugin's removal, does not move a placed order.

## 5. A plugin's own values — `shop.attribute.values/v1`

`AttributeValuesMethods(p)` answers it for the shop (`AttributeValuesProvider`); a plugin calls:

- `Services.AttributeValuesOf(ctx, target)` — `values.get`: the caller's own values on one `AttributeTarget` (`order`,
  `line` with its `line`, `product`, `customer`); on an order also the copies of its customer and product values (`kind`
  says which).
- `Services.SetAttribute(ctx, key, AttributeSetRequest{Target, Field, Value})` — `value.set`, a **write command** (record
  `attribute.value`, undo `value.undo`, emits `ecommerce.attribute_set`): key required (derive it from what the value is
  for), one write per (caller, method, key) for as long as the record lives — a repeat answers the first record with
  `already: true`; the field is the CALLER's (Core's word), on its declared target, checked by `CheckValue`; `null` clears
  it (refused for a required field).
- `Services.UndoAttribute(ctx, key, recordID)` — `value.undo` (emits `ecommerce.attribute_undone`): the value the write
  replaced returns; only by the plugin that wrote it, once, and never past a later write of the same field
  (`replaced`: take that one back first).

The answer is an `AttributeWriteAnswer`: the record (`record_id`, `already`), **or `refused`** — a value's code, or
`not_offered`, `no_record`, `unknown_write`, `undone_already`, `replaced`. **A refusal is a result, never an error**:
Core's desk relays a provider's error as the provider failing, which blocks the call and is retried, so a plugin told "no"
by an error would wait out the retries and read "unavailable" (the kit's first cut did exactly that: four tries and a
503 for a field that was simply not the caller's). A refusal is not kept under its key — the same key may succeed once
the record is there. The events are the shop's, under its capability's namespace (`ecommerce.*`), the only one Core lets
it emit; each says the record, the plugin, the field and the target, after the write committed.

## 6. Address suggestions — the proxy

The visitor's browser asks the shop, never the provider: the merchant's key stays in the plugin. The shop:

1. bounds each visitor (by the basket's or the session's id) with `AddressSuggestLimiter`: at most
   `AddressSuggestPerMinute` (30) suggest and resolve calls in any minute; past it `ErrAddressSuggestLimited` (HTTP 429),
   and the call never reaches the plugin;
2. asks only a plugin whose description says `address_suggestions`, once the visitor typed `MinAddressQuery` (3)
   characters (`AddressSuggestRequest`: `query` up to 200, `country`, `locale`, an opaque `session` for providers that
   bill per session, `limit` up to `MaxAddressSuggestions`, 5);
3. shows at most the limit (`AddressSuggestion`: `id`, `label`, and the `address` when the provider has it); a plugin that
   does not answer is no suggestions — the visitor types;
4. on a pick without an address asks `address.resolve` (`AddressResolveRequest` → `AddressResolveAnswer`, a `ShipAddress`)
   and fills ITS OWN fields with it, checked by its own rules (its countries, its postcode rule) — a suggestion is never
   trusted past them.

The typed text reaches the provider only after the visitor starts typing; the shop's privacy text says so (P14).

## 7. Testing

```go
desk := nildatest.NewServices()
shop := nildatest.NewAttributeShop(desk) // the shop, as nildatest.AttributeShopKey, providing shop.attribute.values/v1
defer shop.Close()
shop.SetToday(time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))
desk.Provider("slots", mySlotPlugin) // answers through nilda.AttributeMethods
order, err := shop.PlaceOrder(ctx, nildatest.AttributeOrderDraft{Currency: "EUR", Lines: …, Values: …})
// err is an *nildatest.AttributeRefusal (Errors, Unanswered) when the shop would refuse it
core, _, srv := desk.Core("slots", nilda.ContractShopAttributeValues)
answer, err := core.API().Services().SetAttribute(ctx, "order-"+order.ID+"-slot", nilda.AttributeSetRequest{…})
```

`AttributeShop` also has `Order`, `SetProductValue` (the owner's product form), `SuggestAddresses` / `ResolveAddress` (the
proxy, bounded) and `Events`. It knows no catalogue and no customer list: any product or customer id is one that exists.

## 8. The decision, and what the market does (read 2026-11-05)

- **WooCommerce — additional checkout fields** (developer.woocommerce.com, "Additional Checkout Fields", read 2026-11-05):
  `woocommerce_register_additional_checkout_field()` with a namespaced `id` (`plugin-namespace/field`), a `label`, a
  `location` of `contact`, `address` or `order`, a `type` of `text`, `select`, `checkbox` or `date` (`min`/`max` as a date
  or an ISO 8601 duration such as `P1D`), `required`, and sanitize/validate callbacks; values are kept on the order (and
  the customer for contact and address fields) under prefixed meta keys. Taken: the namespace, the locations as targets,
  the declared types, the relative date bounds, values kept on the order and the customer.
- **Shopify — checkout UI extensions and metafields** (shopify.dev, checkout-ui-extensions API `2026-10`, read 2026-11-05):
  extensions render only Shopify's own components ("limited to specific UI components and APIs that are exposed by the
  platform"), store data with `applyAttributeChange` (cart attributes) or `applyMetafieldChange` (cart metafields);
  Shopify now prefers cart metafields for their app-reserved namespaces, and since API `2026-04` copies a cart metafield
  to the order when the order metafield definition has `cartToOrderCopyable` (shopify.dev changelog, 2026-02-04).
  Validation moved to server-side cart and checkout validation functions; a validation's `blockOnFailure` decides
  whether a failure other than a violation (a timeout, an exception) blocks checkout (Admin GraphQL `Validation`, read
  2026-11-05); the buyer-journey intercept needs the merchant-granted `block_progress` capability. Address autocomplete is
  the `purchase.address-autocomplete.suggest` target — up to five suggestions shown — with a companion
  `format-suggestion` target that turns a pick into an address. Taken: the platform's own components only (here: the
  shop's form vocabulary, no plugin markup), namespaced fields owned by the app, copied to the order at completion, a
  server-side check whose failure mode the extension declares, five suggestions and a two-step suggest/resolve.
- **Not taken**: arbitrary plugin markup or script in checkout (no capability in Nilda ships code into a browser; the
  app could not draw it), cart attributes without a namespace (Shopify's own reason for preferring metafields: one
  writer clobbers another), and a validation fallback chosen by the platform alone (the plugin knows whether an unchecked
  value is harmless; WooCommerce has none, Shopify lets the merchant set it per validation — the owner may later be
  given an override on the shop's Checkout settings, an open item for the shop).

## 9. What each side does next

- **Core**: nothing new — both contracts are relayed by the desk as files (it reads them from this SDK version); the
  events are emitted under the shop's existing `ecommerce` namespace. The shop's manifest names the contracts.
- **The shop** (plugin-commerce): provide `shop.attribute.values/v1` through `pluginWrite` / `pluginUndo`, answering a
  refusal as `refused` (not an error — see §5); use `shop.attributes/v1`: describe at checkout and on the product page and
  draw the fields by §3, run §4 at `Store.Checkout` for every door (the page, the JSON API, the agent doors), keep the
  snapshot with the order and show `order` fields on the order screen, the buyer's account, the mails and the packing
  slip, the address proxy endpoint with the limiter; `TestEverySurfaceHasADoor` rows for the new screens.
