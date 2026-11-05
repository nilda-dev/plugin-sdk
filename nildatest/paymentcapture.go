package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// HOLD, THEN TAKE (plugin-sdk v0.58.0), as Core decides it (core's internal/payments/capture.go): a hold reported by
// the gateway, one capture of at most what is held, a void, a lost answer asked again with the same key, and a hold
// past its deadline closed after the gateway was asked to look. The consumer is owed the news of every move.

// The default and the longest deadline Core keeps for a hold: seven days, a card's hold online, when the gateway says
// none; thirty at most. Core's numbers.
const (
	holdLifetime    = 7 * 24 * time.Hour
	maxHoldLifetime = 30 * 24 * time.Hour
)

// captureRoutes answers a consumer's capture and void; ok=false when the request is neither.
func (p *Payments) captureRoutes(w http.ResponseWriter, r *http.Request, caller string, parts []string, body []byte) bool {
	if r.Method != http.MethodPost || len(parts) != 4 || parts[1] != "sessions" || (parts[3] != "capture" && parts[3] != "void") {
		return false
	}
	ctx := r.Context()
	p.keyed(w, r, caller, body, func() (int, any, error) {
		if parts[3] == "void" {
			s, err := p.void(ctx, caller, parts[2])
			return http.StatusOK, s, err
		}
		var b struct {
			AmountMinor int64 `json:"amount_minor"`
		}
		if err := json.Unmarshal(body, &b); err != nil {
			return 0, nil, invalid("the body is not a capture: %v", err)
		}
		s, err := p.capture(ctx, caller, parts[2], b.AmountMinor)
		return http.StatusOK, s, err
	})
	return true
}

// capture is Core's Capture: the consumer's own authorized session, one capture of at most what is held, asked of the
// gateway once.
func (p *Payments) capture(ctx context.Context, caller, id string, amount int64) (nilda.PaymentSession, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.PaymentSession{}, err
	}
	if amount <= 0 {
		return nilda.PaymentSession{}, invalid("a capture's amount must be a positive whole number of the currency's smallest unit")
	}
	p.mu.Lock()
	s, ok := p.sessions[id]
	if !ok || s.Consumer != caller {
		p.mu.Unlock()
		return nilda.PaymentSession{}, notFound("no payment session %q", id)
	}
	switch {
	case s.CaptureRequestedMinor > 0:
		if amount != s.CaptureRequestedMinor {
			p.mu.Unlock()
			return nilda.PaymentSession{}, &apiError{http.StatusConflict, "CONFLICT", fmt.Sprintf(
				"a capture of %d was already asked for this payment, and a payment has one capture", s.CaptureRequestedMinor)}
		}
	case s.VoidRequested:
		p.mu.Unlock()
		return nilda.PaymentSession{}, &apiError{http.StatusConflict, "CONFLICT", "this payment's hold is being let go; it cannot be captured"}
	case s.Status != nilda.PaymentAuthorized:
		p.mu.Unlock()
		return nilda.PaymentSession{}, conflict(s)
	case amount > s.AuthorizedMinor:
		p.mu.Unlock()
		return nilda.PaymentSession{}, invalid("a capture of %d is more than the %d held", amount, s.AuthorizedMinor)
	default:
		s.CaptureRequestedMinor = amount
		s.UpdatedAt = time.Now().UTC()
		p.holdDue[id] = true
	}
	p.mu.Unlock()
	return p.askHold(ctx, id)
}

