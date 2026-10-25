package nilda

import "net/http"

// THE BUYER'S ACCOUNT, INSIDE THE SITE (v0.44.0, Core's M4 lane A). The whole guide is docs/COMMERCE.md "Account
// views"; these are the names it uses.
//
// Core's My Account widget is a members area: a mount on the owner's account page that fetches your
// Endpoints().Account and draws your answer in place. Three things make it one:
//
//   - LINKS. Mark a link inside your fragment with AccountNavAttr (data-pb-nav) and Core's shell follows it in
//     place — your endpoint asked with the link's query, the address pushed as ?shop=<that query>, Back
//     restoring it, focus on the new heading. Write the href as a link to the same page, "?shop=view%3Dorders",
//     so it also works with no script, opened in a new tab, or reloaded: on first render Core forwards the page's
//     ?shop= to your endpoint.
//   - SIGN-IN. A view only a member may see answers CommerceSignInRequired for a signed-out visitor. Core's shell
//     then draws Core's own sign-in panel in the mount — sign in, create an account by the site's policy, the
//     second factor, the providers — and fetches the same view again once the buyer is signed in. You never see
//     a password, and a guest view (an order found by its signed link) simply answers as usual.
//   - LOOK. Wear the classes in AccountVocabulary and nothing else — no inline colour. They are style parts of
//     the account widget, so the owner restyles them from the page builder, and Core draws them from the site's
//     design tokens, dark mode included.

// AccountNavAttr marks a link in your account fragment that Core's shell follows in place.
const AccountNavAttr = "data-pb-nav"

// CommerceSignInHeader is the header Core's shell reads on a 401 to draw its sign-in panel.
const CommerceSignInHeader = "Nilda-Sign-In"

// CommerceSignInRequired answers a members-only view for a signed-out visitor: 401 with Nilda-Sign-In: required,
// not cached. Core's shell draws its sign-in panel in the mount and asks for the same view again once the buyer has
// signed in; any other refusal is shown as the mount's failure sentence.
//
//	if _, ok := nilda.CurrentUser(r); !ok {
//	    nilda.CommerceSignInRequired(w)
//	    return
//	}
func CommerceSignInRequired(w http.ResponseWriter) {
	w.Header().Set(CommerceSignInHeader, "required")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
}

// AccountVocabulary is every class Core declares for a members area: the account widget's style parts, with a
// default look from the site's design tokens. A link for the view showing is .pb-account-link with
// aria-current="page"; a finished timeline step is .pb-account-step with .is-done.
var AccountVocabulary = []string{
	"pb-account-nav",       // the account's menu
	"pb-account-link",      // each menu link (aria-current="page" on the one showing)
	"pb-account-card",      // a card: an order, an address, a return
	"pb-account-row",       // a row inside a card or a list
	"pb-account-chip",      // a status chip: Paid, Shipped, Refunded
	"pb-account-step",      // a timeline step (is-done when passed)
	"pb-account-primary",   // the main button (beside Core's own .pb-button)
	"pb-account-secondary", // the other buttons
	"pb-account-field",     // a label and its input
	"pb-account-error",     // a message that something went wrong
	"pb-account-success",   // a message that something worked
	"pb-account-table",     // a table: an order's lines, invoices
}

// EventUserEmailVerified is Core's event for a person whose address is CONFIRMED — the double opt-in link
// redeemed, or a sign-in provider that verified it. Subscribe to it to act on an address (a shop claims the
// guest orders placed with it). Never sent for a sign-up that proved nothing. The subject is the user's id and
// there is no data: read the address back through the API.
const EventUserEmailVerified = "user.email_verified"
