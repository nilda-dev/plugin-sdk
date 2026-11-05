package nildatest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// atReader is Core's in-person rule for a session (v0.58.0, EXPERIMENTAL, door D5): a session naming a reader only on
// a method that takes cards in person and lists that reader; a method that takes cards in person takes no session
// without one — its payer is at a till, not on a page.
func atReader(m nilda.PaymentMethod, params nilda.PaymentSessionParams) error {
	inPerson := nilda.Supports(nilda.SupportsOf(m), nilda.SupportCardPresent)
	switch {
	case params.Reader == "" && inPerson:
		return invalid("%q of %q takes a card at a reader; name one of its readers", params.Method, params.Gateway)
	case params.Reader == "":
		return nil
	case !inPerson:
		return invalid("%q of %q does not take a card in person", params.Method, params.Gateway)
	case !slices.ContainsFunc(m.Readers, func(r nilda.PaymentReader) bool { return r.ID == params.Reader }):
		return invalid("%q of %q has no reader %q", params.Method, params.Gateway, params.Reader)
	}
	return nil
}

// voidable is Core's rule for what a void may let go: a hold (authorized), or a payment still waiting at its card reader
// (pending, in person) — never one held for a person because the processor took another amount.
func voidable(s nilda.PaymentSession) bool {
	return s.Status == nilda.PaymentAuthorized ||
		(s.Status == nilda.PaymentPending && s.Reader != "" && s.FailureCode != nilda.PaymentFailAmountMismatch)
}

// Core's figures for a reader left waiting (core's internal/payments/inperson.go): a reader's payment waits readerWait
// when the gateway names no deadline, at most maxReaderWait; a void it does not answer is asked maxReaderVoidAsks
// times, then left for a person.
const (
	readerWait        = 5 * time.Minute
	maxReaderWait     = 30 * time.Minute
	maxReaderVoidAsks = 8
)

// readerDeadline is Core's: the gateway's deadline from now to at most maxReaderWait on, or readerWait on when it names
// none or one already past.
func readerDeadline(now time.Time, at *time.Time) time.Time {
	switch {
	case at == nil || !at.After(now):
		return now.Add(readerWait).UTC()
	case at.After(now.Add(maxReaderWait)):
		return now.Add(maxReaderWait).UTC()
	}
	return at.UTC()
}

// voidGivenUp is Core's: a reader's payment whose void the kit stopped asking — still waiting, its void asked, nobody
// asking it — a person's to decide. Callers hold p.mu.
func (p *Payments) voidGivenUp(s nilda.PaymentSession) bool {
	return s.Status == nilda.PaymentPending && s.Reader != "" && s.VoidRequested && !p.holdDue[s.ID]
}

// voidAskAfter is Core's (capture.go): after s moved from status was, a void asked of it stays only while it has
// something to let go — dropped once the payment is over; handed on, owed from now, when a payment whose start had not
// answered now waits at its reader. Callers hold p.mu.
func (p *Payments) voidAskAfter(s *nilda.PaymentSession, was string) {
	if !s.VoidRequested {
		return
	}
	switch {
	case s.Status == nilda.PaymentCreated:
	case voidable(*s):
		if was == nilda.PaymentCreated && !p.holdDue[s.ID] {
			p.holdDue[s.ID], p.holdAsks[s.ID] = true, 0
		}
	default:
		s.VoidRequested = false
		delete(p.holdDue, s.ID)
		delete(p.holdAsks, s.ID)
	}
}

// ExpireReader runs Core's expiry for one payment left waiting at its card reader, as if its ExpiresAt had passed: the
// gateway is asked to look it up first (payment.sync, if it is a nilda.PaymentSyncer — what it finds it reports), and a
// payment still waiting afterwards is called off, a void asked of the gateway as the till's Cancel is. A void that goes
// unanswered stays owed (Deliver asks again, at most maxReaderVoidAsks times in all, then leaves it for a person —
// Decide). A session that is not waiting at a reader, or whose void was asked already, is refused.
func (p *Payments) ExpireReader(ctx context.Context, id string) error {
	p.mu.Lock()
	s, ok := p.sessions[id]
	switch {
	case !ok:
		p.mu.Unlock()
		return fmt.Errorf("nildatest: no payment session %q", id)
	case s.Reader == "" || !voidable(*s):
		p.mu.Unlock()
		return fmt.Errorf("nildatest: a %s session is not waiting at a card reader", s.Status)
	case s.VoidRequested:
		p.mu.Unlock()
		return fmt.Errorf("nildatest: this payment's void was asked already; its answer decides it")
	}
	p.mu.Unlock()
	_ = p.Sync(ctx, id) // Core does not wait for a gateway that cannot look: the void asks it
	_, err := p.callOffReader(ctx, id)
	var ae *apiError
	if errors.As(err, &ae) && ae.code == "UNAVAILABLE" {
		return nil // owed, asked again by Deliver
	}
	return err
}

// callOffReader is Core's: a void the kit asks of a payment waiting at its reader on its own account (past its
// deadline, or for a person's reject), under the same key as the till's Cancel. One no longer waiting, or whose void
// is asked already or was given up, is answered as it stands; one whose void is owed is asked again now.
func (p *Payments) callOffReader(ctx context.Context, id string) (nilda.PaymentSession, error) {
	p.mu.Lock()
	s, ok := p.sessions[id]
	if !ok {
		p.mu.Unlock()
		return nilda.PaymentSession{}, notFound("no payment session %q", id)
	}
	if s.Reader != "" && voidable(*s) && !s.VoidRequested {
		s.VoidRequested, s.UpdatedAt = true, time.Now().UTC()
		p.holdDue[id], p.holdAsks[id] = true, 0
	}
	p.mu.Unlock()
	return p.askHold(ctx, id)
}

// maxReplaced is Core's bound on how many updates back chargeKind follows.
const maxReplaced = 16

// chargeKind is Core's: the kind of a charge of kept, OffSessionOf down the chain of updates it replaces, each read for
// kept's consumer only. Callers hold p.mu.
func (p *Payments) chargeKind(kept *savedMethod) string {
	chain := []nilda.PaymentMandate{kept.Mandate}
	for cur := kept; cur.Mandate.Purpose == nilda.MandateUpdate && len(chain) <= maxReplaced; {
		prev, ok := p.saved[cur.Mandate.Replaces]
		if !ok || prev.Consumer != kept.Consumer {
			break
		}
		chain = append(chain, prev.Mandate)
		cur = prev
	}
	kind := ""
	for i := len(chain) - 1; i >= 0; i-- {
		kind = nilda.OffSessionOf(chain[i], kind)
	}
	return kind
}

// keepsRhythm is Core's: a new update names a kept method of this consumer, and an interval exactly when the agreement
// it replaces is a recurring one.
func (p *Payments) keepsRhythm(consumer string, m *nilda.PaymentMandate) error {
	if m == nil || m.Purpose != nilda.MandateUpdate {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	replaced, ok := p.saved[m.Replaces]
	if !ok || replaced.Consumer != consumer {
		return notFound("no kept payment method %q", m.Replaces)
	}
	if (m.Interval != "") != (p.chargeKind(replaced) == nilda.OffSessionRecurring) {
		return invalid("an update keeps the rhythm of the agreement it replaces: an interval exactly when that " +
			"agreement is a recurring one")
	}
	return nil
}
