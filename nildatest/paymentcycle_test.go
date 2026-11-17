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

// A late answer for a cycle paid meanwhile is booked as a duplicate of the charge that paid it, and the consumer hears
// it. The first charge had EXPIRED — by hand, as only a row edited outside Core can be (ExpireByHand); a REJECTED
// charge's money is refused instead (TestALateAnswerOnARejectedRenewalIsRefused), as Core does.
func TestALateAnswerForAPaidCycleIsBookedAsADuplicate(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	charge := nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_9", Cycle: 1}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_slow"}
	slow, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-a", charge)
	if err != nil || slow.Status != nilda.PaymentPending {
		t.Fatalf("the slow charge: %+v, %v", slow, err)
	}
	if err := w.pay.ExpireByHand(slow.ID); err != nil {
		t.Fatal(err)
	}
	w.gw.SavedStart = nilda.PaymentStartResult{}
	paid, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-b", charge)
	if err != nil || paid.Status != nilda.PaymentResolved {
		t.Fatalf("the retry: %+v, %v", paid, err)
	}
	late, err := w.gwAPI.Resolve(ctx, slow.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	if err != nil || late.Status != nilda.PaymentResolved || late.DuplicateOf != paid.ID {
		t.Fatalf("the late money: %+v, %v — want resolved as a duplicate of %s", late, err, paid.ID)
	}
	if s, _ := w.pay.GetSession(paid.ID); s.DuplicateOf != "" {
		t.Fatalf("the charge that paid the cycle became a duplicate: %+v", s)
	}
}

// cycleCase is the double's version of Core's setting for the duplicate rules: charge A of cycle 1 expired (by hand),
// dunning's B holds the cycle, still deciding, and A's money arrived — A is a duplicate of B.
type cycleCase struct {
	w      world
	charge nilda.PaymentChargeParams
	a, b   nilda.PaymentSession
}

func newCycleCase(t *testing.T, aMoney nilda.PaymentResolution) cycleCase {
	t.Helper()
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	c := cycleCase{w: w, charge: nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-1", AmountMinor: 1234,
		Currency: "EUR", Agreement: "sub_9", Cycle: 1}}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_a"}
	var err error
	if c.a, err = w.shopAPI.ChargeSaved(ctx, "sub_9-1-a", c.charge); err != nil {
		t.Fatal(err)
	}
	if err := w.pay.ExpireByHand(c.a.ID); err != nil {
		t.Fatal(err)
	}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_b"}
	if c.b, err = w.shopAPI.ChargeSaved(ctx, "sub_9-1-b", c.charge); err != nil || c.b.Status != nilda.PaymentPending {
		t.Fatalf("dunning's B: %+v, %v", c.b, err)
	}
	late, err := w.gwAPI.Resolve(ctx, c.a.ID, aMoney)
	if err != nil || late.DuplicateOf != c.b.ID {
		t.Fatalf("A's late money while B is still deciding: %+v, %v — want a duplicate of the deciding B, as Core", late, err)
	}
	w.pay.Deliver(ctx)
	return c
}

func (c cycleCase) get(t *testing.T, id string) nilda.PaymentSession {
	t.Helper()
	s, ok := c.w.pay.GetSession(id)
	if !ok {
		t.Fatalf("no session %s", id)
	}
	return s
}

var aMoney = nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR", ProviderRef: "pi_a"}

// (a) As Core: money reported on a REJECTED charge is refused 409 — rejected is final — and never booked as a
// duplicate; the charge stays rejected. (Core records the amount for the owner to refund at the processor.)
func TestALateAnswerOnARejectedRenewalIsRefused(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	charge := nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_9", Cycle: 1}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_slow"}
	slow, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-a", charge)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.Reject(ctx, slow.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); err != nil {
		t.Fatal(err)
	}
	w.gw.SavedStart = nilda.PaymentStartResult{}
	if _, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-b", charge); err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.Resolve(ctx, slow.ID, aMoney); httpStatus(t, err) != 409 {
		t.Fatalf("money on a rejected renewal charge: %v; want 409, as Core", err)
	}
	if s, _ := w.pay.GetSession(slow.ID); s.Status != nilda.PaymentRejected || s.DuplicateOf != "" {
		t.Fatalf("the rejected charge after late money: %+v; want rejected, no duplicate", s)
	}
}

// (b) As Core: a pending (no money) report on the expired A while B holds the cycle is refused and A stays expired.
func TestALatePendingForAHeldCycleIsRefused(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	method := keptForRenewals(t, w)
	charge := nilda.PaymentChargeParams{SavedMethod: method, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_9", Cycle: 1}
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_a"}
	a, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-a", charge)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.pay.ExpireByHand(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.shopAPI.ChargeSaved(ctx, "sub_9-1-b", charge); err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.MarkPending(ctx, a.ID, "pi_a"); httpStatus(t, err) != 409 {
		t.Fatalf("a pending report on the expired A while B holds the cycle: %v; want 409", err)
	}
	if s, _ := w.pay.GetSession(a.ID); s.Status != nilda.PaymentExpired || s.DuplicateOf != "" {
		t.Fatalf("A after it: %+v; want expired, no duplicate", s)
	}
}

