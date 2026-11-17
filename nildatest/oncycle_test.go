package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Core (cycle.go duplicateCycle) books late money on an EXPIRED on-session cycle payment as a duplicate of the
// charge that took the cycle meanwhile. Does the kit?
func TestLateMoneyOnAnExpiredOnSessionPaymentIsTheCyclesDuplicate(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	p := params()
	p.Agreement, p.Cycle = "sub_9", 2
	a, err := w.shopAPI.CreateSession(ctx, "pay-page-1", p)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.pay.Expire(a.ID); err != nil {
		t.Fatal(err)
	}
	w.gw.SavedStart = nilda.PaymentStartResult{}
	b, err := w.shopAPI.ChargeSaved(ctx, "sub_9-2-a2", nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-2",
		AmountMinor: 1234, Currency: "EUR", Agreement: "sub_9", Cycle: 2})
	if err != nil || b.Status != nilda.PaymentResolved {
		t.Fatalf("dunning B: %+v %v", b, err)
	}
	late, err := w.gwAPI.Resolve(ctx, a.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	t.Logf("late on-session money: status=%s duplicate_of=%q err=%v (Core: duplicate_of=%s)", late.Status, late.DuplicateOf, err, b.ID)
	if late.DuplicateOf != b.ID {
		t.Errorf("kit diverges from Core: late money on an expired on-session cycle payment is not booked as a duplicate")
	}
}

// The reverse: an on-session payment HOLDS the cycle; a kept charge that had expired gets late money.
func TestAKeptChargesLateMoneyIsADuplicateOfAnOnSessionHolder(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	charge := nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-2", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_9", Cycle: 2}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_a"}
	a, err := w.shopAPI.ChargeSaved(ctx, "sub_9-2-a", charge)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.pay.ExpireByHand(a.ID); err != nil {
		t.Fatal(err)
	}
	p := params()
	p.Agreement, p.Cycle = "sub_9", 2
	b, err := w.shopAPI.CreateSession(ctx, "pay-page-1", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.Resolve(ctx, b.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	late, err := w.gwAPI.Resolve(ctx, a.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR", ProviderRef: "pi_a"})
	t.Logf("late kept money while on-session holds: status=%s duplicate_of=%q err=%v (Core: duplicate_of=%s)", late.Status, late.DuplicateOf, err, b.ID)
	if late.DuplicateOf != b.ID {
		t.Errorf("kit diverges from Core: an on-session holder is not seen as the cycle's holder")
	}
}
