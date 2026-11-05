package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// EXPRESS WALLET BUTTONS (v0.58.0, EXPERIMENTAL; commerce's plan 10.2, step X1). Apple Pay, Google Pay, Link and PayPal
// buttons on a product page, the cart and the checkout: the payer approves in the wallet's own sheet, the shop re-prices
// delivery for the address the wallet shows, and the order is placed with the address and email the wallet hands over.
// One checkout, never a second: the shop's own basket checks, quote and order, and the same payment session as every
// other payment.
//
// Two shapes, because the two processors work two ways (each read 2026-11-05):
//
//	ExpressToken — Stripe's Express Checkout Element (docs.stripe.com/elements/express-checkout-element/accept-a-payment,
//	               Payment Intents variant; docs.stripe.com/payments/finalize-payments-on-the-server): nothing exists at
//	               the processor until the payer confirms in the sheet; the page then creates a ConfirmationToken, the
//	               server creates and confirms a PaymentIntent with it, and a payment that needs the payer again (3D
//	               Secure, a redirect) answers requires_action with a client secret the page hands to
//	               stripe.handleNextAction. Here: no session before the payer confirms; the consumer creates one with
//	               the token as its Credential (CredentialStripeConfirmationToken), and a start that needs the payer
//	               again answers redirected with a ClientAction.
//	ExpressOrder — PayPal's buttons (developer.paypal.com/sdk/js/reference/): the processor's order exists from the
//	               press (the button's order promise resolves to its id), its amount is updated on the server while the
//	               payer changes address or delivery in PayPal's window (onShippingAddressChange,
//	               onShippingOptionsChange; the Orders v2 PATCH "updates an order with a CREATED or APPROVED status",
//	               developer.paypal.com/docs/api/orders/v2/), and approval comes back to the merchant to capture. Here:
//	               the session starts at the press and answers redirected with a ClientAction (the processor's order
//	               id); the consumer re-prices it (Payments.Reprice → payment.reprice) until the payer approves; approval
//	               goes to the gateway's own finish path (Payments.Confirm, then capture or authorise).
//
// What each party does:
//
//	GATEWAY   describes the method its buttons pay with — PaymentMethod.Express: the kind, the script on its own route
//	          that draws the buttons, the processor's origins the page must open for it, and the PUBLIC configuration
//	          the script reads (a publishable key, a client id — never a secret). Answers a start with a ClientAction
//	          for its script; an ExpressOrder gateway answers payment.reprice (PaymentRepricer).
//	CORE      draws the express widget only where an express method is offered and the owner switched it on, loads the
//	          gateway's script from the gateway's route, opens the listed origins in the page's security policy only
//	          while that gateway offers express, hands the script the public configuration and a bridge to the shop
//	          (CommerceEndpoints.Express, commerceexpress.go), and holds the session's state as for any payment.
//	CONSUMER  (the shop) quotes the basket for the wallet's partial address, starts the session at the press or creates
//	          it with the token at the end, and re-prices an ExpressOrder session while its payer is still choosing.
//
// EXPERIMENTAL: inside payments/v1, outside its no-breaking promise until the shop and a gateway built by Nilda ship green
// on it — the fields' meaning may still change in a minor release, said in CHANGELOG.md. Every field is optional, so a
// gateway or consumer built before ignores them.

// The kinds of express button (PaymentExpress.Kind).
const (
	// ExpressToken — the payer confirms in the wallet's sheet before anything exists at the processor; the page sends a
	// one-use credential and the session is created with it (PaymentSessionParams.Credential). The method lists the
	// credential's type in CredentialTypes.
	ExpressToken = "token"
	// ExpressOrder — the processor's order exists from the press: the session starts then, answers redirected with a
	// ClientAction, is re-priced while the payer changes address or delivery (Payments.Reprice), and the payer's
	// approval reaches the gateway's own finish path. The method lists SupportReprice.
	ExpressOrder = "order"
)

