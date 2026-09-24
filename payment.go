package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// TAKING MONEY: CORE OWNS THE CONTRACT, AND EVERY GATEWAY IS ITS OWN PLUGIN (Nilda's D-84, 2026-09-23).
//
// Three parties, and none of them talks to another directly:
//
//	CONSUMER  capability payment_session — whatever is being paid for: a shop's order, a booking's deposit,
//	          a donation. It asks Core for a payment and is told how it ended.
//	CORE      owns every payment SESSION and REFUND, sends each to the gateway the payer chose, holds the
//	          one state machine below, and tells the consumer the outcome until the consumer has heard it.
//	GATEWAY   capability payment_gateway — one processor: Stripe, PayPal, a bank. It starts a payment at the
//	          processor and reports what the processor says. It never learns what was bought, and it never
//	          talks to the consumer.
//
// STARTING IS SHORT AND THE OUTCOME COMES LATER, as it does at every processor (Stripe Checkout, PayPal
// Orders, Mollie) and every platform built on them (Shopify's payments apps, Saleor, Medusa). Core asks the
// gateway to start (HookPaymentStart) and the gateway answers with the processor's page to send the payer
// to. The verdict arrives afterwards — from the processor's webhook to the gateway's own route, and from the
// gateway to Core (Payments.Resolve, Reject, MarkPending). Core then tells the consumer
// (PaymentConsumer.PaymentUpdated), again and again until it answers, because "the customer paid" is the one
// message a shop may never miss.
//
// Amounts are MINOR UNITS of an ISO 4217 currency (currency.go): an int64, never a float or a formatted
// string. docs/PAYMENTS.md is the whole contract, for both sides.

// The hooks Core sends. The first three go to a GATEWAY, the last three to a CONSUMER. Serve subscribes and
// answers them for a plugin implementing PaymentGateway or PaymentConsumer — nothing to list, nothing to route.
const (
	// HookPaymentDescribe asks a gateway which methods it offers. Answer: PaymentDescribeResponse.
	HookPaymentDescribe = "payment.describe"
	// HookPaymentStart asks a gateway to begin one session at its processor: PaymentStartRequest in,
	// PaymentStartResult out.
	HookPaymentStart = "payment.start"
	// HookPaymentRefund asks a gateway to give money back: PaymentRefundRequest in, PaymentRefundResult out.
	HookPaymentRefund = "payment.refund"
	// HookPaymentConfirm asks a CONSUMER whether a payment may still go through, the last check before a
	// gateway takes the money (Payments.Confirm): PaymentConfirmRequest in, PaymentConfirmResult out.
	HookPaymentConfirm = "payment.session.confirm"
	// HookPaymentSessionUpdated tells a consumer that a session it created changed: PaymentSessionUpdate.
	HookPaymentSessionUpdated = "payment.session.updated"
	// HookPaymentRefundUpdated tells a consumer that a refund it asked for changed: PaymentRefundUpdate.
	HookPaymentRefundUpdated = "payment.refund.updated"
)

// A session's status, a closed set. A consumer branches on it, and a status nobody listed would land in
// whatever its default branch happens to do.
const (
	// PaymentCreated — Core holds the session and the gateway has not answered yet.
	PaymentCreated = "created"
	// PaymentRedirected — the payer was sent to the processor's page and nothing is decided.
	PaymentRedirected = "redirected"
	// PaymentPending — the processor has it and has not decided: a bank transfer on its way, a payment its
	// risk team is reviewing. Also a session Core holds for a PERSON (FailureCode amount_mismatch). Not a
	// failure: cancelling the order behind a pending payment cancels a live payment.
	PaymentPending = "pending"
	// PaymentResolved — the money is there.
	PaymentResolved = "resolved"
	// PaymentRejected — it ended without money; FailureCode says how.
	PaymentRejected = "rejected"
	// PaymentExpired — nothing decided it before ExpiresAt, and Core closed it. It can still become PENDING
	// or RESOLVED: a processor reporting a payment after the expiry is heard, because money that moved is
	// never thrown away. The consumer is told, and decides what a late payment means for its order.
	PaymentExpired = "expired"
)