// (c) Promotion, as Core: B ends without money, so A — paid, nothing refunded — becomes the cycle's own payment, its
// consumer told, and dunning's C is refused.
func TestADuplicateIsPromotedWhenItsHolderEnds(t *testing.T) {
	c := newCycleCase(t, aMoney)
	ctx := context.Background()
	heard := len(c.w.shop.heard)
	if _, err := c.w.gwAPI.Reject(ctx, c.b.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); err != nil {
		t.Fatal(err)
	}
	if a := c.get(t, c.a.ID); a.DuplicateOf != "" {
		t.Fatalf("A after B was rejected: duplicate_of %q; want the cycle's own payment", a.DuplicateOf)
	}
	c.w.pay.Deliver(ctx)
	if got := c.w.shop.heard[heard:]; len(got) != 2 {
		t.Fatalf("the consumer heard %v after B's rejection; want B rejected and A again", got)
	}
	c.w.gw.SavedStart = nilda.PaymentStartResult{}
	if _, err := c.w.shopAPI.ChargeSaved(ctx, "sub_9-1-c", c.charge); httpStatus(t, err) != 409 {
		t.Fatalf("dunning's C for a cycle A pays: %v; want 409", err)
	}
}

// (c) The retell, as Core: B resolves, so A's consumer is told again — A is a duplicate of a charge that has paid.
func TestADuplicatesConsumerIsToldAgainWhenItsHolderResolves(t *testing.T) {
	c := newCycleCase(t, aMoney)
	ctx := context.Background()
	heard := len(c.w.shop.heard)
	if _, err := c.w.gwAPI.Resolve(ctx, c.b.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	c.w.pay.Deliver(ctx)
	if got := c.w.shop.heard[heard:]; len(got) != 2 {
		t.Fatalf("the consumer heard %v after B resolved; want B resolved and A again", got)
	}
	if a := c.get(t, c.a.ID); a.DuplicateOf != c.b.ID {
		t.Fatalf("A after B resolved: duplicate_of %q; want B", a.DuplicateOf)
	}
}

// As Core: a duplicate held back by an open chargeback is not promoted; when the chargeback is won, it is settled
// again and takes the free cycle.
func TestADuplicateUnderAChargebackIsSettledWhenItIsWon(t *testing.T) {
	c := newCycleCase(t, aMoney)
	ctx := context.Background()
	open := nilda.PaymentDisputeReport{ProviderRef: "dp_a", Stage: nilda.DisputeChargeback, Status: nilda.DisputeNeedsResponse,
		AmountMinor: 1234, Currency: "EUR"}
	if _, err := c.w.gwAPI.ReportDispute(ctx, c.a.ID, open); err != nil {
		t.Fatal(err)
	}
	if _, err := c.w.gwAPI.Reject(ctx, c.b.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); err != nil {
		t.Fatal(err)
	}
	if a := c.get(t, c.a.ID); a.DuplicateOf != c.b.ID {
		t.Fatalf("A under an open chargeback: duplicate_of %q; want still B", a.DuplicateOf)
	}
	won := open
	won.Status = nilda.DisputeWon
	if _, err := c.w.gwAPI.ReportDispute(ctx, c.a.ID, won); err != nil {
		t.Fatal(err)
	}
	if a := c.get(t, c.a.ID); a.DuplicateOf != "" {
		t.Fatalf("A after its chargeback was won, B ended: duplicate_of %q; want the cycle's own payment", a.DuplicateOf)
	}
}

// As Core: a duplicate held for a person (a different amount) takes the free cycle when the person accepts it.
func TestAHeldDuplicateAcceptedAfterItsHolderEndedTakesTheCycle(t *testing.T) {
	c := newCycleCase(t, nilda.PaymentResolution{AmountMinor: 1200, Currency: "EUR", ProviderRef: "pi_a"})
	ctx := context.Background()
	if a := c.get(t, c.a.ID); a.Status != nilda.PaymentPending || a.FailureCode != nilda.PaymentFailAmountMismatch {
		t.Fatalf("A with a different amount: %+v; want held for a person", a)
	}
	if _, err := c.w.gwAPI.Reject(ctx, c.b.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.w.pay.Decide(c.a.ID, "resolve", "the payer agreed the amount"); err != nil {
		t.Fatal(err)
	}
	if a := c.get(t, c.a.ID); a.Status != nilda.PaymentResolved || a.DuplicateOf != "" {
		t.Fatalf("A accepted after B ended: %+v; want resolved, the cycle's own payment", a)
	}
}
