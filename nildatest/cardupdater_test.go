package nildatest

import (
	"context"
	"sync"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// watchingShop is a consumer that watches its kept methods (nilda.SavedMethodWatcher).
type watchingShop struct {
	shopConsumer
	mu    sync.Mutex
	heard []nilda.PaymentSavedUpdate
}

func (w *watchingShop) SavedMethodUpdated(_ context.Context, u nilda.PaymentSavedUpdate) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.heard = append(w.heard, u)
	return nil
}

// THE CARD UPDATER (v0.69.0): the gateway reports a kept method its processor changed by the token it kept it under;
// Core changes the label and expiry once per processor event and tells a consumer that watches its methods.
func TestACardTheNetworkUpdatedReachesItsConsumerOnce(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	watcher := &watchingShop{shopConsumer: shopConsumer{confirm: nilda.PaymentConfirmResult{Proceed: true}}}
	w.pay.Consumer("shop", watcher)
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportSaveForLater}}}
	p := params()
	p.Save = mandate()
	s, err := w.shopAPI.CreateSession(ctx, "k-save", p)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa •••• 4242, 11/26", ExpiresAt: &old}})
	if err != nil || r.SavedMethod == "" {
		t.Fatalf("kept: %+v, %v", r, err)
	}
	w.pay.Deliver(ctx)

	next := time.Date(2030, 12, 1, 0, 0, 0, 0, time.UTC)
	u := nilda.PaymentMethodUpdate{Token: "cus_1/pm_1", Label: "Visa •••• 4242, 11/30", ExpiresAt: &next,
		Source: nilda.MethodUpdatedByNetwork, EventID: "evt_1"}
	res, err := w.gwAPI.ReportMethodUpdated(ctx, u)
	if err != nil || res.Matched != 1 || res.Changed != 1 {
		t.Fatalf("the update: %+v, %v", res, err)
	}
	if again, err := w.gwAPI.ReportMethodUpdated(ctx, u); err != nil || again.Matched != 1 || again.Changed != 0 {
		t.Fatalf("the same processor event twice changed it again: %+v, %v", again, err)
	}
	m, _ := w.pay.SavedMethod(r.SavedMethod)
	if m.Label != u.Label || m.ExpiresAt == nil || !m.ExpiresAt.Equal(next) || m.UpdatedAt == nil {
		t.Fatalf("the kept method after the update: %+v", m)
	}
	w.pay.Deliver(ctx)
	if len(watcher.heard) != 1 || watcher.heard[0].Method.ID != r.SavedMethod || watcher.heard[0].PreviousLabel != "Visa •••• 4242, 11/26" ||
		watcher.heard[0].Source != nilda.MethodUpdatedByNetwork {
		t.Fatalf("the consumer heard: %+v", watcher.heard)
	}

	// A token nobody here kept is no error: the processor's account may keep methods other software made.
	if res, err := w.gwAPI.ReportMethodUpdated(ctx, nilda.PaymentMethodUpdate{Token: "cus_9/pm_9", Label: "Visa", Source: nilda.MethodUpdatedByNetwork,
		EventID: "evt_2"}); err != nil || res.Matched != 0 {
		t.Fatalf("an unknown token: %+v, %v", res, err)
	}
	// Only a gateway reports it.
	if _, err := w.shopAPI.ReportMethodUpdated(ctx, u); err == nil || httpStatus(t, err) != 403 {
		t.Fatalf("a consumer reported a processor's update: %v", err)
	}
	// A forgotten method takes no update.
	if _, err := w.shopAPI.ForgetSavedMethod(ctx, r.SavedMethod); err != nil {
		t.Fatal(err)
	}
	u.EventID, u.Label = "evt_3", "Visa •••• 1111, 01/31"
	if res, err := w.gwAPI.ReportMethodUpdated(ctx, u); err != nil || res.Matched != 0 {
		t.Fatalf("a forgotten method was updated: %+v, %v", res, err)
	}
}