// A refund's status.
const (
	RefundRequested = "requested" // Core holds it and the gateway has not answered yet
	RefundPending   = "pending"   // the processor has it and has not decided
	RefundResolved  = "resolved"  // the money went back
	RefundRejected  = "rejected"  // it did not; FailureCode says why
)

// Why a session or a refund ended without money — the FailureCode, a closed set. The first five are a
// GATEWAY's to report; the last two are Core's, and a gateway reporting one is refused.
const (
	// PaymentFailDeclined — the processor said no: a card declined, a bank refused.
	PaymentFailDeclined = "declined"
	// PaymentFailCancelled — the payer walked away. Not a problem to investigate.
	PaymentFailCancelled = "cancelled"
	// PaymentFailProviderUnavailable — the processor could not be reached, and nothing was charged. Answer
	// this rather than failing the hook: an error counts against the PLUGIN, and enough of them switch it off,
	// so a processor's bad afternoon would become a site with no checkout until somebody switches it back on.
	PaymentFailProviderUnavailable = "provider_unavailable"
	// PaymentFailInvalidRequest — the processor cannot take this payment: a currency the account does not
	// take, an amount under its minimum.
	PaymentFailInvalidRequest = "invalid_request"
	// PaymentFailExpired — the processor's page expired before the payer paid. (Core's own expiry sets the
	// status PaymentExpired instead.)
	PaymentFailExpired = "expired"
	// PaymentFailAmountMismatch — Core's: the processor reported a different amount or currency from the
	// session's. Core holds the session PENDING for a person rather than resolving it or throwing it away.
	PaymentFailAmountMismatch = "amount_mismatch"
	// PaymentFailConsumerRefused — Core's: the consumer's confirm step said no (sold out, price changed).
	PaymentFailConsumerRefused = "consumer_refused"
)

// gatewayFailureCodes are the codes a gateway may report; the other two are Core's own verdicts.
var gatewayFailureCodes = []string{PaymentFailDeclined, PaymentFailCancelled, PaymentFailProviderUnavailable,
	PaymentFailInvalidRequest, PaymentFailExpired}

// THE STATE MACHINE, one table. Core holds its own copy, and two tests hold the two equal — Core's
// TestTheMachineIsTheSDKs against this module, and TestCoresPaymentMachineIsTheSDKs here (payments_truth_test.go,
// which reads Core's machine.go) — so the fake an author tests against (nildatest.Payments) cannot move a
// session in a way Core would refuse.
//
// Moving to the status a session already has is not in the table: Core answers an identical report as a
// no-op — a processor delivers the same webhook twice as a matter of course — and a report that cannot move
// the session is refused with 409 Conflict.
var (
	paymentMoves = map[string][]string{
		PaymentCreated:    {PaymentRedirected, PaymentPending, PaymentResolved, PaymentRejected, PaymentExpired},
		PaymentRedirected: {PaymentPending, PaymentResolved, PaymentRejected, PaymentExpired},
		// Not to expired: the processor has a pending payment, so the processor decides it — or a person does.
		PaymentPending: {PaymentResolved, PaymentRejected},
		// A late payment is still money: heard, never dropped.
		PaymentExpired: {PaymentPending, PaymentResolved},
	}
	refundMoves = map[string][]string{
		RefundRequested: {RefundPending, RefundResolved, RefundRejected},
		RefundPending:   {RefundResolved, RefundRejected},
	}
)

// PaymentCanMove reports whether a session may go from one status to another.
func PaymentCanMove(from, to string) bool { return canMove(paymentMoves, from, to) }

// RefundCanMove reports whether a refund may go from one status to another.
func RefundCanMove(from, to string) bool { return canMove(refundMoves, from, to) }

func canMove(moves map[string][]string, from, to string) bool {
	for _, s := range moves[from] {
		if s == to {
			return true
		}
	}
	return false
}