// void is Core's Void: the consumer's own authorized session's hold let go.
func (p *Payments) void(ctx context.Context, caller, id string) (nilda.PaymentSession, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.PaymentSession{}, err
	}
	p.mu.Lock()
	s, ok := p.sessions[id]
	if !ok || s.Consumer != caller {
		p.mu.Unlock()
		return nilda.PaymentSession{}, notFound("no payment session %q", id)
	}
	switch {
	case s.Status == nilda.PaymentRejected && s.FailureCode == nilda.PaymentFailVoided:
		p.mu.Unlock()
		return *s, nil // let go already: twice is no error
	case s.CaptureRequestedMinor > 0:
		p.mu.Unlock()
		return nilda.PaymentSession{}, &apiError{http.StatusConflict, "CONFLICT", "this payment's capture was asked; it cannot be voided"}
	case s.VoidRequested:
	case s.Status != nilda.PaymentAuthorized:
		p.mu.Unlock()
		return nilda.PaymentSession{}, conflict(s)
	default:
		s.VoidRequested = true
		s.UpdatedAt = time.Now().UTC()
		p.holdDue[id] = true
	}
	p.mu.Unlock()
	return p.askHold(ctx, id)
}

// askHold asks the gateway the capture or void a session is owed, once at a time, and applies its answer. No answer
// keeps it owed — Deliver asks again, with the same key — and is a 503, as in Core.
func (p *Payments) askHold(ctx context.Context, id string) (nilda.PaymentSession, error) {
	p.mu.Lock()
	s := p.sessions[id]
	if !p.holdDue[id] {
		defer p.mu.Unlock()
		return *s, nil // answered meanwhile
	}
	if p.asking[id] {
		p.mu.Unlock()
		return nilda.PaymentSession{}, &apiError{http.StatusConflict, "CONFLICT",
			"this payment's gateway is being asked right now; ask again in a moment"}
	}
	p.asking[id] = true
	snapshot := *s
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		delete(p.asking, id)
		p.mu.Unlock()
	}()

	unanswered := func(why error) (nilda.PaymentSession, error) {
		return nilda.PaymentSession{}, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE", fmt.Sprintf(
			"the gateway %q did not answer (%v); Nilda asks it again with the same key", snapshot.Gateway, why)}
	}
	g, ok := p.installedGateway(snapshot.Gateway)
	if !ok {
		return unanswered(fmt.Errorf("it is not running"))
	}
	hook, payload := nilda.HookPaymentVoid, []byte(nil)
	if snapshot.CaptureRequestedMinor > 0 {
		hook = nilda.HookPaymentCapture
		payload, _ = json.Marshal(nilda.PaymentCaptureRequest{Session: snapshot, AmountMinor: snapshot.CaptureRequestedMinor,
			IdempotencyKey: nilda.CaptureKeyPrefix + id})
	} else {
		payload, _ = json.Marshal(nilda.PaymentVoidRequest{Session: snapshot, IdempotencyKey: nilda.VoidKeyPrefix + id})
	}
	gctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	out, handled, err := nilda.DispatchPaymentGatewayHook(gctx, nil, g, hook, payload)
	cancel()
	if err == nil && !handled {
		err = ErrNotSubscribed
	}
	if err != nil {
		return unanswered(err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.holdDue, id)
	if hook == nilda.HookPaymentCapture {
		var res nilda.PaymentCaptureResult
		if err := json.Unmarshal(out, &res); err != nil {
			p.holdDue[id] = true
			return unanswered(err)
		}
		p.takeAnswer(s, res.Status, res.AmountMinor, res.ProviderRef, res.FailureCode, res.FailureMessage)
		return *s, nil
	}
	var res nilda.PaymentVoidResult
	if err := json.Unmarshal(out, &res); err != nil {
		p.holdDue[id] = true
		return unanswered(err)
	}
	s.VoidRequested = false
	if res.Status == nilda.PaymentRejected {
		if res.ProviderRef != "" {
			s.ProviderRef = res.ProviderRef
		}
		s.FailureCode, s.FailureMessage = nilda.PaymentFailVoided, ""
		p.move(s, nilda.PaymentRejected)
		return *s, nil
	}
	p.takeAnswer(s, nilda.PaymentResolved, res.AmountMinor, res.ProviderRef, "", "")
	return *s, nil
}