// A consumer that does not watch its methods is never sent the news, and is not owed it again.
func TestAConsumerThatDoesNotWatchIsNotOwedTheUpdate(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportSaveForLater}}}
	p := params()
	p.Save = mandate()
	s, _ := w.shopAPI.CreateSession(ctx, "k-save", p)
	if _, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa"}}); err != nil {
		t.Fatal(err)
	}
	w.pay.Deliver(ctx)
	if _, err := w.gwAPI.ReportMethodUpdated(ctx, nilda.PaymentMethodUpdate{Token: "cus_1/pm_1", Label: "Visa 2",
		Source: nilda.MethodUpdatedByNetwork, EventID: "evt_1"}); err != nil {
		t.Fatal(err)
	}
	w.pay.Deliver(ctx)
	if n := w.pay.Deliver(ctx); n != 0 {
		t.Fatalf("an update no consumer watches is still owed: %d", n)
	}
	var refused bool
	for _, d := range w.pay.Deliveries() {
		if d.Hook == nilda.HookPaymentSavedUpdated && d.Err == ErrNotSubscribed {
			refused = true
		}
	}
	if !refused {
		t.Fatal("the update was not recorded as a delivery the consumer is not subscribed to")
	}
}

// THE PAYER PAYS A RENEWAL'S CYCLE ON-SESSION (v0.69.0): their session holds the cycle as a kept charge does, so a
// dunning retry beside it is refused 409 naming it — and the other way round.
func TestAnOnSessionPaymentOfACycleHoldsItAgainstTheDunning(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportSaveForLater}}}
	p := params()
	p.Save = mandate()
	s, _ := w.shopAPI.CreateSession(ctx, "k-save", p)
	r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa"}})
	if err != nil {
		t.Fatal(err)
	}
	pay := params()
	pay.Reference, pay.Agreement, pay.Cycle = "renewal-order", "agr-1", 1
	open, err := w.shopAPI.CreateSession(ctx, "renewal-pay:1", pay)
	if err != nil || open.Agreement != "agr-1" || open.Cycle != 1 {
		t.Fatalf("the payer's payment of a cycle: %+v, %v", open, err)
	}
	month := time.Now().AddDate(0, 1, 0)
	w.pay.Now = func() time.Time { return month }
	if _, err := w.shopAPI.ChargeSaved(ctx, "renewal:1:a2", nilda.PaymentChargeParams{SavedMethod: r.SavedMethod, Reference: "renewal-order",
		AmountMinor: 1234, Currency: "EUR", Agreement: "agr-1", Cycle: 1}); err == nil || httpStatus(t, err) != 409 {
		t.Fatalf("a dunning retry beside the payer's open payment of the cycle: %v", err)
	}
	if _, err := w.shopAPI.CreateSession(ctx, "renewal-pay:2", pay); err == nil || httpStatus(t, err) != 409 {
		t.Fatalf("a second payer's payment of one cycle: %v", err)
	}
	if err := w.pay.Expire(open.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := w.shopAPI.ChargeSaved(ctx, "renewal:1:a3", nilda.PaymentChargeParams{SavedMethod: r.SavedMethod, Reference: "renewal-order",
		AmountMinor: 1234, Currency: "EUR", Agreement: "agr-1", Cycle: 1}); err != nil || c.Status != nilda.PaymentResolved {
		t.Fatalf("the retry once the payer's payment expired: %+v, %v", c, err)
	}
}

// An event that changes nothing is not recorded (as Core): heard again later with a change, it applies.
func TestAnEventThatChangedNothingIsNotRecorded(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportSaveForLater}}}
	p := params()
	p.Save = mandate()
	s, _ := w.shopAPI.CreateSession(ctx, "k-save", p)
	if _, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa"}}); err != nil {
		t.Fatal(err)
	}
	same := nilda.PaymentMethodUpdate{Token: "cus_1/pm_1", Label: "Visa", Source: nilda.MethodUpdatedByNetwork, EventID: "evt_1"}
	if res, err := w.gwAPI.ReportMethodUpdated(ctx, same); err != nil || res.Changed != 0 {
		t.Fatalf("nothing new: %+v, %v", res, err)
	}
	same.Label = "Visa 2"
	if res, err := w.gwAPI.ReportMethodUpdated(ctx, same); err != nil || res.Changed != 1 {
		t.Fatalf("the same event with a change, after a no-op: %+v, %v", res, err)
	}
}
