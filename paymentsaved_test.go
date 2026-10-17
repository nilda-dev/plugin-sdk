package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// A mandate records what the payer agreed to, or it is refused.
func TestAMandateRecordsWhatThePayerAgreedTo(t *testing.T) {
	ok := PaymentMandate{Purpose: MandateSubscription, Text: "Every month, €10, until you cancel.", AmountMinor: 1000,
		Currency: "EUR", Interval: IntervalMonth, IntervalCount: 1}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*PaymentMandate){
		"no purpose":                 func(m *PaymentMandate) { m.Purpose = "" },
		"no text":                    func(m *PaymentMandate) { m.Text = " " },
		"text too long":              func(m *PaymentMandate) { m.Text = strings.Repeat("x", maxMandateText+1) },
		"no currency":                func(m *PaymentMandate) { m.Currency = "EURO" },
		"no amount and no rule":      func(m *PaymentMandate) { m.AmountMinor = 0 },
		"a subscription, no rhythm":  func(m *PaymentMandate) { m.Interval, m.IntervalCount = "", 0 },
		"an interval of zero":        func(m *PaymentMandate) { m.IntervalCount = 0 },
		"an interval nobody knows":   func(m *PaymentMandate) { m.Interval = "fortnight" },
		"a negative amount":          func(m *PaymentMandate) { m.AmountMinor = -1 },
		"a pre-order with a rhythm?": func(m *PaymentMandate) { m.Purpose, m.Interval = MandatePreOrder, ""; m.IntervalCount = 2 },
	} {
		m := ok
		change(&m)
		if m.Validate() == nil {
			t.Errorf("%s: accepted %+v", name, m)
		}
	}
	rule := ok
	rule.AmountMinor, rule.AmountRule = 0, "the basket's price at each delivery"
	if err := rule.Validate(); err != nil {
		t.Errorf("an amount set by a rule: %v", err)
	}
}

type keeper struct {
	gatewayOnly
	token string
}

func (k *keeper) StartPaymentWithSaved(_ context.Context, s PaymentSession, token string) (PaymentStartResult, error) {
	k.token = token
	return PaymentStartResult{Status: PaymentResolved, ProviderRef: "pi_1"}, nil
}

func (k *keeper) ForgetSaved(_ context.Context, token string) error {
	k.token = "forgot:" + token
	return nil
}

// payment.start with a kept method reaches StartPaymentWithSaved with its token, and payment.saved.forget reaches
// ForgetSaved; a gateway that keeps nothing refuses the charge rather than starting an ordinary payment.
func TestAKeptMethodsChargeAndReleaseReachTheGateway(t *testing.T) {
	ctx := context.Background()
	k := &keeper{}
	payload, _ := json.Marshal(PaymentStartRequest{Session: PaymentSession{ID: "s1", AmountMinor: 100, Currency: "EUR"},
		Saved: &PaymentSavedToken{Token: "cus/pm"}})
	out, handled, err := DispatchPaymentGatewayHook(ctx, nil, k, HookPaymentStart, payload)
	if err != nil || !handled || k.token != "cus/pm" || !strings.Contains(string(out), PaymentResolved) {
		t.Fatalf("charge: %s %v %v, token %q", out, handled, err, k.token)
	}
	forget, _ := json.Marshal(PaymentForgetRequest{Token: "cus/pm"})
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, k, HookPaymentForget, forget); err != nil || k.token != "forgot:cus/pm" {
		t.Fatalf("forget: %v, %q", err, k.token)
	}
	out, _, err = DispatchPaymentGatewayHook(ctx, nil, gatewayOnly{}, HookPaymentStart, payload)
	if err != nil || !strings.Contains(string(out), PaymentFailInvalidRequest) {
		t.Fatalf("a gateway that keeps nothing: %s, %v", out, err)
	}
	if got := withProvidedHooks(k, nil); !strings.Contains(strings.Join(got, ","), HookPaymentForget) {
		t.Errorf("a gateway that keeps methods is not subscribed to %s: %v", HookPaymentForget, got)
	}
}
