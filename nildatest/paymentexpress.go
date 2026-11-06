package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// EXPRESS WALLET BUTTONS (plugin-sdk v0.58.0, EXPERIMENTAL), as Core will decide them (commerce plan 10.2, Core's X2b):
// a start that answers redirected with only a ClientAction, kept on the session while it is redirected; and the
// consumer's re-price of an open session before its payer approves — asked of the gateway once (payment.reprice), the
// amount changed only on its yes. Express describes are held by the same dispatch Core's describe goes through
// (nilda.DispatchPaymentGatewayHook), and Payments.Methods hands each option its Express.

// repriceRoutes answers a consumer's re-price; ok=false when the request is not one.
func (p *Payments) repriceRoutes(w http.ResponseWriter, r *http.Request, caller string, parts []string, body []byte) bool {
	if r.Method != http.MethodPost || len(parts) != 4 || parts[1] != "sessions" || parts[3] != "reprice" {
		return false
	}
	ctx := r.Context()
	p.keyed(w, r, caller, body, func() (int, any, error) {
		var in nilda.PaymentReprice
		if err := json.Unmarshal(body, &in); err != nil {
			return 0, nil, invalid("the body is not a re-price: %v", err)
		}
		s, err := p.reprice(ctx, caller, parts[2], in)
		return http.StatusOK, s, err
	})
	return true
}

// Reprice re-prices a session as its consumer would, through the same checks — for a gateway's test, which has no
// consumer of its own. Not keyed, as CreateSession is not.
func (p *Payments) Reprice(ctx context.Context, consumer, id string, r nilda.PaymentReprice) (nilda.PaymentSession, error) {
	return p.reprice(ctx, consumer, id, r)
}

// reprice is Core's re-price: the consumer's own session, open (created or redirected) and not yet approved — the
// gateway has not asked Confirm for it — that no credential, kept method or reader started, through a method that
// re-prices; the gateway asked once under the session's lock, the amount changed only when it says the processor's
// order now asks it.
func (p *Payments) reprice(ctx context.Context, caller, id string, in nilda.PaymentReprice) (nilda.PaymentSession, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.PaymentSession{}, err
	}
	if err := in.Validate(); err != nil {
		return nilda.PaymentSession{}, invalid("%v", err)
	}
	p.mu.Lock()
	s, ok := p.sessions[id]
	if !ok || s.Consumer != caller {
		p.mu.Unlock()
		return nilda.PaymentSession{}, notFound("no payment session %q", id)
	}
	if err := p.mayReprice(*s); err != nil {
		p.mu.Unlock()
		return nilda.PaymentSession{}, err
	}
	snapshot := *s
	p.asking[id] = true // Core holds the session's lock while it asks
	p.mu.Unlock()

	res, err := p.askReprice(ctx, snapshot, in)

	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.asking, id)
	if err != nil {
		return nilda.PaymentSession{}, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE",
			fmt.Sprintf("the gateway did not answer the re-price; nothing changed, ask again: %v", err)}
	}
	if !res.Repriced {
		return nilda.PaymentSession{}, &apiError{http.StatusConflict, "CONFLICT",
			"the processor did not change the payment's amount: " + runes(res.FailureMessage, maxPaymentText)}
	}
	// The session may have moved while the gateway was asked (its payer approved a moment before): the gateway's yes is
	// for an order its payer had not approved, so it stands only on a session still open.
	if s.Status != nilda.PaymentCreated && s.Status != nilda.PaymentRedirected {
		return nilda.PaymentSession{}, conflict(s)
	}
	if s.AmountMinor != in.AmountMinor {
		s.AmountMinor = in.AmountMinor
		// The item lines described the amount before; the processor's order no longer holds them (v0.58.0, 10.15).
		s.Lines, s.Breakdown = nil, nil
		s.UpdatedAt = time.Now().UTC()
		p.owe(owed{session: s.ID})
	}
	return *s, nil
}

// mayReprice is the rule, in Core's words: 409 for a session the payer approved or that moved on, 422 for one that was
// never re-priceable. Callers hold p.mu.
func (p *Payments) mayReprice(s nilda.PaymentSession) error {
	switch {
	case p.asking[s.ID]:
		return &apiError{http.StatusConflict, "CONFLICT", "this payment is being asked about right now; ask again in a moment"}
	case s.Status != nilda.PaymentCreated && s.Status != nilda.PaymentRedirected:
		return &apiError{http.StatusConflict, "CONFLICT",
			fmt.Sprintf("this payment is already %s; only one its payer has not approved can be re-priced", s.Status)}
	case p.approved[s.ID]:
		return &apiError{http.StatusConflict, "CONFLICT",
			"the payer approved this payment at the processor; the amount they approved is the amount"}
	case p.credentialed[s.ID] || s.ChargedSaved != "" || s.Reader != "" || s.Save != nil:
		return invalid("a payment a credential, a kept method or a card reader started, or one that keeps a method, is not re-priced")
	case !nilda.Supports(s.Supports, nilda.SupportReprice):
		return invalid("this payment's method cannot be re-priced (it does not list %q)", nilda.SupportReprice)
	}
	return nil
}

// askReprice sends payment.reprice through the same dispatch Serve runs.
func (p *Payments) askReprice(ctx context.Context, s nilda.PaymentSession, in nilda.PaymentReprice) (nilda.PaymentRepriceResult, error) {
	g, ok := p.installedGateway(s.Gateway)
	if !ok {
		return nilda.PaymentRepriceResult{}, fmt.Errorf("gateway %q is not installed", s.Gateway)
	}
	payload, _ := json.Marshal(nilda.PaymentRepriceRequest{Session: s, AmountMinor: in.AmountMinor,
		ShippingOptions: in.ShippingOptions})
	ctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	defer cancel()
	out, handled, err := nilda.DispatchPaymentGatewayHook(ctx, nil, g, nilda.HookPaymentReprice, payload)
	if err != nil {
		return nilda.PaymentRepriceResult{}, err
	}
	if !handled {
		return nilda.PaymentRepriceResult{}, fmt.Errorf("gateway %q does not re-price (it is no nilda.PaymentRepricer)", s.Gateway)
	}
	var res nilda.PaymentRepriceResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nilda.PaymentRepriceResult{}, err
	}
	return res, nil
}

// RepricePayment answers with RepriceResult — the zero value says the processor's order now asks the new amount — or
// RepriceErr, and records the request.
func (g *StubGateway) RepricePayment(_ context.Context, req nilda.PaymentRepriceRequest) (nilda.PaymentRepriceResult, error) {
	g.Repriced = append(g.Repriced, req)
	if g.RepriceErr != nil {
		return nilda.PaymentRepriceResult{}, g.RepriceErr
	}
	if !g.RepriceResult.Repriced && strings.TrimSpace(g.RepriceResult.FailureMessage) == "" {
		return nilda.PaymentRepriceResult{Repriced: true}, nil
	}
	return g.RepriceResult, nil
}
