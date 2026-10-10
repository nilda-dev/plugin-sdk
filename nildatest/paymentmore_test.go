package nildatest

import (
	"context"
	"errors"
	"net/http"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// disputeShop is a consumer that also takes dispute news.
type disputeShop struct {
	shopConsumer
	disputes []nilda.PaymentDispute
}

func (s *disputeShop) DisputeUpdated(_ context.Context, d nilda.PaymentDispute, _ nilda.PaymentSession) error {
	s.disputes = append(s.disputes, d)
	return nil
}

func paidWorld(t *testing.T) (world, nilda.PaymentSession) {
	t.Helper()
	w := newWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	if s, err = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	return w, s
}

func report(status, stage string, held bool) nilda.PaymentDisputeReport {
	return nilda.PaymentDisputeReport{ProviderRef: "dp_1", Stage: stage, Status: status, Reason: "fraudulent",
		AmountMinor: 1234, Currency: "EUR", FundsHeld: held}
}

func TestADisputeIsASnapshotThatNeverTakesAVerdictBack(t *testing.T) {
	w, s := paidWorld(t)
	ctx := context.Background()
	d, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeNeedsResponse, nilda.DisputeChargeback, true))
	if err != nil || d.Status != nilda.DisputeNeedsResponse || d.Session != s.ID {
		t.Fatalf("first report = %+v %v", d, err)
	}
	again, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeNeedsResponse, nilda.DisputeChargeback, true))
	if err != nil || again.ID != d.ID || !again.UpdatedAt.Equal(d.UpdatedAt) {
		t.Fatalf("the same snapshot again changed something: %+v %v", again, err)
	}
	won, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeWon, nilda.DisputeChargeback, true))
	if err != nil || won.ClosedAt == nil {
		t.Fatalf("won = %+v %v; want closed_at set", won, err)
	}
	if _, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeUnderReview, nilda.DisputeChargeback, true)); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a verdict taken back answered %v, want 409", err)
	}
	if _, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeWon, nilda.DisputeInquiry, true)); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a chargeback made an inquiry again answered %v, want 409", err)
	}
	// The money came back after the verdict: a fact, still updatable.
	back, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeWon, nilda.DisputeChargeback, false))
	if err != nil || back.FundsHeld {
		t.Fatalf("funds reinstated after won = %+v %v", back, err)
	}
	if got, _ := w.pay.GetSession(s.ID); got.Status != nilda.PaymentResolved {
		t.Fatalf("a dispute moved the session to %s; it must never touch it", got.Status)
	}
	list, err := w.shopAPI.ListDisputes(ctx, s.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("the consumer reads %v %v", list, err)
	}
	if _, err := w.shopAPI.GetDispute(ctx, d.ID); err != nil {
		t.Fatalf("the consumer cannot read its dispute: %v", err)
	}
}

func TestDisputeNewsReachesOnlyAConsumerThatTakesIt(t *testing.T) {
	w, s := paidWorld(t)
	ctx := context.Background()
	w.pay.Deliver(ctx)
	if _, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeNeedsResponse, nilda.DisputeChargeback, true)); err != nil {
		t.Fatal(err)
	}
	if owed := w.pay.Deliver(ctx); owed != 0 {
		t.Fatalf("%d deliveries still owed; one a consumer cannot take is not owed again", owed)
	}
	last := w.pay.Deliveries()[len(w.pay.Deliveries())-1]
	if last.Hook != nilda.HookPaymentDisputeUpdated || !errors.Is(last.Err, ErrNotSubscribed) {
		t.Fatalf("last delivery = %+v; want the dispute, refused as not subscribed", last)
	}

	shop := &disputeShop{shopConsumer: shopConsumer{confirm: nilda.PaymentConfirmResult{Proceed: true}}}
	w.pay.Consumer("shop", shop)
	if _, err := w.gwAPI.ReportDispute(ctx, s.ID, report(nilda.DisputeUnderReview, nilda.DisputeChargeback, true)); err != nil {
		t.Fatal(err)
	}
	w.pay.Deliver(ctx)
	if len(shop.disputes) != 1 || shop.disputes[0].Status != nilda.DisputeUnderReview {
		t.Fatalf("the dispute consumer heard %+v", shop.disputes)
	}
}

func TestAReversedRefundStaysResolvedAndIsToldAgain(t *testing.T) {
	w, s := paidWorld(t)
	ctx := context.Background()
	rf, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 234})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.ReverseRefund(ctx, rf.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("reversing a refund still requested answered %v, want 409", err)
	}
	if _, err := w.gwAPI.ResolveRefund(ctx, rf.ID, "re_1"); err != nil {
		t.Fatal(err)
	}
	w.pay.Deliver(ctx)
	before := len(w.pay.Deliveries())
	rev, err := w.gwAPI.ReverseRefund(ctx, rf.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined, FailureMessage: "the card was closed"})
	if err != nil || rev.Status != nilda.RefundResolved || rev.ReversedAt == nil || rev.FailureMessage != "the card was closed" {
		t.Fatalf("reversed = %+v %v", rev, err)
	}
	w.pay.Deliver(ctx)
	if len(w.pay.Deliveries()) != before+1 || w.pay.Deliveries()[before].Refund.ReversedAt == nil {
		t.Fatal("the consumer was not told the refund was reversed")
	}
	if got, _ := w.pay.GetSession(s.ID); got.RefundedMinor != 234 {
		t.Fatalf("refunded total = %d; a reversal does not change what was booked", got.RefundedMinor)
	}
}

