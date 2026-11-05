package nildatest

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// HOLD, THEN TAKE (v0.58.0): the kit refuses what Core refuses about a hold — a capture above what is held, a second
// capture of another amount, a refund before the capture, a capture or a void of a session that holds nothing — so a
// consumer tested here is not surprised on a real site.

// holding is a world whose card can hold money, with one manual session the gateway has reported held.
func holding(t *testing.T) (world, nilda.PaymentSession) {
	t.Helper()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportPartialRefund, nilda.SupportManualCapture}}}
	ctx := context.Background()
	p := params()
	p.Capture = nilda.CaptureManual
	s, err := w.shopAPI.CreateSession(ctx, "k1", p)
	if err != nil || s.Capture != nilda.CaptureManual {
		t.Fatalf("a manual session: %+v, %v", s, err)
	}
	deadline := time.Now().Add(7 * 24 * time.Hour).UTC().Truncate(time.Second)
	held, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "eur",
		ProviderRef: "pi_held", ExpiresAt: &deadline})
	if err != nil || held.Status != nilda.PaymentAuthorized || held.AuthorizedMinor != 1234 ||
		held.AuthorizationExpiresAt == nil || !held.AuthorizationExpiresAt.Equal(deadline) {
		t.Fatalf("the hold as reported: %+v, %v", held, err)
	}
	return w, held
}

func TestCaptureNeverExceedsTheAuthorizedAmount(t *testing.T) {
	w, s := holding(t)
	ctx := context.Background()
	if _, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1235); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a capture above the hold: %v", err)
	}
	if len(w.gw.Captured) != 0 {
		t.Fatal("the gateway was asked to capture more than it holds")
	}
	got, err := w.shopAPI.Capture(ctx, "c2", s.ID, 1000)
	if err != nil || got.Status != nilda.PaymentResolved || got.AmountMinor != 1000 || got.CaptureRequestedMinor != 1000 ||
		got.AuthorizedMinor != 1234 {
		t.Fatalf("a partial capture resolves at what was taken: %+v, %v", got, err)
	}
	if len(w.gw.Captured) != 1 || w.gw.Captured[0].AmountMinor != 1000 ||
		w.gw.Captured[0].IdempotencyKey != nilda.CaptureKeyPrefix+s.ID {
		t.Fatalf("the gateway was asked %+v", w.gw.Captured)
	}
	// The same capture again, under another key, is the same capture: answered as it stands, never asked again.
	again, err := w.shopAPI.Capture(ctx, "c3", s.ID, 1000)
	if err != nil || again.Status != nilda.PaymentResolved || len(w.gw.Captured) != 1 {
		t.Fatalf("the same capture again: %+v, %v, asked %d times", again, err, len(w.gw.Captured))
	}
	// Another amount is a second capture, which the contract does not make.
	if _, err := w.shopAPI.Capture(ctx, "c4", s.ID, 200); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a second capture of another amount: %v", err)
	}
	// A refund is bounded by what was TAKEN, not what was held.
	if _, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1001}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a refund past what was captured: %v", err)
	}
	if _, err := w.shopAPI.CreateRefund(ctx, "r2", s.ID, nilda.PaymentRefundParams{AmountMinor: 1000}); err != nil {
		t.Fatalf("a refund of what was captured: %v", err)
	}
}

func TestOnlyAManualCaptureMethodMayBeAskedToHold(t *testing.T) {
	w := newWorld(t) // its card lists no manual_capture
	ctx := context.Background()
	p := params()
	p.Capture = nilda.CaptureManual
	if _, err := w.shopAPI.CreateSession(ctx, "k1", p); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a hold on a method that cannot hold: %v", err)
	}
	if len(w.pay.Sessions()) != 0 {
		t.Fatal("a refused hold left a session behind")
	}
	// Validate refuses what is not a mode, and a hold of nothing.
	p.Capture = "later"
	if p.Validate() == nil {
		t.Error("a capture mode outside the set validated")
	}
	p.Capture = nilda.CaptureManual
	p.AmountMinor = 0
	p.Save = &nilda.PaymentMandate{Purpose: nilda.MandatePreOrder, Text: "charged when it ships", AmountMinor: 1234, Currency: "EUR"}
	if p.Validate() == nil {
		t.Error("a hold of a session that only keeps a method validated")
	}
	// Automatic — said or left out — is every session's way, and holds nothing.
	a := params()
	s, err := w.shopAPI.CreateSession(ctx, "k2", a)
	if err != nil || s.Capture != nilda.CaptureAutomatic {
		t.Fatalf("an automatic session: %+v, %v", s, err)
	}
	if _, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR"}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a hold reported on a session asked to take the money at once: %v", err)
	}
	// A gateway that says a method holds and cannot be asked to capture is refused at describe, as Serve refuses it.
	cannot := &onlyStarts{methods: []nilda.PaymentMethod{{Key: "card", Label: "Card", Supports: []string{nilda.SupportManualCapture}}}}
	if _, _, err := nilda.DispatchPaymentGatewayHook(ctx, nil, cannot, nilda.HookPaymentDescribe, []byte(`{}`)); err == nil {
		t.Error("a gateway that cannot capture described a method that holds")
	}
}

