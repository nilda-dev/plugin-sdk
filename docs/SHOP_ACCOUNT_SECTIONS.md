# shop.account.sections/v1 — another plugin's section on the buyer's account page (EXPERIMENTAL)

From plugin-sdk v0.69.0 (`shopaccountsections.go`). A plugin with something to show a signed-in buyer — their
subscriptions with Pause and Cancel, their points balance, history and referral link — provides it; the shop requires it
(`uses`) and draws the sections in its account menu.

## Whose page it is

Core's My Account widget is a mount (`COMMERCE.md`, "Account views"): Core draws the shell, its sign-in panel, its profile
panel and the account vocabulary; the shop draws everything inside — the menu, the router, every view. So a section joins
the **shop's** menu, as a WooCommerce extension adds a My Account endpoint (`woocommerce_account_menu_items`) while WordPress
says who is signed in, and as a Shopify app's full-page customer-account extension joins the account menu while Shopify's
session token names the customer.

## The one method

`sections.describe` (`AccountSectionsRequest{account_page}`) → `AccountSectionsAnswer{sections}`, at most `MaxAccountSections` (4). Each `AccountSection`:

| Field | What |
|---|---|
| `key` | `^[a-z][a-z0-9_]{0,31}$`; the shop names the section `<plugin>.<key>` (`AccountSectionID`) |
| `label` | the menu's words and the section's heading, per language, one plain line of at most 40 characters; English required. Write every language the site may run in (Nilda's six: en fa ar de es fr) |
| `path` | the plugin's own route that draws the section, **under its `route_prefix`**, no query (`ValidAccountSectionPath`) |
| `order` | a position hint until the owner chooses: the shop's own views stand at 10, 20 … 90; 0 or above 90 is after them |

`account_page` is the path of the site's published account page, or empty while there is none. Keep the last one you were
told: a link you mail ("your points", "manage your subscription") is `AccountSectionPageURL(accountPage, key, …)` on the
site's address, and your old bare page answers it with a 302 into the account page while you know one — the way the shop
answers its own order pages' old addresses.

Fallback `cached`: a provider that does not answer keeps its last good sections. `agent: none` — the owner's doors are the
shop's abilities (on/off and order) and the providing plugin's own support read.

Implement `AccountSectionsProvider` (`AccountSections(ctx)`); `Serve` finds it, or return `AccountSectionsMethods(p)` from
your `ServiceMethods`. Declare `"provides": ["shop.account.sections/v1"]`, the `service_provider` and `route` capabilities
and a `route_prefix`.

## Who the section is for: Core's word, never a request's

The shop **never renders a section and never sends a user id**. For the section's view it draws its menu, the heading and a
mount of Core's storefront runtime pointing at your path:

```html
<section class="nilda-account-view"><h2>Points</h2>
  <div class="pb-commerce-mount" data-pb-commerce="mount" data-endpoint="/loyalty/account?page=2" data-error="…">
    <p class="pb-commerce-loading">…</p></div></section>
```

The buyer's browser fetches that path through Core's proxy, which asserts who is signed in from Core's own session
(`CurrentUser(r)`: `X-Nilda-User`, every client-sent `X-Nilda-*` header stripped first). So your handler reads the buyer
from the request Core vouched for and from nothing else — no `user_id` parameter, no cookie of your own:

```go
func (p *plugin) serveAccount(w http.ResponseWriter, r *http.Request) {
	viewer, ok := nilda.CurrentUser(r)
	if !ok {
		nilda.CommerceSignInRequired(w) // the session lapsed: Core's panel in the shop's mount
		return
	}
	nilda.WriteAccountSection(w, http.StatusOK, p.sectionHTML(r.Context(), viewer.UserID, locale(r)))
}
```

- **Never stored.** `WriteAccountSection` / `AccountSectionHeaders` send `Cache-Control: private, no-store` and
  `Vary: Cookie`, so no cache serves one buyer's section to another.
- **Its look.** `AccountVocabulary` and no inline colour, as the shop's own views. The shop draws the `<h2>`; start yours at
  `<h3>`.
- **Its words.** In the visitor's language (`VisitorLocale(r)`), all six languages; the page's direction comes from Core.
- **Its links.** A link to another state of the section (a history's next page, one subscription) is
  `AccountSectionHref(plugin, key, q)` marked `AccountNavAttr`: Core's shell follows it in place, the shop forwards `q`
  (never `view` or `section`, which are the shop's) to your path.
- **Its forms.** `<form method="post" action="/loyalty/account/redeem">` inside the section: Core's shell posts it to your
  route — Core's CSRF check, Core's identity headers — and draws your answer in the nearest `[data-pb-fragment]` around the
  form, else the whole section. Answer with `WriteAccountSection` too.
- **Staff acting as the buyer.** `Viewer.ImpersonatedBy` is set while support is signed in as the buyer: show the section
  (that is what support is for), but refuse what must be the buyer's own act — a cancellation, a pause, spending points —
  with a sentence saying so.

## Where a section may point

A path is a route of the provider's own. Core stamps every desk answer with the answering plugin's `route_prefix`
(`ServiceAnswer.RoutePrefix`, Core's word), and `Services.AccountSections` — the shop's side — drops the whole answer of a
provider that names a path outside it (`AccountSectionOutsideRoutes`), so no provider can make the account page fetch
Core's API, the shop's routes or another plugin's. An answer from a Core that stamps no prefix is dropped the same way.

## The shop's side

`Services.AccountSections(ctx)` → `AccountSectionsResult{Sections []ProvidedAccountSection, Dropped, Cached}` (or
`AccountSectionsFrom(result)` for a call already made). The shop keeps the owner's choice per section — on or off, its
position — and offers it as abilities; a section the owner turned off is not in the menu and its view is not drawn.