func TestARefundMadeAtTheProcessorIsBookedOnce(t *testing.T) {
	w, s := paidWorld(t)
	ctx := context.Background()
	ext := nilda.PaymentExternalRefund{AmountMinor: 500, Currency: "eur", ProviderRef: "re_dash"}
	rf, err := w.gwAPI.ReportExternalRefund(ctx, s.ID, ext)
	if err != nil || !rf.External || rf.Status != nilda.RefundResolved {
		t.Fatalf("external refund = %+v %v", rf, err)
	}
	again, err := w.gwAPI.ReportExternalRefund(ctx, s.ID, ext)
	if err != nil || again.ID != rf.ID {
		t.Fatalf("the same processor refund twice made %+v %v", again, err)
	}
	if got, _ := w.pay.GetSession(s.ID); got.RefundedMinor != 500 {
		t.Fatalf("refunded total = %d, want 500 once", got.RefundedMinor)
	}
	if _, err := w.gwAPI.ReportExternalRefund(ctx, s.ID, nilda.PaymentExternalRefund{AmountMinor: 800, Currency: "EUR", ProviderRef: "re_2"}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("an external refund past what was paid answered %v, want 422", err)
	}
	// What Nilda may still refund counts what was refunded elsewhere.
	if _, err := w.shopAPI.CreateRefund(ctx, "r-over", s.ID, nilda.PaymentRefundParams{AmountMinor: 800}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a refund past what is left after the external one answered %v, want 422", err)
	}
}

func TestARefundIsDecidedByWhatTheMethodCouldDoWhenPaid(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Supports: []string{nilda.SupportRefund}}}
	s, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := w.pay.GetSession(s.ID); !nilda.Supports(got.Supports, nilda.SupportRefund) {
		t.Fatalf("the session carries %v; want the method's list copied", got.Supports)
	}
	_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	if _, err := w.shopAPI.CreateRefund(ctx, "part", s.ID, nilda.PaymentRefundParams{AmountMinor: 100}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a partial refund through a method that refunds only in full answered %v, want 422", err)
	}
	// The gateway stops offering the method; the old payment can still be refunded in full.
	w.gw.Methods = []nilda.PaymentMethod{}
	if rf, err := w.shopAPI.CreateRefund(ctx, "full", s.ID, nilda.PaymentRefundParams{AmountMinor: 1234}); err != nil || rf.Status != nilda.RefundRequested {
		t.Fatalf("a full refund of an old payment after the method went away = %+v %v", rf, err)
	}
}

func TestTheAddressesTravelToTheGateway(t *testing.T) {
	w := newWorld(t)
	p := params()
	p.Fulfilment = nilda.FulfilmentPhysical
	p.ShippingAddress = &nilda.PaymentAddress{Name: "Ana", Line1: "Ring 1", City: "Wien", PostalCode: "1010", Country: "AT"}
	s, err := w.shopAPI.CreateSession(context.Background(), "k1", p)
	if err != nil || s.Fulfilment != nilda.FulfilmentPhysical || s.ShippingAddress == nil || s.ShippingAddress.Country != "AT" {
		t.Fatalf("session = %+v %v", s, err)
	}
	bad := p
	bad.ShippingAddress = &nilda.PaymentAddress{Country: "Austria"}
	if _, err := w.shopAPI.CreateSession(context.Background(), "k2", bad); err == nil {
		t.Fatal("an address with a country name instead of a code was accepted")
	}
}

func TestAGatewaysSecretIsKeptForItAlone(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	if _, found, err := w.gwAPI.GetSecret(ctx, "webhook_secret"); err != nil || found {
		t.Fatalf("before any: %v %v", found, err)
	}
	if err := w.gwAPI.SetSecret(ctx, "webhook_secret", "whsec_1"); err != nil {
		t.Fatal(err)
	}
	if v, found, err := w.gwAPI.GetSecret(ctx, "webhook_secret"); err != nil || !found || v != "whsec_1" {
		t.Fatalf("read back %q %v %v", v, found, err)
	}
	if err := w.shopAPI.SetSecret(ctx, "webhook_secret", "x"); httpStatus(t, err) != http.StatusForbidden {
		t.Fatalf("a consumer setting a gateway secret answered %v, want 403", err)
	}
}

type syncStub struct {
	StubGateway
	asked []string
}

func (g *syncStub) SyncPayment(_ context.Context, s nilda.PaymentSession) error {
	g.asked = append(g.asked, s.ID)
	return nil
}

func TestSyncAsksOnlyAGatewayThatSyncs(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if err := w.pay.Sync(ctx, s.ID); !errors.Is(err, ErrNotSubscribed) {
		t.Fatalf("a gateway that does not sync: %v, want ErrNotSubscribed", err)
	}
	g := &syncStub{}
	w.pay.Gateway("testpay", g)
	if err := w.pay.Sync(ctx, s.ID); err != nil || len(g.asked) != 1 || g.asked[0] != s.ID {
		t.Fatalf("a syncing gateway: %v asked %v", err, g.asked)
	}
}

func TestTheSiteAddressReachesThePlugin(t *testing.T) {
	pay := NewPayments()
	pay.SiteURL = "https://shop.example/"
	core, _, srv := pay.Core("gw", "payment_gateway")
	defer srv.Close()
	if core.SiteURL != "https://shop.example" {
		t.Fatalf("SiteURL = %q", core.SiteURL)
	}
}
