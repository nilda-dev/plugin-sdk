package nilda

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// A PAYMENT METHOD KEPT FOR LATER (v0.30.0): a subscription's next charge, a pre-order charged when it ships, a card
// the payer updates. The market's way (Shopify's customer payment methods, WooCommerce's tokens, Stripe's
// setup_future_usage=off_session, PayPal's vault): the PAYER is present once — at a session that carries Save, the
// MANDATE they accepted, and where their bank asks for strong customer authentication — and every later charge is
// merchant-initiated, with nobody at the processor's page.
//
// Three parties, as for every payment, and the token never reaches the consumer:
//
//	CONSUMER  sends a session with Save (PaymentSessionParams.Save); hears the session resolved with SavedMethod, the
//	          id Core gave the method; later charges it (Payments.ChargeSaved) and forgets it (ForgetSavedMethod).
//	CORE      keeps the gateway's token for the method, encrypted, with the mandate and the session it came from;
//	          hands the token to the gateway for each charge; forgets it when asked or when the payer is erased.
//	GATEWAY   at a Save session, asks its processor to keep the method; reports it with the resolution
//	          (PaymentResolution.Saved); charges it when Core hands it over (SavedMethodPayer); lets go of it at
//	          the processor when Core forgets it.
//
// A method offers this only when it lists SupportSaveForLater: a consumer offers a subscription, or a pre-order
// paid at shipment, only through such a method — never a promise its gateway cannot keep.

// SupportSaveForLater: the method can keep the payer's method for charges the payer is not present for.
const SupportSaveForLater = "save_for_later"

// PaymentFailAuthenticationRequired — a gateway's code for a charge of a saved method that the payer's bank will take
// only with the payer present (strong customer authentication). Not a decline: the consumer sends the payer a link
// to pay on-session, and keeps the method.
const PaymentFailAuthenticationRequired = "authentication_required"

// HookPaymentForget asks a GATEWAY that implements SavedMethodPayer to let go of a kept method at its processor:
// PaymentForgetRequest in, {} out.
const HookPaymentForget = "payment.saved.forget"

// What a mandate is for: PaymentMandate.Purpose.
const (
	MandateSubscription = "subscription" // charges every Interval
	MandatePreOrder     = "pre_order"    // one charge, when the goods ship
	MandateUpdate       = "update"       // the payer replaces the method a mandate already covers
)

// Mandate intervals: PaymentMandate.Interval.
const (
	IntervalDay   = "day"
	IntervalWeek  = "week"
	IntervalMonth = "month"
	IntervalYear  = "year"
)

const (
	maxMandateText  = 4000 // the agreement as the payer read it
	maxSavedToken   = 1024 // a processor's token for a kept method
	maxSavedLabel   = 100  // "Visa •••• 4242, 12/28"
	maxIntervalStep = 365  // every 1 to 365 intervals
)

// PaymentMandate is what the payer agreed to when their method was kept — the record the card schemes and PSD2 ask a
// merchant to keep (consent to later charges, their amount or how it is set, the currency, the timing and frequency).
// Core stores it with the method, unchanged, for the method's life.
type PaymentMandate struct {
	// Purpose is MandateSubscription, MandatePreOrder or MandateUpdate.
	Purpose string `json:"purpose"`
	// Text is the agreement exactly as the payer read it before they accepted: at most 4000 characters.
	Text string `json:"text"`
	// AmountMinor is each later charge, in Currency; 0 when it varies — then AmountRule says how it is set.
	AmountMinor int64  `json:"amount_minor,omitempty"`
	AmountRule  string `json:"amount_rule,omitempty"`
	Currency    string `json:"currency"`
	// Interval and IntervalCount are a subscription's rhythm (every IntervalCount Intervals); empty for a pre-order's
	// one later charge.
	Interval      string `json:"interval,omitempty"`
	IntervalCount int    `json:"interval_count,omitempty"`
}

