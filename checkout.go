package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// THE SHOP'S CHECKOUT, DRIVEN BY ANOTHER PLUGIN (CHECKOUT.md; commerce unit 9.8).
//
// An agentic-commerce protocol adapter — ACP (OpenAI and Stripe), UCP (Google, Shopify and others) — speaks its
// protocol to the AI platform and drives the shop's own checkout through this contract: one checkout per basket,
// priced by the shop (delivery, tax, discounts) exactly as its checkout page prices it, then either completed with a
// payment credential the buyer's wallet delegated to the agent — only where the merchant lets that agent pay — or
// handed to a person at ContinueURL. The shop PROVIDES shop.checkout/v1; an adapter REQUIRES it. A protocol's new
// version is an adapter's release, never the shop's (owner decision D7, plan 12 §3b).

// ContractShopCheckout is the contract's name.
const ContractShopCheckout = "shop.checkout/v1"

// The contract's methods.
const (
	CheckoutDescribe        = "describe"
	CheckoutSessionCreate   = "session.create"
	CheckoutSessionUpdate   = "session.update"
	CheckoutSessionGet      = "session.get"
	CheckoutSessionComplete = "session.complete"
	CheckoutSessionCancel   = "session.cancel"
)

// A checkout's status, the names ACP and UCP both use.
const (
	// CheckoutIncomplete: something is still needed — Messages with severity error say what.
	CheckoutIncomplete = "incomplete"
	// CheckoutReadyForPayment: everything is known and this caller may complete it with a credential of one of
	// CredentialTypes.
	CheckoutReadyForPayment = "ready_for_payment"
	// CheckoutRequiresEscalation: everything is known, but a person finishes it — pays — at ContinueURL.
	CheckoutRequiresEscalation = "requires_escalation"
	CheckoutCompleted          = "completed"
	CheckoutCanceled           = "canceled"
)

// Why the shop refused a call (CheckoutRefusal.Code).
const (
	// CheckoutRefusedNotFound: no checkout has this token.
	CheckoutRefusedNotFound = "not_found"
	// CheckoutRefusedInvalid: a line, a quantity or a value the shop cannot take; nothing changed.
	CheckoutRefusedInvalid = "invalid"
	// CheckoutRefusedNotAllowed: completing with a credential, for an agent the merchant has not let pay.
	CheckoutRefusedNotAllowed = "not_allowed"
	// CheckoutRefusedClosed: the checkout is completed or canceled and changes no more.
	CheckoutRefusedClosed = "closed"
	// CheckoutRefusedNotCompleted: the order could not be placed — the payment was declined, the stock went; the
	// basket is as it was, and Message says why.
	CheckoutRefusedNotCompleted = "not_completed"
)

// CheckoutMaxLines is the most lines one basket holds through this contract.
const CheckoutMaxLines = 50

// CheckoutDescription is the shop as a protocol adapter announces it.
type CheckoutDescription struct {
	// Name is the shop's name (the seller); Origin the site's address, https://…
	Name   string `json:"name"`
	Origin string `json:"origin"`
	// Currency is the shop's currency (ISO 4217); Country its own (ISO 3166-1 alpha-2), "" when it has not said.
	Currency string `json:"currency"`
	Country  string `json:"country,omitempty"`
	Locale   string `json:"locale"`
	// TermsURL and PrivacyURL are the shop's legal pages, absolute; "" when it has none published.
	TermsURL   string `json:"terms_url,omitempty"`
	PrivacyURL string `json:"privacy_url,omitempty"`
	// CredentialTypes are the delegated payment credentials the shop's payment plugins can charge now
	// (CredentialStripeSPT, …); empty when none can — every checkout is then finished by a person.
	CredentialTypes []string `json:"credential_types"`
	// ReturnsURL is the shop's returns and withdrawal page, absolute; "" when it has none published. From v0.23.0.
	ReturnsURL string `json:"returns_url,omitempty"`
}

// CheckoutSeller is who sells: the trader's identity a consumer must be told before a distance contract binds them
// (Directive 2011/83/EU Art. 6(1)(b), (c)). From v0.23.0.
type CheckoutSeller struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
	VATID   string `json:"vat_id,omitempty"`
}

