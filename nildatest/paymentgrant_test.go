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

// DOOR D5: the holder grants one plugin one agreement; that plugin charges it — the session is its own, the token
// reaches only the gateway — and nothing else: not another agreement, not after the grant is revoked, and it never
// reads the holder's grants.
func TestAGrantLetsOnePluginChargeOneAgreement(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := keptForRenewals(t, w)
	subsCore, _, srv := w.pay.Core("subscriptions", "payment_session")
	t.Cleanup(srv.Close)
	subs := subsCore.API().Payments()
	charge := nilda.PaymentChargeParams{SavedMethod: id, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR",
		Agreement: "sub_1", Cycle: 1}

	if _, err := subs.ChargeSaved(ctx, "before", charge); err == nil || httpStatus(t, err) != 404 {
		t.Fatalf("a plugin charged a method nobody granted it: %v", err)
	}
	if _, err := subs.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "sub_1"}); err == nil ||
		httpStatus(t, err) != 404 {
		t.Fatalf("a plugin granted itself the holder's method: %v", err)
	}
	m, err := w.shopAPI.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "sub_1"})
	if err != nil || len(m.Grants) != 1 || m.Grants[0].Plugin != "subscriptions" || m.Grants[0].CreatedAt.IsZero() {
		t.Fatalf("the grant: %+v, %v", m, err)
	}
	if again, err := w.shopAPI.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "sub_1"}); err != nil ||
		len(again.Grants) != 1 {
		t.Fatalf("granting twice made two grants: %+v, %v", again, err)
	}

	other := charge
	other.Agreement = "sub_2"
	if _, err := subs.ChargeSaved(ctx, "other", other); err == nil || httpStatus(t, err) != 404 {
		t.Fatalf("a grantee charged an agreement it was not granted: %v", err)
	}
	none := charge
	none.Agreement, none.Cycle = "", 0
	if _, err := subs.ChargeSaved(ctx, "none", none); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a grantee charged without naming its agreement: %v", err)
	}
	c, err := subs.ChargeSaved(ctx, "sub_1-1", charge)
	if err != nil || c.Status != nilda.PaymentResolved || c.Consumer != "subscriptions" || c.ChargedSaved != id {
		t.Fatalf("the grantee's charge: %+v, %v", c, err)
	}
	if len(w.gw.SavedCharged) != 1 || w.gw.SavedCharged[0] != "cus_1/pm_1" {
		t.Fatalf("the gateway was handed %v", w.gw.SavedCharged)
	}
	read, err := subs.GetSavedMethod(ctx, id)
	if err != nil || read.Grants != nil || read.Label != "Visa •••• 4242" {
		t.Fatalf("the grantee's read of the method: %+v, %v", read, err)
	}
	// One cycle is one charge whoever asks: the holder cannot charge the cycle the grantee paid.
	if _, err := w.shopAPI.ChargeSaved(ctx, "holder-sub_1-1", charge); err == nil || httpStatus(t, err) != 409 {
		t.Fatalf("the holder charged a cycle the grantee had paid: %v", err)
	}

	if _, err := w.shopAPI.RevokeSavedMethodGrant(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "sub_1"}); err != nil {
		t.Fatal(err)
	}
	month2 := time.Now().AddDate(0, 2, 0)
	w.pay.Now = func() time.Time { return month2 }
	next := charge
	next.Cycle = 2
	if _, err := subs.ChargeSaved(ctx, "sub_1-2", next); err == nil || httpStatus(t, err) != 404 {
		t.Fatalf("a revoked grant still charged: %v", err)
	}
}

// A grant is refused for what Core refuses it: the holder itself, a malformed agreement, a forgotten method.
func TestAGrantIsRefusedWhereCoreRefusesIt(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	id := keptForRenewals(t, w)
	if _, err := w.shopAPI.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "shop", Agreement: "sub_1"}); err == nil ||
		httpStatus(t, err) != 422 {
		t.Fatalf("the holder granted itself: %v", err)
	}
	if _, err := w.shopAPI.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "a b"}); err == nil {
		t.Fatal("an agreement with a space was sent")
	}
	if _, err := w.shopAPI.ForgetSavedMethod(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := w.shopAPI.GrantSavedMethod(ctx, id, nilda.PaymentMandateGrant{Plugin: "subscriptions", Agreement: "sub_1"}); err == nil ||
		httpStatus(t, err) != 422 {
		t.Fatalf("a forgotten method was granted: %v", err)
	}
}