// PaymentMethod is one way a gateway can be paid, as the gateway describes it.
type PaymentMethod struct {
	// Key names the method within its gateway: "card", "ideal", "paypal".
	Key string `json:"key"`
	// Label is what a payer reads, Description the line under it.
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Currencies it takes, as ISO 4217 codes. Empty means any: the honest answer for an account whose
	// currencies the gateway cannot list.
	Currencies []string `json:"currencies,omitempty"`
	// Refunds is whether money paid this way can be given back through Core. Core refuses a refund for a
	// method that says no, before any gateway is asked.
	Refunds bool `json:"refunds,omitempty"`
	// Test is true while the gateway holds its processor's TEST credentials: no real money moves. Core shows
	// it to the site owner and stamps it on every session, so a test order is never shipped as a paid one.
	Test bool `json:"test,omitempty"`
}

// PaymentOption is one method a payer can choose, as a CONSUMER sees it (Payments.Methods): the gateway's
// method, with the gateway it belongs to. Gateway and Method are what PaymentSessionParams names.
type PaymentOption struct {
	Gateway     string `json:"gateway"`
	Method      string `json:"method"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Refunds     bool   `json:"refunds,omitempty"`
	Test        bool   `json:"test,omitempty"`
}

// PaymentSession is one payment, as Core holds it. Core fills every field; the gateway and the consumer read.
type PaymentSession struct {
	ID       string `json:"id"`
	Consumer string `json:"consumer"` // the consumer plugin's key
	Gateway  string `json:"gateway"`  // the gateway plugin's key
	Method   string `json:"method"`
	// Reference is the CONSUMER'S id for what is being paid for — an order, a booking. Opaque to Core and to
	// the gateway; it is how the consumer knows what an outcome is about.
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	// Test is copied from the method at creation: this session moves no real money.
	Test bool `json:"test,omitempty"`
	// Description is what the payer sees on the processor's page and, where the processor allows, on their
	// statement.
	Description string `json:"description,omitempty"`
	// ReturnURL and CancelURL are where the payer comes back to: absolute, on this site. Core fills CancelURL
	// with ReturnURL when the consumer gave none, so a gateway always has both.
	ReturnURL string `json:"return_url"`
	CancelURL string `json:"cancel_url"`
	// RedirectURL is the processor's page, once the gateway has started the session.
	RedirectURL string `json:"redirect_url,omitempty"`
	// ProviderRef is the processor's own id for this payment — what a person searches for at the processor.
	ProviderRef string `json:"provider_ref,omitempty"`
	// FailureCode is set on a rejected session, and on a pending one Core is holding for a person.
	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
	// Email and Locale are the payer's, when the consumer knows them: most processors send their receipt to
	// the first and draw their page in the second.
	Email  string `json:"email,omitempty"`
	Locale string `json:"locale,omitempty"`
	// ExpiresAt is when Core closes a session nothing decided: what the gateway reported at start, or Core's
	// own default.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// RefundedMinor is the sum of the session's RESOLVED refunds.
	RefundedMinor int64     `json:"refunded_minor"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PaymentRefund is one refund, as Core holds it.
type PaymentRefund struct {
	ID             string    `json:"id"`
	Session        string    `json:"session"` // the session it gives money back from
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	Status         string    `json:"status"`
	Reason         string    `json:"reason,omitempty"`
	ProviderRef    string    `json:"provider_ref,omitempty"`
	FailureCode    string    `json:"failure_code,omitempty"`
	FailureMessage string    `json:"failure_message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// PaymentSessionParams is what a CONSUMER sends to begin a payment (Payments.CreateSession).
type PaymentSessionParams struct {
	// Gateway and Method are one PaymentOption the payer chose.
	Gateway string `json:"gateway"`
	Method  string `json:"method"`
	// Reference is your own id for what is being paid for: at most 200 characters, the bound Stripe puts on
	// its client_reference_id, so a gateway can hand it on unchanged.
	Reference   string `json:"reference"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// Description, at most 500 characters: what the payer sees at the processor.
	Description string `json:"description,omitempty"`
	// ReturnURL is required, CancelURL optional (Core uses ReturnURL for both): absolute URLs on THIS site.
	// Core refuses one on another host, because a payment flow that returns the payer anywhere is an open
	// redirect with a bank's page in the middle of it.
	ReturnURL string `json:"return_url"`
	CancelURL string `json:"cancel_url,omitempty"`
	// Email and Locale are the payer's, if you know them (an address, a BCP 47 tag like "de" or "pt-BR").
	Email  string `json:"email,omitempty"`
	Locale string `json:"locale,omitempty"`
}

// Validate refuses what Core refuses without asking anything: a missing gateway, method or reference, an
// amount that is not positive, a currency ISO 4217 gives no minor unit, a return address that is not an
// absolute http(s) URL, a malformed email or locale, and anything past its bound. What only Core can know —
// that the gateway offers the method in that currency, that the URLs are on this site — Core checks.
func (p PaymentSessionParams) Validate() error {
	switch {
	case strings.TrimSpace(p.Gateway) == "" || strings.TrimSpace(p.Method) == "":
		return fmt.Errorf("nilda: a payment names the gateway and the method the payer chose — see Payments.Methods")
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
	if err := validPayerURL("return_url", p.ReturnURL, true); err != nil {
		return err
	}
	if err := validPayerURL("cancel_url", p.CancelURL, false); err != nil {
		return err
	}
	if p.Email != "" {
		if a, err := mail.ParseAddress(p.Email); err != nil || a.Address != p.Email || len(p.Email) > maxEmail {
			return fmt.Errorf("nilda: %q is not a plain email address", p.Email)
		}
	}
	if p.Locale != "" && !validLocale(p.Locale) {
		return fmt.Errorf("nilda: %q is not a language tag (\"de\", \"pt-BR\")", p.Locale)
	}
	return nil
}

// PaymentRefundParams is what a CONSUMER sends to give money back (Payments.CreateRefund).
type PaymentRefundParams struct {
	// AmountMinor may be less than the session's: a partial refund. Core refuses a refund that would take the
	// session's refunds — resolved and still in flight — past what was paid.
	AmountMinor int64 `json:"amount_minor"`
	// Reason, at most 500 characters, is kept on the refund for the site owner.
	Reason string `json:"reason,omitempty"`
}

// Validate refuses a refund that is not positive or whose reason is past its bound.
func (p PaymentRefundParams) Validate() error {
	if p.AmountMinor <= 0 {
		return fmt.Errorf("nilda: a refund's amount_minor must be positive, got %d", p.AmountMinor)
	}
	if utf8.RuneCountInString(p.Reason) > maxPaymentText {
		return fmt.Errorf("nilda: a refund reason is at most %d characters", maxPaymentText)
	}
	return nil
}

// PaymentResolution is what a GATEWAY reports when the processor says the money is there (Payments.Resolve):
// the amount and currency the PROCESSOR says it took, not the session's copied back. Core compares them, and
// a difference holds the session pending for a person (PaymentFailAmountMismatch).
type PaymentResolution struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	ProviderRef string `json:"provider_ref,omitempty"`
}

// Validate refuses a resolution with no positive amount or no currency ISO 4217 knows.
func (r PaymentResolution) Validate() error {
	if err := validAmount(r.AmountMinor, r.Currency); err != nil {
		return err
	}
	return validProviderRef(r.ProviderRef)
}

// PaymentRejection is what a GATEWAY reports when a session or a refund ended without money
// (Payments.Reject, Payments.RejectRefund).
type PaymentRejection struct {
	// FailureCode is one of the gateway's five: declined, cancelled, provider_unavailable, invalid_request,
	// expired.
	FailureCode string `json:"failure_code"`
	// FailureMessage, at most 500 characters, is the processor's reason as a person can read it. It reaches
	// the site owner and the consumer; say "card declined", never a card number.
	FailureMessage string `json:"failure_message,omitempty"`
	ProviderRef    string `json:"provider_ref,omitempty"`
}

// Validate refuses a code outside the gateway's five and a message past its bound.
func (r PaymentRejection) Validate() error {
	if err := validGatewayFailure(r.FailureCode); err != nil {
		return err
	}
	if utf8.RuneCountInString(r.FailureMessage) > maxPaymentText {
		return fmt.Errorf("nilda: a failure message is at most %d characters", maxPaymentText)
	}
	return validProviderRef(r.ProviderRef)
}

// PaymentDescribeResponse answers HookPaymentDescribe.
type PaymentDescribeResponse struct {
	Methods []PaymentMethod `json:"methods"`
}

// PaymentStartRequest is what HookPaymentStart carries. Core asks ONCE per session: an answer that does not
// arrive — a timeout, a crash — closes the session as rejected, provider_unavailable (no payer has seen a
// processor's page, so no money can have moved), and the consumer may offer the payer another try, which is
// a new session. Session.ID is still your idempotency key at the processor, so a retry inside your own
// StartPayment cannot open two checkouts for one session.
type PaymentStartRequest struct {
	Session PaymentSession `json:"session"`
}

// PaymentStartResult is a gateway's answer to HookPaymentStart.
type PaymentStartResult struct {
	// Status is redirected (send the payer to RedirectURL), pending, resolved (settled on the spot) or
	// rejected (with FailureCode).
	Status      string `json:"status"`
	RedirectURL string `json:"redirect_url,omitempty"`
	ProviderRef string `json:"provider_ref,omitempty"`
	// ExpiresAt is when the processor gives up on its page, if it says; Core closes the session then.
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	FailureCode    string     `json:"failure_code,omitempty"`
	FailureMessage string     `json:"failure_message,omitempty"`
}

// PaymentRefundRequest is what HookPaymentRefund carries. Refund.ID is your idempotency key at the processor.
type PaymentRefundRequest struct {
	Refund  PaymentRefund  `json:"refund"`
	Session PaymentSession `json:"session"`
}

// PaymentRefundResult is a gateway's answer to HookPaymentRefund: resolved, rejected (with FailureCode), or
// pending — the processor will say later, and you report it with Payments.ResolveRefund or RejectRefund.
type PaymentRefundResult struct {
	Status         string `json:"status"`
	ProviderRef    string `json:"provider_ref,omitempty"`
	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
}

// PaymentConfirmRequest is what HookPaymentConfirm carries to a consumer.
type PaymentConfirmRequest struct {
	Session PaymentSession `json:"session"`
}

// PaymentConfirmResult is a consumer's answer to HookPaymentConfirm, and what Payments.Confirm returns to the
// gateway. Reason, when Proceed is false, is shown to the PAYER.
type PaymentConfirmResult struct {
	Proceed bool   `json:"proceed"`
	Reason  string `json:"reason,omitempty"`
}

// PaymentSessionUpdate is what HookPaymentSessionUpdated carries.
type PaymentSessionUpdate struct {
	Session PaymentSession `json:"session"`
}

// PaymentRefundUpdate is what HookPaymentRefundUpdated carries.
type PaymentRefundUpdate struct {
	Refund  PaymentRefund  `json:"refund"`
	Session PaymentSession `json:"session"`
}

// PaymentGateway is what a gateway implements. Three short calls: nothing here waits for a payer.
type PaymentGateway interface {
	// DescribePayments lists the methods this gateway offers, as it is configured right now. A gateway with
	// no keys yet offers none, rather than methods that fail.
	DescribePayments(ctx context.Context) ([]PaymentMethod, error)
	// StartPayment begins one session at the processor and answers fast: create the processor's session,
	// return its page. A processor you cannot reach is a result — rejected, PaymentFailProviderUnavailable —
	// not an error (see that constant for why).
	StartPayment(ctx context.Context, s PaymentSession) (PaymentStartResult, error)
	// RefundPayment asks the processor to give money back.
	RefundPayment(ctx context.Context, r PaymentRefund, s PaymentSession) (PaymentRefundResult, error)
}

// PaymentGatewayPlugin is a whole gateway plugin: a Handler — whose Init keeps the *Core, reads the
// processor's keys and starts the route its webhook arrives on — that is also a PaymentGateway.
type PaymentGatewayPlugin interface {
	Handler
	PaymentGateway
}

// ServePaymentGateway is Serve for a gateway. Serve would do the same work; what this adds is the COMPILER'S
// check that g is a PaymentGateway — a method whose signature is one character off (a *PaymentSession where a
// PaymentSession belongs) leaves a plugin Serve cannot recognise as a gateway, so it is never asked.
func ServePaymentGateway(g PaymentGatewayPlugin) { Serve(g) }

// PaymentConsumer is what a plugin that takes payments implements.
type PaymentConsumer interface {
	// ConfirmPayment is the last check before a gateway that asks (Payments.Confirm) takes the money: is the
	// stock still there, is the price still the price. Answer from your own state, fast. Proceed false
	// rejects the session with your Reason; an error means you could not decide, and the gateway asks again.
	ConfirmPayment(ctx context.Context, s PaymentSession) (PaymentConfirmResult, error)
	// PaymentUpdated is told every change of a session you created. Core repeats it until you return nil,
	// so it must be safe to receive twice: key what you do by (s.ID, s.Status), never by arrival.
	PaymentUpdated(ctx context.Context, s PaymentSession) error
	// RefundUpdated is told every change of a refund you asked for — the same promise, the same rule.
	RefundUpdated(ctx context.Context, r PaymentRefund, s PaymentSession) error
}

// DispatchPaymentGatewayHook answers one gateway hook with g, and reports whether it was one. Serve calls it
// for any Handler that is a PaymentGateway; nildatest.Payments calls it to drive yours exactly as Core does.
//
// It CHECKS both directions, once, so every gateway has the same guarantee: a session with no id, no positive
// amount or no currency the standard knows is refused before a processor sees it, and an answer Core could not
// act on — a redirect with nowhere to go, a rejection without one of the five codes, a method with no key or
// two with one — is an error here rather than a session stuck in a state nobody can read.
func DispatchPaymentGatewayHook(ctx context.Context, _ *Core, g PaymentGateway, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookPaymentDescribe:
		methods, err := g.DescribePayments(ctx)
		if err != nil {
			return nil, true, err
		}
		if methods == nil {
			methods = []PaymentMethod{} // a list, never null
		}
		if err := validMethods(methods); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(PaymentDescribeResponse{Methods: methods})
		return b, true, err

	case HookPaymentStart:
		var in PaymentStartRequest
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.start payload: %w", err)
		}
		if err := validSession(in.Session); err != nil {
			return nil, true, err
		}
		res, err := g.StartPayment(ctx, in.Session)
		if err != nil {
			return nil, true, err
		}
		if err := validStart(res); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookPaymentRefund:
		var in PaymentRefundRequest
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.refund payload: %w", err)
		}
		if in.Refund.ID == "" || in.Refund.AmountMinor <= 0 {
			return nil, true, fmt.Errorf("nilda: payment.refund: a refund needs an id and a positive amount")
		}
		if err := validSession(in.Session); err != nil {
			return nil, true, err
		}
		res, err := g.RefundPayment(ctx, in.Refund, in.Session)
		if err != nil {
			return nil, true, err
		}
		if err := validRefundResult(res); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err
	}
	return nil, false, nil
}