// CredentialStripeConfirmationToken is a Stripe ConfirmationToken ("ctoken_…") made by the Express Checkout Element when
// the payer confirms in the wallet's sheet (stripe.createConfirmationToken): the gateway creates and confirms one
// PaymentIntent with it (confirmation_token, confirm=true). Stripe: "You must immediately use the created
// ConfirmationToken to confirm a PaymentIntent; if unused, it expires after 12 hours"
// (docs.stripe.com/payments/finalize-payments-on-the-server, read 2026-11-05). Named like CredentialStripeSPT.
const CredentialStripeConfirmationToken = "stripe.confirmation_token"

// SupportReprice — an open session paid this way can be re-priced before its payer approves (Payments.Reprice →
// payment.reprice): the processor's order is updated in place. A method that lists it belongs to a gateway that
// implements PaymentRepricer; a describe answer that says otherwise is refused (DispatchPaymentGatewayHook).
const SupportReprice = "reprice"

// HookPaymentReprice asks a gateway that implements PaymentRepricer to change the amount of a session's processor order
// before its payer approves: PaymentRepriceRequest in, PaymentRepriceResult out.
const HookPaymentReprice = "payment.reprice"

// The bounds of what express adds — Nilda's choices, except where named.
const (
	// maxExpressScript bounds the script's path, as a provider_ref: a path on the gateway's own route.
	maxExpressScript = 255
	// maxExpressOrigins is the most origins in one directive: Stripe.js needs five across three (docs.stripe.com/
	// security/guide, "Stripe.js" and "Link", read 2026-11-05).
	maxExpressOrigins = 16
	// maxExpressConfig, maxExpressConfigKey and maxExpressConfigValue bound the public configuration.
	maxExpressConfig      = 32
	maxExpressConfigKey   = 64
	maxExpressConfigValue = 1024
	// maxClientAction bounds a ClientAction: a PaymentIntent's client secret or a PayPal order id is under a hundred.
	maxClientAction = 1024
	// maxShippingOptions: "PayPal will display only the first 10 shipping rates provided" (Stripe's
	// shippingaddresschange event, docs.stripe.com/js/elements_object/express_checkout_element_shippingaddresschange_event,
	// read 2026-11-05).
	maxShippingOptions = 10
	// maxShippingOptionID and maxShippingOptionLabel bound one delivery option.
	maxShippingOptionID    = 64
	maxShippingOptionLabel = 100
)

// PaymentExpress is how a method's express buttons are drawn: what Core needs to load the gateway's script on a page,
// open the processor's origins for it and hand it its public configuration. EXPERIMENTAL.
type PaymentExpress struct {
	// Kind is ExpressToken or ExpressOrder.
	Kind string `json:"kind"`
	// Script is the path of the gateway's own script that draws the buttons, on the gateway's route and RELATIVE to its
	// prefix, as a webhook path is ("/express.js" — Core serves "/stripe/express.js"). A processor's own library is
	// loaded by that script from the processor's origin (Stripe.js "directly from js.stripe.com"; PayPal's SDK "directly
	// from https://www.paypal.com/sdk/js"), never copied onto the route.
	Script string `json:"script"`
	// Origins are what the page's Content-Security-Policy must allow for the processor's buttons, by directive. Core
	// opens them only on a page that draws this method's buttons, only while the gateway offers express.
	Origins PaymentExpressOrigins `json:"origins"`
	// Config is what the script reads, by key: a publishable key ("pk_live_…"), a client id, the merchant's country. It
	// is PUBLIC — written into a page that is cached and shown to every visitor — so a value shaped like a secret, or a
	// key named like one, is refused (PaymentExpress.Validate).
	Config map[string]string `json:"config,omitempty"`
}

// PaymentExpressOrigins are a processor's origins by the Content-Security-Policy directive that needs them. Each is
// "https://host" (a port allowed), with at most one leading "*." label — never a bare "*", a scheme alone, a path or a
// keyword.
type PaymentExpressOrigins struct {
	Script  []string `json:"script,omitempty"`
	Frame   []string `json:"frame,omitempty"`
	Connect []string `json:"connect,omitempty"`
	Img     []string `json:"img,omitempty"`
}

