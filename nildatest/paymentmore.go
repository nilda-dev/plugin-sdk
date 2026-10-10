package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// The v0.10.4 additions to Core's side, in memory — disputes, refunds undone and made elsewhere, a gateway's own
// secrets, and the pull path — each as Core decides it (core's internal/payments), so a plugin tested here meets
// the same refusals on a real site.

// GetDispute returns a dispute as Core holds it now.
func (p *Payments) GetDispute(id string) (nilda.PaymentDispute, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	d, ok := p.disputes[id]
	if !ok {
		return nilda.PaymentDispute{}, false
	}
	return *d, true
}

// Disputes returns every dispute, oldest first.
func (p *Payments) Disputes() []nilda.PaymentDispute {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]nilda.PaymentDispute, 0, len(p.disputes))
	for _, d := range p.disputes {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || (out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID)
	})
	return out
}

// Secret returns a gateway's secret as Core keeps it (encrypted there).
func (p *Payments) Secret(gateway, name string) (string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.secrets[gateway][name]
	return v, ok
}

// Sync runs Core's pull path for one session: payment.sync to its gateway, which looks the payment up at its
// processor and reports what it finds with the ordinary calls. A gateway that does not implement
// nilda.PaymentSyncer is never subscribed to the hook, and Sync answers ErrNotSubscribed, as Core's sweep skips it.
func (p *Payments) Sync(ctx context.Context, sessionID string) error {
	p.mu.Lock()
	s, ok := p.sessions[sessionID]
	var snapshot nilda.PaymentSession
	if ok {
		snapshot = *s
	}
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("nildatest: no payment session %q", sessionID)
	}
	g, ok := p.installedGateway(snapshot.Gateway)
	if !ok {
		return fmt.Errorf("gateway %q is not installed", snapshot.Gateway)
	}
	payload, _ := json.Marshal(nilda.PaymentSyncRequest{Session: snapshot})
	gctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	defer cancel()
	_, handled, err := nilda.DispatchPaymentGatewayHook(gctx, nil, g, nilda.HookPaymentSync, payload)
	if !handled {
		return ErrNotSubscribed
	}
	return err
}

// reportDispute is a gateway's snapshot of one dispute (Core's ReportDispute): one dispute per processor id, a
// verdict never taken back, a chargeback never an inquiry again, the money fields always the latest, and the
// consumer told of every change.
func (p *Payments) reportDispute(caller, sessionID string, body []byte) (nilda.PaymentDispute, error) {
	if err := p.may(caller, "payment_gateway"); err != nil {
		return nilda.PaymentDispute{}, err
	}
	var r nilda.PaymentDisputeReport
	if err := json.Unmarshal(body, &r); err != nil {
		return nilda.PaymentDispute{}, invalid("the body is not a dispute report: %v", err)
	}
	if err := r.Validate(); err != nil {
		return nilda.PaymentDispute{}, invalid("%v", err)
	}
	r.Currency = strings.ToUpper(strings.TrimSpace(r.Currency))
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[sessionID]
	if !ok || s.Gateway != caller {
		return nilda.PaymentDispute{}, notFound("no payment session %q", sessionID)
	}
	var cur *nilda.PaymentDispute
	count := 0
	for _, d := range p.disputes {
		if p.sessions[d.Session].Gateway == caller && d.ProviderRef == r.ProviderRef {
			cur = d
		}
		if d.Session == sessionID {
			count++
		}
	}
	now := time.Now().UTC()
	if cur == nil {
		if count >= nilda.MaxDisputesPerSession {
			return nilda.PaymentDispute{}, invalid("this payment already has %d disputes, the most Nilda keeps", count)
		}
		d := &nilda.PaymentDispute{ID: newID(), Session: sessionID, CreatedAt: now}
		applyDispute(d, r, now)
		p.disputes[d.ID] = d
		p.owe(owed{session: sessionID, dispute: d.ID})
		return *d, nil
	}
	if cur.Session != sessionID {
		return nilda.PaymentDispute{}, &apiError{http.StatusConflict, "CONFLICT",
			"this dispute was reported on another payment"}
	}
	if cur.Stage == nilda.DisputeChargeback && r.Stage == nilda.DisputeInquiry {
		return nilda.PaymentDispute{}, &apiError{http.StatusConflict, "CONFLICT", "a chargeback does not become an inquiry again"}
	}
	if r.Status != cur.Status && !nilda.DisputeCanMove(cur.Status, r.Status) {
		return nilda.PaymentDispute{}, &apiError{http.StatusConflict, "CONFLICT",
			fmt.Sprintf("this dispute is already %s; that report cannot change it", cur.Status)}
	}
	next := *cur
	applyDispute(&next, r, now)
	if sameDispute(next, *cur) {
		return *cur, nil // the same snapshot again: the processor's redelivery
	}
	*cur = next
	p.owe(owed{session: sessionID, dispute: cur.ID})
	return *cur, nil
}