// DispatchPaymentConsumerHook answers one consumer hook with c, and reports whether it was one. Serve calls it
// for any Handler that is a PaymentConsumer.
func DispatchPaymentConsumerHook(ctx context.Context, _ *Core, c PaymentConsumer, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookPaymentConfirm:
		var in PaymentConfirmRequest
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.session.confirm payload: %w", err)
		}
		res, err := c.ConfirmPayment(ctx, in.Session)
		if err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookPaymentSessionUpdated:
		var in PaymentSessionUpdate
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.session.updated payload: %w", err)
		}
		if err := c.PaymentUpdated(ctx, in.Session); err != nil {
			return nil, true, err
		}
		return []byte(`{}`), true, nil

	case HookPaymentRefundUpdated:
		var in PaymentRefundUpdate
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.refund.updated payload: %w", err)
		}
		if err := c.RefundUpdated(ctx, in.Refund, in.Session); err != nil {
			return nil, true, err
		}
		return []byte(`{}`), true, nil
	}
	return nil, false, nil
}

// The bounds on what a plugin sends. The reference's is Stripe's own bound on client_reference_id; the email
// and locale bounds are the standards' (RFC 5321's 254-octet address, RFC 5646's 35-character tag); the rest
// are Nilda's choice. Core holds the same numbers.
const (
	maxPaymentReference = 200
	maxPaymentText      = 500
	maxProviderRef      = 255
	maxPayerURL         = 2048
	maxEmail            = 254
	maxLocale           = 35
)

