package nildatest

import (
	"context"
	"strings"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// keptForRenewals is a resolved Save session's kept method, the shop holding it, and the clock a month on.
func keptForRenewals(t *testing.T, w world) string {
	t.Helper()
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportSaveForLater}}}
	p := params()
	p.Save = mandate()
	s, err := w.shopAPI.CreateSession(ctx, "k-save", p)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa •••• 4242"}})
	if err != nil {
		t.Fatal(err)
	}
	month := time.Now().AddDate(0, 1, 0)
	w.pay.Now = func() time.Time { return month }
	return resolved.SavedMethod
}

// DOOR D5: a renewal is named by agreement and cycle, and one cycle is charged once — whatever key the retry carries —
// until its charge is refused, when it may be charged again.
func TestOneCycleOfAnAgreementIsChargedOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := keptForRenewals(t, w)
	charge := nilda.PaymentChargeParams{SavedMethod: id, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_1", Cycle: 1}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentRejected, FailureCode: nilda.PaymentFailDeclined,
		FailureMessage: "declined"}
	first, err := w.shopAPI.ChargeSaved(ctx, "sub_1-1-a", charge)
	if err != nil || first.Status != nilda.PaymentRejected || first.Agreement != "sub_1" || first.Cycle != 1 {
		t.Fatalf("the first attempt: %+v, %v", first, err)
	}
	w.gw.SavedStart = nilda.PaymentStartResult{}
	second, err := w.shopAPI.ChargeSaved(ctx, "sub_1-1-b", charge)
	if err != nil || second.Status != nilda.PaymentResolved {
		t.Fatalf("a cycle whose charge was refused could not be charged again: %+v, %v", second, err)
	}
	_, err = w.shopAPI.ChargeSaved(ctx, "sub_1-1-c", charge)
	if err == nil || httpStatus(t, err) != 409 || !strings.Contains(err.Error(), second.ID) {
		t.Fatalf("a paid cycle was charged a second time under a new key: %v", err)
	}
}

// The cycle is the consumer's agreement's, not one kept method's: a subscription whose card was replaced still pays a
// cycle once, as Core's index holds it.
func TestAReplacedCardPaysACycleOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	first := keptForRenewals(t, w)
	w.gw.SavedStart = nilda.PaymentStartResult{}
	p := params()
	p.Save = mandate()
	s, err := w.shopAPI.CreateSession(ctx, "k-save-2", p)
	if err != nil {
		t.Fatal(err)
	}
	second, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_2", Label: "Visa •••• 1881"}})
	if err != nil {
		t.Fatal(err)
	}
	charge := nilda.PaymentChargeParams{SavedMethod: first, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_1", Cycle: 1}
	paid, err := w.shopAPI.ChargeSaved(ctx, "sub_1-1-old", charge)
	if err != nil || paid.Status != nilda.PaymentResolved {
		t.Fatalf("the cycle on the first card: %+v, %v", paid, err)
	}
	charge.SavedMethod = second.SavedMethod
	if _, err := w.shopAPI.ChargeSaved(ctx, "sub_1-1-new", charge); err == nil || httpStatus(t, err) != 409 {
		t.Fatalf("the same cycle on the replacement card: %v", err)
	}
}
