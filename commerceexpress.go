package nilda

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"
)

// EXPRESS CHECKOUT, THE SHOP'S SIDE (v0.58.0, EXPERIMENTAL; commerce's plan 10.2, step X1). The wallet buttons
// (paymentexpress.go) reach the shop through ONE endpoint of its own, CommerceEndpoints.Express, called by Core's express
// widget from the visitor's browser. It is the shop's ordinary checkout with three steps a wallet needs:
//
//	quote     the basket — the visitor's cart, or one product pressed on its page — priced for the PARTIAL address the
//	          wallet shows before the payer confirms (WalletAddress: country, region, city, postal code; never a street
//	          or a name), with its delivery options. Also asked with no address, to draw the buttons with a total; and,
//	          for an ExpressOrder session, with the Session to re-price (Payments.Reprice) while the payer is choosing.
//	start     ExpressOrder only (PayPal): the button was pressed — the shop places the order and starts the session;
//	          the answer's ClientAction is the processor's order for the buttons.
//	complete  the payer confirmed in the sheet: the payer's email and final address (ExpressPayer) arrive now, after
//	          consent, and nothing personal before. ExpressToken (Stripe): the shop places the order and creates the
//	          session with the wallet's one-use Credential. ExpressOrder: the shop records the payer on the order the
//	          start placed, before the gateway's finish path asks it to confirm.
//
// Each press is keyed by Press, minted by Core's bridge per press: start and complete under one Press are one order and
// one session, however often the browser sends them. The shop answers Available false — and Core draws no buttons —
// for a basket the wallet cannot complete honestly: one that needs a tick the sheet cannot give (a download's consent,
// a pre-order's terms) or a payment it cannot take (an invoice). Beside the buttons Core draws the shop's Notice (the
// terms line) and Total, so the payer reads the final price and what they agree to before they press (the order
// button rule, § 312j BGB).
//
// Why an endpoint and not a contract: the shop.checkout/v1 contract the plan of 2026-10-18 named was retired in
// v0.47.0 (commerce DR-19: the agentic checkout is the shop's own), and the basket lives under a cookie only the shop's
// own route receives. So the wallet buttons call the shop's route as the cart and "notify me" do (CommerceEndpoints),
// and no second checkout is opened anywhere — the endpoint IS the express channel, so no channel field is needed.

// The steps of an express checkout (ExpressRequest.Step).
const (
	ExpressStepQuote    = "quote"
	ExpressStepStart    = "start"
	ExpressStepComplete = "complete"
)

// What an express checkout buys (ExpressRequest.Source).
const (
	// ExpressFromCart — the visitor's basket, as the cart page and the checkout hold it.
	ExpressFromCart = "cart"
	// ExpressFromProduct — one product, pressed on its own page (Item), bought on its own: the basket is untouched.
	ExpressFromProduct = "product"
)

// The bounds of an express request and answer — Nilda's choices.
const (
	maxExpressBody     = 64 << 10
	maxExpressPress    = 64
	maxExpressQuantity = 999
	maxExpressLines    = 50
	maxExpressPhone    = 32
)