func validAmount(minor int64, currency string) error {
	if minor <= 0 {
		return fmt.Errorf("nilda: a payment's amount_minor must be positive, got %d", minor)
	}
	if _, ok := MinorUnits(currency); !ok {
		return fmt.Errorf("nilda: %q is not a currency ISO 4217 gives a minor unit to", currency)
	}
	return nil
}

func validPayerURL(field, raw string, required bool) error {
	if raw == "" && !required {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || len(raw) > maxPayerURL {
		return fmt.Errorf("nilda: %s must be an absolute http(s) URL, got %q", field, raw)
	}
	return nil
}

func validLocale(tag string) bool {
	if len(tag) > maxLocale {
		return false
	}
	for _, part := range strings.Split(tag, "-") {
		if part == "" || len(part) > 8 {
			return false
		}
		for i := 0; i < len(part); i++ {
			c := part[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

func validProviderRef(ref string) error {
	if len(ref) > maxProviderRef {
		return fmt.Errorf("nilda: a provider_ref is at most %d bytes", maxProviderRef)
	}
	return nil
}

func validGatewayFailure(code string) error {
	for _, c := range gatewayFailureCodes {
		if c == code {
			return nil
		}
	}
	return fmt.Errorf("nilda: failure code %q is not one a gateway reports: %s", code,
		strings.Join(gatewayFailureCodes, ", "))
}

// validMethods refuses a describe answer a payer could not choose from: a method with no key or no label, two
// with one key (a session names its method by key), a currency ISO 4217 does not price.
func validMethods(methods []PaymentMethod) error {
	seen := map[string]bool{}
	for _, m := range methods {
		switch {
		case strings.TrimSpace(m.Key) == "" || strings.TrimSpace(m.Label) == "":
			return fmt.Errorf("nilda: payment.describe answered a method with no key or no label: %+v", m)
		case seen[m.Key]:
			return fmt.Errorf("nilda: payment.describe answered two methods keyed %q; a session names one by its key", m.Key)
		}
		seen[m.Key] = true
		for _, c := range m.Currencies {
			if _, ok := MinorUnits(c); !ok {
				return fmt.Errorf("nilda: payment.describe: method %q takes %q, which is not a currency ISO 4217 prices", m.Key, c)
			}
		}
	}
	return nil
}

func validSession(s PaymentSession) error {
	if s.ID == "" {
		return fmt.Errorf("nilda: a payment session with no id cannot be matched to its outcome")
	}
	return validAmount(s.AmountMinor, s.Currency)
}

func validStart(r PaymentStartResult) error {
	switch r.Status {
	case PaymentRedirected:
		if err := validPayerURL("redirect_url", r.RedirectURL, true); err != nil {
			return fmt.Errorf("nilda: payment.start answered redirected, and %w", err)
		}
	case PaymentPending, PaymentResolved:
	case PaymentRejected:
		if err := validGatewayFailure(r.FailureCode); err != nil {
			return fmt.Errorf("nilda: payment.start answered rejected: %w", err)
		}
	default:
		return fmt.Errorf("nilda: payment.start answered status %q; it answers redirected, pending, resolved or rejected", r.Status)
	}
	if utf8.RuneCountInString(r.FailureMessage) > maxPaymentText {
		return fmt.Errorf("nilda: a failure message is at most %d characters", maxPaymentText)
	}
	return validProviderRef(r.ProviderRef)
}

func validRefundResult(r PaymentRefundResult) error {
	switch r.Status {
	case RefundPending, RefundResolved:
	case RefundRejected:
		if err := validGatewayFailure(r.FailureCode); err != nil {
			return fmt.Errorf("nilda: payment.refund answered rejected: %w", err)
		}
	default:
		return fmt.Errorf("nilda: payment.refund answered status %q; it answers pending, resolved or rejected", r.Status)
	}
	if utf8.RuneCountInString(r.FailureMessage) > maxPaymentText {
		return fmt.Errorf("nilda: a failure message is at most %d characters", maxPaymentText)
	}
	return validProviderRef(r.ProviderRef)
}
