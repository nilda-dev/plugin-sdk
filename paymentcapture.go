package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// HOLD THE MONEY, THEN TAKE IT (v0.58.0; commerce's Unit 10, plan 10.1). A made-to-order wheel is paid for when it
// ships: the payer's bank HOLDS the amount at checkout and the shop TAKES it later — or lets it go. This is what every
// processor calls authorise-then-capture (Stripe's capture_method=manual, PayPal's intent=AUTHORIZE, Adyen's manual
// capture) and every platform offers as "take the money when the order ships" (Shopify, WooCommerce); every one of
// them defaults to taking it at once, and so does Nilda.
//
// Three parties, as for every payment:
//
//	CONSUMER  asks for a session with Capture "manual", on a method that lists SupportManualCapture; hears it
//	          AUTHORIZED; later takes some or all of the held amount (Payments.Capture) — once — or lets it go
//	          (Payments.Void).
//	CORE      holds the session's one state machine; asks the gateway to capture or void UNDER THE SESSION'S LOCK, once,
//	          and keeps its answer; asks again, with the same idempotency key, when an answer was lost; and when a hold
//	          passes its deadline asks the gateway to look (payment.sync), then closes it rejected / expired.
//	GATEWAY   asks its processor to hold rather than take; reports the hold (Payments.Authorize) with the processor's
//	          deadline; answers payment.capture and payment.void (PaymentCapturer).
//
// One capture per session, of at most what is held: a smaller one takes that much and the processor releases the rest
// (Stripe's amount_to_capture, WooCommerce's one partial capture). Taking a hold in several parts (multicapture) is not
// in the contract. A refund is only ever of money taken: a session that is authorized is voided, not refunded.

// The hooks Core sends a gateway that implements PaymentCapturer.
const (
	// HookPaymentCapture asks a gateway to take money its processor holds: PaymentCaptureRequest in, PaymentCaptureResult
	// out.
	HookPaymentCapture = "payment.capture"
	// HookPaymentVoid asks a gateway to let a hold go: PaymentVoidRequest in, PaymentVoidResult out.
	HookPaymentVoid = "payment.void"
)

// When a session takes the money: PaymentSessionParams.Capture and PaymentSession.Capture.
const (
	// CaptureAutomatic — at once, the way every session did before v0.58.0. The default ("" means it).
	CaptureAutomatic = "automatic"
	// CaptureManual — the processor holds it, and the consumer takes it later (Payments.Capture) or lets it go
	// (Payments.Void).
	CaptureManual = "manual"
)

// SupportManualCapture: the method can hold the money for a later capture. Cards, Klarna and PayPal can; a SEPA debit,
// iDEAL or a bank transfer cannot — the money moves when the payer pays. Core refuses a manual session on a method
// that does not list it, so a consumer never promises a hold its gateway cannot keep. A method that lists it belongs to
// a gateway that implements PaymentCapturer; a describe answer that says otherwise is refused (DispatchPaymentGatewayHook).
const SupportManualCapture = "manual_capture"

// SupportCardPresent (EXPERIMENTAL: a token only, nothing in Core acts on it yet) — the method takes a card in person,
// at a point of sale (a terminal the gateway drives). A consumer may read it to offer the method at a till rather
// than online; how such a payment starts is not in the contract yet, and the token's meaning may change before it is.
const SupportCardPresent = "card_present"

// PaymentFailVoided — Core's: the consumer let the hold go (Payments.Void). Like amount_mismatch and consumer_refused,
// a gateway reporting it is refused: a hold the PROCESSOR dropped is reported as cancelled or expired.
const PaymentFailVoided = "voided"

// The idempotency keys Core hands a gateway for a session's capture and void — the same key every time it asks about
// that session, so a capture whose answer was lost and is asked again is never a second capture. Pass it to the
// processor unchanged (Stripe's Idempotency-Key, PayPal's PayPal-Request-Id).
const (
	CaptureKeyPrefix = "nilda-capture-"
	VoidKeyPrefix    = "nilda-void-"
)

// maxHoldLifetime is the latest deadline Core keeps for a hold: thirty days, past PayPal's 29 (its longest) — Nilda's
// figure, as for a session's own expiry. A later one is shortened to it. Core holds the same number.
const maxHoldLifetime = 30 * 24 * time.Hour

// PaymentAuthorization is what a GATEWAY reports when the processor holds the money (Payments.Authorize): the amount
// and currency the PROCESSOR holds, not the session's copied back, and the processor's deadline to capture.
type PaymentAuthorization struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	ProviderRef string `json:"provider_ref,omitempty"`
	// ExpiresAt is when the processor lets the hold go uncaptured: Stripe's capture_before, PayPal's 29 days. Core
	// keeps it as the session's AuthorizationExpiresAt (at most 30 days on; now, when it has passed already), and closes
	// a hold nobody captured once it has passed. Absent, Core assumes seven days — a card's hold online, the shortest of
	// the common ones — and the same hold reported again with a deadline corrects it.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Validate refuses a hold with no positive amount, no currency ISO 4217 knows, or a reference past its bound.
