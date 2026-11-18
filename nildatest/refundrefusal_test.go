package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// As Core (renewal recovery, lock-B round 6, P10): a refund refused for being past what the payment can still give
// back carries the numbers it was decided on, and nilda.RefundRefusalOf reads them — so a consumer never has to guess
// what the payer's bank returned from a sentence that says only "part".
func TestARefusedRefundNamesWhatIsStillRefundable(t *testing.T) {
	w, s := paidWorld(t) // 12.34 EUR
	ctx := context.Background()
	lost := report(nilda.DisputeLost, nilda.DisputeChargeback, false)
	lost.AmountMinor = 400
	if _, err := w.gwAPI.ReportDispute(ctx, s.ID, lost); err != nil {
		t.Fatal(err)
	}
	if _, err := w.shopAPI.CreateRefund(ctx, "r0", s.ID, nilda.PaymentRefundParams{AmountMinor: 100}); err != nil {
		t.Fatal(err)
	}
	_, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1234})
	r, ok := nilda.RefundRefusalOf(err)
	if !ok {
		t.Fatalf("a refused refund the consumer cannot read the numbers of: %v", err)
	}
	want := nilda.RefundRefusal{Reason: nilda.RefundRefusalLostChargeback, PaidMinor: 1234, ChargedBackMinor: 400,
		RefundedMinor: 100, RefundableMinor: 734, Currency: s.Currency}
	if r != want {
		t.Fatalf("RefundRefusalOf = %+v, want %+v", r, want)
	}

	// Past the payment with no chargeback: the plain reason, and what is left.
	v, o := paidWorld(t)
	_, err = v.shopAPI.CreateRefund(ctx, "r2", o.ID, nilda.PaymentRefundParams{AmountMinor: 1235})
	r, ok = nilda.RefundRefusalOf(err)
	if !ok || r.Reason != nilda.RefundRefusalExceedsPaid || r.ChargedBackMinor != 0 || r.RefundableMinor != 1234 {
		t.Fatalf("a refund past the payment: %+v %v (%v)", r, ok, err)
	}

	// Anything else is not a refusal of this kind.
	if _, ok := nilda.RefundRefusalOf(nil); ok {
		t.Fatal("nil read as a refund refusal")
	}
}