// expressOrigin is one CSP host source Core will put in a header: https only, a host of at least two labels after an
// optional leading "*." (so "*.com" is refused), lower case, an optional port, nothing after it.
var expressOrigin = regexp.MustCompile(`^https://(\*\.)?[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+(:[0-9]{1,5})?$`)

// expressScript is a path on the gateway's route: rooted, of the characters a URL path needs no escaping for.
var expressScript = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)

// expressConfigKey is a configuration key: snake case, as a setting's key is.
var expressConfigKey = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Validate refuses an express description Core could not act on safely: an unknown kind, a script that is not a path on
// the gateway's own route, an origin that is not one https host, and a public configuration that holds a secret.
func (e PaymentExpress) Validate() error {
	switch e.Kind {
	case ExpressToken, ExpressOrder:
	default:
		return fmt.Errorf("nilda: an express method's kind is %q or %q, got %q", ExpressToken, ExpressOrder, e.Kind)
	}
	if len(e.Script) > maxExpressScript || !expressScript.MatchString(e.Script) || strings.HasPrefix(e.Script, "//") ||
		strings.Contains(e.Script, "/../") || strings.HasSuffix(e.Script, "/..") || strings.HasSuffix(e.Script, "/") {
		return fmt.Errorf("nilda: an express method's script is a path on the gateway's own route, relative to its "+
			"prefix (\"/express.js\"), at most %d bytes; got %q", maxExpressScript, e.Script)
	}
	for directive, list := range map[string][]string{"script": e.Origins.Script, "frame": e.Origins.Frame,
		"connect": e.Origins.Connect, "img": e.Origins.Img} {
		if len(list) > maxExpressOrigins {
			return fmt.Errorf("nilda: an express method lists at most %d %s origins", maxExpressOrigins, directive)
		}
		for _, o := range list {
			if !expressOrigin.MatchString(o) {
				return fmt.Errorf("nilda: an express %s origin is one https host (\"https://js.stripe.com\", "+
					"\"https://*.paypal.com\"), got %q", directive, o)
			}
		}
	}
	return validPublicConfig(e.Config)
}

// validPublicConfig refuses a public configuration that is too large, a key that is not snake case, a value that is not
// one plain line, and anything shaped like a secret (secretShaped).
func validPublicConfig(c map[string]string) error {
	if len(c) > maxExpressConfig {
		return fmt.Errorf("nilda: an express method's public config has at most %d keys", maxExpressConfig)
	}
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	sort.Strings(keys) // the first refusal named is the same every time
	for _, k := range keys {
		v := c[k]
		if len(k) > maxExpressConfigKey || !expressConfigKey.MatchString(k) {
			return fmt.Errorf("nilda: a public config key is snake case, at most %d bytes; got %q", maxExpressConfigKey, k)
		}
		if len(v) > maxExpressConfigValue || !plainLine(v) || !utf8.ValidString(v) {
			return fmt.Errorf("nilda: public config %q is one plain line of at most %d bytes", k, maxExpressConfigValue)
		}
		if why := secretShaped(k, v); why != "" {
			return fmt.Errorf("nilda: public config %q is written into every visitor's page, and %s — a secret stays "+
				"on the gateway's server", k, why)
		}
	}
	return nil
}

// secretKeyWords name a secret in a configuration key, as one of its snake-case words: PayPal's REST app has a "client
// secret" beside its public client id (developer.paypal.com/api/rest/authentication/: "exchange your client ID and
// client secret for an access token"), and the secret has no shape of its own to recognise — so it is refused by its
// NAME. A per-session token (a PayPal client token, a Stripe client secret) is no site-wide configuration either.
var secretKeyWords = []string{"secret", "password", "passwd", "private", "token", "signing", "credential"}