func (a PaymentAuthorization) Validate() error {
	if err := validAmount(a.AmountMinor, strings.ToUpper(strings.TrimSpace(a.Currency))); err != nil {
		return err
	}
	return validProviderRef(a.ProviderRef)
}

// PaymentCaptureRequest is what HookPaymentCapture carries: take AmountMinor (at most Session.AuthorizedMinor) of the
// hold, under IdempotencyKey.
type PaymentCaptureRequest struct {
	Session        PaymentSession `json:"session"`
	AmountMinor    int64          `json:"amount_minor"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// PaymentCaptureResult is a gateway's answer to HookPaymentCapture — the processor's, never a guess:
//
//   - resolved: the processor took the money; AmountMinor is what IT says it took (Core compares it with what was asked,
//     as for any resolution);
//   - pending: the processor has the capture and will say later — report it with Payments.Resolve or Reject;
//   - rejected: the money will not come (FailureCode: the hold expired or was cancelled at the processor). Release
//     whatever the processor still holds before you answer it.
//
// A processor you could not reach, or one whose answer you did not get, is an ERROR: Core keeps the capture asked and
// asks again with the same key. Never answer rejected for "I do not know".
type PaymentCaptureResult struct {
	Status         string `json:"status"`
	AmountMinor    int64  `json:"amount_minor,omitempty"`
	ProviderRef    string `json:"provider_ref,omitempty"`
	FailureCode    string `json:"failure_code,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
}

// PaymentVoidRequest is what HookPaymentVoid carries: let the session's hold go, under IdempotencyKey.
type PaymentVoidRequest struct {
	Session        PaymentSession `json:"session"`
	IdempotencyKey string         `json:"idempotency_key"`
}

// PaymentVoidResult is a gateway's answer to HookPaymentVoid: rejected — the hold is released, or had already lapsed:
// no money moves, and Core closes the session rejected / voided — or resolved, when the processor had already taken
// the money (AmountMinor, as it says): Core books it, and the consumer refunds it if it wants it back. An error, as
// for a capture, keeps the void asked.
type PaymentVoidResult struct {
	Status      string `json:"status"`
	AmountMinor int64  `json:"amount_minor,omitempty"`
	ProviderRef string `json:"provider_ref,omitempty"`
}

// PaymentCapturer is what a gateway implements to hold money for a later capture; its methods list
// SupportManualCapture. Core sends payment.capture and payment.void only to such a gateway.
//
// Core asks while it HOLDS THE SESSION'S LOCK, so that two captures asked at once are one capture. So answer from the
// processor's own answer, and do not report on this session (Resolve, Reject, MarkPending) from inside the hook: such
// a report waits on the lock until the hook's time runs out. The answer IS the report. The processor's webhook for
// the same capture, arriving afterwards, finds the session already moved and changes nothing.
type PaymentCapturer interface {
	CapturePayment(ctx context.Context, req PaymentCaptureRequest) (PaymentCaptureResult, error)
	VoidPayment(ctx context.Context, req PaymentVoidRequest) (PaymentVoidResult, error)
}

// validCapture validates the session's capture mode as a consumer sends it: "", automatic or manual, and manual only on
// a session that takes money — one that only keeps a method (AmountMinor 0) has nothing to hold.
func validCapture(mode string, amount int64) error {
	switch mode {
	case "", CaptureAutomatic:
		return nil
	case CaptureManual:
		if amount <= 0 {
			return fmt.Errorf("nilda: a session that only keeps a method has nothing to hold; capture is automatic for it")
		}
		return nil
	}
	return fmt.Errorf("nilda: capture is %q or %q, got %q", CaptureAutomatic, CaptureManual, mode)
}

func validCaptureRequest(r PaymentCaptureRequest) error {
	if err := validSession(r.Session); err != nil {
		return err
	}
	if r.AmountMinor <= 0 || strings.TrimSpace(r.IdempotencyKey) == "" {
		return fmt.Errorf("nilda: payment.capture needs a positive amount and an idempotency key")
	}
	return nil
}

func validCaptureResult(r PaymentCaptureResult) error {
	switch r.Status {
	case PaymentResolved:
		if r.AmountMinor <= 0 {
			return fmt.Errorf("nilda: payment.capture answered resolved without the amount the processor took")
		}
	case PaymentPending:
	case PaymentRejected:
		if err := validGatewayFailure(r.FailureCode); err != nil {
			return fmt.Errorf("nilda: payment.capture answered rejected: %w", err)
		}
	default:
		return fmt.Errorf("nilda: payment.capture answered status %q; it answers resolved, pending or rejected", r.Status)
	}
	if utf8.RuneCountInString(r.FailureMessage) > maxPaymentText {
		return fmt.Errorf("nilda: a failure message is at most %d characters", maxPaymentText)
	}
	if !noNUL(r.FailureMessage) {
		return errNUL()
	}
	return validProviderRef(r.ProviderRef)
}

