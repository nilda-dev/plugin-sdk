# The storefront — account views inside the site

Core ships the storefront widgets (Products, Cart, Checkout, My Account …) and no commerce code; your plugin
supplies the products and the endpoints the widgets' shells call. `commerce.go` is the reference for the catalogue,
the cart and the checkout. This guide is the **buyer's account** (v0.44.0): how your account views live inside the
owner's themed page instead of on a bare page of your own.

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

## 4. When an address is proved: `user.email_verified`

Core emits `user.email_verified` (`nilda.EventUserEmailVerified`) when a person **confirms** their address — the
double opt-in link, or a sign-in provider that verified it. The subject is the user's id and there is no data; read
the address back through the API. It is never sent for a sign-up that proved nothing (a pending account, a single
opt-in account), so it is the safe moment to act on an address — a shop claims the guest orders placed with it.
Subscribe with the `events` capability (`EVENTS.md`).
