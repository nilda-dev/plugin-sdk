package nildatest

import (
	"context"
	"errors"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// THE REVIEW OF DOOR D5 (2026-11-05) in the kit, held to Core's rules: a charge's kind is its agreement's purpose; a
// void is never sent for a payment that is over; a reader's payment is called off past its deadline; the till's Cancel
// sent before the start's answer is handed on; a person's reject stops the reader first.

func keepFor(t *testing.T, w world, key string, m *nilda.PaymentMandate) (nilda.PaymentSession, error) {
	t.Helper()
	ctx := context.Background()
	p := params()
	p.AmountMinor, p.Save = 0, m
	s, err := w.pay.CreateSession(ctx, "shop", p)
	if err != nil {
		return nilda.PaymentSession{}, err
	}
	r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_" + key, Label: "Visa •••• 4242"}})
	if err != nil || r.SavedMethod == "" {
		t.Fatalf("%s: kept: %+v, %v", key, r, err)
	}
	return r, nil
}

func updateOf(replaces string, monthly bool) *nilda.PaymentMandate {
	m := &nilda.PaymentMandate{Purpose: nilda.MandateUpdate, Text: "The new card pays what the old one paid.",
		AmountMinor: 1234, Currency: "EUR", Replaces: replaces}
	if monthly {
		m.Interval, m.IntervalCount = nilda.IntervalMonth, 1
	}
	return m
}

func kitStatus(t *testing.T, err error) int {
	t.Helper()
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("want the kit's API error, got %v", err)
	}
	return ae.status
}

func TestAChargesKindIsItsAgreementsPurposeInTheKit(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportSaveForLater}}}
	sub, err := keepFor(t, w, "k-sub", mandate())
	if err != nil {
		t.Fatal(err)
	}
	pre, err := keepFor(t, w, "k-pre", &nilda.PaymentMandate{Purpose: nilda.MandatePreOrder, Text: "€12.34 when it ships.",
		AmountMinor: 1234, Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		m    *nilda.PaymentMandate
		want int
	}{
		"an update of a method nobody kept":            {updateOf("01a0d2d2-64f9-79c4-b460-0369f4fce253", true), 404},
		"an update of a subscription with no interval": {updateOf(sub.SavedMethod, false), 422},
		"an update of a pre-order with an interval":    {updateOf(pre.SavedMethod, true), 422},
	} {
		if _, err := keepFor(t, w, name, c.m); err == nil || kitStatus(t, err) != c.want {
			t.Errorf("%s: %v, want %d", name, err, c.want)
		}
	}
	updSub, err := keepFor(t, w, "k-upd-sub", updateOf(sub.SavedMethod, true))
	if err != nil {
		t.Fatal(err)
	}
	updPre, err := keepFor(t, w, "k-upd-pre", updateOf(pre.SavedMethod, false))
	if err != nil {
		t.Fatal(err)
	}
	// What an older Core kept: a pre-order's agreement naming an interval. It reads, and its kind is its purpose's.
	w.pay.mu.Lock()
	w.pay.saved[pre.SavedMethod].Mandate.Interval, w.pay.saved[pre.SavedMethod].Mandate.IntervalCount = nilda.IntervalMonth, 1
	w.pay.mu.Unlock()
	w.pay.Now = func() time.Time { return time.Now().AddDate(0, 1, 0) }
	for _, c := range []struct{ name, method, want string }{
		{"legacy-pre", pre.SavedMethod, nilda.OffSessionUnscheduled},
		{"upd-sub", updSub.SavedMethod, nilda.OffSessionRecurring},
		{"upd-pre", updPre.SavedMethod, nilda.OffSessionUnscheduled},
	} {
		s, err := w.shopAPI.ChargeSaved(ctx, "charge-"+c.name, nilda.PaymentChargeParams{SavedMethod: c.method,
			Reference: c.name, AmountMinor: 1234, Currency: "EUR"})
		if err != nil || s.OffSession != c.want {
			t.Errorf("%s: off_session %q (%v), want %q", c.name, s.OffSession, err, c.want)
		}
	}
	// A method that now takes cards only at a reader is never charged with the payer absent.
	w.gw.Methods[0].Supports = append(w.gw.Methods[0].Supports, nilda.SupportCardPresent)
	w.gw.Methods[0].Readers = []nilda.PaymentReader{{ID: "tmr_front", Label: "Front counter"}}
	if _, err := w.shopAPI.ChargeSaved(ctx, "charge-at-reader", nilda.PaymentChargeParams{SavedMethod: updPre.SavedMethod,
		Reference: "at-reader", AmountMinor: 1234, Currency: "EUR"}); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a kept method charged on a reader's method: %v", err)
	}
}