func validVoidResult(r PaymentVoidResult) error {
	switch r.Status {
	case PaymentRejected:
	case PaymentResolved:
		if r.AmountMinor <= 0 {
			return fmt.Errorf("nilda: payment.void answered resolved without the amount the processor had taken")
		}
	default:
		return fmt.Errorf("nilda: payment.void answered status %q; it answers rejected (released) or resolved (already taken)", r.Status)
	}
	return validProviderRef(r.ProviderRef)
}

// dispatchCaptureHook answers payment.capture and payment.void for DispatchPaymentGatewayHook. handled is false for a
// gateway that holds nothing: Serve never subscribed it.
func dispatchCaptureHook(ctx context.Context, g PaymentGateway, hook string, payload []byte) (out []byte, handled bool, err error) {
	c, is := g.(PaymentCapturer)
	if !is {
		return nil, false, nil
	}
	switch hook {
	case HookPaymentCapture:
		var in PaymentCaptureRequest
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.capture payload: %w", err)
		}
		if err := validCaptureRequest(in); err != nil {
			return nil, true, err
		}
		res, err := c.CapturePayment(ctx, in)
		if err != nil {
			return nil, true, err
		}
		if err := validCaptureResult(res); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookPaymentVoid:
		var in PaymentVoidRequest
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.void payload: %w", err)
		}
		if err := validSession(in.Session); err != nil {
			return nil, true, err
		}
		if strings.TrimSpace(in.IdempotencyKey) == "" {
			return nil, true, fmt.Errorf("nilda: payment.void needs an idempotency key")
		}
		res, err := c.VoidPayment(ctx, in)
		if err != nil {
			return nil, true, err
		}
		if err := validVoidResult(res); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err
	}
	return nil, false, nil
}

// ---- the calls ------------------------------------------------------------------------------------------------

// Authorize reports that the processor HOLDS the money for a session asked with Capture "manual": the amount and
// currency the processor holds, and its deadline. Core moves the session to authorized; a different amount or currency
// is refused (422) and nothing moves — release that hold at the processor. A session asked to take the money at once
// is refused too: report its money with Resolve. The same report twice is a no-op, except that a deadline other than
// the one Core keeps is kept while no capture or void was asked. Once one was asked, a hold report moves nothing — the
// same hold while the session is authorized, 409 after — and a session held for a person (amount_mismatch) is 409.
func (p *Payments) Authorize(ctx context.Context, sessionID string, a PaymentAuthorization) (PaymentSession, error) {
	if err := a.Validate(); err != nil {
		return PaymentSession{}, err
	}
	a.Currency = strings.ToUpper(strings.TrimSpace(a.Currency))
	return p.report(ctx, "/payments/sessions/", sessionID, "/authorize", a)
}

// Capture takes amountMinor of an authorized session's hold — all of it, or less, and the processor releases the rest.
// Core asks the gateway once, under the session's lock, and answers with the session as the gateway left it: resolved
// at the amount taken (AmountMinor becomes it), pending while the processor decides, rejected when the hold was gone.
// One capture per session: asked again with the same amount it is the same capture — answered as it stands, or asked
// again if the first answer was lost — and with another amount it is refused (409). More than is held is 422; a
// session that is not authorized is 409.
//
// A 503 means the gateway did not answer: Core keeps the capture asked and asks again with the same key (you hear how
// it ends in PaymentUpdated), and the same call with the same key may be sent again. idempotencyKey names this
// capture, as for every payment write.
func (p *Payments) Capture(ctx context.Context, idempotencyKey, sessionID string, amountMinor int64) (PaymentSession, error) {
	if err := validIdempotencyKey(idempotencyKey); err != nil {
		return PaymentSession{}, err
	}
	if amountMinor <= 0 {
		return PaymentSession{}, fmt.Errorf("nilda: a capture's amount_minor must be positive, got %d", amountMinor)
	}
	path, err := paymentPath("/payments/sessions/", sessionID, "/capture")
	if err != nil {
		return PaymentSession{}, err
	}
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err = p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, path, captureBody{AmountMinor: amountMinor}, &out)
	return out.Data, err
}

// Void lets an authorized session's hold go: Core asks the gateway, under the session's lock, and the session ends
// rejected with FailureCode "voided" — or resolved, if the processor had already taken the money. A session whose
// capture was asked cannot be voided (409); voiding twice is no error. A 503 is as for Capture.
func (p *Payments) Void(ctx context.Context, idempotencyKey, sessionID string) (PaymentSession, error) {
	if err := validIdempotencyKey(idempotencyKey); err != nil {
		return PaymentSession{}, err
	}
	path, err := paymentPath("/payments/sessions/", sessionID, "/void")
	if err != nil {
		return PaymentSession{}, err
	}
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err = p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, path, struct{}{}, &out)
	return out.Data, err
}

// captureBody is the body of a consumer's capture.
type captureBody struct {
	AmountMinor int64 `json:"amount_minor"`
}
