package nildatest

import (
	"context"
	"reflect"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func score(n int) *int { return &n }

// THE PROCESSOR'S RISK VERDICT (v0.58.0, EXPERIMENTAL): the kit keeps it on the session as Core does — exactly as the
// gateway reported it, from the hold or the resolution, the first one reported — and the consumer reads it there. A
// shop tested here holds an elevated order because the kit told it what Core will.
func TestTheKitKeepsTheRiskOnTheSession(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	elevated := &nilda.PaymentRisk{Level: nilda.RiskElevated, Score: score(72), Reason: "elevated_risk_level"}

	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if s.Risk != nil {
		t.Fatalf("a session nobody paid has a risk: %+v", s.Risk)
	}
	got, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR", ProviderRef: "pi_1", Risk: elevated})
	if err != nil || !reflect.DeepEqual(got.Risk, elevated) {
		t.Fatalf("resolve kept risk %+v (%v), want %+v", got.Risk, err, elevated)
	}
	read, _ := w.shopAPI.GetSession(ctx, s.ID)
	if !reflect.DeepEqual(read.Risk, elevated) {
		t.Errorf("the consumer reads risk %+v, want %+v", read.Risk, elevated)
	}
	w.pay.Deliver(ctx)
	log := w.pay.Deliveries()
	if len(log) == 0 || !reflect.DeepEqual(log[len(log)-1].Session.Risk, elevated) {
		t.Fatalf("the consumer was told %+v", log)
	}
	// The processor assessed the payment once: a later report never moves the verdict, nor clears it.
	for _, later := range []*nilda.PaymentRisk{{Level: nilda.RiskNormal, Score: score(5)}, nil} {
		again, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR", ProviderRef: "pi_1", Risk: later})
		if err != nil || !reflect.DeepEqual(again.Risk, elevated) {
			t.Errorf("a repeated report with risk %+v left %+v (%v)", later, again.Risk, err)
		}
	}

	// A verdict that arrives after the money (the charge read later) is kept then, and the consumer is told.
	late, _ := w.shopAPI.CreateSession(ctx, "k2", params())
	if _, err := w.gwAPI.Resolve(ctx, late.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	w.pay.Deliver(ctx)
	before := len(w.pay.Deliveries())
	notAssessed := &nilda.PaymentRisk{Level: nilda.RiskNotAssessed}
	got, err = w.gwAPI.Resolve(ctx, late.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR", Risk: notAssessed})
	if err != nil || !reflect.DeepEqual(got.Risk, notAssessed) {
		t.Fatalf("a late verdict: %+v %v", got.Risk, err)
	}
	w.pay.Deliver(ctx)
	if log := w.pay.Deliveries(); len(log) != before+1 || !reflect.DeepEqual(log[len(log)-1].Session.Risk, notAssessed) {
		t.Errorf("the consumer was not told the late verdict: %+v", log[before:])
	}
}

// A hold carries the verdict too — the processor scores a card when it authorises it — and the capture keeps it.
func TestTheHoldCarriesTheRisk(t *testing.T) {
	ctx := context.Background()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportManualCapture}}}
	p := params()
	p.Capture = nilda.CaptureManual
	s, err := w.shopAPI.CreateSession(ctx, "k1", p)
	if err != nil {
		t.Fatal(err)
	}
	highest := &nilda.PaymentRisk{Level: nilda.RiskHighest, Score: score(91), Reason: "rule"}
	held, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR", Risk: highest})
	if err != nil || held.Status != nilda.PaymentAuthorized || !reflect.DeepEqual(held.Risk, highest) {
		t.Fatalf("the hold kept risk %+v (%+v, %v)", held.Risk, held.Status, err)
	}
	// The same hold reported again with another verdict moves nothing.
	again, err := w.gwAPI.Authorize(ctx, s.ID, nilda.PaymentAuthorization{AmountMinor: 1234, Currency: "EUR",
		Risk: &nilda.PaymentRisk{Level: nilda.RiskNormal}})
	if err != nil || !reflect.DeepEqual(again.Risk, highest) {
		t.Errorf("a repeated hold moved the verdict: %+v %v", again.Risk, err)
	}
	taken, err := w.shopAPI.Capture(ctx, "c1", s.ID, 1234)
	if err != nil || taken.Status != nilda.PaymentResolved || !reflect.DeepEqual(taken.Risk, highest) {
		t.Errorf("the capture left risk %+v (%+v, %v)", taken.Risk, taken.Status, err)
	}
}
