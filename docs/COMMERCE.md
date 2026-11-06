# The storefront — account views inside the site

Core ships the storefront widgets (Products, Cart, Checkout, My Account …) and no commerce code; your plugin
supplies the products and the endpoints the widgets' shells call. `commerce.go` is the reference for the catalogue,
the cart and the checkout. This guide is the **buyer's account** (v0.44.0): how your account views live inside the
owner's themed page instead of on a bare page of your own — and (§6, v0.58.0, EXPERIMENTAL) the **express wallet
buttons'** one door into your checkout, and (§7, v0.58.0, EXPERIMENTAL) **other plugins' fields** at your checkout and on your
product pages, and (§8, v0.58.0) **what the storefront draws beyond a tile**: choices on a product page, "From", unit
prices, the stock line, the machine facts behind structured data, filters and facets, the payment marks strip, the
mini-cart and search hits.

The My Account widget is a **members area**: a mount on the owner's account page that fetches your
`Endpoints().Account` (`/shop/account`) from the browser and draws your answer in place. The page around it is
cached per URL and identical for everybody, so nothing about a visitor is ever in it — the shell fetches every
view, and Core's sign-in panel, after the page has loaded.

## 1. Links: one page, many views

Mark a link inside your fragment with `data-pb-nav` (`nilda.AccountNavAttr`) and write its `href` as a link to the
same page, carrying your endpoint's query in one `shop` parameter:

```html
<nav class="pb-account-nav">
  <a class="pb-account-link" data-pb-nav href="?shop=view%3Dorders" aria-current="page">Orders</a>
  <a class="pb-account-link" data-pb-nav href="?shop=view%3Daddresses">Addresses</a>
</nav>
```

Core's shell follows it in place: it asks your endpoint with that query (`/shop/account?view=orders`), swaps the
mount, pushes `?shop=view%3Dorders` onto the address, restores the previous view on Back, moves focus to the new
view's first heading and reads that heading out in a polite live region. On first render it forwards the page's own
`?shop=` to your endpoint, so a reload, a link opened in a new tab and a link in a mail all land on the same view.
With no script the link is an ordinary link to the page. A link without `data-pb-nav`, a link to another site, and
a modified click (a new tab) are left to the browser.

Every view is a fragment with its own heading (`<h2>`), so focus has somewhere to go. Forms in a view are sent by
the shell as for the cart (`commerce.go`, "Forms in your fragments").

## 2. Sign-in: Core's panel, in your mount

A view only a member may see answers a signed-out visitor with `nilda.CommerceSignInRequired(w)` — `401` and
`Nilda-Sign-In: required`, not cached:

```go
if _, ok := nilda.CurrentUser(r); !ok {
	nilda.CommerceSignInRequired(w)
	return
}
```

