# Changelog

What changed for a plugin author, per release. The Go API a release freezes is `surface.txt` (and
`nildatest/surface.txt`); the git log has the reasons in full. Versions before v0.10.0 are not written up
here.

How a release is cut (additive only, one tag per milestone, security and money fixes at once, an undated first
section means "unreleased") is `docs/RELEASE_TRAIN.md`; `release_train_test.go` holds the dating rule.

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