// CheckoutPreContract is what the buyer must be told before they are bound, for an agent buying for them to show:
// who sells, the right of withdrawal and how to use it, and where the terms, privacy notice and returns are (Directive
// 2011/83/EU Art. 6(1) and 8(2) — a consumer the trader did not inform is not bound, and a withdrawal period without
// its information runs twelve months longer, Art. 10(1)). OrderButtonText is the words the final step must carry
// ("order with obligation to pay", Art. 8(2)). From v0.23.0.
type CheckoutPreContract struct {
	Seller            CheckoutSeller `json:"seller"`
	WithdrawalDays    int            `json:"withdrawal_days"`
	WithdrawalText    string         `json:"withdrawal_text"`
	WithdrawalFormURL string         `json:"withdrawal_form_url,omitempty"`
	TermsURL          string         `json:"terms_url,omitempty"`
	PrivacyURL        string         `json:"privacy_url,omitempty"`
	ReturnsURL        string         `json:"returns_url,omitempty"`
	OrderButtonText   string         `json:"order_button_text"`
}

// Validate refuses a description an adapter could not announce.
func (d CheckoutDescription) Validate() error {
	if strings.TrimSpace(d.Name) == "" || !strings.HasPrefix(d.Origin, "https://") && !strings.HasPrefix(d.Origin, "http://") {
		return errors.New("nilda: a checkout description needs the shop's name and the site's address")
	}
	return validCurrency("the checkout's currency", d.Currency)
}

// CheckoutLineRequest is one line of a basket: a variant's SKU and how many.
type CheckoutLineRequest struct {
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
}

// CheckoutBuyer is who buys: the email the order's mails go to, a name, a phone.
type CheckoutBuyer struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	Phone string `json:"phone,omitempty"`
}

