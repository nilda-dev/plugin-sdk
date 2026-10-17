package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// A PAYMENT METHOD KEPT FOR LATER, as Core keeps it (v0.30.0, plugin-sdk paymentsaved.go): a session with Save on a
// method that lists SupportSaveForLater; its gateway's resolution with Saved gives the session a SavedMethod; the
// consumer charges it with the payer absent (Payments.ChargeSaved) and forgets it. The token stays here — read it in
// a test with SavedToken, never from a session.

// savedMethod is a kept method and its token, as Core keeps them (the token encrypted there).
type savedMethod struct {
	nilda.SavedPaymentMethod
	token string
}

// SavedMethod returns a kept method as its consumer reads it.
func (p *Payments) SavedMethod(id string) (nilda.SavedPaymentMethod, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.saved[id]
	if !ok {
		return nilda.SavedPaymentMethod{}, false
	}
	return m.SavedPaymentMethod, true
}

// SavedToken is the gateway's token for a kept method — what a gateway's test checks it was handed back. Empty once
// the method was forgotten.
func (p *Payments) SavedToken(id string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m, ok := p.saved[id]; ok {
		return m.token
	}
	return ""
}

// ChargeSaved charges a kept method for consumer, as the HTTP call does.
func (p *Payments) ChargeSaved(ctx context.Context, consumer string, params nilda.PaymentChargeParams) (nilda.PaymentSession, error) {
	return p.chargeSaved(ctx, consumer, params)
}

// ForgetSavedMethod forgets a kept method for consumer, as the HTTP call does: its gateway is asked to let go of it.
func (p *Payments) ForgetSavedMethod(ctx context.Context, consumer, id string) (nilda.SavedPaymentMethod, error) {
	return p.forgetSaved(ctx, consumer, id)
}

// keep records the method a Save session's gateway reported, and gives the session its id. Callers hold p.mu.
func (p *Payments) keep(s *nilda.PaymentSession, saved *nilda.PaymentSaved) {
	if saved == nil || s.Save == nil || s.SavedMethod != "" {
		return
	}
	m := &savedMethod{token: saved.Token, SavedPaymentMethod: nilda.SavedPaymentMethod{
		ID: newID(), Consumer: s.Consumer, Gateway: s.Gateway, Method: s.Method, Label: saved.Label, Email: s.Email,
		Mandate: *s.Save, Session: s.ID, Test: s.Test, ExpiresAt: saved.ExpiresAt, CreatedAt: time.Now().UTC(),
	}}
	p.saved[m.ID] = m
	s.SavedMethod = m.ID
}