// takeAnswer applies what the processor said about a hold's money: resolved at what it took (a different amount from
// the one asked holds the session pending for a person, as any resolution's would), pending, or rejected. Callers hold
// p.mu.
func (p *Payments) takeAnswer(s *nilda.PaymentSession, status string, amount int64, ref, code, message string) {
	if ref != "" {
		s.ProviderRef = ref
	}
	switch status {
	case nilda.PaymentResolved:
		if amount != expectedAmount(*s) {
			p.reported[s.ID] = reportedMoney{amount: amount, currency: s.Currency}
			s.FailureCode, s.FailureMessage = nilda.PaymentFailAmountMismatch, ""
			p.move(s, nilda.PaymentPending)
			return
		}
		s.AmountMinor = amount
		now := time.Now().UTC()
		s.ResolvedAt = &now
		p.move(s, nilda.PaymentResolved)
	case nilda.PaymentPending:
		p.move(s, nilda.PaymentPending)
	case nilda.PaymentRejected:
		s.FailureCode, s.FailureMessage = code, message
		p.move(s, nilda.PaymentRejected)
	}
}

// expectedAmount is what a resolution of s must say was taken: the capture asked for, or the payment's amount.
func expectedAmount(s nilda.PaymentSession) int64 {
	if s.CaptureRequestedMinor > 0 {
		return s.CaptureRequestedMinor
	}
	return s.AmountMinor
}

// authorize is a gateway's Authorize (Core's): a hold only on a session asked to be held, of exactly what it asked,
// with the processor's deadline (holdDeadline). A capture or void asked decides the hold: the same hold while the
// session is authorized, 409 once the ask moved it. A session held for a person (another amount was reported taken) is
// money taken, and refused. The same hold again, while nothing was asked, keeps a deadline it names. Callers hold p.mu.
func (p *Payments) authorize(s *nilda.PaymentSession, body []byte) (any, error) {
	var a nilda.PaymentAuthorization
	if err := json.Unmarshal(body, &a); err != nil {
		return nil, invalid("the body is not an authorization: %v", err)
	}
	if s.Capture != nilda.CaptureManual {
		return nil, invalid("this payment was asked to take the money at once, not to hold it; report its money with resolve")
	}
	if s.CaptureRequestedMinor > 0 || s.VoidRequested {
		if s.Status == nilda.PaymentAuthorized {
			return *s, nil // the same hold, its capture or void being asked
		}
		return nil, conflict(s)
	}
	if s.Status == nilda.PaymentPending && s.FailureCode == nilda.PaymentFailAmountMismatch {
		return nil, &apiError{http.StatusConflict, "CONFLICT", "the processor reported taking a different amount for " +
			"this payment, and a person decides it; a hold cannot replace money taken"}
	}
	if s.Status != nilda.PaymentAuthorized && !nilda.PaymentCanMove(s.Status, nilda.PaymentAuthorized) {
		return nil, conflict(s)
	}
	currency := strings.ToUpper(strings.TrimSpace(a.Currency))
	if a.AmountMinor != s.AmountMinor || currency != s.Currency {
		return nil, invalid("the processor holds %d %s and this payment asked %d %s; release that hold at the processor",
			a.AmountMinor, currency, s.AmountMinor, s.Currency)
	}
	now := time.Now().UTC()
	deadline := holdDeadline(now, a.ExpiresAt)
	if s.Status == nilda.PaymentAuthorized {
		if a.ExpiresAt == nil || !newDeadline(now, s.AuthorizationExpiresAt, deadline) {
			return *s, nil // the same hold again
		}
		s.AuthorizationExpiresAt, s.UpdatedAt = &deadline, now
		p.owe(owed{session: s.ID})
		return *s, nil
	}
	s.AuthorizedMinor, s.AuthorizationExpiresAt = a.AmountMinor, &deadline
	if a.ProviderRef != "" {
		s.ProviderRef = a.ProviderRef
	}
	return p.apply(s, nilda.PaymentAuthorized)
}