// Validate refuses a mandate that does not record what the payer agreed to.
func (m PaymentMandate) Validate() error {
	switch m.Purpose {
	case MandateSubscription, MandatePreOrder, MandateUpdate:
	default:
		return fmt.Errorf("nilda: a mandate's purpose is %s, %s or %s", MandateSubscription, MandatePreOrder, MandateUpdate)
	}
	if strings.TrimSpace(m.Text) == "" || utf8.RuneCountInString(m.Text) > maxMandateText {
		return fmt.Errorf("nilda: a mandate carries the agreement the payer read, 1 to %d characters", maxMandateText)
	}
	if _, ok := MinorUnits(m.Currency); !ok {
		return fmt.Errorf("nilda: %q is not a currency ISO 4217 gives a minor unit to", m.Currency)
	}
	if m.AmountMinor < 0 {
		return fmt.Errorf("nilda: a mandate's amount cannot be negative")
	}
	if m.AmountMinor == 0 && strings.TrimSpace(m.AmountRule) == "" {
		return fmt.Errorf("nilda: a mandate names each charge's amount, or the rule that sets it")
	}
	if utf8.RuneCountInString(m.AmountRule) > maxPaymentText {
		return fmt.Errorf("nilda: a mandate's amount rule is at most %d characters", maxPaymentText)
	}
	switch m.Interval {
	case "":
		if m.Purpose == MandateSubscription {
			return fmt.Errorf("nilda: a subscription's mandate names its interval")
		}
		if m.IntervalCount != 0 {
			return fmt.Errorf("nilda: a mandate with no interval has no interval count")
		}
	case IntervalDay, IntervalWeek, IntervalMonth, IntervalYear:
		if m.IntervalCount < 1 || m.IntervalCount > maxIntervalStep {
			return fmt.Errorf("nilda: a mandate's interval count is 1 to %d", maxIntervalStep)
		}
	default:
		return fmt.Errorf("nilda: a mandate's interval is %s, %s, %s or %s", IntervalDay, IntervalWeek, IntervalMonth, IntervalYear)
	}
	return nil
}