// secretPrefixes are the shapes of a secret value, each from its processor's own page (read 2026-11-05):
//   - "sk_" a Stripe secret key (sk_test_, sk_live_, sk_org_) and "rk_" a restricted key: "Safe to expose: No"
//     (docs.stripe.com/keys); only the publishable "pk_…" is ("You can include it in front-end code");
//   - "whsec_" a Stripe webhook signing secret ("Webhook signing secrets aren't API keys—they're per-webhook secrets",
//     docs.stripe.com/keys);
//   - "A21AA" a PayPal OAuth access token, a bearer credential (its sample response, developer.paypal.com/api/rest/
//     authentication/);
//   - "-----BEGIN" a PEM private key or certificate, and "Bearer " a whole Authorization header.
var secretPrefixes = []string{"sk_", "rk_", "whsec_", "A21AA", "-----BEGIN", "Bearer ", "bearer "}

// jwtShape is a JSON Web Token — a signed credential, never a public setting.
var jwtShape = regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*$`)

// secretShaped says why a configuration entry is a secret, or "" when nothing about it is. The value's shape first —
// a Stripe client secret ("pi_…_secret_…", "seti_…_secret_…") is the PaymentIntent's own key for the payer's browser
// and belongs in a ClientAction, never in a page every visitor is served.
func secretShaped(key, value string) string {
	for _, word := range strings.Split(key, "_") {
		for _, w := range secretKeyWords {
			if word == w {
				return fmt.Sprintf("a key named %q holds a secret", key)
			}
		}
	}
	v := strings.TrimSpace(value)
	for _, p := range secretPrefixes {
		if strings.HasPrefix(v, p) {
			return fmt.Sprintf("a value starting %q is a secret", p)
		}
	}
	if strings.Contains(v, "_secret_") {
		return "a value holding \"_secret_\" is a processor's client secret for one payer"
	}
	if jwtShape.MatchString(v) {
		return "a value shaped like a JSON Web Token is a signed credential"
	}
	return ""
}

// validExpress holds a described method's express buttons to the kind's rule: a token kind charges a credential the
// method takes, an order kind re-prices, and neither is a card at a reader.
func validExpress(m PaymentMethod) error {
	if m.Express == nil {
		return nil
	}
	if err := m.Express.Validate(); err != nil {
		return fmt.Errorf("nilda: payment.describe: method %q: %w", m.Key, err)
	}
	sup := SupportsOf(m)
	switch {
	case Supports(sup, SupportCardPresent):
		return fmt.Errorf("nilda: payment.describe: method %q takes cards at a reader; express buttons are for a page", m.Key)
	case m.Express.Kind == ExpressToken && len(m.CredentialTypes) == 0:
		return fmt.Errorf("nilda: payment.describe: method %q's express buttons send a credential (kind %q); list its "+
			"type in credential_types (%q)", m.Key, ExpressToken, CredentialStripeConfirmationToken)
	case m.Express.Kind == ExpressOrder && !Supports(sup, SupportReprice):
		return fmt.Errorf("nilda: payment.describe: method %q's express buttons open the processor's order at the press "+
			"(kind %q), which the payer re-prices by changing address; list %q in supports", m.Key, ExpressOrder, SupportReprice)
	}
	return nil
}

// ClientAction is what a gateway's own script in the payer's browser needs to go on with a session that answered
// redirected without a page: a PaymentIntent's client secret for stripe.handleNextAction (3D Secure, a redirect), or a
// PayPal order id for the buttons' order promise. Opaque to Core and the consumer; one line of visible ASCII. It is the
// PAYER'S — a Stripe client secret "lets the client access important fields from the PaymentIntent" — so it is handed
// only to the browser that pressed the button, never logged, never put in a URL, and Core drops it once the session
// leaves redirected. Its String never shows it. EXPERIMENTAL.
type ClientAction string

// String never shows the value, so a session printed by mistake leaks no client secret.
func (c ClientAction) String() string {
	if c == "" {
		return ""
	}
	return "[client action redacted]"
}

// GoString likewise, for %#v.
func (c ClientAction) GoString() string { return c.String() }

// validClientAction accepts "" and one line of visible ASCII up to maxClientAction bytes.
func validClientAction(c ClientAction) error {
	if len(c) > maxClientAction {
		return fmt.Errorf("nilda: a client_action is at most %d bytes", maxClientAction)
	}
	for i := 0; i < len(c); i++ {
		if c[i] < 0x21 || c[i] > 0x7e {
			return fmt.Errorf("nilda: a client_action is one line of visible ASCII")
		}
	}
	return nil
}

// PaymentShippingOption is one delivery choice the shop offers for the address the wallet shows, as the processor's
// sheet lists it (Stripe's shippingRates, PayPal's shipping options). AmountMinor is its price in the session's currency,
// tax as the shop shows it; the session's amount already includes the Selected one.
type PaymentShippingOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	AmountMinor int64  `json:"amount_minor"`
	Selected    bool   `json:"selected,omitempty"`
}

// validShippingOptions holds a list to what both sheets read: at most ten, each with an id and a label, no id twice, no
// negative price, and exactly one selected when there are any.
func validShippingOptions(opts []PaymentShippingOption) error {
	if len(opts) > maxShippingOptions {
		return fmt.Errorf("nilda: at most %d delivery options — a wallet's sheet shows no more", maxShippingOptions)
	}
	seen, selected := map[string]bool{}, 0
	for _, o := range opts {
		switch {
		case strings.TrimSpace(o.ID) == "" || len(o.ID) > maxShippingOptionID:
			return fmt.Errorf("nilda: a delivery option's id is 1 to %d bytes", maxShippingOptionID)
		case seen[o.ID]:
			return fmt.Errorf("nilda: two delivery options are called %q", o.ID)
		case strings.TrimSpace(o.Label) == "" || utf8.RuneCountInString(o.Label) > maxShippingOptionLabel:
			return fmt.Errorf("nilda: delivery option %q's label is 1 to %d characters", o.ID, maxShippingOptionLabel)
		case !plainLine(o.ID) || !plainLine(o.Label):
			return fmt.Errorf("nilda: delivery option %q's id and label are plain lines", o.ID)
		case o.AmountMinor < 0:
			return fmt.Errorf("nilda: delivery option %q costs a negative amount", o.ID)
		}
		seen[o.ID] = true
		if o.Selected {
			selected++
		}
	}
	if len(opts) > 0 && selected != 1 {
		return fmt.Errorf("nilda: exactly one delivery option is selected, got %d", selected)
	}
	return nil
}

// PaymentReprice is what a CONSUMER sends to change an open session's amount before its payer approves
// (Payments.Reprice): the new total, in the session's currency, and — when delivery is chosen in the processor's
// window — the options for the address now shown, the chosen one Selected. EXPERIMENTAL.
type PaymentReprice struct {
	AmountMinor     int64                   `json:"amount_minor"`
	ShippingOptions []PaymentShippingOption `json:"shipping_options,omitempty"`
}

// Validate refuses a re-price with no positive amount or with delivery options a sheet could not show.
func (r PaymentReprice) Validate() error {
	if r.AmountMinor <= 0 {
		return fmt.Errorf("nilda: a re-price's amount_minor must be positive, got %d", r.AmountMinor)
	}
	return validShippingOptions(r.ShippingOptions)
}

// PaymentRepriceRequest is what HookPaymentReprice carries: make the processor's order for Session ask AmountMinor now,
// with these delivery options. Setting an amount is idempotent by nature — asked twice, the order asks the same.
type PaymentRepriceRequest struct {
	Session         PaymentSession          `json:"session"`
	AmountMinor     int64                   `json:"amount_minor"`
	ShippingOptions []PaymentShippingOption `json:"shipping_options,omitempty"`
}

// PaymentRepriceResult is a gateway's answer to HookPaymentReprice: Repriced when the processor's order now asks the new
// amount; not, with FailureMessage, when the processor refused (the payer approved meanwhile, the order is gone) — Core
// answers the consumer 409 and the session keeps its amount. A processor you could not reach is an ERROR: nothing
// changed, Core answers the consumer 503, and the consumer may ask again.
type PaymentRepriceResult struct {
	Repriced       bool   `json:"repriced"`
	FailureMessage string `json:"failure_message,omitempty"`
}

// PaymentRepricer is what a gateway implements to re-price an open session's processor order before its payer approves;
// its methods list SupportReprice. Core sends payment.reprice only to such a gateway, only for a session that is created
// or redirected, that no credential, kept method or reader started, and whose payer's approval has not reached Core
// (Payments.Confirm). EXPERIMENTAL.
type PaymentRepricer interface {
	RepricePayment(ctx context.Context, req PaymentRepriceRequest) (PaymentRepriceResult, error)
}

// repriceable reports whether a session may still be re-priced, by what the session itself says: the payer has not
// approved (created or redirected — the processor's order is open), and nothing charged on the spot started it (a
// credential, a kept method, a reader: the payer approved an amount already, or is not there to approve another). Core
// adds the one fact the session does not carry: that the gateway has not asked Payments.Confirm for it yet.
func repriceable(s PaymentSession) error {
	switch {
	case s.Status != PaymentCreated && s.Status != PaymentRedirected:
		return fmt.Errorf("nilda: only a payment its payer has not approved can be re-priced; this one is %s", s.Status)
	case s.ChargedSaved != "" || s.Reader != "" || s.Save != nil:
		return fmt.Errorf("nilda: a kept method's charge, a card at a reader and a session that keeps a method are not re-priced")
	case !Supports(s.Supports, SupportReprice):
		return fmt.Errorf("nilda: this payment's method cannot be re-priced (it does not list %q)", SupportReprice)
	}
	return nil
}

func validRepriceResult(r PaymentRepriceResult) error {
	if r.Repriced && r.FailureMessage != "" {
		return fmt.Errorf("nilda: payment.reprice answered repriced with a failure message")
	}
	if utf8.RuneCountInString(r.FailureMessage) > maxPaymentText {
		return fmt.Errorf("nilda: a failure message is at most %d characters", maxPaymentText)
	}
	if !noNUL(r.FailureMessage) {
		return errNUL()
	}
	return nil
}

// dispatchRepriceHook answers payment.reprice for DispatchPaymentGatewayHook. handled is false for a gateway that does
// not re-price: Serve never subscribed it.
func dispatchRepriceHook(ctx context.Context, g PaymentGateway, payload []byte) (out []byte, handled bool, err error) {
	rp, is := g.(PaymentRepricer)
	if !is {
		return nil, false, nil
	}
	var in PaymentRepriceRequest
	if err := json.Unmarshal(payload, &in); err != nil {
		return nil, true, fmt.Errorf("nilda: payment.reprice payload: %w", err)
	}
	if err := validSession(in.Session); err != nil {
		return nil, true, err
	}
	if err := (PaymentReprice{AmountMinor: in.AmountMinor, ShippingOptions: in.ShippingOptions}).Validate(); err != nil {
		return nil, true, err
	}
	if err := repriceable(in.Session); err != nil {
		return nil, true, err
	}
	res, err := rp.RepricePayment(ctx, in)
	if err != nil {
		return nil, true, err
	}
	if err := validRepriceResult(res); err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}

// Reprice changes an open session's amount before its payer approves — the wallet's address or delivery choice changed
// what the order costs (v0.58.0, EXPERIMENTAL). Core asks the gateway once (payment.reprice) and answers with the
// session at its new amount. Refused: a session the payer approved or that ended (409: its approval reached Core, or it
// is no longer created or redirected), one a credential, a kept method or a reader started, one whose method does not
// list SupportReprice (422), the processor's own refusal (409, its reason in the message). A 503 means the gateway did
// not answer: nothing changed, and the same call with the same key may be sent again. idempotencyKey names this
// re-price (the session and the quote it is for).
func (p *Payments) Reprice(ctx context.Context, idempotencyKey, sessionID string, r PaymentReprice) (PaymentSession, error) {
	if err := validIdempotencyKey(idempotencyKey); err != nil {
		return PaymentSession{}, err
	}
	if err := r.Validate(); err != nil {
		return PaymentSession{}, err
	}
	path, err := paymentPath("/payments/sessions/", sessionID, "/reprice")
	if err != nil {
		return PaymentSession{}, err
	}
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err = p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, path, r, &out)
	return out.Data, err
}
