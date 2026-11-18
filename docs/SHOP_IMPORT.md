# shop.import/v1 — an old shop written into the shop by an import plugin (EXPERIMENTAL)

Door D10 of the shop's plan (Core's `docs/plans/wordpress-m3-2026-11-04/D10-SHOP-IMPORT.md`). From plugin-sdk v0.70.0.
The shop provides it; an import plugin (`site_import`) uses it, and must hold the `import` capability the owner granted
AND name the contract under `uses` (with `service_consumer`).

The plugin reads the old shop (WooCommerce now; Shopify and Wix later), turns it into the NEUTRAL records below — the
shop's own vocabulary, no platform's — and writes them through the shop, one kind a batch. What a WooCommerce field means
lives in the plugin; the shop keeps only this door.

| Method | Import call | Assistant | Budget | What |
|---|---|---|---|---|
| `settings.answer` | no | none | default | what the shop would do with the old shop's settings: the money decision (`same`, `adopt`, `convert`, `no_prices`, or `ask` when the currencies differ and the owner has not answered), the ECB's rate, `tax_include_differs`, this shop's currency, whether it is empty |
| `records.put` | **yes** | none | 15 s | one batch of ONE kind; `apply: false` is the preview, which writes nothing |
| `records.list` | no | read | default | what imports brought, held, paired and converted, with the old prices as read |
| `publish.plan` | no | none | 15 s | what the act would do now: counts, groups (`converted`, `per_person`, SKU pairs, sales starting within 30 days), the settings it replaces |
| `publish.prices` | no | none | default | a page of the converted products' old and new prices |
| `publish.apply` | **yes** | none | 15 s | the owner's act, a page at a time under the act's marker, with the owner's choices; each page names what it made live (`live`: kind, source id, public path) for the plugin's redirects |
| `publish.undo` | **yes** | none | 15 s | takes the act back, a page at a time |

`none` methods are reached through the import plugin's own abilities, which keep the job, preview first and ask the owner
before the act. `records.list` is also the assistant's read tool: it is the shop's own truth after the act.

## What the act made live