// onlyStarts is a gateway with no capture: it starts and refunds, nothing else.
type onlyStarts struct{ methods []nilda.PaymentMethod }

func (g *onlyStarts) DescribePayments(context.Context) ([]nilda.PaymentMethod, error) {
	return g.methods, nil
}
func (g *onlyStarts) StartPayment(context.Context, nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	return nilda.PaymentStartResult{Status: nilda.PaymentPending}, nil
}
func (g *onlyStarts) RefundPayment(context.Context, nilda.PaymentRefund, nilda.PaymentSession) (nilda.PaymentRefundResult, error) {
	return nilda.PaymentRefundResult{Status: nilda.RefundPending}, nil
}

func TestAVoidedSessionEndsRejectedVoided(t *testing.T) {
	w, s := holding(t)
	ctx := context.Background()
	got, err := w.shopAPI.Void(ctx, "v1", s.ID)
	if err != nil || got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailVoided {
		t.Fatalf("a voided hold: %+v, %v", got, err)
	}
	if len(w.gw.Voided) != 1 || w.gw.Voided[0].IdempotencyKey != nilda.VoidKeyPrefix+s.ID {
		t.Fatalf("the gateway was asked %+v", w.gw.Voided)
	}
	// Twice is no error, and asks nothing more.
	if again, err := w.shopAPI.Void(ctx, "v2", s.ID); err != nil || again.FailureCode != nilda.PaymentFailVoided || len(w.gw.Voided) != 1 {
		t.Fatalf("voided again: %+v, %v", again, err)
	}
	if _, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1234); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a capture of a voided hold: %v", err)
	}
	// "voided" is Core's verdict: a gateway reporting it is refused before anything is sent.
	if err := (nilda.PaymentRejection{FailureCode: nilda.PaymentFailVoided}).Validate(); err == nil {
		t.Error("a gateway may report voided")
	}
	if w.pay.Deliver(ctx) != 0 || w.shop.heard[len(w.shop.heard)-1] != "session:rejected" {
		t.Fatalf("the consumer heard %v", w.shop.heard)
	}
}

func TestACaptureOrVoidOfASessionThatHoldsNothingIsRefused(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1234); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a capture of a redirected session: %v", err)
	}
	if _, err := w.shopAPI.Void(ctx, "v1", s.ID); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a void of a redirected session: %v", err)
	}
	// Only the consumer whose session it is.
	if _, err := w.gwAPI.Capture(ctx, "c2", s.ID, 1234); httpStatus(t, err) != http.StatusForbidden {
		t.Fatalf("a gateway capturing: %v", err)
	}
}

func TestRefundBeforeCaptureIsRefusedByTheKit(t *testing.T) {
	w, s := holding(t)
	if _, err := w.shopAPI.CreateRefund(context.Background(), "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a refund of money only held: %v", err)
	}
	if len(w.pay.Sessions()) != 1 {
		t.Fatal("the refusal changed the sessions")
	}
}

func TestALostCaptureAnswerIsAskedAgainWithTheSameKeyInTheKit(t *testing.T) {
	w, s := holding(t)
	ctx := context.Background()
	w.gw.CaptureErr = errors.New("the processor did not answer")
	if _, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1234); httpStatus(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("a capture nobody answered: %v", err)
	}
	held, _ := w.pay.GetSession(s.ID)
	if held.Status != nilda.PaymentAuthorized || held.CaptureRequestedMinor != 1234 {
		t.Fatalf("an unanswered capture is still asked: %+v", held)
	}
	asked := len(w.gw.Captured) // the client retries a 503 itself, under the same key: each one asked again
	w.gw.CaptureErr = nil
	w.pay.Deliver(ctx) // Core's job asks again
	got, _ := w.pay.GetSession(s.ID)
	if got.Status != nilda.PaymentResolved || len(w.gw.Captured) != asked+1 {
		t.Fatalf("asked again: %+v, asked %d then %d times", got, asked, len(w.gw.Captured))
	}
	for _, c := range w.gw.Captured {
		if c.IdempotencyKey != nilda.CaptureKeyPrefix+s.ID || c.AmountMinor != 1234 {
			t.Fatalf("a capture asked again under another key or amount: %+v", c)
		}
	}
	w.pay.Deliver(ctx)
	if len(w.gw.Captured) != asked+1 {
		t.Fatal("an answered capture was asked again")
	}
}