// CheckoutAddress is where the parcel goes.
type CheckoutAddress struct {
	Name       string `json:"name,omitempty"`
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city,omitempty"`
	Region     string `json:"region,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	// Country is ISO 3166-1 alpha-2.
	Country string `json:"country,omitempty"`
}

// CheckoutCreateRequest opens a checkout for a new basket.
type CheckoutCreateRequest struct {
	Lines        []CheckoutLineRequest `json:"lines"`
	Buyer        *CheckoutBuyer        `json:"buyer,omitempty"`
	Address      *CheckoutAddress      `json:"address,omitempty"`
	DiscountCode string                `json:"discount_code,omitempty"`
	// Agent is the AI agent the adapter serves, as the adapter knows it (its API key, a verified signature): its
	// operator is recorded on the order ("sold by agents"), and only AgentBuy may complete with a credential.
	Agent *Agent `json:"agent,omitempty"`
}

// Validate refuses a request the shop cannot answer.
func (r CheckoutCreateRequest) Validate() error {
	return validCheckoutLines(r.Lines)
}

// CheckoutUpdateRequest changes a checkout; what it leaves out stays as it was.
type CheckoutUpdateRequest struct {
	Token string `json:"token"`
	// Lines, when sent, is the whole basket from now on.
	Lines   []CheckoutLineRequest `json:"lines,omitempty"`
	Buyer   *CheckoutBuyer        `json:"buyer,omitempty"`
	Address *CheckoutAddress      `json:"address,omitempty"`
	// DeliveryOptionID is one of the state's DeliveryOptions.
	DeliveryOptionID string `json:"delivery_option_id,omitempty"`
	// DiscountCode, when sent, replaces the code; "" removes it.
	DiscountCode *string `json:"discount_code,omitempty"`
	Agent        *Agent  `json:"agent,omitempty"`
}

// Validate refuses a request the shop cannot answer.
func (r CheckoutUpdateRequest) Validate() error {
	if err := validCheckoutToken(r.Token); err != nil {
		return err
	}
	if r.Lines != nil {
		return validCheckoutLines(r.Lines)
	}
	return nil
}

// CheckoutRef names one checkout, for session.get and session.cancel.
type CheckoutRef struct {
	Token string `json:"token"`
	Agent *Agent `json:"agent,omitempty"`
}

// Validate refuses a request with no token.
func (r CheckoutRef) Validate() error { return validCheckoutToken(r.Token) }

// CheckoutCompleteRequest places the order.
type CheckoutCompleteRequest struct {
	Token string         `json:"token"`
	Buyer *CheckoutBuyer `json:"buyer,omitempty"`
	// TermsAccepted says the buyer accepts the shop's terms (TermsURL); the shop refuses without it.
	TermsAccepted bool `json:"terms_accepted"`
	// DigitalConsent, for a download: the buyer wants it at once and gives up withdrawing (EU). It counts only with
	// DigitalConsentText — the state's sentence, word for word, which the agent showed its buyer and they agreed to
	// (Directive 2011/83/EU Art. 16(m): their prior express consent and acknowledgement); the shop keeps it as proof.
	DigitalConsent     bool   `json:"digital_consent,omitempty"`
	DigitalConsentText string `json:"digital_consent_text,omitempty"`
	// Credential is the delegated payment credential the shop charges. Never logged, never kept: it passes to the
	// payment plugin and Core's payments, which keep nothing of it either.
	Credential *PaymentCredential `json:"credential,omitempty"`
	Agent      *Agent             `json:"agent,omitempty"`
}

// Validate refuses a request the shop cannot answer.
func (r CheckoutCompleteRequest) Validate() error {
	if err := validCheckoutToken(r.Token); err != nil {
		return err
	}
	if r.Credential != nil {
		return r.Credential.Validate()
	}
	return nil
}

// CheckoutResult is the shop's answer: the checkout as it now stands, or why it refused.
type CheckoutResult struct {
	State   *CheckoutState   `json:"state,omitempty"`
	Refused *CheckoutRefusal `json:"refused,omitempty"`
}

// Validate checks that exactly one of the two is there, and the state.
func (r CheckoutResult) Validate() error {
	if (r.State == nil) == (r.Refused == nil) {
		return errors.New("nilda: a checkout answer is a state or a refusal, exactly one")
	}
	if r.Refused != nil {
		switch r.Refused.Code {
		case CheckoutRefusedNotFound, CheckoutRefusedInvalid, CheckoutRefusedNotAllowed, CheckoutRefusedClosed,
			CheckoutRefusedNotCompleted:
		default:
			return fmt.Errorf("nilda: a checkout refusal with code %q", r.Refused.Code)
		}
		return nil
	}
	return r.State.Validate()
}

// CheckoutRefusal is why the shop did not do what it was asked; Message is for the agent to read.
type CheckoutRefusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CheckoutState is a checkout as it stands.
type CheckoutState struct {
	// Token is the checkout's handle: the protocols' checkout session id. A bearer value — whoever holds it reaches
	// the basket — so an adapter gives it only to the agent that made it.
	Token    string         `json:"token"`
	Status   string         `json:"status"`
	Currency string         `json:"currency"`
	Lines    []CheckoutLine `json:"lines"`
	Totals   CheckoutTotals `json:"totals"`
	// RequiresShipping is false for a basket of downloads and services: no address, no delivery option.
	RequiresShipping bool `json:"requires_shipping"`
	// DeliveryOptions are what this basket at this address may be sent by; DeliveryOptionID the one chosen.
	DeliveryOptions  []CheckoutDeliveryOption `json:"delivery_options,omitempty"`
	DeliveryOptionID string                   `json:"delivery_option_id,omitempty"`
	Buyer            *CheckoutBuyer           `json:"buyer,omitempty"`
	Address          *CheckoutAddress         `json:"address,omitempty"`
	// Messages are what the shop has to say: severity error blocks completing, warning and info do not.
	Messages []CheckoutMessage `json:"messages,omitempty"`
	// ContinueURL is where a person finishes this checkout on the shop's own page, with everything the agent filled
	// in; "" once it is completed or canceled.
	ContinueURL string `json:"continue_url,omitempty"`
	// CredentialTypes are the credentials session.complete charges for THIS caller now; empty when it may not pay
	// (the status is then requires_escalation once nothing is missing).
	CredentialTypes []string `json:"credential_types,omitempty"`
	TermsURL        string   `json:"terms_url,omitempty"`
	// PreContract is what the buyer must be told before they are bound — an agent completing for them shows it
	// first. From v0.23.0.
	PreContract *CheckoutPreContract `json:"pre_contract,omitempty"`
	// DigitalConsentText is, when the basket holds a download, the sentence the buyer must agree to before it is
	// delivered at once (their right of withdrawal then ends); complete sends it back with DigitalConsent. "" for a
	// basket with none. From v0.23.0.
	DigitalConsentText string `json:"digital_consent_text,omitempty"`
	// Order is the order it became, once completed.
	Order     *CheckoutOrder `json:"order,omitempty"`
	ExpiresAt *time.Time     `json:"expires_at,omitempty"`
}

// Validate refuses a state an adapter could not report.
func (s CheckoutState) Validate() error {
	if strings.TrimSpace(s.Token) == "" {
		return errors.New("nilda: a checkout state carries its token")
	}
	switch s.Status {
	case CheckoutIncomplete, CheckoutReadyForPayment, CheckoutRequiresEscalation, CheckoutCompleted, CheckoutCanceled:
	default:
		return fmt.Errorf("nilda: a checkout status %q", s.Status)
	}
	if s.Status == CheckoutCompleted && s.Order == nil {
		return errors.New("nilda: a completed checkout names its order")
	}
	if s.Status == CheckoutReadyForPayment && len(s.CredentialTypes) == 0 {
		return errors.New("nilda: a checkout ready for payment says which credentials it takes")
	}
	if s.Status == CheckoutRequiresEscalation && s.ContinueURL == "" {
		return errors.New("nilda: a checkout a person must finish carries the link to finish it")
	}
	if err := validCurrency("the checkout's currency", s.Currency); err != nil {
		return err
	}
	for _, m := range s.Messages {
		switch m.Severity {
		case "error", "warning", "info":
		default:
			return fmt.Errorf("nilda: a checkout message of severity %q", m.Severity)
		}
	}
	return nil
}

// Blocked reports whether a message stops the checkout completing.
func (s CheckoutState) Blocked() bool {
	for _, m := range s.Messages {
		if m.Severity == "error" {
			return true
		}
	}
	return false
}

// CheckoutLine is one line as the shop priced it.
type CheckoutLine struct {
	SKU      string `json:"sku"`
	Title    string `json:"title"`
	Quantity int64  `json:"quantity"`
	// UnitPriceMinor is one item's price now, in minor units of the checkout's currency; TotalMinor the line's.
	UnitPriceMinor int64  `json:"unit_price_minor"`
	TotalMinor     int64  `json:"total_minor"`
	URL            string `json:"url,omitempty"`
	ImageURL       string `json:"image_url,omitempty"`
}

// CheckoutTotals are the checkout's sums, in minor units. TaxIncluded: TaxMinor is inside TotalMinor, as the shop's
// prices include it; otherwise it is added.
type CheckoutTotals struct {
	SubtotalMinor int64 `json:"subtotal_minor"`
	DiscountMinor int64 `json:"discount_minor"`
	ShippingMinor int64 `json:"shipping_minor"`
	TaxMinor      int64 `json:"tax_minor"`
	TotalMinor    int64 `json:"total_minor"`
	TaxIncluded   bool  `json:"tax_included,omitempty"`
}

// CheckoutDeliveryOption is one way the basket may be sent.
type CheckoutDeliveryOption struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Kind is the shop's: flat_rate, free_shipping, local_pickup, carrier, …
	Kind       string `json:"kind,omitempty"`
	PriceMinor int64  `json:"price_minor"`
}

// CheckoutMessage is one thing the shop says about the checkout. Field names what it is about ("address",
// "email", "delivery", "cart", "discount", …).
type CheckoutMessage struct {
	Severity string `json:"severity"`
	Field    string `json:"field,omitempty"`
	Text     string `json:"text"`
}

// CheckoutOrder is the order a checkout became.
type CheckoutOrder struct {
	// ID is the order's public reference, as its mails print it.
	ID string `json:"id"`
	// Status is the shop's order status; Paid whether the money is in.
	Status string `json:"status"`
	Paid   bool   `json:"paid"`
	// URL is the order's page for the buyer.
	URL string `json:"url,omitempty"`
}

func validCheckoutToken(token string) error {
	if t := strings.TrimSpace(token); t == "" || len(t) > 128 {
		return errors.New("nilda: a checkout is named by its token")
	}
	return nil
}

func validCheckoutLines(lines []CheckoutLineRequest) error {
	if len(lines) == 0 || len(lines) > CheckoutMaxLines {
		return fmt.Errorf("nilda: a basket holds 1 to %d lines", CheckoutMaxLines)
	}
	for _, l := range lines {
		if strings.TrimSpace(l.SKU) == "" || l.Quantity < 1 {
			return errors.New("nilda: every basket line names a SKU and a quantity of at least 1")
		}
	}
	return nil
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// CheckoutProvider is the shop. Serve finds it by itself; declare "provides": ["shop.checkout/v1"] with
// service_provider.
type CheckoutProvider interface {
	DescribeCheckout(ctx context.Context) (CheckoutDescription, error)
	CreateCheckout(ctx context.Context, req CheckoutCreateRequest) (CheckoutResult, error)
	UpdateCheckout(ctx context.Context, req CheckoutUpdateRequest) (CheckoutResult, error)
	GetCheckout(ctx context.Context, req CheckoutRef) (CheckoutResult, error)
	CompleteCheckout(ctx context.Context, req CheckoutCompleteRequest) (CheckoutResult, error)
	CancelCheckout(ctx context.Context, req CheckoutRef) (CheckoutResult, error)
}

// CheckoutMethods are the shop's contract methods, each checking the request before the shop sees it and the answer
// before it leaves. A request the checks refuse is answered as a refusal, not an error: the adapter tells its agent
// what was wrong.
func CheckoutMethods(c CheckoutProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopCheckout, CheckoutDescribe, func(ctx context.Context, _ struct{}) (CheckoutDescription, error) {
			return checked(c.DescribeCheckout(ctx))
		}),
		Serves(ContractShopCheckout, CheckoutSessionCreate, func(ctx context.Context, req CheckoutCreateRequest) (CheckoutResult, error) {
			return checkedCheckout(req.Validate(), func() (CheckoutResult, error) { return c.CreateCheckout(ctx, req) })
		}),
		Serves(ContractShopCheckout, CheckoutSessionUpdate, func(ctx context.Context, req CheckoutUpdateRequest) (CheckoutResult, error) {
			return checkedCheckout(req.Validate(), func() (CheckoutResult, error) { return c.UpdateCheckout(ctx, req) })
		}),
		Serves(ContractShopCheckout, CheckoutSessionGet, func(ctx context.Context, req CheckoutRef) (CheckoutResult, error) {
			return checkedCheckout(req.Validate(), func() (CheckoutResult, error) { return c.GetCheckout(ctx, req) })
		}),
		Serves(ContractShopCheckout, CheckoutSessionComplete, func(ctx context.Context, req CheckoutCompleteRequest) (CheckoutResult, error) {
			return checkedCheckout(req.Validate(), func() (CheckoutResult, error) { return c.CompleteCheckout(ctx, req) })
		}),
		Serves(ContractShopCheckout, CheckoutSessionCancel, func(ctx context.Context, req CheckoutRef) (CheckoutResult, error) {
			return checkedCheckout(req.Validate(), func() (CheckoutResult, error) { return c.CancelCheckout(ctx, req) })
		}),
	}
}

// checkedCheckout answers a refused request as a refusal and checks the shop's answer.
func checkedCheckout(invalid error, run func() (CheckoutResult, error)) (CheckoutResult, error) {
	if invalid != nil {
		return CheckoutResult{Refused: &CheckoutRefusal{Code: CheckoutRefusedInvalid,
			Message: strings.TrimPrefix(invalid.Error(), "nilda: ")}}, nil
	}
	return checked(run())
}

// ---- an adapter's side -------------------------------------------------------------------------------------

// checkoutAnswer asks the shop one checkout question: one shop, so the first answer is the answer and none is an
// error.
func (s *Services) checkoutAnswer(ctx context.Context, method string, req any, key string, into validator) error {
	res, err := s.Call(ctx, ContractShopCheckout, method, req, ServiceCallOptions{IdempotencyKey: key})
	if err != nil {
		return err
	}
	if len(res.Answers) == 0 {
		return fmt.Errorf("nilda: no shop answered %s %s", ContractShopCheckout, method)
	}
	if err := res.Answers[0].Decode(into); err != nil {
		return err
	}
	return into.Validate()
}

// CheckoutDescription reads the shop as an adapter announces it.
func (s *Services) CheckoutDescription(ctx context.Context) (CheckoutDescription, error) {
	var d CheckoutDescription
	return d, s.checkoutAnswer(ctx, CheckoutDescribe, struct{}{}, "", &d)
}

// CreateCheckout opens a checkout. idempotencyKey (the protocol request's own) makes a repeat one checkout; "" for
// none.
func (s *Services) CreateCheckout(ctx context.Context, req CheckoutCreateRequest, idempotencyKey string) (CheckoutResult, error) {
	var r CheckoutResult
	return r, s.checkoutAnswer(ctx, CheckoutSessionCreate, req, idempotencyKey, &r)
}

// UpdateCheckout changes a checkout.
func (s *Services) UpdateCheckout(ctx context.Context, req CheckoutUpdateRequest, idempotencyKey string) (CheckoutResult, error) {
	var r CheckoutResult
	return r, s.checkoutAnswer(ctx, CheckoutSessionUpdate, req, idempotencyKey, &r)
}

// GetCheckout reads a checkout as it stands.
func (s *Services) GetCheckout(ctx context.Context, req CheckoutRef) (CheckoutResult, error) {
	var r CheckoutResult
	return r, s.checkoutAnswer(ctx, CheckoutSessionGet, req, "", &r)
}

// CompleteCheckout places the order; a repeat with the same key is the same order.
func (s *Services) CompleteCheckout(ctx context.Context, req CheckoutCompleteRequest, idempotencyKey string) (CheckoutResult, error) {
	var r CheckoutResult
	return r, s.checkoutAnswer(ctx, CheckoutSessionComplete, req, idempotencyKey, &r)
}

// CancelCheckout ends a checkout without an order.
func (s *Services) CancelCheckout(ctx context.Context, req CheckoutRef, idempotencyKey string) (CheckoutResult, error) {
	var r CheckoutResult
	return r, s.checkoutAnswer(ctx, CheckoutSessionCancel, req, idempotencyKey, &r)
}