Each page of `publish.apply` answers `live`: for every product the page published (or had published under the same
marker, when the page is asked again) and every category the import brought, its `kind` (`product`, `category`), its
old shop's `source_id` and its public `path` here, from the site's root — the address the shop's storefront links to
(a product's from Core's breadcrumb trail, so the owner's permalink setting is followed; a category's under the
shop's category archive). The import plugin writes the old shop's addresses as redirects to them. `publish.undo` answers
none. The field is additive; a provider built before it answers none.

## Who may call it

EVERY method needs the `import` capability the owner granted (`"caller_capability": "import"`): the read, the plan, the
act and the undo as much as `records.put`. Core checks it — and the site's import switch — before it reads the request
and before any shop is asked: 403 `import_not_granted` without the grant, 409 `plugin_import_off` while the switch is
off (not retried). Core's own call — the owner's assistant — reaches `records.list` alone (agent `read`), whatever the
switch says. The contract is CLOSED (`"closed": true`): every object refuses a member it does not
declare, so `"KIND"` beside `"kind"`, or a `"NAME"` the schema never bounded, is refused at Core, at the client and in
`Serves` before the shop decodes the request. And `"name"` beside `"NAME"` in a record — both declared, one key to Go —
is refused by Core's desk (422 `key_given_twice`) after the grant and before the schema; the source ids of `sku_pairs`
and the tax classes of `tax_class_rates` are a map's keys, where `"abc"` and `"ABC"` are two.

## The import call

`records.put`, `publish.apply` and `publish.undo` are marked `"import": true` (`ContractMethod.Import`): the act writes
pages back through Core as the batches do, and is quiet as Core's own import publish is — no `content.published` webhook,
automation or author notice per product. The act's `import.id` is the same job id as its batches', its `planned` the act's
rows (the plan's counts); its `marker` is still the act's own. Each request carries `import: {id, planned}`
(`ImportCall`: the plugin's job id, 1 to 200 characters of letters, digits and `. _ : -`; the records the whole job plans,
0 to 1,000,000). Core's service desk:

1. refuses a caller that does not hold `import` — 403, `details.reason` `import_not_granted` — before any provider is
   asked; Core's own calls are refused too;
2. reads `import.id` and `import.planned` from the request its schema already checked;
3. opens the call under the marker `plugin:<caller key>:<id>`: what the shop then writes back through Core's API (its
   products' pages, its terms, media reads) is quiet — no mail, webhook, notice or automation — and counted on the job's
   own hourly allowance, sized from `planned`, never on the shop's token;
4. relays to ONE shop: the one the call names, or the only one; with two and none named it refuses (422
   `provider_not_named`);
5. never answers it from a cache; takes a request up to 1 MiB (`MaxImportInputBytes`).

`ParseContract` refuses an import call that is not answered by the provider, does not block, is not idempotent, is a tool
for the assistant, or whose request does not require that `import` object.

## A batch

```json
{ "import": {"id": "job-7", "planned": 5200},
  "source": "woocommerce:shop.example.com", "platform": "woocommerce", "apply": true, "locale": "fa",
  "settings": {"currency": "EUR", "prices_include_tax": true, "weight_unit": "kg", "dimension_unit": "cm",
               "from": "site", "money": "same"},
  "kind": "product",
  "records": [{"source_id": "shop.example.com#123", "status": "published",
               "data": {"name": "Linen apron", "slug": "linen-apron", "kind": "simple", "visibility": "everywhere",
                        "regular_price": "19.99"},
               "unmapped": ["meta:_wc_pre_orders_enabled"]}] }
```

- `source` is `<platform>:<host>`; `source|kind|source_id` is what the shop keys a record by, so a batch sent again is
  harmless: `already` (the same record and money), `kept` (changed here since: never overwritten), `updated`.
- `platform` is one of `woocommerce`, `shopify`, `wix`, `squarespace`, `csv`; `status` one of `published`, `draft`,
  `pending`, `private`, `scheduled` (the plugin maps its platform's words).
- `unmapped` names every source field the plugin read and could not carry; the shop counts them as the loss
  `unmapped_field` with their names, so the report's field counts balance.
- The plugin resolves Core's things BEFORE the call: pictures and downloads as Core media ids (downloads uploaded born
  private: `API.UploadPrivateMedia`), a product's description as a Core editor document, its excerpt as plain text. The shop
  never fetches an address.
- A product's or a variant's `tax_status` is `taxable` (also when it says nothing: a record is never sold untaxed by
  omission), `shipping_only` or `none`; `virtual` marks an item nothing is delivered for (a service, a booking), which
  asks no address and no shipping. Both are the plugin's to resolve from its platform's words.
- Money is a decimal string in the settings' currency, `""` for none (`-?[0-9]{1,15}(.[0-9]{1,6})?`); the shop converts
  ONLY at the settings' `convert` rate, rounding once, and keeps the money as read so nothing converts twice. Times are
  RFC 3339 UTC. References to other records are their source ids.
- At most 800 records a batch (an apply's batch is the shop's N per kind; a preview's four times that).

The kinds (`ShopImportKinds()`, in the order an import sends them): `category`, `tag`, `attribute` (a shared axis with its
terms), `product`, `variant` (its record's `parent_source_id` is the product; what it inherits is already resolved by the
plugin), `links` (upsells, cross-sells, grouped), `review` (never an e-mail, avatar, IP address, user id or "verified": it
arrives unverified, labelled "From our previous shop"), `coupon` (never the e-mails it was limited to or who used it; a
code limited to e-mails or of an uncertain kind is held `coupon_restricted`). The contract checks every record's data
against its batch's kind, field by field (lengths, patterns, values): Core before the shop is asked, this module on both
sides.

The answer is a result per record — `outcome`, `refs` (the page, the term, and with hold `sku_taken` the owner's product
whose SKU it met), `hold` (`no_price`, `currency`, `download_missing`, `hidden`, `password`, `external_url`,
`page_missing`, `coupon_restricted`, `sku_taken`) and its losses in the batch's locale — with the kind echoed.
`ShopImportAnswer.ValidateFor` refuses an answer that is not about the batch sent.

The shop writes drafts only, sends no mail, webhook, notice or catalogue event, and publishes nothing held. The act
publishes what was public at the old shop, with the owner's choices: converted products stay drafts unless
`publish_converted`; used once-per-customer codes switch on unless `switch_on_per_person` is false; a SKU pair is
`keep_mine` unless named `keep_both`.

Customers, orders kept as history, gift cards and store credit (phase B) and subscriptions (phase C) are added to v1
additively, each when the shop's code that writes it lands.

## Testing

`nildatest.Services.ImportPlugin(key, "shop.import/v1")` is Core for an import plugin, holding `import` as the owner
granted it; the desk then relays `records.put` as Core does (granted only, the marker in `ServiceCallLog.ImportMarker`, one
shop). A plugin made with `Services.Core` is refused, as Core refuses it.