// PaymentSaved is what a GATEWAY reports, with the resolution of a Save session, about the method its processor
// kept (PaymentResolution.Saved).
type PaymentSaved struct {
	// Token is what the gateway needs to charge the method again — its processor's ids ("cus_…/pm_…", a vault id).
	// Core keeps it encrypted, hands it back only to this gateway, and never shows it to anyone.
	Token string `json:"token"`
	// Label is how a person recognises the method: "Visa •••• 4242, 12/28", "PayPal (a…@example.com)". Never a full
	// card number.
	Label string `json:"label"`
	// ExpiresAt is when the method stops working, if the processor says (a card's expiry).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Validate refuses a kept method with no token or past its bounds.
func (s PaymentSaved) Validate() error {
	if strings.TrimSpace(s.Token) == "" || len(s.Token) > maxSavedToken {
		return fmt.Errorf("nilda: a kept method carries its processor's token, 1 to %d bytes", maxSavedToken)
	}
	if strings.TrimSpace(s.Label) == "" || utf8.RuneCountInString(s.Label) > maxSavedLabel {
		return fmt.Errorf("nilda: a kept method has a label a person recognises, 1 to %d characters", maxSavedLabel)
	}
	return nil
}

// String never shows the token.
func (s PaymentSaved) String() string { return s.Label + " [token redacted]" }

// GoString likewise, for %#v.
func (s PaymentSaved) GoString() string { return s.String() }

// SavedPaymentMethod is a kept method, as Core holds it and a CONSUMER reads it (Payments.GetSavedMethod). The token
// is never part of it.
type SavedPaymentMethod struct {
	ID       string `json:"id"`
	Consumer string `json:"consumer"`
	Gateway  string `json:"gateway"`
	Method   string `json:"method"`
	// Label is the gateway's PaymentSaved.Label.
	Label string `json:"label"`
	// Email is the payer's, from the session the method was kept at.
	Email string `json:"email,omitempty"`
	// Mandate is what the payer agreed to; Session the session it was kept at.
	Mandate PaymentMandate `json:"mandate"`
	Session string         `json:"session"`
	// Test is true for a method kept under the processor's test credentials.
	Test      bool       `json:"test,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// ForgottenAt is set once the method was forgotten: it charges nothing again.
	ForgottenAt *time.Time `json:"forgotten_at,omitempty"`
}

// PaymentChargeParams is what a CONSUMER sends to charge a kept method (Payments.ChargeSaved): the payer is not
// there, so there are no return addresses and no page.
type PaymentChargeParams struct {
	// SavedMethod is the id Core gave the method (PaymentSession.SavedMethod).
	SavedMethod string `json:"saved_method"`
	// Reference, AmountMinor, Currency and Description as for any session.
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Description string `json:"description,omitempty"`
	// Fulfilment and ShippingAddress, if you know them, for the processor's fraud checks.
	Fulfilment      string          `json:"fulfilment,omitempty"`
	ShippingAddress *PaymentAddress `json:"shipping_address,omitempty"`
}

// Validate refuses a charge Core would refuse without looking anything up.
func (p PaymentChargeParams) Validate() error {
	switch {
	case strings.TrimSpace(p.SavedMethod) == "":
		return fmt.Errorf("nilda: a charge names the kept method (PaymentSession.SavedMethod)")
	case strings.TrimSpace(p.Reference) == "":
		return fmt.Errorf("nilda: a payment needs a reference — your own id for what is being paid for")
	case utf8.RuneCountInString(p.Reference) > maxPaymentReference:
		return fmt.Errorf("nilda: a payment reference is at most %d characters", maxPaymentReference)
	case utf8.RuneCountInString(p.Description) > maxPaymentText:
		return fmt.Errorf("nilda: a payment description is at most %d characters", maxPaymentText)
	}
	if err := validAmount(p.AmountMinor, p.Currency); err != nil {
		return err
	}
	if err := validFulfilment(p.Fulfilment); err != nil {
		return err
	}
	if p.ShippingAddress != nil {
		return p.ShippingAddress.Validate()
	}
	return nil
}

// SavedMethodPayer is a gateway that keeps methods for later (its methods list SupportSaveForLater). At a session
// with Save it asks the processor to keep the method — the payer is there, and their bank authenticates them — and
// reports it with the resolution (PaymentResolution.Saved). Then:
//
//   - StartPaymentWithSaved charges a kept method with the payer absent: a merchant-initiated charge. Answer resolved
//     when the processor took the money, pending when it is deciding, rejected when it will not —
//     PaymentFailAuthenticationRequired when the bank wants the payer present, declined for a card that is refused.
//     There is no page to redirect to. Never store, log or echo the token.
//   - ForgetSaved lets go of the method at the processor (Stripe: detach; PayPal: delete the vault token). A token
//     the processor no longer knows is forgotten already: answer nil.
type SavedMethodPayer interface {
	StartPaymentWithSaved(ctx context.Context, s PaymentSession, token string) (PaymentStartResult, error)
	ForgetSaved(ctx context.Context, token string) error
}

// PaymentSavedToken carries a kept method's token to its gateway, inside PaymentStartRequest, for one charge.
type PaymentSavedToken struct {
	Token string `json:"token"`
}

// String never shows the token.
func (t PaymentSavedToken) String() string { return "[token redacted]" }

// GoString likewise, for %#v.
func (t PaymentSavedToken) GoString() string { return t.String() }

// PaymentForgetRequest is what HookPaymentForget carries.
type PaymentForgetRequest struct {
	Token string `json:"token"`
}

// String never shows the token.
func (r PaymentForgetRequest) String() string { return "[token redacted]" }

// GoString likewise, for %#v.
func (r PaymentForgetRequest) GoString() string { return r.String() }

// ---- the consumer's calls -------------------------------------------------------------------------------------

// ChargeSaved charges a kept method with the payer absent. Core checks the method is this consumer's, not forgotten,
// and in the mandate's currency, creates a session and hands the token to its gateway; the answer is the session as
// the gateway left it — resolved, pending or rejected (PaymentFailAuthenticationRequired: send the payer a link to
// pay on-session). The outcome reaches PaymentUpdated as for any session.
//
// idempotencyKey names THIS CHARGE — "renewal-<contract>-<cycle>" — so a retry after a dropped connection charges
// once. A retry after a decline is a new attempt with a new key.
func (p *Payments) ChargeSaved(ctx context.Context, idempotencyKey string, params PaymentChargeParams) (PaymentSession, error) {
	if err := validIdempotencyKey(idempotencyKey); err != nil {
		return PaymentSession{}, err
	}
	if err := params.Validate(); err != nil {
		return PaymentSession{}, err
	}
	params.Currency = strings.ToUpper(strings.TrimSpace(params.Currency))
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err := p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, "/payments/charges", params, &out)
	return out.Data, err
}

// GetSavedMethod reads a kept method — only the consumer whose session kept it.
func (p *Payments) GetSavedMethod(ctx context.Context, id string) (SavedPaymentMethod, error) {
	path, err := paymentPath("/payments/saved-methods/", id, "")
	if err != nil {
		return SavedPaymentMethod{}, err
	}
	var out struct {
		Data SavedPaymentMethod `json:"data"`
	}
	err = p.api.Get(ctx, path, nil, &out)
	return out.Data, err
}

// ForgetSavedMethod forgets a kept method: Core asks its gateway to let go of it at the processor, and drops the
// token. It charges nothing again; forgetting it twice is no error. Forget a method when its last subscription ends
// and its pre-order is paid — keeping a payer's card longer than needed is keeping personal data without a reason.
func (p *Payments) ForgetSavedMethod(ctx context.Context, id string) (SavedPaymentMethod, error) {
	path, err := paymentPath("/payments/saved-methods/", id, "/forget")
	if err != nil {
		return SavedPaymentMethod{}, err
	}
	var out struct {
		Data SavedPaymentMethod `json:"data"`
	}
	err = p.api.Post(ctx, path, struct{}{}, &out)
	return out.Data, err
}