// expressPress is a press's key: what Core's bridge mints, url-safe.
var expressPress = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// WalletAddress is the address a wallet shows BEFORE its payer confirms: enough to price delivery and tax, and nothing
// else. The four fields are all there is — no street, no name, no phone — because the wallets give no more: Stripe's
// shippingaddresschange "might anonymize the shipping address by removing sensitive information that isn't necessary to
// calculate shipping costs … the shipping address in the US can only contain a city, state, and ZIP code"
// (docs.stripe.com/js/elements_object/express_checkout_element_shippingaddresschange_event, read 2026-11-05); PayPal's
// onShippingAddressChange gives city, state, countryCode and postalCode (developer.paypal.com/sdk/js/reference/, read
// 2026-11-05). The full address and the name arrive with complete (ExpressPayer), once the payer consented.
type WalletAddress struct {
	Country    string `json:"country"`
	Region     string `json:"region,omitempty"`
	City       string `json:"city,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
}

// Validate refuses an address with no ISO 3166-1 alpha-2 country or a part past an address's bounds.
func (a WalletAddress) Validate() error {
	if !countryCode.MatchString(a.Country) {
		return fmt.Errorf("nilda: a wallet's address names its country as an ISO 3166-1 alpha-2 code (\"DE\"), got %q", a.Country)
	}
	return PaymentAddress{Country: a.Country, Region: a.Region, City: a.City, PostalCode: a.PostalCode}.Validate()
}

// ExpressItem is the one product an express checkout from a product page buys: its id (as Products answers it), the
// shopper's variation answers (as AddToCart receives them) and how many.
type ExpressItem struct {
	Product  string            `json:"product"`
	Options  map[string]string `json:"options,omitempty"`
	Quantity int               `json:"quantity"`
}

// ExpressPayer is who pays and where it goes, as the wallet hands it over once the payer confirmed: Stripe's confirm
// event (billingDetails, shippingAddress, payerEmail — "Pass emailRequired: true … These wallets don't share the
// customer's email by default"), PayPal's approved order (payer, shipping).
type ExpressPayer struct {
	Email           string          `json:"email"`
	Name            string          `json:"name,omitempty"`
	Phone           string          `json:"phone,omitempty"`
	ShippingAddress *PaymentAddress `json:"shipping_address,omitempty"`
	BillingAddress  *PaymentAddress `json:"billing_address,omitempty"`
}

// Validate refuses a payer with no plain email address, a name or phone past its bound, or an address that is not one.
func (p ExpressPayer) Validate() error {
	if a, err := mail.ParseAddress(p.Email); err != nil || a.Address != p.Email || len(p.Email) > maxEmail {
		return fmt.Errorf("nilda: an express payer has a plain email address, got %q", p.Email)
	}
	if utf8.RuneCountInString(p.Name) > maxAddressLine || !plainLine(p.Name) {
		return fmt.Errorf("nilda: an express payer's name is one line of at most %d characters", maxAddressLine)
	}
	if len(p.Phone) > maxExpressPhone || !plainLine(p.Phone) {
		return fmt.Errorf("nilda: an express payer's phone is one line of at most %d bytes", maxExpressPhone)
	}
	for _, a := range []*PaymentAddress{p.ShippingAddress, p.BillingAddress} {
		if a != nil {
			if err := a.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExpressRequest is what Core's express widget sends the shop's Express endpoint.
type ExpressRequest struct {
	// Step is ExpressStepQuote, ExpressStepStart or ExpressStepComplete.
	Step string `json:"step"`
	// Gateway and Method are the express method whose buttons were drawn (a PaymentOption with Express).
	Gateway string `json:"gateway"`
	Method  string `json:"method"`
	// Source is ExpressFromCart or ExpressFromProduct; Item is the product, for the second only.
	Source string       `json:"source"`
	Item   *ExpressItem `json:"item,omitempty"`
	// Address is the wallet's partial address, for quote and start — never with complete, which carries the final one.
	Address *WalletAddress `json:"address,omitempty"`
	// ShippingOption is the id of the delivery option the payer chose (one the last quote answered).
	ShippingOption string `json:"shipping_option,omitempty"`
	// Press names one press of a button, minted by Core's bridge: start and complete under one Press are one order and
	// one session. Required for both.
	Press string `json:"press,omitempty"`
	// Session is an ExpressOrder session the start made: a quote with it re-prices it, complete names it.
	Session string `json:"session,omitempty"`
	// Credential is the wallet's one-use credential, for an ExpressToken complete (CredentialStripeConfirmationToken).
	// Hand it to Payments.CreateSession once; never store, log or echo it.
	Credential *PaymentCredential `json:"credential,omitempty"`
	// Payer is who pays, for complete only: nothing personal travels before the payer confirmed.
	Payer *ExpressPayer `json:"payer,omitempty"`
}

// Validate refuses a request the shop cannot answer, and above all anything personal before consent: a quote or a start
// carries only the partial address, and complete carries the payer and no partial address.
func (r ExpressRequest) Validate() error {
	if strings.TrimSpace(r.Gateway) == "" || strings.TrimSpace(r.Method) == "" {
		return errors.New("nilda: an express request names the gateway and the method whose buttons were pressed")
	}
	switch r.Source {
	case ExpressFromCart:
		if r.Item != nil {
			return errors.New("nilda: an express checkout of the cart names no item")
		}
	case ExpressFromProduct:
		if r.Item == nil || strings.TrimSpace(r.Item.Product) == "" || r.Item.Quantity < 1 || r.Item.Quantity > maxExpressQuantity {
			return fmt.Errorf("nilda: an express checkout of a product names it and a quantity of 1 to %d", maxExpressQuantity)
		}
	default:
		return fmt.Errorf("nilda: an express checkout is of the %q or of one %q, got %q", ExpressFromCart, ExpressFromProduct, r.Source)
	}
	if r.Press != "" && (len(r.Press) > maxExpressPress || !expressPress.MatchString(r.Press)) {
		return fmt.Errorf("nilda: an express press is 1 to %d url-safe characters", maxExpressPress)
	}
	if !noNUL(r.Gateway, r.Method, r.ShippingOption, r.Session) || !plainLine(r.ShippingOption) || !plainLine(r.Session) {
		return errors.New("nilda: an express request's names are plain lines")
	}
	if r.Address != nil {
		if err := r.Address.Validate(); err != nil {
			return err
		}
	}
	switch r.Step {
	case ExpressStepQuote, ExpressStepStart:
		if r.Payer != nil || r.Credential != nil {
			return fmt.Errorf("nilda: an express %s carries no payer and no credential — nothing personal travels before "+
				"the payer confirmed in the wallet", r.Step)
		}
		if r.Step == ExpressStepStart && (r.Press == "" || r.Session != "") {
			return errors.New("nilda: an express start is keyed by its press and starts a session; it names none")
		}
	case ExpressStepComplete:
		if r.Press == "" {
			return errors.New("nilda: an express complete is keyed by its press")
		}
		if r.Address != nil {
			return errors.New("nilda: an express complete carries the payer's final address (payer), not the wallet's partial one")
		}
		if r.Payer == nil {
			return errors.New("nilda: an express complete carries the payer the wallet handed over")
		}
		if err := r.Payer.Validate(); err != nil {
			return err
		}
		if (r.Credential == nil) == (r.Session == "") {
			return errors.New("nilda: an express complete carries the wallet's credential (a token kind) or names the " +
				"session its start made (an order kind) — one of the two")
		}
		if r.Credential != nil {
			if err := r.Credential.Validate(); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("nilda: an express step is %q, %q or %q, got %q", ExpressStepQuote, ExpressStepStart,
			ExpressStepComplete, r.Step)
	}
	return nil
}

// DecodeExpressRequest reads the body of a call to the Express endpoint — at most 64 KiB, no field it does not know (a
// street or a name inside the partial address is refused, not dropped) — and validates it.
func DecodeExpressRequest(body io.Reader) (ExpressRequest, error) {
	raw, err := io.ReadAll(io.LimitReader(body, maxExpressBody+1))
	if err != nil {
		return ExpressRequest{}, err
	}
	if len(raw) > maxExpressBody {
		return ExpressRequest{}, fmt.Errorf("nilda: an express request is at most %d bytes", maxExpressBody)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r ExpressRequest
	if err := dec.Decode(&r); err != nil {
		return ExpressRequest{}, fmt.Errorf("nilda: an express request: %w", err)
	}
	return r, r.Validate()
}

// ExpressLine is one line a wallet's sheet shows under the total: an item, delivery, tax (Stripe's lineItems).
type ExpressLine struct {
	Label       string `json:"label"`
	AmountMinor int64  `json:"amount_minor"`
}

// ExpressAnswer is the shop's answer to every step.
type ExpressAnswer struct {
	// Available is whether the shop takes this basket by express; false, Core draws no buttons and Message says why in
	// the shopper's language.
	Available bool   `json:"available"`
	Message   string `json:"message,omitempty"`
	// Currency, TotalMinor and Total are what the sheet asks and what Core writes beside the buttons: Total formatted by
	// the shop (Core never formats money), tax as the shop shows it.
	Currency   string `json:"currency,omitempty"`
	TotalMinor int64  `json:"total_minor,omitempty"`
	Total      string `json:"total,omitempty"`
	// Lines are the sheet's lines; ShippingRequired asks the wallet for an address; ShippingOptions are the delivery
	// choices for the address quoted, the Selected one in the total.
	Lines            []ExpressLine           `json:"lines,omitempty"`
	ShippingRequired bool                    `json:"shipping_required,omitempty"`
	ShippingOptions  []PaymentShippingOption `json:"shipping_options,omitempty"`
	// Capture is how the session will take the money (CaptureAutomatic or CaptureManual): a processor's page script
	// mounts with the same (Stripe: "Make sure the Elements options you provide (such as captureMethod …) match the
	// equivalent parameters you pass when creating and confirming the Intent").
	Capture string `json:"capture,omitempty"`
	// Notice is the sentence beside the buttons, in the shop's words ("By paying you accept the terms"), and TermsURL
	// the page it names.
	Notice   string `json:"notice,omitempty"`
	TermsURL string `json:"terms_url,omitempty"`
	// After start and complete: the session, its status, what the gateway's script needs (ClientAction) or where to send
	// the payer (RedirectURL), and the order's page the payer lands on (OrderURL).
	Session      string       `json:"session,omitempty"`
	Status       string       `json:"status,omitempty"`
	ClientAction ClientAction `json:"client_action,omitempty"`
	RedirectURL  string       `json:"redirect_url,omitempty"`
	OrderURL     string       `json:"order_url,omitempty"`
}

// Validate refuses an answer Core's widget could not draw truthfully.
func (a ExpressAnswer) Validate() error {
	if utf8.RuneCountInString(a.Message) > maxPaymentText || !plainLine(a.Message) {
		return fmt.Errorf("nilda: an express answer's message is one line of at most %d characters", maxPaymentText)
	}
	if !a.Available {
		return nil
	}
	if err := validAmount(a.TotalMinor, a.Currency); err != nil {
		return err
	}
	if strings.TrimSpace(a.Total) == "" || utf8.RuneCountInString(a.Total) > 64 || !plainLine(a.Total) {
		return errors.New("nilda: an express answer writes its total as the shop shows it, one line")
	}
	if len(a.Lines) > maxExpressLines {
		return fmt.Errorf("nilda: an express answer has at most %d lines", maxExpressLines)
	}
	for _, l := range a.Lines {
		if strings.TrimSpace(l.Label) == "" || utf8.RuneCountInString(l.Label) > maxShippingOptionLabel || !plainLine(l.Label) {
			return fmt.Errorf("nilda: an express line's label is one line of 1 to %d characters", maxShippingOptionLabel)
		}
	}
	if err := validShippingOptions(a.ShippingOptions); err != nil {
		return err
	}
	if a.Capture != "" && a.Capture != CaptureAutomatic && a.Capture != CaptureManual {
		return fmt.Errorf("nilda: an express answer's capture is %q or %q", CaptureAutomatic, CaptureManual)
	}
	if utf8.RuneCountInString(a.Notice) > maxPaymentText || !plainLine(a.Notice) {
		return fmt.Errorf("nilda: an express notice is one line of at most %d characters", maxPaymentText)
	}
	switch a.Status {
	case "", PaymentCreated, PaymentRedirected, PaymentPending, PaymentAuthorized, PaymentResolved, PaymentRejected:
	default:
		return fmt.Errorf("nilda: an express answer's status is a session's, got %q", a.Status)
	}
	if a.ClientAction != "" && a.Status != PaymentRedirected {
		return errors.New("nilda: an express answer hands a client_action only for a redirected session")
	}
	if err := validClientAction(a.ClientAction); err != nil {
		return err
	}
	for field, u := range map[string]string{"terms_url": a.TermsURL, "redirect_url": a.RedirectURL, "order_url": a.OrderURL} {
		if err := validPayerURL(field, u, false); err != nil {
			return err
		}
	}
	return nil
}