// holdDeadline is Core's: seven days on when the processor names none, thirty at most, and now when its deadline has
// passed already — the hold lapses at the next sweep rather than standing for a week the processor does not give.
func holdDeadline(now time.Time, at *time.Time) time.Time {
	switch {
	case at == nil:
		return now.Add(holdLifetime)
	case !at.After(now):
		return now
	case at.After(now.Add(maxHoldLifetime)):
		return now.Add(maxHoldLifetime)
	}
	return at.UTC()
}

// newDeadline is Core's: a repeated hold's deadline says something new when it is another time (to the microsecond
// Core's database keeps) and not both passed already.
func newDeadline(now time.Time, kept *time.Time, deadline time.Time) bool {
	if kept == nil {
		return true
	}
	if !deadline.After(now) && !kept.After(now) {
		return false
	}
	return !deadline.Truncate(time.Microsecond).Equal(kept.Truncate(time.Microsecond))
}

// askHoldsDue asks every gateway the captures and voids it has not answered — Core's delivery job does the same.
func (p *Payments) askHoldsDue(ctx context.Context) {
	p.mu.Lock()
	due := make([]string, 0, len(p.holdDue))
	for id := range p.holdDue {
		due = append(due, id)
	}
	p.mu.Unlock()
	for _, id := range due {
		_, _ = p.askHold(ctx, id) // an unanswered one stays owed
	}
}

// ExpireHold runs Core's expiry for one hold, as if its AuthorizationExpiresAt had passed: the gateway is asked to look
// the payment up first (payment.sync, if it is a nilda.PaymentSyncer) — a capture made at the processor is money, and is
// reported — and a session still authorized afterwards ends rejected, FailureCode expired. One whose gateway could not
// look is left for the next sweep (the error is returned); one whose capture or void is still being asked waits for
// that answer. A session that holds nothing is refused.
func (p *Payments) ExpireHold(ctx context.Context, id string) error {
	p.mu.Lock()
	s, ok := p.sessions[id]
	switch {
	case !ok:
		p.mu.Unlock()
		return fmt.Errorf("nildatest: no payment session %q", id)
	case s.Status != nilda.PaymentAuthorized:
		p.mu.Unlock()
		return fmt.Errorf("nildatest: a %s session holds nothing to expire", s.Status)
	case p.holdDue[id]:
		p.mu.Unlock()
		return fmt.Errorf("nildatest: this hold's capture or void is still being asked; its answer decides it")
	}
	p.mu.Unlock()
	if err := p.Sync(ctx, id); err != nil && !errors.Is(err, ErrNotSubscribed) {
		return fmt.Errorf("nildatest: the gateway could not look the hold up, so it is asked again later: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.Status == nilda.PaymentAuthorized && !p.holdDue[id] {
		s.FailureCode, s.FailureMessage = nilda.PaymentFailExpired, ""
		p.move(s, nilda.PaymentRejected)
	}
	return nil
}

// CapturePayment answers with CaptureResult — the zero value says the processor took what was asked — or CaptureErr,
// and records the request.
func (g *StubGateway) CapturePayment(_ context.Context, req nilda.PaymentCaptureRequest) (nilda.PaymentCaptureResult, error) {
	g.Captured = append(g.Captured, req)
	if g.CaptureErr != nil {
		return nilda.PaymentCaptureResult{}, g.CaptureErr
	}
	if g.CaptureResult.Status == "" {
		return nilda.PaymentCaptureResult{Status: nilda.PaymentResolved, AmountMinor: req.AmountMinor,
			ProviderRef: "stub_" + req.Session.ID}, nil
	}
	return g.CaptureResult, nil
}

// VoidPayment answers with VoidResult — the zero value says the hold is released — or VoidErr, and records the request.
func (g *StubGateway) VoidPayment(_ context.Context, req nilda.PaymentVoidRequest) (nilda.PaymentVoidResult, error) {
	g.Voided = append(g.Voided, req)
	if g.VoidErr != nil {
		return nilda.PaymentVoidResult{}, g.VoidErr
	}
	if g.VoidResult.Status == "" {
		return nilda.PaymentVoidResult{Status: nilda.PaymentRejected}, nil
	}
	return g.VoidResult, nil
}
