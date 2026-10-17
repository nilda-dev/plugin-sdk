package nildatest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func mandate() *nilda.PaymentMandate {
	return &nilda.PaymentMandate{Purpose: nilda.MandateSubscription, Text: "Coffee beans every month, €12.34, until you cancel.",
		AmountMinor: 1234, Currency: "EUR", Interval: nilda.IntervalMonth, IntervalCount: 1}
}

// A METHOD KEPT FOR LATER (v0.30.0): kept at a Save session on a method that keeps methods, charged with the payer
// absent through the gateway that kept it — which alone ever sees the token — and forgotten, after which it charges
// nothing.
func TestAKeptMethodIsChargedLaterAndForgotten(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	p := params()
	p.Save = mandate()
	if _, err := w.shopAPI.CreateSession(ctx, "k-refused", p); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a method that keeps nothing was asked to keep: %v", err)
	}
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportSaveForLater}}}
	s, err := w.shopAPI.CreateSession(ctx, "k-save", p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Save == nil || s.Save.Text != p.Save.Text {
		t.Fatalf("the session lost its mandate: %+v", s)
	}
	resolved, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "cus_1/pm_1", Label: "Visa •••• 4242, 12/28"}})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SavedMethod == "" {
		t.Fatal("the resolved session names no kept method")
	}
	kept, err := w.shopAPI.GetSavedMethod(ctx, resolved.SavedMethod)
	if err != nil || kept.Label != "Visa •••• 4242, 12/28" || kept.Mandate.AmountMinor != 1234 || kept.Session != s.ID {
		t.Fatalf("the kept method: %+v, %v", kept, err)
	}

	charge := nilda.PaymentChargeParams{SavedMethod: resolved.SavedMethod, Reference: "renewal-1", AmountMinor: 1234, Currency: "EUR"}
	c, err := w.shopAPI.ChargeSaved(ctx, "renewal-1", charge)
	if err != nil {
		t.Fatal(err)
	}
	if c.Status != nilda.PaymentResolved || c.ChargedSaved != resolved.SavedMethod || c.RedirectURL != "" ||
		len(w.gw.SavedCharged) != 1 || w.gw.SavedCharged[0] != "cus_1/pm_1" {
		t.Fatalf("the charge: %+v, handed %v", c, w.gw.SavedCharged)
	}
	if again, err := w.shopAPI.ChargeSaved(ctx, "renewal-1", charge); err != nil || again.ID != c.ID || len(w.gw.SavedCharged) != 1 {
		t.Fatalf("the same key charged twice: %+v, %v", again, err)
	}
	other := charge
	other.Currency = "USD"
	if _, err := w.shopAPI.ChargeSaved(ctx, "renewal-usd", other); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a charge outside the mandate's currency: %v", err)
	}

	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentRejected, FailureCode: nilda.PaymentFailAuthenticationRequired}
	needs, err := w.shopAPI.ChargeSaved(ctx, "renewal-2", nilda.PaymentChargeParams{SavedMethod: resolved.SavedMethod,
		Reference: "renewal-2", AmountMinor: 1234, Currency: "EUR"})
	if err != nil || needs.Status != nilda.PaymentRejected || needs.FailureCode != nilda.PaymentFailAuthenticationRequired {
		t.Fatalf("a charge the bank wants the payer for: %+v, %v", needs, err)
	}

	gone, err := w.shopAPI.ForgetSavedMethod(ctx, resolved.SavedMethod)
	if err != nil || gone.ForgottenAt == nil || len(w.gw.Forgotten) != 1 || w.pay.SavedToken(resolved.SavedMethod) != "" {
		t.Fatalf("forgotten: %+v, %v, released %v", gone, err, w.gw.Forgotten)
	}
	if _, err := w.shopAPI.ForgetSavedMethod(ctx, resolved.SavedMethod); err != nil {
		t.Fatalf("forgetting twice: %v", err)
	}
	if _, err := w.shopAPI.ChargeSaved(ctx, "renewal-3", nilda.PaymentChargeParams{SavedMethod: resolved.SavedMethod,
		Reference: "renewal-3", AmountMinor: 1234, Currency: "EUR"}); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a forgotten method charged: %v", err)
	}
}

// A Save session may take no money now — a pre-order paid at shipment, a card replaced — and its resolution keeps the
// method all the same. A resolution that keeps a method on a session that asked for none is refused.
func TestASetUpOnlySessionKeepsTheMethodWithNoCharge(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportSaveForLater}}}
	p := params()
	p.AmountMinor = 0
	if _, err := w.shopAPI.CreateSession(ctx, "k-zero", p); err == nil {
		t.Fatal("a session for nothing, keeping nothing, was taken")
	}
	p.Save = &nilda.PaymentMandate{Purpose: nilda.MandatePreOrder, Text: "€59 when the lamp ships, by 1 March.",
		AmountMinor: 5900, Currency: "EUR"}
	s, err := w.shopAPI.CreateSession(ctx, "k-setup", p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "seti_1/pm_9", Label: "Mastercard •••• 4444"}})
	if err != nil || r.Status != nilda.PaymentResolved || r.SavedMethod == "" {
		t.Fatalf("the set-up session: %+v, %v", r, err)
	}

	plain, err := w.shopAPI.CreateSession(ctx, "k-plain", params())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.gwAPI.Resolve(ctx, plain.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
		Saved: &nilda.PaymentSaved{Token: "x", Label: "Visa"}}); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a method kept on a session that asked for none: %v", err)
	}
}

// A kept method's token never prints, nor does the request that carries it.
func TestAKeptMethodsTokenNeverPrints(t *testing.T) {
	for _, v := range []any{nilda.PaymentSaved{Token: "pm_secret", Label: "Visa"}, nilda.PaymentSavedToken{Token: "pm_secret"},
		nilda.PaymentForgetRequest{Token: "pm_secret"}} {
		for _, out := range []string{fmt.Sprintf("%v", v), fmt.Sprintf("%+v", v), fmt.Sprintf("%#v", v)} {
			if strings.Contains(out, "pm_secret") {
				t.Errorf("printed: %s", out)
			}
		}
	}
}
