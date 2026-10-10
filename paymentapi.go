package nilda

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
// counting refunds still in flight — or for a method that cannot refund; a 503 means the gateway could not be
// asked what it offers, and the same key runs again. Otherwise it answers the refund `requested` at once: the
// gateway is asked by Core's delivery job, never inside this call, and how it ends arrives in RefundUpdated.
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
// has already REJECTED the session (consumer_refused), unless it had ended another way while the consumer was
// asked: release the authorisation at your processor, and do not capture.
//
// An error is one of two kinds. A transport error, a 429 or a 5xx (*APIError.Retryable — a 503 is Core
// saying it could not ask the consumer) may succeed later: the authorisation holds for days at every
// processor, so ask again later rather than capturing unasked. A 409 (the session expired or already ended)
// or a 404 (not a session of this gateway's) is final — asking again gets the same answer — so release the
// authorisation.
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

// ReverseRefund reports that a refund that had gone through was undone at the processor — a bank sent the money
// back. Only a resolved refund can be reversed; it stays resolved, Core sets its ReversedAt and tells the consumer
// again. The rejection's code and message say why ("the card was closed").
func (p *Payments) ReverseRefund(ctx context.Context, refundID string, r PaymentRejection) (PaymentRefund, error) {
	if err := r.Validate(); err != nil {
		return PaymentRefund{}, err
	}
	return p.reportRefund(ctx, refundID, "/reverse", r)
}

// ReportExternalRefund reports a refund made at the processor outside Nilda — by hand in its dashboard — on a
// session routed to you. Core records it as a resolved, External refund, adds it to the session's refunded total
// and tells the consumer, so the shop's books match the processor's. The same ProviderRef twice is one refund.
func (p *Payments) ReportExternalRefund(ctx context.Context, sessionID string, r PaymentExternalRefund) (PaymentRefund, error) {
	if err := r.Validate(); err != nil {
		return PaymentRefund{}, err
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	path, err := paymentPath("/payments/sessions/", sessionID, "/external-refunds")
	if err != nil {
		return PaymentRefund{}, err
	}
	var out struct {
		Data PaymentRefund `json:"data"`
	}
	err = p.api.Post(ctx, path, r, &out)
	return out.Data, err
}

// ReportDispute reports a dispute on a session routed to you, as the processor holds it NOW (a snapshot: read
// it again from the processor on each event). Core keeps one dispute per ProviderRef and tells the consumer; a
// snapshot equal to the stored one changes nothing. A verdict cannot be taken back (409), and a chargeback
// cannot become an inquiry again — but the money fields can change after the verdict.
func (p *Payments) ReportDispute(ctx context.Context, sessionID string, r PaymentDisputeReport) (PaymentDispute, error) {
	if err := r.Validate(); err != nil {
		return PaymentDispute{}, err
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	path, err := paymentPath("/payments/sessions/", sessionID, "/disputes")
	if err != nil {
		return PaymentDispute{}, err
	}
	var out struct {
		Data PaymentDispute `json:"data"`
	}
	err = p.api.Post(ctx, path, r, &out)
	return out.Data, err
}

// ListDisputes reads a session's disputes, oldest first — for the consumer that created it or the gateway it was
// routed to.
func (p *Payments) ListDisputes(ctx context.Context, sessionID string) ([]PaymentDispute, error) {
	path, err := paymentPath("/payments/sessions/", sessionID, "/disputes")
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []PaymentDispute `json:"data"`
	}
	if err := p.api.Get(ctx, path, nil, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = []PaymentDispute{}
	}
	return out.Data, nil
}

// GetDispute reads one dispute, for either side of its session.
func (p *Payments) GetDispute(ctx context.Context, id string) (PaymentDispute, error) {
	path, err := paymentPath("/payments/disputes/", id, "")
	if err != nil {
		return PaymentDispute{}, err
	}
	var out struct {
		Data PaymentDispute `json:"data"`
	}
	err = p.api.Get(ctx, path, nil, &out)
	return out.Data, err
}

// SetSecret keeps one of the gateway's own secrets — a webhook signing secret its processor shows only once —
// in Core, encrypted at rest, under name. It survives a restart and a lost kv, which a plugin's own store does
// not on every install. Setting a name again replaces its value.
func (p *Payments) SetSecret(ctx context.Context, name, value string) error {
	if err := validSecret(name, value); err != nil {
		return err
	}
	return p.api.JSON(ctx, http.MethodPut, "/payments/secrets/"+name, secretBody{Value: value}, nil)
}

// GetSecret reads one of the gateway's own secrets back; found is false when none was set under name.
func (p *Payments) GetSecret(ctx context.Context, name string) (value string, found bool, err error) {
	if !secretName.MatchString(name) {
		return "", false, fmt.Errorf("nilda: a secret's name is 1–64 lowercase letters, digits or underscores, got %q", name)
	}
	var out struct {
		Data secretBody `json:"data"`
	}
	err = p.api.Get(ctx, "/payments/secrets/"+name, nil, &out)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return out.Data.Value, true, nil
}

// secretBody is the body of a secret's write and read.
type secretBody struct {
	Value string `json:"value"`
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
