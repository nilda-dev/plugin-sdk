package nilda

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// Payments is the payment contract's door into Core, for both of its sides: the CONSUMER that asks for money
// (`payment_session`) and the GATEWAY that reports what its processor said (`payment_gateway`). Every call is
// HTTP under /api/rest/v1/payments with this plugin's token, so Core knows who is asking and answers each
// plugin only about the sessions that are its own — the ones a consumer created, the ones routed to a
// gateway. Another plugin's session answers 404, the way a processor answers one account's key about another
// account's payment.
//
// A refusal is an *APIError: 404 for a session that is not yours, 409 (Conflict) for a report the session has
// already moved past, 422 for what Validate would have caught, 403 for a missing capability.
//
// Nil-safe like the rest of the client: on a plugin with no API access every call returns the client's own
// error.
type Payments struct{ api *API }

// Payments returns the payment calls.
func (a *API) Payments() *Payments { return &Payments{api: a} }

// ---- the consumer's side (payment_session) ----------------------------------------------------------------

// Methods lists what a payer can choose from to pay in currency: every enabled gateway's methods that take
// it, in the order the site owner set. An empty list means this site cannot take a payment in that currency
// — tell the payer so, rather than showing a choice with nothing in it.
func (p *Payments) Methods(ctx context.Context, currency string) ([]PaymentOption, error) {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := MinorUnits(code); !ok {
		return nil, fmt.Errorf("nilda: %q is not a currency ISO 4217 gives a minor unit to", currency)
	}
	var out struct {
		Data []PaymentOption `json:"data"`
	}
	if err := p.api.Get(ctx, "/payments/methods", url.Values{"currency": {code}}, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = []PaymentOption{}
	}
	return out.Data, nil
}

// CreateSession asks Core for a payment. Core checks that the gateway offers the method in that currency and
// that the URLs are on this site, stores the session, asks the gateway to start it, and answers with the
// session as the gateway left it: Status redirected means send the payer to RedirectURL; otherwise it is
// already pending, resolved or rejected.
//
// idempotencyKey names THIS ATTEMPT, and is required. Sent again, it returns the same session instead of a
// second one, which is what makes a retry after a dropped connection safe; so a payer's second attempt at the
// same order — after a declined card — needs a new one ("order-123#2"). At most 255 bytes, as Core allows.
func (p *Payments) CreateSession(ctx context.Context, idempotencyKey string, params PaymentSessionParams) (PaymentSession, error) {
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
	err := p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, "/payments/sessions", params, &out)
	return out.Data, err
}

// GetSession reads one session — the consumer that created it, or the gateway it was routed to. The answer to
// "has this been paid" is here whenever a PaymentUpdated has not arrived yet: a payer back on your return page
// before the processor's webhook, say.
func (p *Payments) GetSession(ctx context.Context, id string) (PaymentSession, error) {
	path, err := paymentPath("/payments/sessions/", id, "")
	if err != nil {
		return PaymentSession{}, err
	}
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err = p.api.Get(ctx, path, nil, &out)
	return out.Data, err
}

// CreateRefund asks Core to give money back from a resolved session. Core refuses one past what was paid —
// counting refunds still in flight — or for a method that cannot refund, and otherwise asks the gateway and
// answers with the refund as the gateway left it; the outcome of a pending one arrives in RefundUpdated.
//
// idempotencyKey names this refund, required, as in CreateSession: repeating a refund because an answer was
// lost must not give the money back twice.
func (p *Payments) CreateRefund(ctx context.Context, idempotencyKey, sessionID string, params PaymentRefundParams) (PaymentRefund, error) {
	if err := validIdempotencyKey(idempotencyKey); err != nil {
		return PaymentRefund{}, err
	}
	if err := params.Validate(); err != nil {
		return PaymentRefund{}, err
	}
	path, err := paymentPath("/payments/sessions/", sessionID, "/refunds")
	if err != nil {
		return PaymentRefund{}, err
	}
	var out struct {
		Data PaymentRefund `json:"data"`
	}
	err = p.api.WithIdempotencyKey(idempotencyKey).Post(ctx, path, params, &out)
	return out.Data, err
}

// GetRefund reads one refund, for the consumer that asked for it or the gateway that carries it out.
func (p *Payments) GetRefund(ctx context.Context, id string) (PaymentRefund, error) {
	path, err := paymentPath("/payments/refunds/", id, "")
	if err != nil {
		return PaymentRefund{}, err
	}
	var out struct {
		Data PaymentRefund `json:"data"`
	}
	err = p.api.Get(ctx, path, nil, &out)
	return out.Data, err
}