// A void whose answer was lost is never sent for a payment that ended meanwhile (Core's askHold): the payer paid, so
// the session stays resolved, and nobody is told void_requested beside it.
func TestAVoidOwedToAPaymentThatEndedIsNeverSent(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil {
		t.Fatal(err)
	}
	w.gw.VoidErr = errors.New("the processor did not answer")
	if _, err := w.shopAPI.Void(ctx, "cancel-1", s.ID); err == nil || httpStatus(t, err) != 503 {
		t.Fatalf("an unanswered void: %v", err)
	}
	w.gw.VoidErr = nil
	paid, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 450, Currency: "EUR"})
	if err != nil || paid.Status != nilda.PaymentResolved || paid.VoidRequested {
		t.Fatalf("paid before the void reached the reader: %+v, %v", paid, err)
	}
	// The same, reached another way: a session no longer voidable when the delivery job asks.
	s2, err := w.shopAPI.CreateSession(ctx, "till-2#1", atTill())
	if err != nil {
		t.Fatal(err)
	}
	w.gw.VoidErr = errors.New("the processor did not answer")
	_, _ = w.shopAPI.Void(ctx, "cancel-2", s2.ID)
	w.gw.VoidErr = nil
	w.pay.mu.Lock()
	w.pay.sessions[s2.ID].Status = nilda.PaymentResolved
	w.pay.mu.Unlock()
	asked := len(w.gw.Voided)
	w.pay.Deliver(ctx)
	got, _ := w.pay.GetSession(s2.ID)
	if len(w.gw.Voided) != asked || got.Status != nilda.PaymentResolved || got.FailureCode != "" || got.VoidRequested {
		t.Fatalf("a void sent for a paid payment: asked %d→%d, %+v", asked, len(w.gw.Voided), got)
	}
}

// A reader's payment waits minutes, then is looked up and called off (ExpireReader); a reader that will not stop is
// asked maxReaderVoidAsks times and left for a person, who rejects it.
func TestAReaderLeftWaitingIsCalledOffInTheKit(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil || s.ExpiresAt == nil || s.ExpiresAt.After(time.Now().Add(readerWait+time.Minute)) {
		t.Fatalf("a reader's payment waits at most readerWait: %+v, %v", s.ExpiresAt, err)
	}
	if err := w.pay.ExpireReader(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.pay.GetSession(s.ID); got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailVoided {
		t.Fatalf("a reader left waiting: %+v", got)
	}
	stuck, err := w.shopAPI.CreateSession(ctx, "till-2#1", atTill())
	if err != nil {
		t.Fatal(err)
	}
	w.gw.VoidErr = errors.New("the reader did not answer")
	_ = w.pay.ExpireReader(ctx, stuck.ID)
	for range maxReaderVoidAsks + 2 {
		w.pay.Deliver(ctx)
	}
	if got, _ := w.pay.GetSession(stuck.ID); got.Status != nilda.PaymentPending || !got.VoidRequested ||
		len(w.gw.Voided) != 1+maxReaderVoidAsks {
		t.Fatalf("a reader that will not stop: %+v, asked %d", got, len(w.gw.Voided))
	}
	decided, err := w.pay.Decide(stuck.ID, "reject", "the reader was unplugged")
	if err != nil || decided.Status != nilda.PaymentRejected || decided.FailureCode != nilda.PaymentFailCancelled || decided.VoidRequested {
		t.Fatalf("a person rejects what the reader would not stop: %+v, %v", decided, err)
	}
}

// cancelledAtStart is a reader gateway whose start answer arrives only after the till's Cancel was sent.
type cancelledAtStart struct {
	StubGateway
	cancel func(id string)
}

func (g *cancelledAtStart) StartPayment(ctx context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	g.cancel(s.ID)
	return g.StubGateway.StartPayment(ctx, s)
}

func TestATillsCancelBeforeTheStartsAnswerIsHandedOnInTheKit(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	var during nilda.PaymentSession
	var duringErr error
	g := &cancelledAtStart{StubGateway: *w.gw}
	g.cancel = func(id string) { during, duringErr = w.shopAPI.Void(ctx, "cancel-"+id, id) }
	w.pay.Gateway("testpay", g)
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if duringErr != nil || during.Status != nilda.PaymentCreated || !during.VoidRequested {
		t.Fatalf("the till's cancel before the start answered: %+v, %v", during, duringErr)
	}
	if err != nil || s.Status != nilda.PaymentRejected || s.FailureCode != nilda.PaymentFailVoided || len(g.Voided) != 1 {
		t.Fatalf("the cancel handed to the reader: %+v, %v, asked %d", s, err, len(g.Voided))
	}
}

// A person's reject of a payment waiting at its reader stops the reader first; no answer is an error and the void stays
// owed.
func TestAPersonsRejectStopsTheReaderInTheKit(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil {
		t.Fatal(err)
	}
	w.gw.VoidErr = errors.New("the reader did not answer")
	if _, err := w.pay.Decide(s.ID, "reject", "the payer left"); err == nil || len(w.gw.Voided) != 1 {
		t.Fatalf("a reject whose void went unanswered: %v, asked %d", err, len(w.gw.Voided))
	}
	if got, _ := w.pay.GetSession(s.ID); got.Status != nilda.PaymentPending || !got.VoidRequested {
		t.Fatalf("the void stays owed: %+v", got)
	}
	w.gw.VoidErr = nil
	decided, err := w.pay.Decide(s.ID, "reject", "the payer left")
	if err != nil || decided.Status != nilda.PaymentRejected || decided.FailureCode != nilda.PaymentFailVoided || len(w.gw.Voided) != 2 {
		t.Fatalf("a reject that stopped the reader: %+v, %v, asked %d", decided, err, len(w.gw.Voided))
	}
}