func (p *Payments) chargeSaved(ctx context.Context, consumer string, params nilda.PaymentChargeParams) (nilda.PaymentSession, error) {
	if err := p.may(consumer, "payment_session"); err != nil {
		return nilda.PaymentSession{}, err
	}
	if err := params.Validate(); err != nil {
		return nilda.PaymentSession{}, invalid("%v", err)
	}
	currency := strings.ToUpper(strings.TrimSpace(params.Currency))
	p.mu.Lock()
	m, ok := p.saved[params.SavedMethod]
	if !ok || m.Consumer != consumer {
		p.mu.Unlock()
		return nilda.PaymentSession{}, notFound("no kept payment method %q", params.SavedMethod)
	}
	kept := *m
	p.mu.Unlock()
	if kept.ForgottenAt != nil {
		return nilda.PaymentSession{}, invalid("the payment method %q was forgotten; it charges nothing again", params.SavedMethod)
	}
	if !strings.EqualFold(kept.Mandate.Currency, currency) {
		return nilda.PaymentSession{}, invalid("the payer agreed to charges in %s, not %s", kept.Mandate.Currency, currency)
	}
	// The method must still be offered: a gateway switched off charges nothing, as Core refuses it.
	offered, err := p.method(ctx, kept.Gateway, kept.Method, currency)
	if err != nil {
		return nilda.PaymentSession{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(sessionLifetime)
	s := &nilda.PaymentSession{
		ID: newID(), Consumer: consumer, Gateway: kept.Gateway, Method: kept.Method, Reference: params.Reference,
		AmountMinor: params.AmountMinor, Currency: currency, Status: nilda.PaymentCreated, Test: kept.Test,
		Description: params.Description, Email: kept.Email, Fulfilment: params.Fulfilment,
		ShippingAddress: params.ShippingAddress, ChargedSaved: kept.ID, Supports: nilda.SupportsOf(offered),
		ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now,
	}
	p.mu.Lock()
	p.sessions[s.ID] = s
	p.order = append(p.order, s.ID)
	snapshot := *s
	p.mu.Unlock()

	res, err := p.startSaved(ctx, snapshot, kept.token)
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.Status != nilda.PaymentCreated {
		return *s, nil
	}
	if err != nil {
		s.FailureCode, s.FailureMessage = nilda.PaymentFailProviderUnavailable, ""
		p.move(s, nilda.PaymentRejected)
		return *s, nil
	}
	if res.Status == nilda.PaymentRedirected {
		// The payer is not there: a gateway that answers with a page has nothing Core can send anyone to.
		s.FailureCode, s.FailureMessage = nilda.PaymentFailProviderUnavailable, ""
		p.move(s, nilda.PaymentRejected)
		return *s, nil
	}
	s.ProviderRef = res.ProviderRef
	if res.Status == nilda.PaymentRejected {
		s.FailureCode, s.FailureMessage = res.FailureCode, res.FailureMessage
	}
	p.move(s, res.Status)
	return *s, nil
}

func (p *Payments) startSaved(ctx context.Context, s nilda.PaymentSession, token string) (nilda.PaymentStartResult, error) {
	g, ok := p.installedGateway(s.Gateway)
	if !ok {
		return nilda.PaymentStartResult{}, fmt.Errorf("gateway %q is not installed", s.Gateway)
	}
	payload, _ := json.Marshal(nilda.PaymentStartRequest{Session: s, Saved: &nilda.PaymentSavedToken{Token: token}})
	ctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	defer cancel()
	out, _, err := nilda.DispatchPaymentGatewayHook(ctx, nil, g, nilda.HookPaymentStart, payload)
	if err != nil {
		return nilda.PaymentStartResult{}, err
	}
	var res nilda.PaymentStartResult
	err = json.Unmarshal(out, &res)
	return res, err
}

func (p *Payments) readSaved(caller, id string) (nilda.SavedPaymentMethod, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.SavedPaymentMethod{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.saved[id]
	if !ok || m.Consumer != caller {
		return nilda.SavedPaymentMethod{}, notFound("no kept payment method %q", id)
	}
	return m.SavedPaymentMethod, nil
}

// forgetSaved asks the gateway to let go of the method at its processor, then drops the token. A gateway that cannot
// be reached leaves the method forgotten here — it charges nothing again — and its token kept until the gateway lets
// go, as Core retries the release.
func (p *Payments) forgetSaved(ctx context.Context, caller, id string) (nilda.SavedPaymentMethod, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.SavedPaymentMethod{}, err
	}
	p.mu.Lock()
	m, ok := p.saved[id]
	if !ok || m.Consumer != caller {
		p.mu.Unlock()
		return nilda.SavedPaymentMethod{}, notFound("no kept payment method %q", id)
	}
	if m.ForgottenAt == nil {
		now := time.Now().UTC()
		m.ForgottenAt = &now
	}
	token, gateway := m.token, m.Gateway
	p.mu.Unlock()
	if token != "" {
		if g, ok := p.installedGateway(gateway); ok {
			payload, _ := json.Marshal(nilda.PaymentForgetRequest{Token: token})
			gctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
			_, _, err := nilda.DispatchPaymentGatewayHook(gctx, nil, g, nilda.HookPaymentForget, payload)
			cancel()
			if err == nil {
				p.mu.Lock()
				m.token = ""
				p.mu.Unlock()
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return m.SavedPaymentMethod, nil
}

// savedRoutes answers the saved-method calls; ok=false when the request is not one of them.
func (p *Payments) savedRoutes(w http.ResponseWriter, r *http.Request, caller string, parts []string, body []byte) bool {
	ctx := r.Context()
	switch {
	case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "charges":
		p.keyed(w, r, caller, body, func() (int, any, error) {
			var params nilda.PaymentChargeParams
			if err := json.Unmarshal(body, &params); err != nil {
				return 0, nil, invalid("the body is not a charge: %v", err)
			}
			s, err := p.chargeSaved(ctx, caller, params)
			return http.StatusCreated, s, err
		})
	case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "saved-methods":
		m, err := p.readSaved(caller, parts[2])
		answer(w, http.StatusOK, m, err)
	case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "saved-methods" && parts[3] == "forget":
		m, err := p.forgetSaved(ctx, caller, parts[2])
		answer(w, http.StatusOK, m, err)
	default:
		return false
	}
	return true
}

// mayKeep refuses a Save session on a method that does not keep methods, as Core does.
func mayKeep(m nilda.PaymentMethod, params nilda.PaymentSessionParams) error {
	if params.Save != nil && !slices.Contains(nilda.SupportsOf(m), nilda.SupportSaveForLater) {
		return invalid("%q of %q does not keep a payment method for later", params.Method, params.Gateway)
	}
	return nil
}