func TestAnExpiredHoldIsRejectedExpiredInTheKit(t *testing.T) {
	w, s := holding(t)
	if err := w.pay.ExpireHold(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := w.pay.GetSession(s.ID)
	if got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailExpired {
		t.Fatalf("a lapsed hold: %+v", got)
	}
	if err := w.pay.ExpireHold(context.Background(), s.ID); err == nil {
		t.Error("a session that holds nothing was expired as a hold")
	}
}

// A late hold report moves nothing once a capture or void was asked (Core's Authorize): the same hold while the session
// is still authorized, 409 once the ask moved it — never a slow capture taken back to authorized.
func TestALateHoldAfterACaptureOrVoidWasAskedMovesNothingInTheKit(t *testing.T) {
	ctx := context.Background()
	later := time.Now().Add(20 * 24 * time.Hour).UTC().Truncate(time.Second)
	w, s := holding(t)
	w.gw.CaptureResult = nilda.PaymentCaptureResult{Status: nilda.PaymentPending}
	if p, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1234); err != nil || p.Status != nilda.PaymentPending {
		t.Fatalf("a capture the processor decides later: %+v, %v", p, err)
	}
	if _, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR", ExpiresAt: &later}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a hold reported after its capture was asked: %v", err)
	}
	if got, _ := w.pay.GetSession(s.ID); got.Status != nilda.PaymentPending || !got.AuthorizationExpiresAt.Equal(*s.AuthorizationExpiresAt) {
		t.Fatalf("a late hold moved a capture in flight: %+v", got)
	}

	w, s = holding(t)
	w.gw.VoidErr = errors.New("the processor did not answer")
	_, _ = w.shopAPI.Void(ctx, "v1", s.ID)
	again, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR", ExpiresAt: &later})
	if err != nil || again.Status != nilda.PaymentAuthorized || !again.VoidRequested || !again.AuthorizationExpiresAt.Equal(*s.AuthorizationExpiresAt) {
		t.Fatalf("a hold reported while its void is asked: %+v, %v", again, err)
	}
}

// A session held for a person (the processor reported taking another amount) is money taken: a hold is refused and the
// reported amount stays, as in Core.
func TestAHoldNeverErasesMoneyTheProcessorReportedInTheKit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Supports: []string{nilda.SupportRefund, nilda.SupportManualCapture}}}
	p := params()
	p.Capture = nilda.CaptureManual
	s, err := w.shopAPI.CreateSession(ctx, "k1", p)
	if err != nil {
		t.Fatal(err)
	}
	if r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 999, Currency: "EUR"}); err != nil ||
		r.FailureCode != nilda.PaymentFailAmountMismatch {
		t.Fatalf("money of another amount: %+v, %v", r, err)
	}
	if _, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR"}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a hold on a payment held for a person: %v", err)
	}
	got, _ := w.pay.GetSession(s.ID)
	if got.Status != nilda.PaymentPending || got.FailureCode != nilda.PaymentFailAmountMismatch || got.AuthorizedMinor != 0 ||
		w.pay.reported[s.ID].amount != 999 {
		t.Fatalf("a hold erased the money the processor reported: %+v, %+v", got, w.pay.reported[s.ID])
	}
}

// A deadline already past is now, so the hold lapses at the next sweep; the same hold reported again with a deadline
// corrects the one kept; another amount on a held session is refused — Core's Authorize.
func TestAHoldsDeadlineIsTheProcessorsInTheKit(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Supports: []string{nilda.SupportRefund, nilda.SupportManualCapture}}}
	p := params()
	p.Capture = nilda.CaptureManual
	s, err := w.shopAPI.CreateSession(ctx, "k1", p)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).UTC()
	held, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR", ExpiresAt: &past})
	if err != nil || held.AuthorizationExpiresAt == nil || held.AuthorizationExpiresAt.After(time.Now()) || held.AuthorizationExpiresAt.Before(past) {
		t.Fatalf("a hold whose deadline has passed: %+v, %v", held, err)
	}

	w, s = holding(t) // seven days on
	real := time.Now().Add(2 * 24 * time.Hour).UTC().Truncate(time.Second)
	w.pay.Deliver(ctx)
	heard := len(w.shop.heard)
	again, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR", ExpiresAt: &real})
	if err != nil || !again.AuthorizationExpiresAt.Equal(real) {
		t.Fatalf("the processor's newer deadline: %+v, %v", again, err)
	}
	if none, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR"}); err != nil ||
		!none.AuthorizationExpiresAt.Equal(real) || !none.UpdatedAt.Equal(again.UpdatedAt) {
		t.Fatalf("the same hold with no deadline: %+v, %v", none, err)
	}
	w.pay.Deliver(ctx)
	if len(w.shop.heard) != heard+1 || w.shop.heard[heard] != "session:authorized" {
		t.Fatalf("the consumer heard %v", w.shop.heard)
	}
	if _, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 999, Currency: "EUR"}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a hold of another amount on a held session: %v", err)
	}
}