// ---- the gateway's side (payment_gateway) ------------------------------------------------------------------
//
// These take no idempotency key and the client does not retry them: the state machine already makes each one
// safe to repeat — the same report twice is answered as a no-op — and the retry that matters is your
// PROCESSOR's. When a report fails, answer the processor's webhook with a 5xx and it delivers the webhook
// again, later, which is exactly the retry wanted. A 409 is different: the session already ended another way,
// repeating changes nothing, so answer the processor 2xx and log what it said.

// Resolve reports that the processor has the money, with the amount and currency the PROCESSOR says it took.
// Core compares them with the session's, and a difference holds the session pending for a person.
func (p *Payments) Resolve(ctx context.Context, sessionID string, r PaymentResolution) (PaymentSession, error) {
	if err := r.Validate(); err != nil {
		return PaymentSession{}, err
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	return p.report(ctx, "/payments/sessions/", sessionID, "/resolve", r)
}

// Reject reports that the payment ended without money.
func (p *Payments) Reject(ctx context.Context, sessionID string, r PaymentRejection) (PaymentSession, error) {
	if err := r.Validate(); err != nil {
		return PaymentSession{}, err
	}
	return p.report(ctx, "/payments/sessions/", sessionID, "/reject", r)
}

// MarkPending reports that the processor has the payment and has not decided — a bank transfer in flight, a
// risk review. providerRef may be empty.
func (p *Payments) MarkPending(ctx context.Context, sessionID, providerRef string) (PaymentSession, error) {
	if err := validProviderRef(providerRef); err != nil {
		return PaymentSession{}, err
	}
	return p.report(ctx, "/payments/sessions/", sessionID, "/pending", providerRefBody{ProviderRef: providerRef})
}

// Confirm asks the consumer, through Core, whether this payment may still go through — call it after the
// payer has authorised and before you capture, if your processor separates the two. Proceed false means Core
// has already REJECTED the session (consumer_refused): release the authorisation at your processor, and do
// not capture. An error means Core could not ask; the authorisation holds for days at every processor, so
// ask again later rather than capturing unasked.
func (p *Payments) Confirm(ctx context.Context, sessionID string) (PaymentConfirmResult, error) {
	path, err := paymentPath("/payments/sessions/", sessionID, "/confirm")
	if err != nil {
		return PaymentConfirmResult{}, err
	}
	var out struct {
		Data PaymentConfirmResult `json:"data"`
	}
	err = p.api.Post(ctx, path, nil, &out)
	return out.Data, err
}

// ResolveRefund reports that a refund the processor left pending went through.
func (p *Payments) ResolveRefund(ctx context.Context, refundID, providerRef string) (PaymentRefund, error) {
	if err := validProviderRef(providerRef); err != nil {
		return PaymentRefund{}, err
	}
	return p.reportRefund(ctx, refundID, "/resolve", providerRefBody{ProviderRef: providerRef})
}

// RejectRefund reports that a refund failed at the processor.
func (p *Payments) RejectRefund(ctx context.Context, refundID string, r PaymentRejection) (PaymentRefund, error) {
	if err := r.Validate(); err != nil {
		return PaymentRefund{}, err
	}
	return p.reportRefund(ctx, refundID, "/reject", r)
}

// providerRefBody is the body of the two reports that carry nothing but the processor's reference.
type providerRefBody struct {
	ProviderRef string `json:"provider_ref,omitempty"`
}

func (p *Payments) report(ctx context.Context, prefix, id, verb string, body any) (PaymentSession, error) {
	path, err := paymentPath(prefix, id, verb)
	if err != nil {
		return PaymentSession{}, err
	}
	var out struct {
		Data PaymentSession `json:"data"`
	}
	err = p.api.Post(ctx, path, body, &out)
	return out.Data, err
}

func (p *Payments) reportRefund(ctx context.Context, id, verb string, body any) (PaymentRefund, error) {
	path, err := paymentPath("/payments/refunds/", id, verb)
	if err != nil {
		return PaymentRefund{}, err
	}
	var out struct {
		Data PaymentRefund `json:"data"`
	}
	err = p.api.Post(ctx, path, body, &out)
	return out.Data, err
}

// paymentPath builds one route, refusing an empty id rather than posting to the collection by accident.
func paymentPath(prefix, id, suffix string) (string, error) {
	if strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("nilda: a payment call needs the session's or refund's id")
	}
	return prefix + url.PathEscape(id) + suffix, nil
}

func validIdempotencyKey(key string) error {
	switch {
	case strings.TrimSpace(key) == "":
		return fmt.Errorf("nilda: a payment write needs an idempotency key naming this attempt — without one, a " +
			"retry after a lost answer makes a second payment or a second refund")
	case len(key) > maxIdempotencyKey:
		return fmt.Errorf("nilda: an idempotency key is at most %d bytes, as Core allows", maxIdempotencyKey)
	}
	return nil
}

// maxIdempotencyKey is Core's bound on the Idempotency-Key header (Stripe's, too).
const maxIdempotencyKey = 255
