# Changelog

What changed for a plugin author, per release. The Go API a release freezes is `surface.txt` (and
`nildatest/surface.txt`); the git log has the reasons in full. Versions before v0.10.0 are not written up
here.

How a release is cut (additive only, one tag per milestone, security and money fixes at once, an undated first
section means "unreleased") is `docs/RELEASE_TRAIN.md`; `release_train_test.go` holds the dating rule.


## v0.65.0 — 2026-11-13 (abilities: a size budget instead of a count, and their effects; nildatest: what the assistant cannot reach yet)

Core's agent-control C4 (owner decision D4, 2026-11-13): no ability count that forces work back to the panel (Core
#432). **Removes one constant** — the owner's decision (`SDK_ALLOW_SURFACE_REMOVAL=1`). And a test helper mirroring
Core #430 and #431 (the agent doors, `nilda plugin check`'s assistant check). And unit A0b of Core's agent-control
plan, the effect vocabulary (Core #433, `module.ToolEffect`, `module.RuleOf`), which needs a Core that reads `effect`.

Removed:

- `nilda.MaxManifestAbilities` (200). Core no longer counts a plugin's abilities; a plugin offers as many as it has.
  A test that compared `len(abilities)` with it holds the size below instead.

Added:

- `nilda.MaxManifestAbilityDefinitionBytes` (1 MiB): what one plugin's abilities put in front of a model — the bytes
  of their labels, descriptions and input schemas together. Core's install refuses a manifest whose labels and
  descriptions alone cross it; a running plugin whose schemas take it over is reported (the plugin's record,
  `plugins_installed`, the Plugins screen), never cut.
- `nildatest.AssistantHoles(raw []byte) ([]AssistantHole, error)` and `nildatest.AssistantHole` (`Where`, `Unit`,
  `Gap`): which of Core's own holes a plugin.json declares — the surfaces the site's assistant cannot operate yet, each
  with the unit of Core's plan that closes it. Today `manifest.account` and `manifest.editor_commands`, both C3. Held
  to Core's `internal/plugin/agentdoors.go` and to Core's assistant check of every first-party manifest. A key that is
  null, `""`, `false` or `0` is unset; an empty list or object is the surface, used.
- `nilda.Effect` and its eleven words, Core's own: `EffectNone`, `EffectMoney`, `EffectCredential`, `EffectRedirect`,
  `EffectSpend`, `EffectDeletePermanent`, `EffectRole`, `EffectOutboundMail`, `EffectBulk`, `EffectPublish`,
  `EffectOutward`; `nilda.Effects()`, `Effect.Known()` and `Effect.AlwaysAsks()` (Core's rule: is the person asked
  before every call). Held to Core's `internal/module/effect.go`, words and rule table.
- `Ability.Effect` — on the wire (`Ability.effect`, proto field 9) and in plugin.json (`"effect"`, written by
  `nildatest.ManifestAbilities`). `Serve` refuses an effect Core does not know at Init.
- `SettingField.Effect` (`"effect"` in plugin.json), and `SettingField.MarshalJSON`, which refuses an unknown effect.

Changed:

- `nildatest.ManifestAbilities` holds the abilities to that budget, schemas included, and fails naming the size and
  the limit — with or without `NILDA_WRITE_MANIFEST=1`, so an over-budget list is never written.
- `always_ask` is sent and written from the effect too: an ability's or a setting's `always_ask` is now `AlwaysAsk`
  OR `Effect.AlwaysAsks()`, so a Core that reads only `always_ask` (before Core's A0b) still asks for money, mail,
  bulk and the rest. `AlwaysAsk` stays, and stays readable by Core, for plugins built before effects. `AlwaysAsk`
  beside an effect that does not ask (`none`, `publish`, `spend`, `outward`) is refused — at Init for an ability,
  when it marshals for a setting — as Core refuses the contradiction in a manifest.


## v0.64.0 — 2026-11-13 (nildatest: Payments answers users from a Site)

Additive; test helper only.

Added:

- `nildatest.Payments.Site` (`*Site`). Set it and the fake Core `Payments.Core` hands out also answers `GET /users` and
  `GET /users/{id}` from that Site's accounts, under the plugin's own `users.read` grant, so a test needing both
  payments and the site's users runs on one fake. Nil (the default) is unchanged: only `/payments` is served.


## v0.63.0 — 2026-11-12 (payments: a refund Core kept as money no payment holds, a reversed refund no longer counts)

Core's payments security review (2026-11-12) changed what a gateway hears back about refunds. Additive; a money fix
(docs/RELEASE_TRAIN.md rule 2: tag at once).

Added:

- `nilda.FailureUnmatched` (`"unmatched"`) and `PaymentRefund.Unmatched()`. `ReportExternalRefund` may answer with a
  resolved refund whose `failure_code` is `"unmatched"`: NOT a refund of the payment, but money the processor gave back
  that Nilda holds on no payment (a payment after a refusal, a second payment of the order, more than was paid, or one
  beside refunds still pending at the bank). Nothing of the order changed and the consumer is not told; the owner
  settles it. Its id can be read with `GetRefund` and reported reversed with `ReverseRefund`. Before, a refund past
  what was paid was a 422.
- An external refund that passes the cap only because a Nilda refund is still being asked of its gateway is refused
  with 503: report it again shortly (fail the webhook); a 503 is never final.

Changed:

- `RefundedMinor` is what the session's resolved refunds gave back LESS ANY REVERSED: a reversed refund no longer
  counts, and the payment can be refunded again up to what was paid. Before, a reversal left the total unchanged.
- PAYMENTS.md: ONE STABLE PROCESSOR REFERENCE PER PAYMENT. A gateway reports a payment (Resolve, Authorize, a capture's
  answer) under one reference for its whole life — its stable payment id (Stripe: the PaymentIntent `pi_…`; never a
  Checkout Session `cs_…` for a paid payment, and never the PaymentIntent in one report and a charge in another).
  Core treats money on a paid payment under another reference as a second payment of the order, and a refund made at
  the processor while it is open is answered `Unmatched()` and kept for the owner.
- `nildatest.Payments` does all of the above as Core does: a reversal is taken off the refunded total, an external
  refund past what was paid or on a refused payment is kept unmatched (read and reversed by its gateway, never seen by
  the consumer), and one past the cap only because of a refund still `requested` is a 503.


## v0.62.0 — 2026-11-11 (security: only Core's proxy reaches a route, payment tokens never reach the log, arguments held to their schema, who acted)

The fixes of the SDK review of 2026-11-11. All additive; tag at once (docs/RELEASE_TRAIN.md rule 2: a security fix).

- ONLY CORE'S PROXY REACHES A ROUTE. The resident server listens on loopback, which every process on the machine can
  dial — another plugin included — with `X-Nilda-User` naming anybody, and `CurrentUser` believed it. Contract:
  `InitRequest.route_key` (field 14), a secret Core makes per launch and sends on every request it proxies, in
  `X-Nilda-Route-Key` (`RouteKeyHeader`). `StartHTTP` now wraps the handler in `GuardRoute`, which answers 403 to a
  request without that exact key (constant-time) and removes the header before the handler runs; wrap your own server
  in `GuardRoute`. A Core that sends no key changes nothing. NEEDS CORE: generate the key, send it at Init, set it on
  every proxied request and strip a client's copy.
- ROUTE BODIES ARE BOUNDED. `StartHTTP` cuts a request body at `DefaultMaxRequestBody` (32 MiB) and keeps its 60-second
  `DefaultHTTPWriteTimeout`; `StartHTTPWith(h, HTTPOptions{MaxBodyBytes, WriteTimeout})` chooses others. A handler
  that streams a long download lifts its own response's deadline (`http.NewResponseController(w).SetWriteDeadline`).
  A BEHAVIOUR CHANGE on rebuilding: a route that takes uploads over 32 MiB must now say so with `StartHTTPWith`.
  Call `StartHTTP` inside your Init (the route key arrives with it); `GuardRoute` around a handler `StartHTTP` already
  guards is harmless.
- A PAID DOWNLOAD IS NO LONGER CUT AT 30 SECONDS. `API.MediaFile` reads the bytes on a client with no total timeout;
  the caller's ctx bounds it. `http.Client.Timeout` covers the body, so a large file read slowly always stopped.
- PAYMENT TOKENS NEVER REACH THE LOG. `PaymentCredential`, `PaymentSaved`, `PaymentSavedToken`, `PaymentForgetRequest`
  and `ClientAction` printed `[redacted]` through fmt, but the logger writes JSON from the fields: logging a
  `PaymentStartRequest` wrote the saved card's token and the client secret in clear. The logger now writes each as its
  String at any depth and every other value as before.
- A CONTRACT REFUSAL NEVER QUOTES THE VALUE. `ContractMethod.ValidateInput` / `ValidateOutput` (and so `Services.Call`
  and `DispatchServiceHook`) said "type: <the value> has type …" — an owner's application password in a site.reader
  start, a buyer's address in a carrier's rates, into Core's log. They now say where and which rule, never the value.
- A VISITOR TOOL'S ARGUMENTS, AND AN ABILITY'S A MODEL WROTE, ARE HELD TO ITS `InputSchema`. Serve compiles each at Init
  and refuses a call that does not match, as the plugin's answer, before `Run` or `HandleHook` sees it: every visitor
  tool call, and an ability call whose `CallerKind` is `ai` or `mcp`. Core's importer and a person's own press are not
  held (Core's own code builds those bodies). No arguments is `{}`. A schema the SDK cannot compile is logged and its
  calls pass, as before. Core (from the release pinning v0.62.0) holds a visitor tool's arguments too, and like an
  ability's it refuses a top-level argument the schema does not name.
- THE DATASTORE POOL. `LimitDatastorePool(db)` holds a pool opened on `Core.DatastoreDSN` to `DatastoreMaxConns` (2);
  `database/sql` opens without a limit, and Core caps the plugin's role at 20 connections, so an unbounded pool fails
  its own calls under load. The guide says the rest: ctx on every query, no transaction held across a call to Core,
  placeholders only.
- `NormalizePrivacyPhone` reads every script's decimal digits (`۰۹۱۲…`, `٠٩١٢…` are `0912…`). It kept ASCII only, so a
  number typed on a Persian or Arabic keyboard normalised to "" and was never exported nor erased. Core normalises with
  this function, so it follows when its pin moves. A BEHAVIOUR CHANGE for a plugin that stores the normal form: a row
  stored from non-ASCII digits held "" and matches only once it is normalised again.
- `NewOIDCClient` refuses an issuer, a token endpoint or an authorization endpoint that is not https (a loopback host
  excepted, for tests): the token endpoint receives the client secret, the code and the PKCE verifier.
- WHO ACTED. `CallInfo.ActorUserID` (contract `HookRequest.actor_user_id`, field 9): the Core user id of the person a
  door acted for — who pressed a row action or saved a form, who asked the assistant or holds the connected AI app's
  grant, who ran Core's importer. Set by Core from the session, never from the payload; an id only. Empty on Core's
  own hooks, service-desk calls and visitors' calls. A plugin can now record who refunded an order.
- `VisitorCall.VisitorKey`: Core's daily, per-plugin hash of a visitor-tool caller's address, as `VisitorKey(r)` reads
  on a route. Limit by it instead of `IP`, which Core still sends until the plugins limiting by it have moved.
- Said where an author reads it: `Core.Trash` failing means undo your own mark; `IsFirstRun` is true on a reinstall too,
  so seed without overwriting; `AddressSuggestLimiter` is keyed by `VisitorKey`, not a cookie alone; `ShopOrder.Email`
  is for delivering the order, never for an ad network without the order's consent; what §6 of the guide means for a
  route and for egress.

## v0.61.0 — 2026-11-11 (the shop's order events with their money and the buyer's consent — the shop's door D1)

- THE SHOP'S ORDER EVENTS (door D1, the shop's plan 2026-11-05 §1J; `shopevents.go`, `docs/SHOP_EVENTS.md`). EXPERIMENTAL:
  contract `shop.events/v1` (`ContractShopEvents`, `"status": "experimental"`) — its shape may still change in a minor
  release until its first Nilda plugin ships green on it.
  - Five events in the shop's namespace (`ShopEventTypes()`): `EventShopOrderPlaced` `ecommerce.order.placed`,
    `EventShopOrderPaid` `ecommerce.order.paid`, `EventShopOrderCancelled` `ecommerce.order.cancelled`,
    `EventShopOrderRefunded` `ecommerce.order.refunded` (one per refund, `refund_minor` and each line's
    `quantity_refunded`), `EventShopOrderFulfilled` `ecommerce.order.fulfilled` (one per shipment, `fulfillment_id`). The
    shop's older `ecommerce.order_paid` and siblings are unchanged.
  - Their data is `ShopOrderEvent`: the shop's `event_id` (kept by every re-emission) and `sequence`, the order's id and
    reference, `occurred_at`, `currency`, `net_minor` (goods after discounts, tax and delivery excluded — the sum of the
    lines'), `shipping_minor`, `tax_minor`, `total_minor`, `refunded_minor`, `lines` (`ShopEventLine`: SKU, quantities so
    far, `net_minor`, `tax_minor`) and `consent`. No email, name or address. `Validate` holds it; `DecodeShopOrderEvent`
    is a subscriber's checked decode.
  - `ConsentSnapshot`: Consent Mode v2's four signals, each `ConsentGranted` / `ConsentDenied` / `ConsentUnknown`, with
    `Source` (`ConsentFromFirstTouch`, `ConsentNotTold`) and `RecordedAt`, copied onto the order at placement;
    `NotToldConsent(at)`; `AdUserDataGranted()` and `AdStorageGranted()` read unknown as no.
  - `events.read` (`ShopEventsRead`): `ShopEventsRequest{AfterSequence, Limit}` (at most `MaxShopEventsRead` = 100,
    `DefaultShopEventsRead` = 50) → `ShopEventsAnswer{Events, More}`, `ValidateFor` holding order and bounds.
    `ShopEventsProvider` / `ShopEventsMethods` for the shop (Serve finds it by itself), `Services.ShopEvents` for a
    subscriber. `TestTheShopEmitsTheEventsContract` holds the shop's `shopevents.go` and manifest to it.
  - `VisitorConsent(r) (ConsentSnapshot, bool)`: the visitor's recorded consent Core's proxy sends on a request that
    changes something (`X-Nilda-Visitor-Consent`, core `internal/plugin/visitorconsent.go`), source `ConsentFromCore`,
    every signal granted or denied (`Validate` refuses unknown from Core); ok false with no header or a malformed one.
  - `nildatest.NewShopEvents()`: a fake shop feed — `Record` (numbers and checks an event), `Deliver` (to a plugin's
    HandleEvent), `Events`, and `events.read` through a `Services` desk.

## v0.60.0 — 2026-11-10 (a request's `<`, `>` and `&` go on the wire as themselves; site import: an author's bio and link, a media item's upload date and an item's modified date — snapshot version 5)

This section holds two things that would have been two unreleased headings: the release train allows one (`release_train_test.go`), and
nothing between v0.59.0 and the next tag was tagged. The tag is the owner's.

- SNAPSHOT VERSION 5: `siteimport.Author` gains `Bio` and `Link` (`json:"bio,omitempty"`, `json:"link,omitempty"`), after `SourceID`.
  `SiteReaderSnapshotVersion` is 5. Core compares the number for EQUALITY (`readerjob.go`, `readersources.go`): a reader built against
  this SDK names 5 and a Core that reads only up to 4 refuses it as a whole, and a Core that reads 1 to 5 refuses a reader still
  naming 4 — so a plugin and a Core move to the same SDK tag together. Core's snapshot (the file a live read is kept in) reads
  versions 1 to 5; an author written before this field has no bio and no link. An author without them writes exactly the bytes
  version 4 wrote, so every golden that predates the field still reproduces. A reader sets `Bio` only when the source has a
  biography (WordPress REST `/users` `description`) and `Link` only when it has the author's own website (`url`); an export file has
  neither. Bound them: a bio of at most 2,000 characters, a link of at most 2,048 bytes, no control and no format characters (Unicode Cc and Cf: a right-to-left override or a zero-width space can make a bio or an address read as something it is not). No slug and no avatar
  (a guest author has no slug; the avatar is a file, which is a media read of its own).
- SNAPSHOT VERSION 5, a media file's upload date: `siteimport.MediaItem` gains `UploadedAt *time.Time` (`json:"uploaded_at,omitempty"`),
  after `Private`. The snapshot version stays 5 (this is the same unreleased version as the author's bio and link). A reader sets it
  from the source's own UTC date for the file (WordPress export `post_date_gmt` of the attachment, REST `date_gmt`) and leaves it nil
  when the source does not give a UTC one: a zone-less local date is not used, as the zone is a guess. A date before 1990 or more than
  a day ahead of the reader's clock is dropped. A file without a date writes exactly the bytes it wrote before, and a snapshot
  written before this field has no dates. Core dates a file it ADDS to the library; a file the library already holds (identical
  bytes) keeps the date it had. The post a file was attached to is still not carried (Nilda's library has no such link).
- EVERYTHING THE SDK SENDS CORE IS ENCODED WITHOUT HTML ESCAPING (`wire.go`). v0.58.0 did it for a provider's answer to a service
  hook; the plugin-side direction still used `json.Marshal`, so a request body (`API.JSON`, `API.Post` and the other helpers that
  encode a value) and a service call's input (`Services.Call`, also nested into the request) wrote each `<`, `>` and `&` as a
  six-byte escape, and Core's caps on a request, which count the bytes it reads, were reached at a fraction of the real size
  (a WordPress page body: about 1.8 bytes a byte, tag-dense HTML up to six). One encoder, `encodeWire`, now serves the answer, the
  request body and the call input: `json.Marshal`'s encoding (compact, sorted keys, a raw member compacted) with HTML escaping
  off, no trailing newline, U+2028/U+2029 still escaped. No exported name changes (`surface.txt` untouched). A body that is a
  `[]byte` or an `io.Reader` is the caller's own bytes and is not re-encoded.
- COMPATIBLE BOTH WAYS: `<` and `\u003c` are the same character to every JSON decoder, so an older Core reads a newer plugin's
  bytes and a newer Core reads an older plugin's with no change on either side; `wire_request_test.go` decodes both forms through
  whole-value, struct, number-preserving and token-by-token decoders and the SDK's own service-answer decode, and holds the sizes.
- SNAPSHOT VERSION 5, an item's modified date: `siteimport.Item` gains `ModifiedAt *time.Time` (`json:"modified_at,omitempty"`), after
  `Template`. The snapshot version stays 5 (the same unreleased version as the author's bio and link). A reader sets it from the source's
  own UTC date (WordPress export `post_modified_gmt`, REST `modified_gmt`) and leaves it nil when the source gives no UTC one: a zone-less
  `post_modified` / `modified` is not used, as the zone is a guess. A reader hands the date as it reads it; Core drops one before 1990 or
  more than a day ahead of its own clock, and raises one earlier than the item's publish date to that date. An item without a date writes exactly the bytes it wrote
  before, and a snapshot written before this field has no dates. Core keeps it beside the item's own edit time (`content.source_modified_at`)
  and tells a search engine it as the item's modified date until the owner first edits the item. The report's sentence for a lost modified
  date (`nc_modified_date`) is retired: the date is carried.

## v0.59.0 — 2026-11-10 (a list row's own state; a hidden form field; complete choices — commerce plan CONTRACT_GAPS_2026-11-10)

- `AdminPage.row_state` (`ListRowState`: `columns`, `actions`), the hook `admin.list.rows` (`AdminListRowsHook`,
  `AdminListRowsRequest/Result`, `AdminListRow`, `DispatchAdminListRows`) and `MaxRowStateColumns`: cells and buttons
  worked out per row. An older Core ignores it and draws the page as before.
- `FormField.hidden`: a value the form carries and nobody sees. An older Core ignores it and draws a normal box.
- `CommerceProduct.choices_complete`: `Choices` name every combination; `Validate` refuses it without choices. An older
  Core ignores it.

## v0.58.0 — 2026-11-06 (item lines on a payment — commerce plan 10.15; a plugin's own fields in the shop — the shop's door D2; write commands and experimental contracts — the shop's door D0; hold the money, then capture or void it — commerce plan 10.1; merchant-initiated charges and a card at a reader — the shop's door D5; express wallet buttons — commerce plan 10.2; the processor's risk verdict — commerce plan 10.3; paperless return labels — commerce plan 10.9b; service points — commerce plan 10.9; the storefront's release 1 — commerce plan 1C; a plugin's own licence — commerce 02-PRICING C4; a service answer's `<`, `>` and `&` unescaped on the wire — site import, option A)

- A PLUGIN'S OWN LICENCE (commerce 02-PRICING C4; `licence.go`, `docs/PLUGIN_SDK.md` "Reading your licence and going
  live"). EXPERIMENTAL until the shop's live gate ships green on it. `(*API).Licence(ctx)` reads the plugin's own paid
  edition from Core's `GET /plugin-licence` (core #227; SPEC_99 §6.2): `Licence{State, Source, PaidUntil, LiveUntil,
  Trial, Quantity}` — `LicenceState` `none` | `trial` | `active` | `grace` | `lapsed` (`LicenceStates()`), `Source` plan,
  purchase or trial, a zero time "no end" — and `TakesRealMoney()` (trial, active or grace). A Core older than the route
  (404) reads `LicenceNone` with no error; any other error reads `LicenceNone` AND the error, never licensed.
  `(*API).BindLicence(ctx)` asks Core's `POST /plugin-licence/bind` to bind the account's licence to a self-hosted site:
  `BindResult{Bound, Reason, Licence}`, `Reason` one of `BindReasons()` — `BindReasonNoneFree`, `BindReasonNotConnected`,
  `BindReasonHosted`, `BindReasonNoSiteLicenceLeft`, `BindReasonAddressNotActivatable`, `BindReasonAccountBlocked` — and
  a failed call an error, never bound. `Licence` marshals in Core's shape. Both nil-safe. `licence_test.go` holds the
  routes, JSON names, states and reasons to Core's source. `nildatest.NewLicence()`: a fake Core licence — `Set`,
  `BindGrants`, `BindRefuses`, `BindFails`, `Binds`, `Licence`, `Core`.

- THE STOREFRONT'S RELEASE 1 (commerce plan 1C; `commercestorefront.go`, `paymentmarks.go`, `docs/COMMERCE.md` §8). All
  optional and additive; a shop answering none of it draws what it drew before.
  - `CommerceProduct` gains `PriceFrom` (`"price_from"`: Core prints "From" before `Price`), `UnitPrice` (`"unit_price"`,
    "€12.40 / kg", beside every price Core draws), `StockNote` (`"stock_note"`, "Only 3 left"), `Choices`
    (`"choices"`, `CommerceChoice`: `selection`, `sku`, `price`, `old_price`, `unit_price`, `in_stock`, `stock_note`,
    `media_id`, `facts` — every combination of `Variations`, for the product page; at most `MaxCommerceChoices` = 250) and
    `Facts` (`"facts"`, `CommerceFacts`: `amount` a plain decimal, `currency` ISO 4217, `availability` from
    `SchemaAvailabilities()` — `SchemaInStock` … `SchemaOnlineOnly`, schema.org's words —, `condition` from
    `SchemaConditions()`, `gtin`, `mpn`, `brand`) — the machine values Core writes into structured data and never draws.
    `CommerceProduct.Validate` and `CommerceFacts.Validate` hold them (`MaxCommerceNote` = 200, `MaxCommerceFactText` =
    70, a GTIN of 8/12/13/14 digits with its GS1 check digit, an amount `ParseMinor` takes, one currency per product, a
    complete and distinct selection per choice, distinct SKUs of at most 127 bytes); `DispatchCommerceHook` runs it on every
    product it answers, so a product breaking a rule fails the call naming itself. Read 2026-11-06: Google's merchant
    listing and product variant structured data, Merchant Center's product data specification, Shopify's Liquid
    `product.variants` (250 unpaginated) and `price_varies`, WooCommerce's variation threshold.
  - `CommerceQuery.Sort` documents `best-selling` and `rating`. EXPERIMENTAL: `CommerceQuery.Filters` (`"filters"`,
    `CommerceFilters`: `price_min`, `price_max`, `options`, `brands`, `min_rating`, `in_stock`, `terms`), bounded by
    `CommerceFilters.Validate` (`MaxCommerceFilterAxes` 8, `MaxCommerceFilterValues` 20, `MaxCommerceFilterValueBytes` 128,
    `MaxCommerceRating` 5); a products, count or facets payload outside them is refused.
  - EXPERIMENTAL: the optional `CommerceFacets` (`Facets(ctx, q, want) ([]CommerceFacet, error)`) on hook
    `commerce.facets` (`HookCommerceFacets`; payload the query's fields and `want`, answer `{"facets": […]}`), `Serve` and
    `ServeCommerce` subscribing it only for a shop that implements it. `CommerceFacet` (`key`, `kind`, `label`, `options`,
    `min`, `max`), `CommerceFacetValue` (`value`, `label`, `count`), the kinds `CommerceFacetPrice`, `CommerceFacetOption`,
    `CommerceFacetBrand`, `CommerceFacetRating`, `CommerceFacetStock`, `CommerceFacetCategory`, `CommerceFacetKey`,
    `CommerceFacetInStock`, `MaxCommerceFacets` 16, `MaxCommerceFacetOptions` 60; counts are disjunctive.
  - PAYMENT MARKS: `PaymentMethod.Marks` and `PaymentOption.Marks` (`"marks"`), each one of `PaymentMarks()` (visa,
    mastercard, amex, maestro, paypal, klarna, apple-pay, google-pay, sepa, ideal, bancontact, eps, bank-transfer,
    cash-on-delivery; `PaymentMark…` constants, `ValidPaymentMark`), none twice — a describe naming any other is refused.
    The shop names the marks of everything its checkout takes — the options it offers and its own offline methods — on
    `CommerceEndpoints.PaymentMarks` (`"payment_marks"`), checked the same way; that list is all Core's strip draws.
  - `nildatest.Storefront` (`NewStorefront`): `Products`, `Product`, `Endpoints`, `Count`, `Facets` asked over the hooks'
    JSON as Core asks, and `CheckFacetCounts`, which holds every facet option's count to the shop's own `CountProducts`
    disjunctively. `nildatest.Payments` passes a method's marks on to its options.
  - Docs: `COMMERCE.md` §8 (a grid's `Variations` mean "needs a choice", `Cart?view=mini`, search hits through
    `ProductForContent`, every field above); `PLUGIN_SDK.md` documents the manifest's `taxonomy_archives` (Core has it).
  - SDK-first: Core mirrors the fields on its wire struct (`internal/plugin/commerce.go`, `payments.Method`/`Option`),
    asks `commerce.facets` and draws them in its next storefront step; an older Core drops them, and a shop may send them
    today.

- MAIL TEMPLATES: `MaxManifestEmailTemplates` 64 → 128 (Core D-111): Core lets a plugin declare 128 email templates, so a shop's legally required mails (the DSA review-report notices) fit beside its others; the 2 MB manifest bound still limits the total.

- PAYMENTS: ITEM LINES (commerce plan 10.15, "Stripe item lines"; `paymentlines.go`, `docs/PAYMENTS.md` §5 "Item lines").
  `PaymentSessionParams.Lines` (`"lines"`, `PaymentLine`s: `name`, `quantity`, `unit_amount_minor`, `total_minor`,
  `tax_minor`, `sku`, `url`, `image_url`, `fulfilment`) and `PaymentSessionParams.Breakdown` (`"breakdown"`,
  `PaymentBreakdown`: `shipping_minor`, `discount_minor`, `tax_minor`, `tax_included`) say what the payer is buying, for
  the processor's page to list. Optional. `Validate` holds them to the amount exactly — sum of totals + shipping −
  discount + tax unless included = `amount_minor`; each total is unit × quantity; nothing negative; the lines' tax within
  `tax_minor` — and to `MaxPaymentLines` (100, Stripe Checkout's payment-mode maximum), `MaxPaymentLineName` (127,
  PayPal's item name), `MaxPaymentLineSKU` (127), `MaxPaymentLineQuantity` (999,999), http(s) URLs. `PaymentSession.Lines`
  and `Breakdown` carry them to the gateway on `payment.start`; a re-price drops them. Read 2026-11-05: Stripe Checkout's
  `line_items` (no total in payment mode, one coupon, tax only by rate) and PayPal Orders v2's `items` and
  `amount.breakdown` (schema 2.36); PAYMENTS.md says how each gateway maps them and when it falls back to one line.
- `nildatest.Payments` keeps them, starts the gateway with them and drops them on a re-price; `StubGateway.Started`
  records the sessions it started (`TestTheItemLinesTravelToTheGatewayAndARepriceDropsThem`).
- Additive and SDK-first: Core keeps and relays them from its 10.15 step (`coreahead_test.go` lists the session's
  `lines` and `breakdown`); an older Core ignores the fields, so a consumer may send them today and the gateway sees none
  until then.

- THE SHOP: A PLUGIN'S OWN FIELDS, EXPERIMENTAL (the shop's door D2, plan 2026-11-05 §1J; `shopattributes.go`,
  `docs/ATTRIBUTES.md`, built on door D0). Two contracts, one per direction. `shop.attributes/v1`
  (`ContractShopAttributes`), PROVIDED by a plugin that owns fields or suggests addresses and used by the shop: `describe`
  (fallback `cached`) answers an `AttributeDescription` of `AttributeField`s — `key`, `target` (`AttributeOrder`,
  `AttributeLine`, `AttributeProduct`, `AttributeCustomer`), `type` (`AttributeFieldTypes()`: text, textarea, email,
  phone, url, number, boolean, select, radio, date — form types the screens and the owner's app already draw), `label`
  and `help` per language (`LabelIn`), `shown` (`AttributeShownCheckout`, `AttributeShownProductPage`,
  `AttributeShownProductForm`, `AttributeShownOrder`), `required`, `choices` (`AttributeChoice`), `min_length` /
  `max_length`, `min` / `max`, `min_date` / `max_date` (a date or `P<n>D`), `skus` — plus `validates` with its
  `when_unanswered` (`AttributeBlock`, `AttributeAllow`) and `address_suggestions`; `validate` (fallback `block`;
  `AttributeValidateRequest` → `AttributeValidateAnswer` of `AttributeError`s); `address.suggest` and `address.resolve`
  (fallback `skip`; `AddressSuggestRequest` → `AddressSuggestAnswer` of at most `MaxAddressSuggestions` = 5
  `AddressSuggestion`s; `AddressResolveRequest` → `AddressResolveAnswer`). `shop.attribute.values/v1`
  (`ContractShopAttributeValues`), PROVIDED by the shop: `values.get` (`AttributeValuesRequest` of an `AttributeTarget` →
  `AttributeValuesAnswer`), and the write commands `value.set` (`AttributeSetRequest`; record `attribute.value`, emits
  `EventAttributeSet`, `ecommerce.attribute_set`) and `value.undo` (`UndoInput`; emits `EventAttributeUndone`), each
  answering an `AttributeWriteAnswer` — the `WriteAnswer`, or `refused` with a code (a value's, `AttributeNotOffered`,
  `AttributeNoRecord`, `AttributeUnknownWrite`, `AttributeUndoneAlready`, `AttributeReplaced`): a refusal is a result,
  never an error, because Core's desk relays an error as the provider failing (blocked, retried, "unavailable").
- `AttributeField.CheckValue(raw, today)` is the one rule for a value (an `*AttributeValueError` with a code the shop
  words in the buyer's language); `AttributeField.Validate` / `AttributeDescription.Validate` the rules of a declaration;
  `AttributeField.FormField(locale)` the field in the `PageForm` vocabulary; `AttributeInputName` its input's name.
  `AttributeMethods` / `AttributeValuesMethods` answer the contracts and check every answer (`AttributeProvider`,
  `AttributeValidator`, `AddressSuggester`, `AttributeValuesProvider`; `Serve` finds them); the shop asks with
  `Services.AttributeFields`, `ValidateAttributes`, `SuggestAddresses`, `ResolveAddress`, the plugin with
  `Services.AttributeValuesOf`, `SetAttribute`, `UndoAttribute`. `AddressSuggestLimiter` bounds the shop's address proxy
  to `AddressSuggestPerMinute` (30) calls per visitor a minute (`ErrAddressSuggestLimited`).
- `nildatest.AttributeShop` (`NewAttributeShop`) is the shop's side, holding the rules: offered fields only, the one value
  rule, required fields, validate with each plugin's fallback, the snapshot into the order (`AttributeOrder`,
  `AttributeSnapshot`), the write commands with keys, attribution, undo and events (`ShopEvent`), the bounded proxy.
- Docs: `WRITE_COMMANDS.md` §7 — a write command's refusal is an answer, not an error; the shop emits under `ecommerce.*`.
- Additive: nothing changes for a plugin built before. Core relays both contracts as files (nothing to build there); the
  shop provides and uses them from its D2 step (plan row 10.5).

- SITE READER, EXPERIMENTAL (site-import stage 2, card K1; `sitereader.go`, `docs/SITE_READER.md`). The contract `site.reader/v1`: a plugin that
  reads another platform's site answers `sources`, `start`, `feed`, `next` and `cancel` with pieces of the `siteimport` model; only Core's
  importer asks, one named provider, fallback `block`, agent class `none`. Sequence numbers make a retry return the same piece; a restarted
  plugin answers `reader_restarted`; the owner's password is held in memory for the read only. Needs the next tag; Core's side is card K2.
- SITE MODEL, EXPERIMENTAL (site-import stage 2, card X0a; package `siteimport`). The neutral description of a site that a
  reader plugin hands to Core's importer — `Site` and its parts — and Core's streaming snapshot encoding (`Encode`, `Decode`), copied from
  Core's `internal/importer` with its json tags unchanged. The shape follows Core's snapshot version (4), not this module's. Core's nine golden
  sites are copied under `siteimport/testdata/golden` with their sha256 manifest, and every one must read and write back to the same bytes.
- A SERVICE ANSWER'S `<`, `>` AND `&` GO ON THE WIRE AS THEMSELVES (site import, owner's option A; `services.go`, `docs/SERVICES.md`,
  `docs/SITE_READER.md`). `DispatchServiceHook` (so `Serve`, and `nildatest`'s fake host) encoded a provider's answer with
  `json.Marshal`, which writes each `<`, `>` and `&` as a six-byte escape, also inside a `json.RawMessage`; Core measures
  `site.reader/v1`'s bounds (a piece, an element, the whole read) on the bytes it reads, so HTML-heavy content was cut at a
  fraction of its size. The answer is now `json.Marshal`'s encoding with HTML escaping off: compact, map keys sorted, a raw
  member compacted as it is (an escape the provider wrote stays as written), U+2028/U+2029 still escaped, decoding to the same
  values and checked against the contract as before. A consumer reads the same values. A reader plugin that encodes its piece's
  members escaped itself still sends (and rightly counts) six bytes each; the room is regained once it encodes and measures them
  unescaped. SITE_READER.md now also states a reader's refusal shape, `code: sentence`.
- REFUND REASON FOR FRAUD (commerce plan 10.3 F2/F3; `payment.go`). `RefundReasonFraudulent` (`"fraudulent"`): a refund whose reason is exactly this was given because the payment was fraud; a gateway that can tell its processor does (plugin-stripe 0.8.1 sends Stripe `reason=fraudulent`). Core landed the session's `risk` (core #181), so nothing is ahead of Core.

- IMPORT CAPABILITY (site-import S4; `capabilities.go`). `CapImport` (`import`): a plugin that brings a site over from another
  platform may use Core's `/api/rest/v1/plugin-import` door — first, a lookup of what earlier imports already made on this site
  (a page, a term, a file, by the old site's id) so a re-run makes nothing twice. Plugin-only, shown to the owner at install.
  Needs a Core that knows the capability; an older Core refuses a manifest that declares it.
- SHIPPING: SERVICE POINTS, EXPERIMENTAL (commerce plan 10.9, step S7; `shippingpoints.go`, `docs/SHIPPING.md` §3
  "Service points"). `shipping.carrier/v1` gains the method `service_points` (`CarrierServicePointsGet`, agent `read`,
  described EXPERIMENTAL): `ServicePointsRequest` (`service`, `country`, `postal_code`, `city`, `line1`, `latitude`,
  `longitude`, `limit` 1 to `MaxServicePoints` = 50, 0 for `DefaultServicePoints` = 10, `locale`; a country alone is
  refused) answered by `ServicePointsResult` (`points` nearest first, or a failure code; none is "nothing near") of
  `ServicePoint`s (`id`, `name`, `carrier`, `kind` — `ServicePointShop`/`Locker`/`PostOffice`/`Depot`, `parcel_shop`,
  `locker`, `post_office`, `depot` — `address`, `latitude`, `longitude`, `distance_meters`, `hours` of
  `ServicePointHours{day, opens, closes}`, `requires_post_number`). A carrier implements `CarrierServicePoints`; without
  it the method answers `unsupported`. `CarrierSupportServicePoints` (`"service_points"`) marks a service that delivers to
  a point; `CarrierRate.NeedsServicePoint` (`"needs_service_point"`) its rates, offered only to a shop that sets
  `RatesRequest.ServicePoints` (`"service_points"`); `RatesRequest.ServicePoint` and `LabelRequest.ServicePoint`
  (`ChosenServicePoint{id, post_number}`) carry the buyer's choice. A new failure code, `CarrierInvalidServicePoint`
  (`"invalid_service_point"`). `CarrierMethods` refuses a point service from a carrier that cannot list points, a point
  rate the shop did not ask for, a points answer past its limit, and answers a label for a point service with no point,
  or a point on a door service, `rejected` / `invalid_service_point` without asking the carrier to buy; a return label
  names no point. The shop calls `Services.ShippingServicePoints(ctx, provider, req)` — one carrier, the point rate's.
  Read 2026-11-05: Sendcloud's Service Points API v3 and its shipments' `to_service_point`; Shippo's API has no point
  search. `nildatest.StubCarrier.Points` lists points for its point services (`NeedsServicePoint` quotes) and
  `LabelsAsked` records label requests.
- Additive: a carrier built before is answered `unsupported` by `service_points`; a shop built before sets no
  `service_points` and is offered no point rate. Core relays the method once it is built on this release (its contract
  book reads this file); its agent tool is `shipping.carrier.v1.service_points`.

- SHIPPING: PAPERLESS RETURN LABELS, EXPERIMENTAL (commerce plan 10.9b; `shipping.go`, `docs/SHIPPING.md` §3 "Return
  labels, and paperless ones"). `LabelRequest.Paperless` (`"paperless"`, only with `Return` — `Validate` refuses it on an
  outbound label) asks a service listing `CarrierSupportPaperlessReturns` (`"paperless_returns"`) for a return the buyer
  drops off with a code instead of a printout; a carrier that cannot answers with a printed `return_label`, never a
  refusal. `LabelResult.DropOffCode` (`"drop_off_code"`, one plain line of 1 to 64 characters, on a `purchased` label only)
  is the code shown at the counter (ShipEngine's `handoff_code`); a QR to scan is a document of the new kind `"return_qr"`
  (`png` or `pdf`, never `zpl`) — Shippo's `qr_code_url` (`extra.qr_code_requested`, docs.goshippo.com
  spec/shippoapi/public-api.yaml, read 2026-11-05) and Sendcloud's labelless return (`delivery_option`
  `drop_off_labelless`, `functionalities.labelless`, sendcloud.dev/.openapi/v3/returns/openapi.yaml, read 2026-11-05).
  `LabelResult.ValidateFor(req)` refuses either on a label not asked as a return; `CarrierMethods` and
  `Services.CreateShippingLabel` run it. `nildatest.StubCarrier.Paperless` gives a `DROP-…` code and a `return_qr`, and the
  stub's return labels are now `return_label` documents.

- PAYMENTS: THE PROCESSOR'S RISK VERDICT, EXPERIMENTAL (commerce plan 10.3, step F1; `paymentrisk.go`, `docs/PAYMENTS.md`
  §4 "The processor's risk verdict", §5 "Reading the processor's risk verdict" and "Risk's decisions"). `PaymentRisk`
  (`{"level","score","reason"}`): `Level` one of `RiskNormal`, `RiskElevated`, `RiskHighest`, `RiskNotAssessed`,
  `RiskUnknown` (`"normal"`, `"elevated"`, `"highest"`, `"not_assessed"`, `"unknown"` — Stripe Radar's
  `outcome.risk_level`, docs.stripe.com/api/charges/object, read 2026-11-05), `Score *int` 0 to 100 (Stripe's
  `outcome.risk_score`; never beside not_assessed or unknown), `Reason` one plain line of at most 200 characters.
  `PaymentResolution.Risk` and `PaymentAuthorization.Risk` (`"risk"`) carry it from the gateway; their `Validate` — run
  by the client before it sends — refuses any other level, a score out of range or on a payment nobody assessed, and a
  reason that is not one plain line. `PaymentSession.Risk` (`"risk"`): Core keeps the FIRST verdict a hold or a
  resolution reports, never moves or clears it, keeps a late one and tells the consumer. Nil means the gateway did not
  say — not `not_assessed`.
- `nildatest.Payments` keeps it as Core will (`TestTheKitKeepsTheRiskOnTheSession`, `TestTheHoldCarriesTheRisk`).
- Additive and SDK-first: Core keeps it from plan 10.3's step F1 (`coreahead_test.go` lists the session's `risk`); an
  older Core ignores the field, so a gateway may send it today and nothing is kept until then.

- PAYMENTS AND THE SHOP: EXPRESS WALLET BUTTONS, EXPERIMENTAL (commerce plan 10.2, step X1; `paymentexpress.go`,
  `commerceexpress.go`, `docs/PAYMENTS.md` §4 "Express wallet buttons" and §5 "Express checkout and re-pricing",
  `docs/COMMERCE.md` §6). A method says how its buttons are drawn, `PaymentMethod.Express` (`PaymentExpress`: `Kind`
  `ExpressToken` — Stripe's Express Checkout Element, nothing at the processor until the payer confirms — or `ExpressOrder`
  — PayPal's buttons, the order exists from the press; `Script`, a path on the gateway's own route; `Origins`, a
  `PaymentExpressOrigins` of https hosts by CSP directive; `Config`, PUBLIC values), passed on `PaymentOption.Express`.
  `PaymentExpress.Validate`, run by `DispatchPaymentGatewayHook` on every describe answer, refuses a public config holding a
  value shaped like a secret (`sk_`, `rk_`, `whsec_`, `A21AA`, `-----BEGIN`, `Bearer `, `_secret_`, a JWT) or under a key
  named like one (`secret`, `password`, `passwd`, `private`, `token`, `signing`, `credential`) — PayPal's client secret has
  no shape, so its name is what is refused — and a token kind without a credential type, an order kind without `reprice`,
  express on a card-at-a-reader method, a script off the route, an origin that is not one https host.
- `PaymentStartResult.ClientAction` / `PaymentSession.ClientAction` (`ClientAction`, `"client_action"`): what the gateway's
  own script on the payer's page needs (a PaymentIntent's client secret for `stripe.handleNextAction`, a PayPal order id).
  A `redirected` answer carries a `redirect_url`, a `client_action` or both — never neither; a client action is only on a
  redirected answer, one line of visible ASCII up to 1,024 bytes, and prints `[client action redacted]`.
- `CredentialStripeConfirmationToken` (`"stripe.confirmation_token"`): the wallet's one-use token, sent as the session's
  `Credential` on the existing delegated-credential path (v0.20.0) — named like `stripe.shared_payment_token`.
- `Payments.Reprice(ctx, key, id, PaymentReprice{AmountMinor, ShippingOptions})` (`POST /payments/sessions/{id}/reprice`)
  and the gateway hook `HookPaymentReprice` (`payment.reprice`: `PaymentRepriceRequest` → `PaymentRepriceResult`) for a
  gateway that implements `PaymentRepricer`, on a method listing `SupportReprice` (`DispatchPaymentGatewayHook` refuses it
  otherwise; Serve subscribes it). Only before the payer approves: a session `created` or `redirected`, whose approval has
  not reached Core (`Confirm`), that no credential, kept method or reader started. At most ten `PaymentShippingOption`s,
  exactly one selected. `payments/v1` describes the method (experimental).
- The shop's side: `CommerceEndpoints.Express`, a POST on the shop's route taking an `ExpressRequest`
  (`ExpressStepQuote`/`Start`/`Complete`, `ExpressFromCart`/`ExpressFromProduct` with an `ExpressItem`, a partial
  `WalletAddress` — country, region, city, postal code, nothing else — the press, the session, the credential, the
  `ExpressPayer`) and answering an `ExpressAnswer` (with `ExpressLine`s); `DecodeExpressRequest` refuses an unknown field,
  so a street or a name in the partial address is refused, not dropped. It is the shop's own endpoint and not
  `shop.checkout/v1` (retired in v0.47.0, commerce DR-19): the basket's cookie reaches only the shop's route.
- `nildatest.Payments` holds the same rules — a re-price after `Confirm`, of a credential's session, through a method that
  cannot, refused; a refusal 409 and no answer 503 with nothing changed; a start's client action kept only while
  redirected — with `Payments.Reprice` for a gateway's test; `StubGateway` answers `payment.reprice` (`RepriceResult`,
  `RepriceErr`, `Repriced`).
- Additive and SDK-first: Core serves the hook, the route and the session's `client_action` from plan 10.2's step X2b, and
  draws the express widget from X2a. Core's X2b has them now, so the truth tests hold all three again
  (`coreahead_test.go` no longer skips them).
- EXPRESS, THE REVIEW OF X1: THE PAYER PAYS WHAT THE SHEET SHOWED, OR NOTHING. `ExpressRequest.ShownTotal`
  (`*ExpressTotal{AmountMinor, Currency}`, JSON `"shown_total":{"amount_minor","currency"}`) is the total the wallet's sheet
  showed the payer, sent with `complete` only: REQUIRED with a token kind's `credential`, optional with an order kind's
  `session`; `Validate` refuses it missing there, with `quote` or `start`, or not a positive amount in an ISO 4217
  currency. The shop re-quotes at the payer's final address and chosen `shipping_option` (which `complete` now carries)
  and on any difference answers `ExpressAnswer.Code` `ExpressTotalChanged` (`"code":"total_changed"`, HTTP 409) with the
  new total and nothing charged — `Validate` refuses such an answer with a `status`, `client_action`, `redirect_url` or
  `order_url`, unavailable, or with no message. Every quote naming a `session` carries the start's `press`, and `Validate`
  refuses one that does not: the shop re-prices only the session its start keyed by that press (`docs/COMMERCE.md` §6).
  Core's bridge forwards `shown_total` from its next X2a step (`coreahead_test.go` lists it).
- `WidgetExpressCheckout` (`"express_checkout"`): the key of Core's wallet-button widget (config `placement` — empty for a
  product's page, `cart`, `checkout` — `product_id`, `error_text`), so a shop's templates can place it; Core's X2a
  registers it as a shop widget and `TestTheWidgetKeysAreInCoresRegistry` holds the SDK's list to Core's.
- FIX: `Serve` now hands `payment.reprice` to a `PaymentRepricer`'s `RepricePayment`. It subscribed the hook and then left
  it to the author's `HandleHook`, so a PayPal-like gateway built on `Serve` failed every re-price.
- Docs: PAYMENTS.md §4 documents Core's page bridge as built — `window.nilda.express.gateways.<key> = {mount}`, the
  `nilda:express-gateway` event, the host `{element, config, locale, gateway, method, kind, quote, start, complete, done,
  available}`, the press minted per start or token kind's complete and reused by the bridge's retries
  (`TestTheExpressBridgeIsCores` holds it to Core's source) — and that Core opens a processor's origins on its public page
  responses only, while a gateway offers a switched-on express method and the shop has its Express endpoint.
- Docs: PayPal's stored-credential kind is said for a card only (`payment_type`, Orders v2 schema 2.36); a PayPal wallet's
  is not sent yet (plugin-paypal's open item P-13).

- PAYMENTS, DOOR D5 — WHICH KIND OF MERCHANT-INITIATED CHARGE (`paymentsaved.go`, `docs/PAYMENTS.md` §5 "A method kept for
  later" and "Door D5's decisions"). `PaymentSession.OffSession` (`"off_session"`): on every charge of a kept method
  (`ChargeSaved`) Core sets `OffSessionRecurring` (`"recurring"`) or `OffSessionUnscheduled` (`"unscheduled"`) from what
  the mandate is FOR — a subscription's charges recurring, a pre-order's unscheduled, an update's the kind of the agreement
  it replaces — `OffSessionOf(PaymentMandate, replaced string)` — and the gateway's `StartPaymentWithSaved` reads it to
  tell its processor (Stripe's `off_session` `recurring` / `one_off`). `""` on every session a payer is present for.
  `PaymentMandate.Replaces` (`"replaces"`): an update names the kept method whose agreement it replaces (one of the
  consumer's — Core answers 404 otherwise) and names an interval exactly when that agreement is a recurring one.
  `PaymentMandate.Validate` refuses, on a NEW mandate, a pre-order naming an interval, an update naming nothing it
  replaces and `Replaces` on anything else; a mandate Core kept before is read as it was kept (review 2026-11-05: the
  first cut read the interval, which the consumer writes, so a pre-order naming one claimed "recurring").
  The rest of the door was already here (v0.30.0): the consumer holds the kept method's id, Core the token; the charge is
  keyed and inside the mandate; a decline is a result. `nildatest` sets it, and `StubGateway.SavedSessions` records the
  sessions its kept-method charges were for.
- PAYMENTS, DOOR D5 — A CARD AT A READER, EXPERIMENTAL (`paymentinperson.go`, `docs/PAYMENTS.md` §4 "A card at a reader"):
  `SupportCardPresent` moves here and is defined. A method listing it lists its card readers, `PaymentMethod.Readers`
  (`"readers"`, `PaymentReader{ID, Label}`, 1 to 100, ids at most 255 bytes, labels 1 to 100 characters; none on any other
  method), passed on `PaymentOption.Readers`, and belongs to a `PaymentCapturer` (`DispatchPaymentGatewayHook` refuses it
  otherwise). `PaymentSessionParams.Reader` / `PaymentSession.Reader` (`"reader"`) make an in-person session: no return
  address, no `Save`, no `Credential`; Core refuses a reader the method does not list and an online session on a
  `card_present` method. The gateway answers its start `pending`; an answer with a page is closed `rejected`,
  `invalid_request`; a lost answer is held `pending` and looked up. `Payments.Void` also calls off a session still
  waiting at its reader (`pending`, not held for a person) — and, sent before the start answered (`created`), is recorded
  and handed to the reader once it answers, never a 409. A reader's payment has a deadline (the start answer's
  `ExpiresAt`, at most 30 minutes; five minutes when it names none): past it Core asks `payment.sync`, then voids it
  itself; a void unanswered eight times is left `pending` for a person, whose reject of a payment still waiting at its
  reader asks the void first. A gateway REJECTS a reader action that timed out at the processor. A report that ends a
  session drops a void asked of it (`void_requested` is never beside `resolved`), and a void is never asked of a session
  no longer voidable. A reader's id and label are one plain line: no control character, no bidirectional override or
  isolate (U+202A–U+202E, U+2066–U+2069). A kept method is never charged on a method that lists `card_present`.
  `nildatest.Payments.ExpireReader` runs Core's deadline for one reader's payment. Inside `payments/v1`, optional fields only an in-person
  gateway meets, and outside its no-breaking promise until a till built by Nilda ships green on them. `nildatest` holds
  the same rules.
- Additive: an older Core takes no in-person session (it reads no `reader`, so a session without a return address is
  refused there) and never sets `off_session`; a gateway built before ignores both. Needs the Core built on this release (its migration
  0378).

- PAYMENTS: AUTHORISE, THEN CAPTURE (`paymentcapture.go`, `docs/PAYMENTS.md` §4 "Holding the money" and §5 "Take the
  money later"). A session status `PaymentAuthorized` (`"authorized"`): the processor holds the money and has not taken
  it. The machine's new moves: created, redirected, pending and expired → authorized; authorized → pending, resolved,
  rejected (never expired — Core asks the gateway before it closes a lapsed hold). Core's machine is held to it by
  `TestCoresPaymentMachineIsTheSDKs` and Core's `TestTheMachineIsTheSDKs`.
- `PaymentSessionParams.Capture` (`"capture"`): `CaptureAutomatic` (the default, `""`) or `CaptureManual`, only on a
  method listing `SupportManualCapture` (`"manual_capture"`) and never for a session that only keeps a method.
  `PaymentSession` gains `Capture`, `AuthorizedMinor`, `AuthorizationExpiresAt`, `CaptureRequestedMinor` and
  `VoidRequested`; a captured session resolves at what was taken (its `AmountMinor` becomes it).
- The gateway's report `Payments.Authorize(ctx, id, PaymentAuthorization{AmountMinor, Currency, ProviderRef,
  ExpiresAt})` (`POST /payments/sessions/{id}/authorize`); the consumer's `Payments.Capture(ctx, key, id, amountMinor)`
  and `Payments.Void(ctx, key, id)` (`…/capture`, `…/void`). One capture per session, of at most what is held; the same
  amount again is the same capture, another is 409; a refund of an authorized session is 409. A 503 is a gateway that
  did not answer: Core keeps it asked and asks again with the same key.
- Gateway hooks `HookPaymentCapture` (`payment.capture`: `PaymentCaptureRequest` → `PaymentCaptureResult`) and
  `HookPaymentVoid` (`payment.void`: `PaymentVoidRequest` → `PaymentVoidResult`), asked under the session's lock with
  the idempotency key `CaptureKeyPrefix`/`VoidKeyPrefix` + the session's id. A gateway implements `PaymentCapturer`;
  Serve subscribes it, `DispatchPaymentGatewayHook` checks both directions and refuses a describe answer listing
  `manual_capture` from a gateway that is not one. `payments/v1` describes both methods.
- `PaymentFailVoided` (`"voided"`): Core's verdict for a hold the consumer let go; a gateway reporting it is refused.
- `nildatest.Payments` holds the same rules — a capture above the hold, a second capture of another amount, a refund
  before the capture and a capture or void of a session that holds nothing are refused; a lost answer is asked again by
  `Deliver` under the same key — and `ExpireHold` runs Core's expiry of one hold. `StubGateway` answers both hooks
  (`CaptureResult`, `CaptureErr`, `Captured`, `VoidResult`, `VoidErr`, `Voided`).
- A hold report's rules (Core's `Authorize`, `docs/PAYMENTS.md` §4 step 1): a deadline already past is kept as now, so
  the hold lapses at the next sweep (seven days when none is named, thirty at most, as before); the same hold reported
  again with another deadline keeps it — and the consumer hears it — while no capture or void was asked; a hold of
  another amount or currency is 422 on an `authorized` session too; once a capture or void was asked a hold report
  moves nothing (the same hold while `authorized`, 409 after — never a slow capture back to `authorized`); and a
  session held for a person (`amount_mismatch`) is 409, its reported amount kept. `nildatest` holds the same rules.
- A void whose answer was lost (Core's `Reject`, `docs/PAYMENTS.md` §4): a gateway's `cancelled` or `expired` on an
  `authorized` session whose void was asked ends it as the void's answer would — `rejected`, `voided`, the void no
  longer owed — so the consumer hears it was voided. `nildatest` holds the same rule.
- The reserved payment names shrink to `partial_capture` (multicapture, which the contract does not do).
- Additive: an older Core answers the three routes 404 and ignores `capture` — it takes the money at once — so read the
  session's `capture` back: one that does not say `manual` holds nothing. Needs the Core built on this release.


- `ServiceContract.Status` (`"status"`): `"experimental"` marks a published contract outside the v1 no-breaking promise
  until its first Nilda plugin ships green on it; empty or `"stable"` is inside it. `ServiceContract.Experimental()`,
  `ContractStable`, `ContractExperimental`. `ParseContract` refuses any other status.
- `ContractMethod.Write` (`"write"`, `ContractWrite{Record, Undo, Emits}`): a WRITE COMMAND — a method by which a plugin
  changes a record its provider keeps. `ParseContract` holds it to `docs/WRITE_COMMANDS.md` §2: idempotent, fallback
  `block`, never a read tool, never one of Core's hooks, its answer requiring `record_id` and `already`
  (`WriteAnswer`), an `undo` that is a write command of the same record kind whose request requires `record_id`
  (`UndoInput`), and the event it emits once committed.
- `docs/WRITE_COMMANDS.md`: the pattern every write any contract carries keeps — the idempotency key stored by the
  provider under a unique index for as long as the record lives, the writing plugin kept on the record, the undo, the
  event back, compensation instead of transactions across plugins.
- Additive: no contract shipped before carries either field, so each loads as before; an older Core that reads a
  contract file with them is built on an older SDK and never sees them.

## v0.57.0 — 2026-11-04 (privacy requests by phone)

- `PrivacyRequest.Phones` (`"phones"`, omitted when empty): numbers an operator holding `privacy.manage` typed for a
  guest who booked with a phone and no address. A phone is never verified, so Core sends one only from that operator.
  Each is in the one normal form `NormalizePrivacyPhone` writes — the ASCII digits, a leading `00` dropped, `""` below
  6 or above 20 digits (booking's blocklist rule, moved here so Core and every plugin compare alike). Exact on that form,
  never a suffix or a fuzzy match: an erasure that matched another guest cannot be taken back. It guesses no country, so
  `+43 660 1234567` and `0660 1234567` are two numbers and Core's screen asks for both. `DispatchPrivacyHook` hands a
  holder the normal forms and drops an entry that is no phone.
- `PrivacyRequest.AskedPhone(phone)`: the match, by that rule; `""` is never asked about.
- `PrivacyRecord.Phone` (`"phone"`): the request phone a record matched. A record names exactly one of `Email` or
  `Phone`, one the request asked about; `PrivacyExport.Validate` refuses a record naming neither, both, or an unasked
  one, and refuses the whole answer for it. `Email` is now omitted from the wire when empty — every valid answer before
  this one had an address, so none of them changes.
- `nildatest.CheckPersonalDataHolderByPhone(ctx, h, phone)`: the export, erase, export, erase round asked by phone.
- Additive both ways: an older Core sends no `phones` and an older plugin ignores them. Needs the Core that sends them.

## v0.56.0 — 2026-11-04 (Core quiets a plugin's own API calls and events made while serving an importer call)

- `CallHeader` (`"X-Nilda-Call"`): inside a hook call whose request Core named (`CallOf(ctx).RequestID`), every request
  the API client makes with that `ctx` — any verb, any scheme — carries the id on this header, and `Core.Emit` puts the
  same id in `EmitEvent`'s gRPC metadata under `x-nilda-call`. Outside a call, or in one Core did not name, neither is
  sent. Core uses it to keep quiet the callbacks a plugin makes while serving an importer call; it names a call only
  while that call is in flight. Nothing to change in a plugin that passes its call's `ctx` on. Needs the Core that
  reads it; an older Core ignores the header and the metadata.

## v0.55.0 — 2026-11-04 (a form can pick a file)

- `FormFile` (`"file"`, the widget vocabulary's `FieldFile`) is a form field type: one Core media id of any kind — the
  file a downloadable product sells, as WooCommerce's "Downloadable files" picks it. Core's picker for it offers the
  library's private files too. Needs the Core that draws it.

## v0.54.0 — 2026-11-04 (a file that is sold is private, and a plugin's whole admin can be translated)

- `MediaItem.Private` and `API.SetMediaPrivate(ctx, id, private)` (`PATCH /media/{id}`, `media.write`): a private
  media item is served to nobody on its public address (`/api/v1/media/{id}/file` answers "not found" for it, every
  size), so a file a plugin sells is not free to whoever learns its address. Core refuses to make private a file a page,
  a post or a setting shows, naming them (an `*APIError`, 422); upload a copy for the sale.
- `MediaFile` now reads the bytes through the API's own file route (`GET /media/{id}/file`, `media.read`) with the
  plugin's token, so it opens a private item too; it no longer fetches the item's `url`. The bytes are the original,
  always as a download (`Disposition` is `attachment` with the file's name). Needs the Core that serves the route.
- `nildatest.Site` serves both and refuses a private file on the public address; `SetMediaUsedOn(id, where…)` makes
  it answer as Core does for a file the site shows.
- The translation bounds follow Core's, which now sends an admin only its own language's table:
  `MaxManifestTranslationStrings` is 1500 a language (was 300), the new `MaxManifestTranslationLocaleBytes` is 192 KiB a
  language, and `MaxManifestTranslationBytes` is 1 MiB for all languages as stored (was 128 KiB, which every admin's
  browser used to receive). A manifest within the new bounds needs the Core that has them.

## v0.53.0 — 2026-11-03 (which door Core took a call through, and abilities no model is offered)

- `CallerKind(ctx)` and `CallInfo.CallerKind`: which door Core took the hook call through — `CallerKindAI` (the
  site's assistant: the panel's chat, the app's, Core's admin tools acting for it), `CallerKindMCP` (a connected AI
  app, or a connector's REST invoke), `CallerKindImporter` (Core's importer) or `CallerKindOwnerUI` (a person's own
  press) — and `false` when Core did not say: an older Core, or one of Core's own hooks. It is the new wire field
  `HookRequest.caller_kind`, set by Core's own code and never from the payload, so a model that writes an ability's
  arguments cannot write it (`TestTheCallerKindNeverComesFromTheArguments`). A kind names a door, never a person.
  `nilda.WithCallInfo(ctx, nilda.CallInfo{CallerKind: …})` sets it in a plugin's test.
- `Ability.Callers` (`"callers"` in plugin.json, written by `nildatest.ManifestAbilities`; the wire's
  `Ability.callers`): the caller kinds Core may call an ability for. Empty is every ability before. One that names
  neither `ai` nor `mcp` is in no tool list a model reads, and Core refuses it to a kind it does not name; Init
  refuses a kind Core does not send, or one named twice. Booking's import doors are the first (its plan's ADR-7: the
  guest list never passes through an AI). Both need the Core that reads them; check `CallerKind` in `Run` as well.

## v0.52.0 — 2026-11-03 (the v0.50.0 work below, tagged again; no change of its own)

## v0.51.0 — 2026-11-03 (the v0.50.0 work below, tagged again; no change of its own)

## v0.50.0 — 2026-11-03 (an action's one-time value, a report's notice, a body, a layout, a direction, Core's resources typed and faked, a route's gross prices, the manifest bounds)

- Core's manifest bounds, exported: `MaxManifestAdminPages`, `MaxManifestLabelLen`, `MaxManifestTranslationLen` and the
  rest of the 86 `MaxManifest…` constants (manifestbounds.go, the guide's table) — every count, length and size Core's
  manifest validation states as a number, each with its unit (a count, characters or bytes), held to Core's source and
  its unit by `TestTheManifestBoundsAreCores`, so a plugin's test holds its plugin.json to Core's numbers instead of
  copies (the shop's sixty had gone stale: a translation string was 400 bytes there and is 500 characters in Core).
- `AdminActionResult.Reveal` (and `MaxAdminRevealLen`): a value the owner must copy now — a new API key — drawn by the
  admin in a dialog that stays until closed, with a copy button, never translated and kept out of Core's logs.
- `AdminReportResult.Notice` (and `MaxAdminNoticeLen`): one sentence above a report, chiefly what a zero means
  ("requests are switched off"), translated like a label. Core's lesson 13; the shop's Reports page already used all
  eight of Core's metrics, so it had nowhere to say it.
- Both found by the shop's Unit 12 end review; Core draws them from the same release.
- `TextBody`, `BodyText`, `CheckBody` and `ErrNotABody`: a content item's body as the Lexical document Core stores and
  refuses any other shape of (`TestTheBodyRuleAndTheDirectionAreCores` holds `CheckBody` to Core's rule).
- `LayoutTree`, `LayoutNode`, `LayoutSection` / `LayoutColumn` / `LayoutWidget`, and `Widget…` keys for the shop's
  widgets beside `WidgetButton` and `WidgetPostContent` (held to Core's LayoutNode and widget registry).
- `LocaleDirection`: "rtl" or "ltr" from the language's script, Core's list of right-to-left scripts.
- `ShowsPricesIncludingTax(r)` and `nildatest.Visitor.ShowPricesIncludingTax`: whether a consumer price must include tax,
  as Core tells the shop's own routes (EU Directive 98/6/EC) — the hook queries' answer, for its cart page and offer cards.
- Core's resources, typed: methods on `*API` for the REST routes a plugin used through the untyped `Get`/`Post`/`JSON` —
  `User`, `Users` (`UserQuery`), `Site`, `PageBySlug` (`PublishedPage`, `HeadMeta`), `Breadcrumbs` (`Crumb`),
  `ContentTerms`, `SetContentTerms` (which keeps the item's other taxonomies, as Core's full-replace PUT does not),
  `TaxonomyTerms` (`Paging`), `CreateTerm` (`NewTerm`), `TermContent` (`TermContentQuery`, `ContentRef`), `Menus`,
  `Menu` (`MenuTree`), `CreateMenu`, `AddMenuItem` (`NewMenuItem`), `HoldingPages` (`HoldingPage`), `SetLayout`
  (`LayoutWrite`), `PublishContent` (`PublishResult`), `Media` (`MediaItem`) and `MediaFile`; with `Term`, `Menu`,
  `MenuItem`, `User`, `Site`, and the constants `MenuLocation…`, `MenuItem…`, `LayoutWrittenTo…` and `UserStatus…`.
  `TestTheTypedRoutesAreCoresOwn`, `TestTheTypedShapesAreCores` and `TestTheTypedConstantsAreCores` hold them to Core.
  `MediaFile` answers a `MediaBytes` — the open bytes with the item and the type, length and `Content-Disposition` Core
  served them with — so a plugin streaming a download needs no second read; Core's REST API has no file route, and the
  shop's `GET /media/{id}/file` on it was a 404 on every install.
  nildatest.NewSite: a stateful fake of the same routes (and the content routes) for a plugin's tests. Its `/reference` routes answer
  from its own `Reference()`, so one fake Core serves both.
- nildatest.Reference: `SetEUVATRates`, `SetExchangeRates`, `SetTaxTable`, `Down`, `Asked` — it answered two of the five
  reads, so a plugin's invoices and tax table needed fakes of its own.
- The guide says what today's Core does: a translation string is bounded at 500 characters, not 400 bytes; an
  `editor_commands` `content` must be an object with a `nodes` list; a row action whose handler panicked is "may have
  run", like a lost answer; and on Dragonfly a full kv namespace is re-counted at most once a minute before a refusal.
  nildatest's `Payments.Decide` refuses a note holding a NUL, as Core's does.
- Every payment `Validate` (session, refund, resolution, rejection, address, credential, dispute, external refund,
  charge), the idempotency key, and a gateway's describe, start and refund answers refuse a NUL character in their
  text, as Core does since its 2026-10-30 review: "text in a payment may not hold a NUL character". nildatest refuses
  one in a pending or refund-resolve report's `provider_ref` too. `TestTheNULRuleIsCores` holds each to Core's line.

## v0.49.0 — 2026-11-03 (what Core sends a plugin, typed)

- Core's own events by name: `EventContentCreated` … `EventContentImported`, `EventFormSubmitted`,
  `EventFormSubscribe`, `EventUserErased` (beside the existing `EventUserEmailVerified`), and `CoreEventData` for their
  data (`docs/EVENTS.md`, "Core's own events"). `TestCoreEventNamesAreCoresOwn` holds the list to Core's.
- The storefront's wire: `AddToCartRequest` and `FirstTouch` (what Core's add-to-cart button sends), `CommerceOutOfStock`
  and `CommerceOutOfStockCode` (the 409 that opens the back-in-stock form), `CommerceRedirectHeader` ("Nilda-Redirect").
  `TestTheStorefrontWireIsCoresScript` holds them to Core's script.
- `SchemaExport` and `TableExport`: a plugin's kept data as the Plugins screen exports it, for a successor that takes it
  in. `TestTheDataExportIsCoresShape` holds them to Core's.
- nildatest: `AsUser`, `AsAgent`, `FromVisitor` (and `Visitor`) put a test request in the shape Core's proxy sends.
- `Cap…` constants, one per capability Core registers (`TestTheCapabilitiesAreCoresOwn`, both ways), and
  `AdminPageURL` / `AdminFormURL`, where Core's admin draws a plugin's page and form (`TestTheAdminAddressesAreTheAdminsOwn`).
- Additive only. Found by the shop's Unit 12 end review: the shop spelled each of these by hand.

## v0.48.0 — 2026-10-27 (the default page of a plugin's own content type)

- Manifest `templates`: `[{content_type, name, tree}]` — the default single page of a content type the manifest
  declares, in the page builder's layout format (`docs/PLUGIN_SDK.md`, "The default page of your content type"; Core
  D-109). The active theme's own design and the owner's edit win over it; switching the plugin off takes it back.
  Manifest only: no Go API changes, so a plugin may declare it against any SDK version Core reads.

## v0.47.0 — 2026-10-26 (four retired contracts removed: offers, business buyers, back-in-stock, the shop's checkout for adapters)

- Removed `shop.offers/v1`, `shop.buyers/v1`, `stock.alerts/v1` and `shop.checkout/v1` — their contract files, their
  types (`Offer*`, `OffersProvider`, `BuyerTerms*`, `Quote*`, `BuyersProvider`, `StockAlertsDescription`,
  `StockAlertProvider`, `Checkout*`, `CheckoutProvider`), the `Services` methods that called them (`Offers`,
  `ConfirmOffers`, `BuyerTerms`, `RequestQuote`, `OpenQuote`, `StockAlertsDescription`, `SubscribeStockAlert`,
  `CheckoutDescription`, `CreateCheckout`, `UpdateCheckout`, `GetCheckout`, `CompleteCheckout`, `CancelCheckout`),
  `OffersMethods`, `BuyersMethods`, `StockAlertMethods`, `CheckoutMethods`, and `docs/OFFERS.md`, `docs/BUYERS.md`,
  `docs/STOCK_ALERTS.md`, `docs/CHECKOUT.md`. Offers, wholesale buyers and quotes, back-in-stock requests and the
  agentic checkout (ACP, UCP) are the shop's own features since commerce DR-19 (Unit 12): nothing provides or consumes
  these contracts any more, and dropping `shop.checkout/v1` also removes a provider that was handed every agent
  checkout's payment credential. Breaking for a plugin that used them; the retired plugins (plugin-offers, plugin-b2b,
  plugin-stock-alerts, plugin-acp, plugin-ucp) are not built any more. `TestContractsHaveNoRetiredOnes` keeps them gone.
- Kept: `StockAlertRequest`, `StockAlertResult` and their three statuses — what Core's "notify me" form sends the shop's
  `CommerceEndpoints.StockAlert` and reads back — and `CommerceEndpoints.StockAlert` / `StockAlertConsent`;
  `ShopOrderLine.OfferID` and `QuoteID` (the shop writes them).

## v0.46.0 — 2026-10-26 (an Install line at the top of a page; the site's one recycle bin for a plugin's own records)

- `AdminPage.Install`: on a `list` or `settings` page, the capability or contract of one of the manifest's `recommends`.
  Core draws its Install / Switch on line for it at the top of the page until something on the site brings it, for a
  person who may install plugins; a value that is not one of the plugin's recommends, or a report or calendar page that
  sets it, is refused (`docs/PLUGIN_SDK.md`, "The same line on a page"). An older Core ignores the key.
- The `trash` capability, `Core.Trash(ctx, TrashItem{Kind, ID, Name, ExpiresAfter})` and `Core.Untrash(ctx, kind, id)`:
  a plugin keeps a deleted row marked in its own tables and tells Core it is in the site's ONE recycle bin; Core lists
  it with its own items for 30 days (an hour to 30 days when asked) — Core D-108, commerce DR-22. On the wire
  `PUT`/`DELETE /api/rest/v1/plugin-trash/{kind}/{id}` under the plugin-only scope `manage:trash`.
- `TrashRestoreHook` (`trash.restore`) and `TrashPurgeHook` (`trash.purge`), admitted by `trash`, and
  `DispatchTrash(ctx, hook, payload, restore, purge)`: Core asks the owning plugin to bring an item back (a refusal is
  its answer, in its words) and tells it when one is gone for good — only a person's act or the 30 days, never an
  ability or an assistant tool. `TrashRef`, `TrashRetention`, `MinTrashRetention`, `MaxTrashKind`, `MaxTrashID`,
  `MaxTrashName`, `TrashItem.Validate`.
- `nildatest.NewTrash()`: a fake of the bin's routes (403 without `trash`, 422 for an item Core refuses) that drives
  the two hooks as Core does — `Items`, `Restore`, `Purge`, `TrashEntry`, `HookFunc`.

## v0.45.0 — 2026-10-26 (the buyer's profile inside the account)

- `AccountProfileSlot` (`data-pb-profile`): the element in an account view that Core's profile panel replaces —
  name, email, password, devices, my data, delete, all Core's own routes (`docs/COMMERCE.md` "The profile").
## v0.44.0 — 2026-10-25 (the buyer's account inside the site)

- `CommerceSignInRequired(w)` and `CommerceSignInHeader`: a members-only account view answers a signed-out visitor
  401 with `Nilda-Sign-In: required`, and Core's account shell draws Core's own sign-in panel in the mount, then asks
  for the same view again once the buyer is signed in (`docs/COMMERCE.md`).
- `AccountNavAttr` (`data-pb-nav`): a link in the account fragment Core's shell follows in place, the view kept in the
  page's address as `?shop=<query>` and forwarded to the endpoint on first render.
- `AccountVocabulary`: the classes a members area wears — the account widget's style parts, drawn by Core from the
  site's design tokens.
- `EventUserEmailVerified` (`user.email_verified`): a person confirmed their address (the confirmation link, or a
  sign-in provider that verified it); never sent for a sign-up that proved nothing.
- `nildatest.AsksForSignIn`: whether a response is the sign-in answer the shell listens for.

## v0.43.0 — 2026-10-25 (the mail door: mail a plugin declares and the owner edits; the visitor's language; events the site can listen for)

- `EmailTemplateDecl`, `EmailVariable`, `EmailTemplateText` and the manifest's `email_templates`: a plugin declares each
  mail it sends — words per language in Core's Liquid, recipient (`EmailToPerson`, `EmailToOwner`, `EmailToOther`),
  lane (`EmailTransactional`, `EmailMarketing`), required, variables (kinds `EmailText`, `EmailHTML`, `EmailURL`,
  `EmailNumber`, `EmailDate`, `EmailList`, `EmailObject`) — and Core keeps it as `<plugin key>.<key>` in its template
  store, where the owner and the assistant edit it. At most 64 templates; English required; a key is never reused.
- `Core.SendTemplatedEmail(ctx, TemplatedEmail{Key, To, Locale, Data, ReplyTo, Ref, IdempotencyKey})` and
  `ErrTemplateOff`: send a declared template by key; Core renders the owner's words in the site's mail layout, takes
  the lane from the template, adds the owner's copies, logs it with `Ref`, and sends the same idempotency key once in
  24 hours. A switched-off template answers `ErrTemplateOff` (not a failure). The contract gains
  `SendTemplatedEmail`.
- `nildatest`: `Host.UseEmailTemplates`, `ManifestEmailTemplates`, `Host.SwitchOffTemplate`; a templated mail is
  recorded with `Key`, `Ref`, `Locale`, `Data`, `ReplyTo`, `IdempotencyKey` and the default's words rendered.
- `VisitorLocale(r)`: the language the visitor is reading — always one of the site's languages, from the page's
  address or the browser's preference.
- `EventDecl`, `EventField` and the manifest's `emits`: a declared event joins Core's catalogue — automations start on
  it, webhooks subscribe to it ("Shop: Order paid"). At most 32. An undeclared event still reaches plugins only.

## v0.42.0 — 2026-10-24 (report rows that link and act; a setup page and what goes well with a plugin; why a site cannot take a payment yet; whether a site publishes a privacy policy)

- `ColumnLink` (`"link"`, a `ReportLink` cell: `{label, page}` an own declared page, `{label, screen}` one of
  `ReportScreens()`'s Core screens, `{label, install}` one of the manifest's `recommends`) and `ColumnAction`
  (`"action"`, a cell holding one of the page's declared action keys or `""`): a report row that says what to do
  next opens it or does it. A report page may declare `actions`; a press sends the row's `ReportRowKey` cell as the id.
  `AdminReportResult.Validate(page)` and `ReportLink.Validate` check both. `ColumnTypes()` does not grow: the two are a
  report's cells alone.
- `SetupStatusHook` (`"setup.status"`), `SetupStatus` (`Done`, `Total`, `Completed`), `MaxSetupSteps`,
  `DispatchSetupStatus`, and the manifest's `setup_page`: Core opens the plugin's setup report after install and shows
  "Finish setting up <plugin> (x of y)" in its onboarding list until the plugin says completed.
- `Recommendation` (`Capability` or `Contract`, and `Why`), `MaxRecommendations` (8), `MaxRecommendationWhy`,
  `ValidateRecommendations`, and the manifest's `recommends`: Core shows "Goes well with" with the marketplace's
  plugins for each and an Install button.
- `Payments.Readiness(ctx, currency)` and `PaymentsReadiness` (`Gateways` of `PaymentGatewayState{Key, Name,
  Enabled}`, `MethodsOn`, `Test`), `GET /payments/readiness`: "no gateway installed", "installed and switched off" and
  "none takes this currency" told apart. `nildatest` answers it, and `nildatest.Payments.SwitchGateway` switches a
  gateway off.
- `Reference().PrivacyPolicy(ctx)` and `PrivacyPolicy` (`Published`, `Path`), `GET /reference/privacy-policy`: whether the
  site publishes a privacy policy — Core's written one at `/privacy`, or a published page at the slug `privacy`.
  `nildatest.NewReference` (`SetSeller`, `SetPrivacyPolicy`, `Core`) answers it and `Seller`.
- Needs the Core release built on it; an older Core ignores `setup_page`, `recommends` and report `actions` and draws
  link and action cells as text, and answers `Readiness` and `PrivacyPolicy` 404. Additions only; `surface.txt` grows by 47,
  `nildatest/surface.txt` by 6.

## v0.41.0 — 2026-10-24 (an admin button, form or setting that always asks; Core says when the assistant asked)

- `RowAction.AlwaysAsk`, `PageForm.AlwaysAsk`, `SettingField.AlwaysAsk` (`always_ask` in plugin.json): the assistant
  asks the person before pressing such a button, saving such a form or changing such a setting, whatever a connector
  was granted — the screen is no longer a second door around an `AlwaysAsk` ability.
- `CallInfo.ViaAssistant` (`contract.HookRequest` field 7): true when the site's assistant asked for the hook call, so
  a plugin holds the assistant to its own limits on every door.

## v0.40.0 — 2026-10-24 (an ability that always asks; the test host refuses a refund that would pay twice)

- `Ability.AlwaysAsk` (and `always_ask` on the wire, `contract.Ability` field 7, and in plugin.json, which
  `nildatest.ManifestAbilities` writes): Core asks the person before every call of such an ability, whatever a connector
  was granted — for money moving and data leaving the site. Needs the Core release built on it; an older Core ignores
  the field and gates the ability by its class as before. Additions only; `surface.txt` grows by 1.

- `nildatest`'s payments refuse a refund while a chargeback on the session is open (409) and count a lost chargeback's
  amount as already given back (422 past the rest; one in another currency counts as the whole payment), as Core does
  since the release that keeps `PaymentSession.Instrument`. No new names; `surface.txt` does not change.

## v0.39.0 — 2026-10-24 (the ECB's euro rates, with exact cross rates; what a payment was paid with)

- `Reference().ExchangeRates(ctx, at)` and `ExchangeRateSet` (`Day`, `PublishedAt`, `Rates`, `Source`, `FetchedAt`): the
  European Central Bank's euro reference rates in force at an instant, from Nilda Central through Core, as decimal
  strings; `ExchangeRateSet.Rate(from, to)` is the exact cross rate through the euro (a `*big.Rat`). For an invoice in a
  currency other than the seller's national one, which states its VAT in the seller's currency (Directive 2006/112/EC
  Art. 91(2), 230). Needs the Core release built on it; an older Core answers 404. Additions only; `surface.txt` grows by 8.
- `MailAddressBlocked(err)` and `MailDeferred(err)` (with how long to wait): read why Core did not send a mail — an address
  that bounced or complained, or the site's email paused, its daily allowance spent, or this plugin over its sending rate.
  A transactional mail is never answered `nil` when it was not sent (it was, for the pause and the allowance, before: a shop
  switched a gift card on as mailed). `nildatest.Host.EmailRefusal`, `MailBlockedError()` and `MailDeferredError(retry)`: a
  test host that refuses exactly as Core does. `google.golang.org/genproto/googleapis/rpc` becomes a direct requirement.
  Needs the Core release built on it. Additions only; `surface.txt` grows by 2, `nildatest/surface.txt` by 3.
- `CommerceProductPages` (`ProductForContent`) and `HookCommerceProductForContent` (`"commerce.product_for_content"`): an
  optional extra of a shop — which product a content item, a product's own page, is — so a product widget on that page with
  no product chosen draws it. `Serve` subscribes the hook only for a shop that implements it. Needs the Core release built
  on it; an older Core never asks. Additions only; `surface.txt` grows by 3.

- `PaymentResolution.Instrument` (`"instrument"`) with `InstrumentCard`, `InstrumentSEPADebit`, `InstrumentBankTransfer`,
  `InstrumentWallet`, `InstrumentOther`, and `PaymentSession.Instrument` and `PaymentSession.ResolvedAt`: a gateway says what
  the payer paid with, and a session keeps it and the moment it first resolved. A shop reads them for how long the money can
  still be claimed back — a bank debit for eight weeks, a card for about four months. `Validate` refuses any other value; an
  older gateway sends none and stays valid. `nildatest` copies the instrument and stamps `ResolvedAt` once. Needs the Core
  release built on it. Additions only; `surface.txt` grows by 8 (three fields, five constants).

## v0.38.0 — 2026-10-23 (a row's buttons that fit it, and filters above the rows)

- `RowAction.ShownWhen` (`"shown_when"`) and `RowCondition` (`Column`, `In`, `NotIn`): a list page's button is drawn
  only on the rows whose cell fits — "Approve" on a request, "End the hold" on a held booking — instead of on every row,
  where pressing the wrong one was the owner's only way to learn it did not fit. A null cell reads as `""`. A
  convenience of the screen, not a lock: your hook still checks the row.
- `AdminPage.Filters` (`"filters"`) and `ListFilter` (`Key`, `Label`, `Column`, `Kind`, `Choices`, `AlsoColumn`,
  `Periods`): drop-downs above a list page's rows. `FilterChoice` matches one stored value of a column;
  `FilterPeriod` matches a date or timestamp column against a stretch of days in the site's time zone (`PeriodToday`
  … `PeriodLast30`, listed by `FilterPeriods()`), and with `AlsoColumn` matches when either column falls in it.
  `MaxListFilters` is four per page.
- Needs the Core release built on it; an older Core ignores both keys and draws every button and no filter, so a plugin
  built on this still installs there. Additions only; `surface.txt` grows by 29.

## v0.37.0 — 2026-10-22 (privacy requests about the people in a plugin's own tables)

- The `personal_data` capability and its two hooks, `HookPrivacyExport` (`"privacy.export"`) and `HookPrivacyErase`
  (`"privacy.erase"`), each within `PrivacyHookBudget` (15 s): a plugin that keeps data about people answers the site's
  access and erasure requests (GDPR Art. 15, 17, 20; Art. 28(3)(e)) by email (Core D-103). Implement
  `PersonalDataHolder` (`ExportPersonalData`, `ErasePersonalData`) and `Serve` subscribes and answers both;
  `DispatchPrivacyHook` routes them by hand.
- `PrivacyRequest` (`Emails`, `RequestID`, `Asked`), `NormalizePrivacyEmail`; `PrivacyExport` of `PrivacyRecord`s, each
  naming the address it matched — one about an address Core did not ask about refuses the whole answer
  (`PrivacyExport.Validate`); `PrivacyErasure` with `Erased`, `Kept` (`PrivacyKept`: anonymised, the fields kept and
  why) and `Held` (`PrivacyHeld`: not erased, and why), each on `PrivacyBasisLegalObligation` (Art. 17(3)(b): the
  accounting facts of a payment) or `PrivacyBasisLegalClaims` (Art. 17(3)(e): an open dispute)
  (`PrivacyErasure.Validate`); `MaxPrivacyRecords` (5,000) per answer.
- `nildatest.CheckPersonalDataHolder`: the round Core runs — export, erase, export again — and the rules the guide
  states (everything found is accounted for, a held record is one export returned, erasing twice changes nothing).
- Needs the Core release built on it; an older Core refuses `personal_data` as an unknown capability. Additions only;
  `surface.txt` grows by 39, `nildatest/surface.txt` by 1.

## v0.36.0 — 2026-10-22 (the step picker)

- `PluginMobile.Label` (`"label"` on a plugin's `mobile` block): what the plugin does in two or three plain words, the
  row the owner app's step picker shows at its first level, translated through your translations; without it the
  picker shows your `Name`.
- `PageMobile.Pick` (`"pick"` on a pin page): makes the page a level of the step picker; needs `pin: true`.
- `PagePick`: a pin page's `group_by`, `photo`, `tools` and `actions`.
- `PickGroupBy`: one way to group a page's rows, by a column or a taxonomy through the column holding each row's
  content id.
- `PickAction`: one chip offered after a pick — a field, a row action, an ability, or only a sentence.
- `PickOpSet`: a typed value sets the field.
- `PickOpAdd`: a typed value is added to a money or number field.
- `PickOpPercent`: a typed value changes a money or number field by a percentage.
- `PickToneWarning`: draws the chip in the warning colour, and never first.
- `MaxPickActions`: four chips per page.
- `MaxPickTools`: twelve tools a pin of the page may bring.

## v0.35.0 — 2026-10-21 (a second value for pickers)

- `AdminPage.Detail` (`"detail"`): on a `list` page, the key of a text column of its table that is shown after the
  name wherever the page's rows are picked (a form's reference field) or named (a list column with `Reference`) —
  "Yoga flow · yoga-flow-2" — so two rows whose first column reads the same can be told apart. No value there shows
  the name alone. It must be a column of `Table` other than the first of `Columns`, a text column, and only a
  `list` page may set it; it need not be among the displayed columns. Needs the Core release built on it; an older
  Core ignores the key and shows the name alone. Additions only; `surface.txt` grows by 1.

## v0.34.0 — 2026-10-19 (the tax table beyond the EU)

- `Reference().TaxTable(ctx, day)`: the reference rows Nilda Central keeps for the places beyond the EU, applying on a
  day (Brussels time) — a `TaxTable` of `TaxReferenceRow`s, each with a stable key, a kind (`TaxRowRate`,
  `TaxRowThreshold`, `TaxRowImportRegime`, `TaxRowRegistration`, `TaxRowNotice`), its country and subdivision, the days
  it applies, its official source and the day it was read, and a value announced for the next day (`TaxReferenceNext`).
  Read a row's value with `Rate` (`TaxRateValue`), `Threshold` (`TaxThresholdValue`), `ImportRegime`
  (`TaxImportRegimeValue`), `Registration` (`TaxRegistrationValue`) or `Notice` (`TaxNoticeValue`); `TaxTable.Of` is one
  country's rows. Needs the Core release that serves `/api/rest/v1/reference/tax-table`; an older Core answers 404.
  Additions only; `surface.txt` grows by 52.

## v0.33.0 — 2026-10-19 (calendar admin page; link targets; form replacements; widget preview flag)

- The `calendar` admin page kind: `AdminPageCalendar`, `AdminCalendarHook`, `AdminCalendarRequest` /
  `AdminCalendarResult`, `CalendarEvent`, `CalendarShade`, `DispatchAdminCalendar`; `AdminPage.RowsPage` and
  `DefaultView`. Needs the Core release built on it; an older Core drops the page.
- Link targets — where a site's links, menus and the assistant may lead into your plugin: `LinkTargetsHook`
  (`"link.targets"`), `LinkTargetsRequest`, `LinkTarget` / `LinkParam` / `LinkChoice` / `LinkTargetProvider`,
  `LinkParamSelect` / `LinkParamNumber`, `DispatchLinkTargets`, and `LinkTarget.Validate` for the same rules
  at Init. Needs the Core release built on it; an older Core never asks.
- `WidgetRenderRequest.Preview` (`"preview"`): true on the page builder's canvas and share-link previews of a
  draft, which a widget that records where it lives must skip. And `FormReplacement` — the manifest's
  `form_replacements`, the template forms one of your widgets replaces, at build time and on the plugin's
  first enable (a release the owner publishes).
- `LinkTarget.Validate` and `DispatchLinkTargets` now apply Core's full link-target rules: keys are trimmed,
  an empty label, a duplicate param key, a number with `Min` above `Max`, a select with no choice, a choice
  value outside `[A-Za-z0-9_.-]{0,64}` and a path character outside `[A-Za-z0-9/_.~-]` are refused, and the
  dispatch drops a duplicate target key and any target past the twentieth. `InitResult.Hooks` now says
  `link.targets` is admitted by `route`.

## v0.32.0 — 2026-10-18 (sidebar groups; columns that read as words)

- `ListColumn.Labels`: what each stored value reads as — `"labels": {"fixed_amount": "Fixed amount"}` — translated
  through your translations; the panel showed the stored code.
- `ListColumn.Reference`: the column holds a row id of another of your list pages, and is drawn as that row's first
  column, linked to it — a shipment's order reads "NLD-1042", not a uuid.
- `ListColumn.TypeBy` and `Types`: a cell drawn by its row — a discount's value is money for one kind and a
  percentage for another. `ColumnPercent` (`"percent"`) is new: the number itself, 20 is 20 %.

- `AdminPage.Group` (`"group"` on any page): the heading the page sits under in your section of the sidebar, so a
  plugin with many pages reads in a few kinds — a shop's Sales, Catalog, Marketing, Reports, Settings. Pages of one
  group sit together in the order declared; translated through your translations. The page `icon` is now drawn
  beside each page in the sidebar too; Core draws the market's common names (Lucide's: `receipt`, `package`,
  `percent`, `gift`, `truck`, `star`, `undo-2`, `shield-alert`, `bar-chart`, `list-checks`, `activity` …).

## v0.31.0 — 2026-10-17 (a form that edits your own content type)

- `AdminPage.Edits` (`"edits"` on a list page with a form): the content type whose items the form edits. Core shows
  that type nowhere else — no sidebar row of its own, its taxonomies in your section — and opens your form for every
  link to one of its items, with the item's content id as the row id. Core refuses at install an `edits` naming a
  type you do not declare, a page whose form does not edit, or a type two pages claim.

## v0.30.0 — 2026-10-17 (a payment method kept for later)

**A payer's method kept for later** (PAYMENTS.md §5): a subscription's next charge, a pre-order charged when it ships.
A consumer sends `PaymentSessionParams.Save`, a `PaymentMandate` — what the payer agreed to — on a method that lists
`SupportSaveForLater`; such a session may be for 0. The gateway reports the kept method with the resolution
(`PaymentResolution.Saved`, a `PaymentSaved`); the session carries `SavedMethod`. The consumer reads it
(`GetSavedMethod`, a `SavedPaymentMethod`), charges it with the payer absent (`ChargeSaved`, `PaymentChargeParams`;
the session carries `ChargedSaved`) and forgets it (`ForgetSavedMethod`). A gateway that keeps methods implements
`SavedMethodPayer`: its token arrives inside the start request (`PaymentStartRequest.Saved`, a `PaymentSavedToken`),
and the release as `payment.saved.forget` (`PaymentForgetRequest`). A sixth gateway failure code,
`authentication_required`: the payer's bank wants them present. Core holds every charge inside its mandate — at
most its amount (`MaxAmountMinor` for an amount a rule sets), a pre-order's one charge once, a subscription's no
sooner than its interval — and a charge whose answer was lost is held pending and asked again for the same session,
which a gateway makes idempotent by the session's id: never a second charge. `nildatest.Payments` keeps, charges and forgets methods
as Core does, with the same mandate checks (`SavedMethod`, `SavedToken`, `ChargeSaved`, `ForgetSavedMethod`, and `Now` to move a renewal's clock; `StubGateway.SavedStart`, `SavedCharged`, `Forgotten`).

**A pre-order on the product page** (`CommerceProduct.PreOrder`, `CommercePreOrder`): an item sold before it exists —
Core's add-to-cart button says "Pre-order" and draws the date it ships by and how it is paid beneath it, the facts a
buyer is owed before paying. A catalogue variant may be `AvailabilityPreOrder` (`preorder`), with the
`AvailabilityDate` it ships by (shop.catalog/v1, as Google's and schema.org's pre-order are). Additions only.

## v0.29.0 — 2026-10-17 (a product's photos)

**A product's photos** (`CommerceProduct.Images`, `CommerceImage`): every photo in the order the product page's gallery
shows them, each with its media-library id (Core draws it at every size it keeps) and the choice it shows
(`Selection`), so picking a colour turns the gallery to that colour's photo. `Image` stays the main photo a tile
shows. Additions only; `surface.txt` grows by 6.

## v0.28.1 — 2026-10-17 (a secret form field; renamed SKUs)

**A form field may be `secret`** (`FormField.Secret`): a text or long-text value written and never shown back —
licence keys pasted in — whose value the assistant's confirmation card leaves out, saying only how many lines.
**`commerce.sku_renamed`** (`EventCommerceSKURenamed`): the shop announces the SKUs its product form renamed, for a
plugin that keeps SKUs to rewrite its own (CATALOG.md, "When a code is renamed"). Docs: a save the assistant sends in
part reaches your hook whole, filled from your load; `plugin_form_save` is destructive and the same save is made once,
so make your version change with everything you store. Additions only; `surface.txt` grows by 2.

**`nildatest.MobilePreview` draws a list page's form** as the app lays it out — New above the list, a row opening in
its form with its actions under it — the same picture `nilda plugin check --mobile` draws. Docs: a form's side cards
follow the main ones on a phone; a money field naming no currency is in the form's own `currency` field; a hosted
site without the AI-execution plan runs only `read` abilities, a self-hosted one every class (Core's D-96). No change
to the Go API.

## v0.28.0 — 2026-10-17 (edit forms on a list page)

**A list page can create and edit its rows** (`adminform.go`; PLUGIN_SDK.md, "A list page can also create and edit
its rows"): `AdminPage.Form` declares a form — sections, fields in the settings vocabulary plus `date`, `datetime`,
`image`, `gallery`, `repeater`, `money`, `tags`, `reference` and `stat` (`FormFieldTypes`), `shown_when` and
`required_when` — that Core draws in the panel and the Nilda app and gives the site's assistant. Two new hooks,
`admin.form.load` and `admin.form.save` (`AdminFormLoadHook`, `AdminFormSaveHook`), routed by `DispatchAdminForm`;
the save answers the row as saved, or field errors and nothing saved, or a conflict. Additions only; `surface.txt`
grows by 72.

## v0.27.0 — 2026-10-16 (a signed-in buyer's own prices; telling the site's owner)

**Telling the site's owner** (`notifications` in plugin.json; PLUGIN_SDK.md, "Telling the site's owner"): declare which
of your events the owner hears of, and Core turns each emit into a notice in the panel, the Nilda app's Activity and
a push. A manifest field only — no Go API changes (Core v0.x with the field; an older Core ignores it).

**A signed-in buyer's own prices** (`commerce.go`; PLUGIN_SDK.md, "A signed-in buyer may see their own prices"): a
shop that implements `CommercePriceGroups.PriceGroup(ctx, userID)` names the price group a signed-in visitor is in, and
Core then asks for products with `CommerceQuery.Viewer` set (`CommerceViewerOf(ctx)` inside `Product`), keeping the
pages it draws for that group apart from everyone else's. Price by the group alone. A new optional hook,
`commerce.price_group`, subscribed by `Serve` only for a shop that implements it. Additions only; `surface.txt` grows
by 10.

## v0.26.0 — 2026-10-16 (what an ability changed; the phone in your tests)

`AbilityChange` (`abilities.go`): an ability that changes something may answer, beside its own result, `changes` —
label, before, after — and Core shows the rows under the assistant's answer in the Nilda app and the panel, "Price
€12.00 → €13.00". Plain text, at most ten rows, a row without a label dropped; the model still reads the whole answer.
Additions only; `surface.txt` grows by 4.

`nildatest.MobilePreview` (`nildatest/mobile.go`): draws your plugin as the Nilda app shows it — its row on the Apps
tab, its home, each page with how it reaches the phone — and holds the drawing to a golden file
(`NILDA_WRITE_PREVIEW=1` writes it). The drawing is the one `nilda plugin check --mobile` prints, held equal to Core's
by a test over the first-party plugins. `PageMobile.Hide`'s doc no longer promises a "do this on a computer" card
the app does not draw. `nildatest/surface.txt` grows by 2.

## v0.25.1 — 2026-10-16 (the sign-in server's `iss`)

`AccountFinish.Iss`: the `iss` the service sent back to Core's callback (RFC 9207), "" when none. Compare it with
the issuer you sent the browser to — refuse a mismatch, and a missing one when the server's metadata says
`authorization_response_iss_parameter_supported`. Additions only; `surface.txt` grows by 1.

## v0.25.0 — 2026-10-16 (the owner's account elsewhere; staff acting as the person)

**The owner's account elsewhere** (`account.go`; PLUGIN_SDK.md, "The owner's account elsewhere"). Declare
`"account": {"name", "host"}` in plugin.json and implement `AccountHandler` — `AccountStart`, `AccountFinish`,
`AccountStatus`, `AccountDisconnect` — and the owner gets a Connect button with the account's status in the admin and
in the Nilda app. Core makes the link only on that press, sends the browser only to https on your `host`, binds the
sign-in to the browser that started it, and hands you the code; you keep the tokens with `SetSecret`. `Serve`
subscribes and answers the four hooks (`account.start`, `account.finish`, `account.status`, `account.disconnect`) for a
Handler that implements the interface; `DispatchAccount` is for routing them by hand. Types: `PluginAccount`,
`AccountStart`, `AccountStartResult`, `AccountFinish`, `AccountStatus`. Additions only; `surface.txt` grows by 27.

**Staff acting as the person** (`viewer.go`). `Viewer.ImpersonatedBy` is the Core user id of a member of staff acting
AS the viewer through Core's impersonation (Core sends `X-Nilda-Impersonated-By`), "" for the person themself or an
older Core. Hold what must be the person's own act — a purchase, a payment method, deleting their data — and record
both ids on what you allow. Additions only; `surface.txt` grows by 1.

## v0.24.0 — 2026-10-15 (your plugin in the Nilda app; orders, deliveries, offers and business buyers)

Additions only: `surface.txt` grows by 229 entries in all, nothing removed; `nildatest/surface.txt` unchanged. The
Nilda app's part (46 of them): In `plugin.json`: a plugin-level `description` and
`mobile` (`icon`, `headline`), a page's `mobile` (`hide`, `row`, `pin`), a report page's `metrics`, and on a column the
types `money` (with `currency` or `currency_column`) and `badge` (with `badges`). In Go: `ReportMetric`, `PageMobile`,
`MobileRow`, `PluginMobile`, `HeadlineMetric`, the `Column*`, `Badge*` and `Metric*` words with `ColumnTypes`,
`BadgeTones`, `MetricFormats` and `MobileIcons`. A row action's `confirm` is now at most 300 characters. Every installed
plugin already appears in the owner's app without any of it; see PLUGIN_SDK.md, "In the Nilda app".

**Orders, and what a plugin delivers for them** (commerce unit 10, licence keys; `docs/ORDERS.md`). Two contracts:
`shop.orders/v1`, which the shop provides — `order.get`, an order's reference, status, buyer email and language,
currency and lines with their quantity refunded (`ShopOrderRequest`, `ShopOrderResult`, `ShopOrder`, `ShopOrderLine`, the
`OrderStatus*` words, `OrdersProvider`, `OrdersMethods`, `Services.ShopOrder`); and `shop.deliverables/v1`, which a
delivering plugin provides — `describe` (the SKUs it delivers, which the shop sells as digital content) and `order.items`
(`DeliverablesDescription`, `DeliverablesRequest`, `DeliverablesResult`, `DeliveredItem` with `DeliverableKey` and
`DeliverableLink`, `DeliverablesProvider`, `DeliverablesMethods`, `Services.DeliveredSKUs`, `Services.OrderDeliverables`).
`docs_truth_test` reads Core's plugin refusal as Core now writes it (`PlainText`).

**Offers at the cart, the checkout and after the purchase** (commerce unit 10, the conversion layer; `docs/OFFERS.md`):
`shop.offers/v1`, which an offers plugin provides — `offers.for` and `offers.confirm` (`OffersRequest`, `OfferLine`,
`OffersResult`, `Offer`, `OffersConfirmRequest`, `OffersConfirmResult`, the `OfferStage*` words, `OfferPercentMax`,
`OffersProvider`, `OffersMethods`, `Services.Offers`, `Services.ConfirmOffers`); `ShopOrderLine.OfferID`. A provider
that returns a nil list now answers an empty one, for `shop.orders/v1` and `shop.deliverables/v1` too.

**Business buyers** (commerce unit 10, the B2B starter; `docs/BUYERS.md`): `shop.buyers/v1`, which a B2B plugin
provides — `buyer.terms`, `quote.request` and `quote.open` (`BuyerTermsRequest`, `BuyerTerms`, `BuyerPrice`,
`BuyerMinimum`, `QuoteLine`, `QuoteRequestInput`, `QuoteRequestResult`, `QuoteOpenRequest`, `QuoteOpenResult`, `Quote`,
`BuyersProvider`, `BuyersMethods`, `Services.BuyerTerms`, `Services.RequestQuote`, `Services.OpenQuote`). A quote's price names its quote and quantity (`BuyerPrice.QuoteID`, `MaxQty`), and an order line the
quote it was bought at (`ShopOrderLine.QuoteID`). `Services.ConfirmOffers` returns an error when an offers plugin that
is there did not answer, never "the offer ended". A quote request may be refused with its reason (`QuoteRequestResult.Refused`).

## v0.23.0 — 2026-10-15 (one list decides which agent may pay; what a buyer and a shopper are told and kept)

Additions only (`surface.txt` grows by 35 entries, `nildatest/surface.txt` by 1; nothing removed). On the wire: Core's
`GET /api/rest/v1/agents/tier` and `X-Nilda-Visitor-Key` header; optional new fields on `shop.catalog/v1`, `shop.checkout/v1`
and `stock.alerts/v1` subscribe.

### Added

- **`API().AgentTier(ctx, operator)`** and **`AgentBlocked`**: what the site's owner lets an AI agent's operator do,
  from the one list on SEO → Shopping agents — for a plugin serving an agent platform by an API key, and for a shop
  told an agent's name by another plugin (commerce plan 13, B4–B5).
- **`WithCallInfo`**: sets what `CallOf` answers, for a test driving a provider method directly; nildatest's service
  desk now tells a provider which plugin asked, as Core does.
- **nildatest**: `Services.SetAgentTier`.
- **What an agent's buyer must be told** (commerce plan 13, D2–D3): `CheckoutState.PreContract` — a
  `CheckoutPreContract` with the `CheckoutSeller`, the withdrawal period, text and form, the terms, privacy and returns
  pages and the final button's words; `CheckoutState.DigitalConsentText` and
  `CheckoutCompleteRequest.DigitalConsentText`, the download waiver's sentence sent back word for word;
  `CheckoutDescription.ReturnsURL`; `CheckoutLine.Digital`, the line the waiver is about.
- **Channel prices as EU law has them** (E1, E5): `CatalogVariant.ShownPriorPriceMinor`, the 30-day lowest price a
  reduction is announced against, and `CatalogVariant.UnitPricing` (`CatalogUnitPricing`); `CatalogVariant.PriceWithoutTax`
  (E6), a shown price that holds no VAT, for a channel to leave out.

- **Back-in-stock requests that can be limited and proven** (commerce plan 13, C1–C2): `VisitorKey(r)`, an opaque daily
  key for a request's address; `StockAlertRequest.Requester` (it) and `StockAlertRequest.ConsentText` (the sentence the
  form showed); `CommerceEndpoints.StockAlertConsent`, the plugin's sentence for Core to draw under the email box.

- **`plugin.json`'s `processing`** (documented; Core reads it): what a plugin does with personal data, who receives it
  and how long it is kept, listed in the site's privacy record while the plugin is active (commerce plan 13, D4).

### Changed

- **`Services.StockAlertsDescription`**: a stock-alerts plugin that is installed and did not answer (a timeout, an
  error, an answer the contract refuses) is now an error naming it, not `ok == false` — "nobody collects" is said only
  when no plugin provides the contract (commerce plan 13, C7).

## v0.22.0 — 2026-10-15 (back-in-stock requests)

Additions only (`surface.txt` grows by 29 entries; nothing removed). On the wire: a new contract file,
`contracts/stock.alerts/v1/contract.json`, and `stock_alert` on the commerce endpoints answer — optional, so an older
Core ignores it and draws no form.

### Added

- **`stock.alerts/v1`** (docs/STOCK_ALERTS.md): a plugin keeps back-in-stock requests — `StockAlertProvider`
  (`DescribeStockAlerts`, `SubscribeStockAlert`; `Serve` finds it, `StockAlertMethods` names it), `StockAlertsDescription`,
  `StockAlertRequest` (`Validate`), `StockAlertResult` (`StockAlertSubscribed`, `StockAlertAlready`,
  `StockAlertRefused`); the shop asks `Services.StockAlertsDescription` and `Services.SubscribeStockAlert`.
- **`CommerceEndpoints.StockAlert`**: the shop's POST for a sold-out item's request; Core's add-to-cart widget draws
  "Notify me when available" while it is set.

## v0.21.0 — 2026-10-15 (the shop's checkout, driven by an agentic-commerce adapter)

Additions only (`surface.txt` grows by 140 entries; nothing removed). A new contract file,
`contracts/shop.checkout/v1/contract.json`; nothing else changes on the wire.

### Added

- **`shop.checkout/v1`** (docs/CHECKOUT.md): the shop's checkout for an ACP or UCP adapter — `describe`,
  `session.create`, `session.update`, `session.get`, `session.complete`, `session.cancel`. The shop implements
  `CheckoutProvider` (`Serve` finds it, `CheckoutMethods` names it); an adapter calls `Services.CheckoutDescription`,
  `CreateCheckout`, `UpdateCheckout`, `GetCheckout`, `CompleteCheckout` and `CancelCheckout` with the protocol
  request's idempotency key. Every answer is a `CheckoutResult` — a `CheckoutState` (statuses `CheckoutIncomplete`,
  `CheckoutReadyForPayment`, `CheckoutRequiresEscalation`, `CheckoutCompleted`, `CheckoutCanceled`) or a
  `CheckoutRefusal` an agent can read. Completing with a `PaymentCredential` is for an `Agent` of tier `AgentBuy` only.

## v0.20.0 — 2026-10-15 (an AI agent that proves who it is, and pays for its buyer)

Additions only (`surface.txt` grows by 21 entries, `nildatest/surface.txt` by 3; nothing removed). On the wire:
`VisitorCall.agent`, and `credential` on the payment session request and the start request, `credential_types` on a
payment method — each optional, so an older Core or gateway ignores them. `credential` and `credential_types` were
reserved names until now (docs/PAYMENTS.md §8).

### Added

- **Identified agents (Web Bot Auth)**: `Agent` (`Operator`, `Tier`), `AgentTier` (`AgentBrowse`, `AgentBuy`),
  `AgentOf(r)` on your resident server, and `VisitorCall.Agent` in a visitor tool. Core checks the signature; an agent
  the owner blocked never reaches you.
- **A delegated payment credential**: `PaymentCredential` (`Type`, `Token`, `Validate`; `String` never shows the
  token), `CredentialStripeSPT`, `PaymentMethod.CredentialTypes`, `PaymentOption.CredentialTypes`,
  `PaymentSessionParams.Credential`, `PaymentStartRequest.Credential`, and `CredentialPayer` — the gateway's half,
  charging the token on the spot. Core hands it over once and keeps nothing of it.
- **nildatest**: the payments kit carries the credential to the gateway and refuses it on a method that does not take
  its type; `StubGateway` charges one (`CredentialStart`, `Charged`).

## v0.19.0 — 2026-10-15 (prices as a buyer pays them, in the catalogue)

Additions only (`surface.txt` grows by 2 entries; nothing removed; no wire change). The `shop.catalog/v1` contract file
grows two optional fields — an object in the contract takes fields it does not name, so a Core on an older SDK still
accepts an answer carrying them.

### Added

- **`CatalogVariant.ShownPriceMinor` and `ShownSalePriceMinor`**: the regular and sale price as a buyer pays them, with
  the shop's own tax when its prices are entered without it — what Google and Meta list in the EU.

## v0.18.0 — 2026-10-15 (what a visitor's own AI agent may do; files at the site's root)

Additions only (`surface.txt` grows by 18 entries, `nildatest/surface.txt` by 1; nothing removed). One wire
addition: `InitResponse.visitor_tools` (field 6) and the `VisitorTool` message — a Core that predates it ignores the
field, so a plugin declaring visitor tools still loads there and offers none. Needs a Core that grants the new
capabilities `visitor_tools` and `site_files`.

### Added

- **Visitor tools**: `InitResult.VisitorTools`, `VisitorTool` (`Name`, `Description`, `Effect`, `InputSchema`, `Paths`,
  `Run`, `Validate`), `VisitorEffect` (`VisitorRead`, `VisitorSubmit` — nothing else), `VisitorCall` and
  `VisitorToolHook`. Core publishes each beside its own public tools (WebMCP and the public MCP) as
  `<plugin-key>.<name>`. Init refuses a tool Core would refuse, and two with one name.
- **`nildatest.ManifestVisitorTools`** holds plugin.json's `"visitor_tools"` to Init, as `ManifestAbilities` does.
- **`site_files`** in plugin.json (`/agents.md`, `/.well-known/<name>`): Core sends GET and HEAD for those paths to
  your resident server. No Go API; documented in `docs/PLUGIN_SDK.md`.

## v0.17.0 — 2026-10-15 (the shop's catalogue for channels)

Additions only (`surface.txt` grows by 71 entries; nothing removed; no wire change). A new contract file,
`contracts/shop.catalog/v1/contract.json`, which Core reads from the SDK it is built with: a Core built on an older SDK
refuses a manifest naming it. `docs/CATALOG.md` is the whole of it.

### Added

- **The catalogue contract**: `CatalogProvider` (`DescribeCatalog`, `CatalogPage`, `CatalogProduct`) and
  `CatalogMethods`; `Serve` finds a `CatalogProvider` by itself, as it finds a `Carrier` (a plugin may be both). The
  shapes `CatalogDescription`, `CatalogPageRequest` (`UpdatedSince` for deltas, at most `CatalogPageMax`),
  `CatalogPage`, `CatalogProduct` (`Removed` for a product taken off sale), `CatalogVariant` (prices as numbers,
  availability, GTIN, MPN), `CatalogProductRequest`, `CatalogProductResult`, each with `Validate`.
- **A channel's calls**: `Services.CatalogDescription`, `Services.CatalogPage`, `Services.CatalogProduct`.

## v0.16.0 — 2026-10-13 (where a visitor is, the EU's VAT rates and the seller)

Additions only (`surface.txt` grows by 35 entries; nothing removed; no wire change).

### Added

- **`VisitorCountry(r)`** — the country Core placed a request to your resident server in (the `X-Nilda-Visitor-Country`
  header Core asserts and a client cannot forge): ISO 3166-1 alpha-2, the evidence EU VAT law accepts for where a
  buyer of a digital service is.
- **`API().Reference().EUVATRates(ctx, day)`** — the EU member states' VAT rates on a day (`EUVATRateSet`,
  `EUVATRate`, `Standard`, `Reduced`), Nilda Central's copy of the Commission's TEDB. Needs a Core that serves
  `/reference/eu-vat-rates`.
- **`API().Reference().Seller(ctx)`** — who sells on the site, from its imprint (`Seller`: name, legal form, register,
  VAT ID, contact, address) — the supplier an invoice names. Needs a Core that serves `/reference/seller`.

## v0.15.0 — 2026-10-13 (shipping carriers)

Additions only (`surface.txt` grows by 207 entries, `nildatest/surface.txt` by 15; nothing removed; no wire change). A
new contract file, `contracts/shipping.carrier/v1/contract.json`, which Core reads from the SDK it is built with: a
Core built on an older SDK refuses a manifest naming it. `docs/SHIPPING.md` is the whole of it.

### Added

- **The carrier contract**: `Carrier` (`DescribeCarrier`, `Rates`, `CreateLabel`, `VoidLabel`) and the optional
  `CarrierTracker` and `CarrierDocuments`; `CarrierMethods` answers them, checking every request before the carrier
  sees it and every answer before it leaves; a `Rates` past `CarrierRatesBudget` (4 s) answers "unavailable". Serve
  answers a plugin that implements `Carrier` even when it wrote no `ServiceMethods`.
- **The shapes** — `CarrierDescription`, `CarrierService`, `RatesRequest`, `RatesResult`, `CarrierRate`,
  `ShipAddress`, `Parcel`, `Customs`, `CustomsItem`, `LabelRequest`, `LabelResult`, `DocumentRef` (`DocumentData`,
  `Bytes`), `VoidRequest`, `VoidResult`, `TrackRequest`, `TrackResult`, `TrackEvent`, `DocumentsRequest`,
  `DocumentsResult` — each with `Validate`, and the closed sets of statuses and failure codes.
- **The shop's calls**: `Services.ShippingRates`, `CreateShippingLabel` (the request's key travels as the idempotency
  key), `VoidShippingLabel`, `TrackShipment`, `ShippingDocuments`.
- **`ContractToolName`** and **`ServiceContract.AssistantTools`** — the published name of the assistant's tool for a
  contract method, written once for Core and its guards.
- **`nildatest.NewStubCarrier`** — a carrier in memory; **`_examples/carrier`** — a whole carrier plugin to copy.

### Changed

- A contract type may not carry a `[]byte`: its schema would refuse the base64 JSON sends (a guard holds it).

## v0.14.0 — 2026-10-13 (one plugin asking another through Core)

Additions only (`surface.txt` grows by 35 entries, `nildatest/surface.txt` by 15; nothing removed; no wire change —
a relayed call is an ordinary hook named `svc:<contract>.<method>`, carrying the fields v0.13.0 added). Needs a Core
built on this release: an older Core refuses `service_provider` and `service_consumer` at install, and answers the
route 404. `docs/SERVICES.md` is the whole story; `docs/EVENTS.md` writes down what v0.13.0's stored events promise.

### Added

- **`API.Services().Call(ctx, contract, method, input, ServiceCallOptions)`** — a consumer asks every active provider
  of a contract (or one, `Provider`) through Core's service desk, and gets a `ServiceResult`: each provider's
  `ServiceAnswer`, and a `ServiceMiss` with the reason for each that did not answer — a deadline is a result, not an
  error. The request is checked against the contract before it leaves and each answer after. `IdempotencyKey` makes a
  repeated call one call.
- **`ServiceProvider`** (`ServiceMethods() []ServiceMethod`) and **`Serves`** for typed methods — `Serve` routes each
  call to the method, checking the request first and the answer after. **`DispatchServiceHook`**, **`ServiceHook`**,
  **`SplitServiceHook`** and **`ServiceHookPrefix`** for an author routing by hand.
- **`ParseContract`** reads and checks a contract file; **`RegisterContractForTest`** makes one known to the process
  for a test (Core never sees it; not covered by the v1 promise).
- **`nildatest.NewServices()`** — the desk in memory: `Provider`, `StubService`, `Core` for a consumer, `Ask` for a
  provider's test, `Calls`.

### Changed

- A contract method Core relays (one with no `hook`) must be `answered_by: provider` — the desk asks providers on a
  consumer's behalf; `ParseContract` refuses anything else. No released contract had one.

## v0.13.0 — 2026-10-13 (who asked; events kept until delivered; contracts as data)

Additions only (`surface.txt` grows by 35 entries; nothing removed). The contract grows by `HookRequest` fields 3–6
(`request_id`, `contract`, `idempotency_key`, `caller`) and `EventRequest` fields 3–7 (`id`, `source`, `time`,
`subject`, `dataschema`), so an older Core leaves them empty.

### Added

- **`CallOf(ctx) CallInfo`** inside a hook: the request that led to the call, and for a call relayed from another
  plugin the contract, the asking plugin and its idempotency key. **`EventOf(ctx) EventInfo`** inside `HandleEvent`:
  the event's CloudEvents 1.0 context — `ID` (kept by a redelivery), `Source` (set by Core), `Time`, `Subject`,
  `DataSchema`.
- **Service contracts as data**: `contracts/<name>/v<major>/contract.json`, read with **`Contracts`** and
  **`ContractNamed`**; each **`ContractMethod`** says who answers it, its budget, retry class, fallback, idempotency,
  the assistant's class and the JSON Schema (2020-12) of its request and answer, checked with **`ValidateInput`** /
  **`ValidateOutput`**. The first file, `payments/v1`, describes the payment hooks unchanged.

### Changed (Core, from the release built on it)

- An event a plugin subscribes to is stored before it is delivered and retried for about three days; `Emit` returns
  once Core has stored it.

## v0.12.0 — 2026-10-12 (receipts are not newsletters)

Additions only (`surface.txt` grows by 1 entry, `nildatest/surface.txt` by 2; nothing removed). The contract grows by
two fields of `SendEmailRequest` (`html` = 5, `transactional` = 6), so an older Core ignores them and sends as before.

### Added

- **`Core.SendTransactionalEmail(ctx, to, subject, text, html, lang)`** — a message the recipient needs because of
  something they did (an order, a payment, a download, a return): Core's urgent lane, no unsubscribe footer, and
  delivered to a recipient who unsubscribed from marketing too. Before it, every plugin mail was marketing mail, so a
  buyer who had once unsubscribed got no order confirmation and no download link. `html` is an optional HTML part,
  kept to an allow-list of tags (images and links included, nothing that runs). `docs/PLUGIN_SDK.md` "Sending mail".
- **`nildatest.Email.HTML`** and **`nildatest.Email.Transactional`**, so a test can see which kind a plugin sent.

## v0.11.0 — 2026-10-11 (a place for a plugin's own secrets; what the plan allows a plugin; abilities in plugin.json)

Additions only (`surface.txt` grows by 6 entries, `nildatest/surface.txt` by 8; nothing removed). Needs a Core built
on this release: an older Core refuses the new `secrets` capability at install, and answers the new routes 404.

### Added

- **The `secrets` capability and `API.Secrets()`** — `Set`, `Get`, `Delete`: a plugin keeps its own credentials with
  Core, encrypted, surviving a restart, readable by that plugin alone (an OAuth refresh token that rotates on every
  use; a signing secret shown once). A name is 1–64 lowercase letters, digits or underscores, a value 1–4096 bytes,
  at most 32 names per plugin; replacing one is always allowed. An uninstall removes them all. `docs/PLUGIN_SDK.md`
  "Keeping a secret".
- **`nildatest.NewSecrets()`**, a fake of Core's `/plugin-secrets` routes that answers as Core does.
- **`API.PlanLimit(ctx, name)`** and the manifest's **`limits`** (`name`, `label`, `unit`): a plugin of Nilda's own
  declares numbers a Nilda Cloud plan sets for it — a shop's products — and reads the site's back to enforce them.
  `limited` false is no limit, and a Core older than this release answers 404, read as no limit.
  `docs/PLUGIN_SDK.md` "What the site's plan allows you".

- **The manifest's `abilities`** (`name`, `label`, `description`, `class`) and **`nildatest.ManifestAbilities`**, which
  writes them from what `Init` returns (`NILDA_WRITE_MANIFEST=1`, **`nildatest.WriteManifestEnv`**) and fails while
  the two disagree. **A Core built on this release offers the assistant only an ability declared there exactly**, so a
  plugin with the `abilities` capability regenerates its `plugin.json` before it runs on that Core; the marketplace
  review reads and checks the list (no ranking, steering, upselling or links). And on that Core an ability of class
  `write` or above **asks the person first** — Run is called once, after the yes. `docs/PLUGIN_SDK.md` "Telling the AI
  agent what you can do".

### Unchanged — and why that is the design

- `Payments.SetSecret` / `GetSecret` keep their route and their capability (`payment_gateway`). Core now answers them
  from the same store, so a gateway built on v0.10.4 needs no change.

## v0.10.5 — 2026-10-10 (a test-mode hint for the payer)

Additions only (`surface.txt` grows by 3 entries; nothing removed). Cut from v0.10.4 on its own branch
(`release/v0.10.x`), so it carries nothing of what `main` has added since.

### Added — payments

- **`PaymentMethod.TestHint` / `PaymentOption.TestHint`** (`test_hint`): how a payer pays in test mode with the
  method — the processor's test card or test login — by language (`"en"` always; fa, ar, de, es, fr as the
  gateway writes them). Set it with `Test`; Core passes it to consumers unchanged, and a shop shows it beside the
  method while `Test` is true. **`TestHintFor(hint, lang)`** picks the language, falling back to English. A Core
  built before this release drops the field: the consumer then shows no hint, as before.

## v0.10.4 — 2026-10-10 (the payment contract's second step)

Additions only (`surface.txt` grows by 96 entries, `nildatest/surface.txt` by 6; nothing removed). Needs a Core built
on this release for the new routes and hooks; an older Core answers the new routes 404 and never sends the new
hooks. `docs/PAYMENTS.md` has the whole of it.

### Added — payments

- **What a method can do is a list**: `PaymentMethod.Supports` / `PaymentOption.Supports` (`SupportRefund`,
  `SupportPartialRefund`, `SupportDisputes`, `SupportSync`), read with `SupportsOf` (a method that lists nothing is
  read as its `Refunds` bool says) and `Supports`. Core copies it onto `PaymentSession.Supports` when a payment is
  made and decides every refund by it — a gateway that stopped offering a method no longer makes its old payments
  unrefundable.
- **The payer's addresses and what is fulfilled**: `PaymentSessionParams` / `PaymentSession` gain `Fulfilment`,
  `BillingAddress`, `ShippingAddress` (`PaymentAddress`).
- **Disputes**: `PaymentDispute`, `PaymentDisputeReport`, the dispute machine (`DisputeCanMove`, `DisputeFinal`),
  `Payments.ReportDispute` / `ListDisputes` / `GetDispute`, and the optional `PaymentDisputeConsumer` hearing
  `payment.dispute.updated` (`PaymentDisputeUpdate`).
- **Refunds**: `Payments.ReverseRefund` (a refund undone at the processor stays resolved and gains
  `PaymentRefund.ReversedAt`) and `Payments.ReportExternalRefund` (a refund made in the processor's dashboard,
  `PaymentRefund.External`).
- **The pull path**: the optional `PaymentSyncer` answering `payment.sync` (`PaymentSyncRequest`).
- **A gateway's own secrets**: `Payments.SetSecret` / `GetSecret`, kept by Core, encrypted.
- **`Core.SiteURL`**: the site's public address, from `Init` (`InitRequest.site_url`, field 13). Core restarts a
  plugin with a route when it changes.

### Changed — documented, not breaking

- **A gateway's error on `payment.refund` is an answer**: "I do not know whether the processor paid it — ask again".
  Core keeps the refund requested and no longer counts the error toward switching the plugin off.
- `nildatest.Payments` decides a refund by the session's `Supports`, as Core does: a gateway that cannot describe
  itself no longer blocks a refund; one that is not running is still a 503.

### Reserved

`credential`, `credential_types` (delegated payment tokens, UCP/ACP) and the `supports` tokens `capture`,
`partial_capture`, `void`. `TestReservedPaymentNamesAreUnused` fails if anything takes them before they are built.

## v0.10.3 — 2026-10-08

No change to the Go API (`surface.txt` is untouched) and none to the wire contract. The module path is now
`github.com/nilda-dev/plugin-sdk`: the GitLab group is retired, and `v0.10.3` is the first tag that declares it.
Every earlier tag declares a GitLab path, so a plugin or Core must pin `v0.10.3` or later once it imports the new path.

## v0.10.2 — 2026-10-04

One addition to the Go API: `CommerceEndpoints.Reviews` (`surface.txt`).

### Added — commerce

- **`CommerceEndpoints.Reviews`**: a GET answering one product's reviews and review form as a fragment. Core's
  new Product Reviews widget asks it with `?sku=<product id>` and draws it inside the site's page; empty, the
  widget draws nothing.

### Documented — Core's storefront shell

- **Forms in your commerce fragments** (`commerce.go`): Core's shell sends a POST form found in its mount
  itself and draws your answer back in place (or into the `data-pb-fragment` around the form); send the shopper
  to a processor's page with the `Nilda-Redirect` header on a 200, not a 3xx. Core's CSRF check now accepts the
  browser's same-origin stamp, so these forms — and a plain form posted to your route — are no longer refused.

## v0.10.1 — 2026-09-25

One addition to the Go API: `nildatest.Payments.Decide` (`nildatest/surface.txt`). `surface.txt` is as in
v0.10.0. What a plugin can notice:

### Changed — the SDK

- **An error your handler returns reaches Core as your ANSWER, whatever it wraps.** `Serve` sends it as gRPC
  `Unknown`, the one code Core reads as the plugin answering — a row action's or an ability's refusal, a payment
  consumer's "tell me again". grpc-go sends the code of any gRPC status an error wraps, so a refusal wrapping a
  Core call's error (kv's `ResourceExhausted`, say) arrived as that code and counted toward switching the plugin
  off.
- **A panic in your code now counts as a failure.** It reaches Core as `Internal`; it used to arrive as a plain
  error, which Core read as your answer — a row action that panicked on every press was shown to the owner as
  your words and never counted.
- **`API.UploadMedia` with an Idempotency-Key sends the same bytes on every run** — its multipart boundary is
  derived from the key and everything the body carries, the file included. A keyed upload re-run after a crash
  was refused by Core as "a different request". (Derived from the key alone, as first shipped here, a file
  could carry the boundary and add form fields of its own; a file that contains its boundary is refused.)
- **The plugin's key no longer rides an https request's own headers.** `NewTransport` / `HTTPClient` put it on
  the CONNECT (which the egress proxy reads) and, for plain http, on the request; on https the request headers
  travel inside TLS to the provider, which learned the key.

### Changed — `nildatest.Payments`, following Core

- **`CreateRefund` answers `requested`, and `Deliver` asks the gateway.** Core asks a gateway about a refund from
  its delivery job, never inside the consumer's call; a test that read the gateway's answer straight from
  `CreateRefund` calls `Deliver` first.
- A refund whose gateway cannot be asked answers **503**, and a 503 gives its Idempotency-Key back (the same
  key runs again); the method's currencies are not checked again for a refund.
- A keyed request sent again while the first is still running answers **409**.
- A mismatched resolve writes no failure message (Core keeps the processor's amount for the owner), and the
  same mismatch reported twice changes nothing; a pending report on a pending session changes nothing; a
  processor's reference past 255 bytes is refused on a pending report and a refund resolve.
- **`Decide`** — a person settling a pending payment, as the owner does on Settings → Payments: a held mismatch
  resolved at the processor's amount (one in another currency refused), a rejection keeping `amount_mismatch`
  as its reason with the note as its message. A consumer can test the order it books at an amount it did not ask.
- As Core, now: the same key with another body while the first runs is **409** (it was 422); a panic under a
  key records a **500** instead of leaving the key "still being processed" for good; an over-refund while the
  gateway cannot be asked is **503** (it was 422); a start the gateway could not answer carries no failure
  message; a consumer's refusal in the confirm step is not written onto a payment settled meanwhile, and its
  reason is cut to 500 characters; a report's body is checked before its session is looked up (a malformed one
  is 422, never 404); a currency is compared as Core normalises it (`" eur"` is EUR).
- **The kv budget**: a `Host` refuses a write past 10,000 keys or 16 MiB — counted as Core counts them, the
  key under Core's namespace prefix plus the value — with `ResourceExhausted` and Core's words, after sweeping
  expired keys. It said it did not model this, and a plugin that filled its namespace passed its tests.

### Documentation

- `docs/PAYMENTS.md` says what Core does now: refunds are asked from a job; a consumer's "tell me again" error
  is not counted against the plugin; money reported on a refused payment is recorded for the owner (still
  409); a person accepting a mismatch resolves at the processor's amount; no readable site address refuses a
  payment; the 503 and in-flight 409 refusals.
- A shop that must emit its catalogue event is a Handler served with `Serve` (it keeps the `*Core` from Init);
  `ServeCommerce` never hands its `Commerce` one. The guide's walkthrough and `commerce.go` show that shape.
- The API token's rate limit (`API_RATE_LIMIT_PER_TOKEN`, 1,000 an hour by default) and what the client does
  when it runs out.
- The guides now say what Core does where they said otherwise: which hooks need a subscription (every one —
  `Serve` lists a provider's for you, and a row action or a report page is listed by hand), what
  `nilda plugin build` does with `os`/`arch` and the signing key, where a plugin's log lines go and at which
  level, that emitted events arrive in no fixed order, how the payment confirm step's errors divide into
  "ask again" and "final", and the broker and TLS details a plugin in another language needs.
- `_examples/shop` reads `content.saved` in the shape Core sends (`content_id`, `type`) and listens for
  `ecommerce.order_paid`, an event the site's shop really emits, instead of `order.paid`, which nothing
  sends.
- `CHANGELOG.md` (this file).
- After an independent review of the above, the guides say, where they said otherwise or nothing:
  - which code Core reads as your answer (`UNKNOWN`; ANY_LANGUAGE), and that a panic counts;
  - that a gateway's budget is AT MOST 15 seconds — `payment.start` and `payment.describe` keep the asking
    request's deadline;
  - that a lost `payment.start` answer usually means no charge, and what to do with one that happened;
  - that a second `Resolve` with another amount is a 409, not a no-op;
  - that the assistant reads every non-secret setting — `secret` is the line the model does not cross;
  - that a free install's assistant runs only `read` abilities;
  - that a row action is not told who pressed it;
  - the `content.*`, `schedule:` and `ability:` payloads for a plugin in another language;
  - never to put the plugin key on an https request's own headers;
  - that `Health` is never called; that a 503 gives its Idempotency-Key back; that `-ldflags='-s -w'` keeps a
    binary's build information; that a field type's `base` is a content field type, not a widget one;
  - how to read the Core-reading tests' skips, and that they fail when a file they read has moved.
- `_examples/shop` has a `plugin.json` declaring the settings page it reads and the capabilities it uses, held
  by its tests.

### Changed — what Core does, and the guides say so

- The `kv` row: one plugin's 10,000 keys and 16 MiB now hold on every install. Core used to keep them on a
  Lite install only, and keeps them on Dragonfly too now — a plugin there past either answers
  `ResourceExhausted`, where it used to be let through. (On Dragonfly, writes racing each other at the very edge
  can pass it together — by what one plugin writes at that instant, never more.)
- A call past Core's rate limit (email, emit, kv) answers `ResourceExhausted` **with a `google.rpc.RetryInfo`**
  saying when there is room again; a write to a full kv namespace answers the same code with none — retrying
  does not help there. The two could not be told apart before.
- `route_prefix`: Core now refuses `/category`, `/tag`, `/search` and any language's code (`/fa`, `/en`,
  `/pt`) — Core serves pages under each, after a plugin's route has had its turn, so a plugin holding one
  answered every category page or every Persian one. A plugin already installed under one of them keeps it
  until its next update. The code is the whole prefix: `/my-account` is not Burmese (Core refused it as such at
  first, reading only up to the hyphen).
- Tables: the 16-index cap counts every index Core builds (unique columns and a list page's `order_by`
  included); a table may not be named like another's primary key (`orders_pkey`); a plain-number default must
  fit its column's range; and switching on a reinstall over the tables an earlier install left cannot move a
  primary key, as an update already could not.

- **The site's assistant manages plugins now — never removes one** (Core's D-87): it can turn a plugin on,
  update it to the version the install ships, and read and change its settings (`plugin_settings_set`, for a
  person who may configure your plugin, after they approved it). The settings go through the same save as
  your admin section — declared fields only, a required field never emptied, a secret written and never read
  back — and your plugin restarts with them, as after any save.
- **A row action's error reaches the owner in your words** — its first 300 characters, through your
  `translations` like a success `Message`. It used to arrive as "internal error". An error you give a person
  (a row action, a report, an ability) no longer counts toward `PLUGIN_MAX_FAILURES`; a timeout or a crash on
  those calls still does, and a row action whose process stopped mid-call is "may have run", like a timeout.
- **An Idempotency-Key replays only under the same scopes.** A repeat made after an update changed your
  plugin's scopes is a different request (422), never the first answer.
- **The egress proxy challenges a request that names no plugin** (`407` with a Basic challenge) instead of
  refusing it as undeclared, and keeps no connection to the provider open after a plain (http) request.
- A hook, schedule, ability or event subscription your capabilities do not admit is logged at start — a hook by
  its name; schedules, abilities and events by the capability they need (they were dropped in silence).
- `admin_pages` and `editor_commands`: help past 500 characters, a placeholder, a choice, a page icon, a
  command's group, icon or keyword past 120, and more than 16 keywords are refused at install.
- `nilda plugin check` refuses a field Nilda does not know, as the guide said it did — only `build` did.
- `backup.*` is Core's event namespace too: a plugin emitting under it is refused.
- `POST /api/rest/v1/media` takes the media upload limit, not the general body limit.
- The guides say an ability is not told which person ran it (Core records that as a known limit).

## v0.10.0 — 2026-09-24

### Added

- **The payment contract (Nilda D-84).** Two capabilities: `payment_gateway` for a plugin that talks to a
  processor, `payment_session` for one that takes money. `PaymentGateway`, `PaymentConsumer`,
  `ServePaymentGateway`, the wire types, one state machine (`PaymentCanMove`, `RefundCanMove`), ISO 4217
  minor units (`MinorUnits`, `ParseMinor`, `FormatMinor`), and `core.API().Payments()` for the calls into
  Core. `Serve` subscribes and answers both interfaces' hooks. `nildatest.Payments`, `StubGateway` and
  `Webhook` test either side with nothing running. `docs/PAYMENTS.md` is the contract; `_examples/gateway`
  is a whole gateway. Needs a Core built on this release — an older Core refuses a manifest declaring
  either capability.
- `ClassAnalyze`, the risk class Core had and this module did not.
- `FieldChoicesRequest.Locale` and `FieldValidateRequest.Locale`: the language to answer a field question in.
- `API.UploadMedia`: `POST /media` takes a multipart upload, which `api.Post` cannot send.
- `nildatest.Host.Advance` (expire KV entries without sleeping) and `Host.SetSessionsPerIdentity`.
- `nildatest/surface.txt`: the test kit's exported names are frozen by a ledger, like the SDK's.

### Changed — behaviour a plugin can notice

- **OIDC extra scopes add to the defaults.** `NewOIDCClient`'s extra scopes are now requested alongside
  `openid email profile` rather than instead of them.
- **Two abilities with one name fail `Init`.** Core offered the agent the first declaration while the SDK
  ran the last one's `Run`; a duplicate name is now an error at startup.
- **A sub-second KV TTL expires.** `KVSet` with a TTL under one second used to be truncated to zero —
  "never expire"; it is now rounded up to one second.
- **`nildatest` refuses more of what Core refuses**: an event name that is not the plugin's (Core's C11
  rule, with Core's codes), a KV value past Core's 64 KiB, `SendEmail` and `RevokeIdentity` with empty
  required fields, and `KVIncr` on text with Core's code. A plugin that relied on the fake being lax fails
  its own tests now — as it would on a real install.
- `ServeCommerce` serves the storefront's cart routes when the `Commerce` is an `http.Handler`.
- The slog handler behind `Log()` follows slog's grouping rules.

### Deprecated

- `nildatest.SessionsPerIdentity`, a package variable shared by every test in a binary. Use
  `Host.SetSessionsPerIdentity`.