// applyDispute writes a report onto a dispute, as Core does: closed_at when it first reaches a final status.
func applyDispute(d *nilda.PaymentDispute, r nilda.PaymentDisputeReport, now time.Time) {
	d.ProviderRef, d.Stage, d.Status, d.Reason = r.ProviderRef, r.Stage, r.Status, r.Reason
	d.AmountMinor, d.Currency, d.FundsHeld, d.Refundable, d.URL = r.AmountMinor, r.Currency, r.FundsHeld, r.Refundable, r.URL
	d.RespondBy, d.EvidenceSubmittedAt, d.OpenedAt = utc(r.RespondBy), utc(r.EvidenceSubmittedAt), utc(r.OpenedAt)
	if nilda.DisputeFinal(d.Status) && d.ClosedAt == nil {
		t := now
		d.ClosedAt = &t
	}
	d.UpdatedAt = now
}

func sameDispute(a, b nilda.PaymentDispute) bool {
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC().Truncate(time.Microsecond) // Postgres keeps microseconds
	return &u
}

func (p *Payments) listDisputes(caller, sessionID string) ([]nilda.PaymentDispute, error) {
	if _, err := p.readSession(caller, sessionID); err != nil {
		return nil, err
	}
	out := []nilda.PaymentDispute{}
	for _, d := range p.Disputes() {
		if d.Session == sessionID {
			out = append(out, d)
		}
	}
	return out, nil
}

func (p *Payments) readDispute(caller, id string) (nilda.PaymentDispute, error) {
	d, ok := p.GetDispute(id)
	if !ok {
		return nilda.PaymentDispute{}, notFound("no dispute %q", id)
	}
	if _, err := p.readSession(caller, d.Session); err != nil {
		return nilda.PaymentDispute{}, notFound("no dispute %q", id)
	}
	return d, nil
}

// externalRefund records a refund made at the processor outside Nilda (Core's ReportExternalRefund): resolved at
// once, External, added to the session's refunded total, the consumer told. The same processor refund twice is
// one refund.
func (p *Payments) externalRefund(caller, sessionID string, body []byte) (nilda.PaymentRefund, error) {
	if err := p.may(caller, "payment_gateway"); err != nil {
		return nilda.PaymentRefund{}, err
	}
	var r nilda.PaymentExternalRefund
	if err := json.Unmarshal(body, &r); err != nil {
		return nilda.PaymentRefund{}, invalid("the body is not an external refund: %v", err)
	}
	if err := r.Validate(); err != nil {
		return nilda.PaymentRefund{}, invalid("%v", err)
	}
	currency := strings.ToUpper(strings.TrimSpace(r.Currency))
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[sessionID]
	if !ok || s.Gateway != caller {
		return nilda.PaymentRefund{}, notFound("no payment session %q", sessionID)
	}
	for _, rf := range p.refunds {
		if rf.Session == sessionID && rf.External && rf.ProviderRef == r.ProviderRef {
			return *rf, nil
		}
	}
	if s.Status != nilda.PaymentResolved {
		return nilda.PaymentRefund{}, &apiError{http.StatusConflict, "CONFLICT",
			fmt.Sprintf("only a resolved payment has refunds; this one is %s", s.Status)}
	}
	if currency != s.Currency {
		return nilda.PaymentRefund{}, invalid("the refund is in %s, the payment in %s", currency, s.Currency)
	}
	if s.RefundedMinor+r.AmountMinor > s.AmountMinor {
		return nilda.PaymentRefund{}, invalid("a refund of %d would take this payment's refunds past the %d paid",
			r.AmountMinor, s.AmountMinor)
	}
	now := time.Now().UTC()
	rf := &nilda.PaymentRefund{ID: newID(), Session: sessionID, AmountMinor: r.AmountMinor, Currency: currency,
		Status: nilda.RefundResolved, Reason: runes(r.Reason, maxPaymentText), ProviderRef: r.ProviderRef,
		External: true, CreatedAt: now, UpdatedAt: now}
	p.refunds[rf.ID] = rf
	s.RefundedMinor += rf.AmountMinor
	p.owe(owed{session: sessionID, refund: rf.ID})
	return *rf, nil
}

// secret keeps and reads a gateway's own secrets (Core's /payments/secrets/{name}).
func (p *Payments) secret(caller, method, name string, body []byte) (any, error) {
	if err := p.may(caller, "payment_gateway"); err != nil {
		return nil, err
	}
	if !validSecretName(name) {
		return nil, invalid("a secret's name is 1–64 lowercase letters, digits or underscores")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if method == http.MethodGet {
		v, ok := p.secrets[caller][name]
		if !ok {
			return nil, notFound("no secret %q", name)
		}
		return map[string]string{"value": v}, nil
	}
	var in struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &in); err != nil || in.Value == "" || len(in.Value) > 4096 {
		return nil, invalid("a secret's value is 1–4096 bytes")
	}
	if p.secrets[caller] == nil {
		p.secrets[caller] = map[string]string{}
	}
	p.secrets[caller][name] = in.Value
	return map[string]string{}, nil
}

func validSecretName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