The shell then draws **Core's** sign-in panel in the mount: sign in, create an account by the site's registration
policy (open, held for the owner's approval, invite only, or closed — each said in words), forgot password, the
second factor, the "check your email" notice, and a button per sign-in provider the owner enabled. Signed in, the
shell asks for the same view again. Your plugin never sees a password and draws none of these states; a guest view
(an order opened by its signed link) simply answers as usual. Any other refusal shows the mount's failure sentence.

A provider sign-in comes back to the same page and view: the panel starts it with `next` set to the page's address,
which Core keeps with the sign-in only when it is a path on this site. A refused provider sign-in comes back with
`?signin_error=` and the panel says why. Test a members-only view with `nildatest.AsksForSignIn`.

## 3. The look: the account vocabulary

Wear these classes and nothing else — no inline colour, no style attribute. They are **style parts of the account
widget**, so the owner restyles each from the page builder, and Core draws them from the site's design tokens,
dark mode included. `nilda.AccountVocabulary` is the same list.

| class | what it is |
|---|---|
| `.pb-account-nav` | the account's menu |
| `.pb-account-link` | each menu link; `aria-current="page"` on the one showing |
| `.pb-account-card` | a card: an order, an address, a return |
| `.pb-account-row` | a row inside a card or a list |
| `.pb-account-chip` | a status chip: Paid, Shipped, Refunded |
| `.pb-account-step` | a timeline step; `is-done` when passed, `aria-current="step"` on the current one |
| `.pb-account-primary` | the main button — put Core's `pb-button` beside it for the site's button look |
| `.pb-account-secondary` | the other buttons |
| `.pb-account-field` | a label and its input |
| `.pb-account-error` | a message that something went wrong |
| `.pb-account-success` | a message that something worked |
| `.pb-account-table` | a table: an order's lines, the invoices |

## 4. The profile: Core's panel, in your view

A Profile view marks one element with `data-pb-profile` (`nilda.AccountProfileSlot`, v0.45.0) and Core's shell
replaces it with **Core's** profile panel: the person's name, email (changed by a confirmation mail), password,
signed-in devices with "sign out everywhere else", "download my data" and "delete my account". Every form goes to
Core's own `/api/v1/account/*` and `/api/v1/privacy/export` with the reader's bearer; the email, password and delete
ask the current password (and the second factor), and all of it is refused while staff act as the person. Your
plugin draws no field of it and sees none of it. Put a sentence inside the element for a page with no script; the
panel replaces it.

## 5. When an address is proved: `user.email_verified`

Core emits `user.email_verified` (`nilda.EventUserEmailVerified`) when a person **confirms** their address — the
double opt-in link, or a sign-in provider that verified it. The subject is the user's id and there is no data; read
the address back through the API. It is never sent for a sign-up that proved nothing (a pending account, a single
opt-in account), so it is the safe moment to act on an address — a shop claims the guest orders placed with it.
Subscribe with the `events` capability (`EVENTS.md`).

## 6. Express wallet buttons: the `Express` endpoint (v0.58.0, EXPERIMENTAL)

Core's express widget draws a gateway's wallet buttons (Apple Pay, Google Pay, Link, PayPal — the method's
`PaymentOption.Express`, `docs/PAYMENTS.md` §4) on a product page, the cart, the mini-cart and the checkout, and talks to
your checkout through ONE endpoint: `CommerceEndpoints.Express`, a POST on your route taking an `ExpressRequest` and
answering an `ExpressAnswer`, both JSON (`commerceexpress.go`). It is called from the visitor's browser, so your basket's
cookie goes with it. Leave it empty and Core draws no buttons.

| step | when | carries | you do |
|---|---|---|---|
| `quote` | the buttons are drawn; the wallet shows an address; the payer picks a delivery option | `source` (`cart`, or `product` with its `item`), the PARTIAL `address`, `shipping_option`; after a `start`, its `session` AND its `press` | price the basket for that address: `total_minor`, `total` (your words), `lines`, `shipping_options` (one `selected`), `capture`, the `notice` beside the buttons and `terms_url`. With a `session`, also re-price it (`Payments.Reprice`) — the session the start keyed by that `press`, never one found by its id alone |
| `start` | a PayPal-like button is pressed (kind `order`) | the quote's fields and the `press` | place the order and start the session keyed by the press; answer its `session`, `status` and `client_action` (the PayPal order the buttons need) |
| `complete` | the payer confirmed in the sheet | the `press`, the `payer` (email, name, phone, final addresses), the `shipping_option` the payer chose, the `shown_total` the sheet showed (`{"amount_minor","currency"}`, `ExpressTotal`) and the wallet's `credential` (kind `token`) or the `session` the start made (kind `order`) | re-quote at the payer's FINAL address with that option; any difference from `shown_total` → answer `code: "total_changed"` (below), charge nothing. Else `token`: place the order with the payer's details and create the session with the credential, keyed by the press. `order`: record the payer on the start's order before the gateway asks you to confirm. Answer `status`, `client_action` (3D Secure) and `order_url` |

- **Before the payer confirms, nothing personal travels.** A wallet shows a partial address — Stripe: "the shipping
  address in the US can only contain a city, state, and ZIP code"; PayPal: city, state, country code, postal code — and
  `WalletAddress` carries exactly those four, nothing else. `DecodeExpressRequest` refuses a street or a name inside it,
  and `Validate` refuses a `payer` or a `credential` with `quote` or `start`, and a partial address with `complete`.
- **One press, one order.** `press` is minted by Core's bridge per press; use it as the order's and the session's key, so
  a browser that sends `complete` twice makes one order and one charge. Every quote after a `start` carries the start's
  press beside its `session`, and **you require the press whenever `session` is set** (`Validate` refuses a request
  naming a session without one): re-price only the session your start keyed by that press, so a page that learned a
  session id cannot re-price another payer's order.
- **The payer pays what the sheet showed, or nothing.** `complete` carries `shown_total`, the total the wallet's sheet
  showed when the payer confirmed — REQUIRED with a `credential` (kind `token`: nothing else ties the charge to what the
  payer read), optional with a `session` (kind `order`: the session's amount, re-priced while the payer chose, binds it;
  compare it when it comes). The final address can price differently from the partial one the last quote saw (a street
  outside a delivery zone, a region's tax), and a price can change between the quote and the press. So re-quote at the
  final address and the chosen `shipping_option`, and on ANY difference — amount or currency — answer
  `{"available": true, "code": "total_changed", "message": …, "total_minor", "total", "lines", "shipping_options", …}`
  with the new total and nothing else: no session created, no order placed or confirmed, no `status`, `client_action`,
  `redirect_url` or `order_url` (`ExpressAnswer.Validate` holds this; `nilda.ExpressTotalChanged`), with HTTP 409 — Core's
  bridge hands a 4xx's body to the gateway's script as it is. The script ends the sheet unpaid, says so and asks a fresh
  quote, so the new total stands beside the buttons (`docs/PAYMENTS.md` §4, "The page bridge"); paying it is a new press.
- **Not available is an answer.** Answer `available: false` (and a `message` the shopper reads) for a basket the wallet
  cannot complete honestly: one that needs a tick the sheet cannot give — a download's consent, a pre-order's terms — or a
  payment it cannot take, an invoice. Core then draws no buttons.
- **The same checkout, never a second.** Every step runs your ordinary basket checks, prices and order — the endpoint is
  the express channel, so the order knows it came this way without a `channel` field. (The 2026-10-18 plan put this on
  `shop.checkout/v1`; that contract was retired in v0.47.0, and the basket's cookie reaches only your route.)
- **The credential is the payer's.** Hand it to `Payments.CreateSession` once; never store, log or echo it. A
  `client_action` likewise goes back only to the browser that asked.


## 7. Other plugins' fields: door D2 (v0.58.0, EXPERIMENTAL)

A plugin may ask your buyer something at checkout or on a product page (a delivery date, an engraving), keep a value on a
product or a customer, or suggest addresses while the buyer types. It declares the fields through `shop.attributes/v1`;
you — the shop — draw them with your own form helpers, check every value with `AttributeField.CheckValue`, ask the plugin
`validate` before you place the order (its `when_unanswered` says whether a plugin that does not answer blocks the order),
copy every value into the order with its label, answer the plugin's own reads and writes through
`shop.attribute.values/v1` (write commands: keyed, attributed, undoable, announced; a refusal is a result), and proxy an
address query to the plugin bounded per visitor. `docs/ATTRIBUTES.md` is the whole of it; `nildatest.AttributeShop` holds
the same rules.


## 8. The storefront's release 1 (v0.58.0; commerce plan 1C)

Everything here is OPTIONAL: a shop that answers none of it draws what it drew before. The SDK carries it first: Core
draws the new fields, asks `commerce.facets` and shows the marks from its storefront step built on this release — an
older Core drops them, so a shop may send them today (the "Choose options" link, `view=mini` and the search hits below
are Core's already). Every new value is checked before
it leaves your plugin — `CommerceProduct.Validate` on each product `Products`, `Product` and `ProductForContent` answer,
`CommerceFilters.Validate` on each query, the facets' rules on each `Facets` answer — so a value Core could not draw, or a
search engine would refuse, fails the call in your own tests first and names the product. `nildatest.Storefront` asks
your shop the way Core does.

### A product that needs a choice, and "From"

**`Variations` on a grid answer means "needs a choice".** Core's product grid no longer adds a product with Variations
from its tile: the tile's button becomes a link to the product's page, "Choose options" in the page's language (Shopify's
"Choose options", WooCommerce's "Select options") — no page URL, no button. So answer a variable product's `Variations` in
`Products` too, not only in `Product`; a product without them is added from the tile as before.

**`PriceFrom`** says `Price` is the lowest of several (Shopify's `price_varies` / `price_min`, WooCommerce's price range):
Core prints its own "From" before it, translated. Set it only beside a `Price`.

### Unit price and the stock line

**`UnitPrice`** (`"€12.40 / kg"`, formatted by you) is drawn beside every price Core draws for the product — a tile, the
product page, a search hit, a choice. Where EU Directive 98/6/EC asks for it (goods sold by weight, volume or length) it is
the shop's legal duty to send it. **`StockNote`** (`"Only 3 left"`) is your words, sent only when the merchant switched the
line on; Core draws it under the price and still learns no count. Each is one plain line of at most `MaxCommerceNote`
(200) characters.

### Choices: the product page follows the shopper (`Choices`)

On `Product` and `ProductForContent` only — a grid never draws them — `Choices` lists every sellable combination of
`Variations` as a `CommerceChoice`: its `Selection` (every axis answered, by the axis's `Key`, or its `Name` when it has
no `Key`, with one of its `Options`), and its own `SKU`, `Price`, `OldPrice`, `UnitPrice`, `InStock`, `StockNote`,
`MediaID` and `Facts`. Core carries them as data in the page so picking a colour changes the price, the code, the stock,
the photo and the button before anything is added. Rules: at most `MaxCommerceChoices` (250 — what Shopify's storefront
hands a theme unpaginated; past it, Core asks the question and lets your add-to-cart answer it), only on a product with
Variations, no two choices of the same combination, no two with the same SKU (Google's variants each need a unique id), a
SKU of at most 127 bytes, and one currency across the product's and its choices' facts.

### Machine facts (`Facts`)

`Price` is for a person and Core never parses it. `Facts` (`CommerceFacts`) is the same product for a machine, written into
the page's structured data (a Product's Offer; with choices, a ProductGroup with a variant per choice) and never drawn:

| field | rule (Google's merchant listings and Merchant Center, read 2026-11-06) |
|---|---|
| `amount` | a plain decimal with a period, no symbol, grouping or sign, no more decimals than the currency has (`ParseMinor`'s rule): `"19.90"`, `"1500"` for yen. With `ShowPricesIncludingTax`, gross, as `Price` is. Set with `currency` or not at all; Google lists a merchant offer only above zero |
| `currency` | three upper-case ISO 4217 letters with a minor unit (`MinorUnits`) |
| `availability` | one of `SchemaAvailabilities()`: `InStock`, `OutOfStock`, `PreOrder`, `PreSale`, `BackOrder`, `SoldOut`, `Discontinued`, `LimitedAvailability`, `InStoreOnly`, `OnlineOnly` — schema.org's words, NOT the catalogue's `in_stock` |
| `condition` | one of `SchemaConditions()`: `NewCondition`, `RefurbishedCondition`, `UsedCondition` |
| `gtin` | 8, 12, 13 or 14 digits, the GS1 check digit correct, no dash or space |
| `mpn`, `brand` | one plain line of at most `MaxCommerceFactText` (70) characters; one brand |

Core makes no Offer without an `amount` — it never guesses a number from your formatted `Price` (an `AggregateOffer` is
never made either).

### Filters and facets (EXPERIMENTAL)

**Sort** gains `best-selling` and `rating` (highest first) beside `""` (your own order, which Core calls "Recommended"),
`newest`, `price-asc`, `price-desc` and `name`; by what you count as sold and rated.

**`CommerceQuery.Filters`** (`CommerceFilters`) is what a shopper narrowed a catalogue by in Core's product filter:
`price_min` / `price_max` (inclusive, in the unit your price facet's `min` / `max` are in — Core never converts),
`options` (axis key → values), `brands`, `min_rating` (stars and up, 1–5), `in_stock`, `terms` (category slugs). Each list
is an OR in itself and an AND with the others. Honour them in `Products` and `CountProducts` alike, so the pager and the
list agree. Bounds: at most `MaxCommerceFilterAxes` (8) axes, `MaxCommerceFilterValues` (20) values in a list,
`MaxCommerceFilterValueBytes` (128) bytes a value, no value twice.

**`CommerceFacets`** answers what a filter offers. Core sends hook `commerce.facets` (`HookCommerceFacets`; `Serve`
subscribes it only for a shop that implements it) with the query's fields and `want`, the facet keys the owner chose, in
their order:

```json
{"term": "mugs", "filters": {"in_stock": true}, "want": ["option:colour", "price", "brand", "rating", "stock", "category"]}
```

and reads `{"facets": [CommerceFacet…]}`. A key is a kind — `price`, `brand`, `rating`, `stock`, `category` — or
`option:<axis key>` (`CommerceFacetKey`). Answer each key you can, at most once, only keys asked; leave out what you cannot
and the widget draws no such filter. A `CommerceFacet` is `key`, `kind`, `label` (in the shop's language) and either
`min` / `max` (price: a range, no options) or `options` (`CommerceFacetValue`: `value`, `label`, `count`; at most
`MaxCommerceFacetOptions`, 60). A rating facet's values are `"1"` to `"5"` (that many stars and up); a stock facet's one
value is `in_stock` (`CommerceFacetInStock`). **Counts are disjunctive**: each option counts what the query finds with that
value picked IN PLACE OF its own facet's selection, every other filter kept — so a second colour shows what it adds, as
WooCommerce's, Shopify's and BigCommerce's filters do. `nildatest.Storefront.CheckFacetCounts` holds your counts to your
own `CountProducts` this way.

### The payment marks strip

`CommerceEndpoints.PaymentMarks` names the acceptance marks of everything your checkout takes today, in the order to draw
them: each gateway option you offer (its `PaymentOption.Marks`, which the gateway declared on `PaymentMethod.Marks`) and
your own offline methods (`bank-transfer`, `cash-on-delivery`). Each is one of `PaymentMarks()` — visa, mastercard, amex,
maestro, paypal, klarna, apple-pay, google-pay, sepa, ideal, bancontact, eps, bank-transfer, cash-on-delivery — none twice.
Yours and not Core's because only you know which methods a buyer can actually choose; Core's strip draws exactly this list
and nothing else. Empty, no strip.

### The mini-cart drawer: `Cart` with `view=mini`

Core's cart badge may open a drawer — a native `<dialog>` from the page's end side — whose body is your `Cart` endpoint
asked with `view=mini` (`/shop/cart?view=mini`) EACH TIME it opens, never drawn into the cached page. Answer a compact
basket: the lines with their quantity steppers and Remove, the coupon box, the total and Checkout; your forms are sent by
the shell and redrawn in place, as in any mount. Core adds its own heading, close button and "View basket" link around it.
A shop that ignores `view` serves its full basket there, which works but is long.

### Search hits: `ProductForContent`

Core's live search panel finds products through Core's one search engine and asks your `ProductForContent` (hook
`commerce.product_for_content`) for the first hits of your product content type, to draw their `Image` and `Price` beside
the title. Up to ten questions per answer share one 1.5-second budget — answer from what you hold; a slow answer costs the
panel its pictures, never its results. A shop that does not implement `ProductForContent` gets plain hits.
