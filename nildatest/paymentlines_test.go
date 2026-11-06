package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// ITEM LINES (commerce plan 10.15, v0.58.0): the kit keeps a session's lines and breakdown, hands them to the gateway on
// payment.start as Core will, refuses lines that do not add up before any gateway is asked, and drops them when a
// re-price moves the amount they described.
func TestTheItemLinesTravelToTheGatewayAndARepriceDropsThem(t *testing.T) {
	ctx := context.Background()
	w := expressWorld(t)
	p := params()
	p.Method = "paypal"
	p.AmountMinor = 48000
	p.Lines = []nilda.PaymentLine{{Name: "Chair", Quantity: 2, UnitAmountMinor: 22000, TotalMinor: 44000, TaxMinor: 7025}}
	p.Breakdown = &nilda.PaymentBreakdown{ShippingMinor: 4000, TaxMinor: 7664, TaxIncluded: true}
	s, err := w.shopAPI.CreateSession(ctx, "lines-1", p)
	if err != nil {
		t.Fatalf("a session with lines: %v", err)
	}
	if len(s.Lines) != 1 || s.Lines[0].Name != "Chair" || s.Breakdown == nil || s.Breakdown.ShippingMinor != 4000 {
		t.Fatalf("the session does not keep its lines: %+v %+v", s.Lines, s.Breakdown)
	}
	if n := len(w.gw.Started); n == 0 || len(w.gw.Started[n-1].Lines) != 1 || w.gw.Started[n-1].Breakdown == nil {
		t.Fatalf("payment.start did not carry the lines: %+v", w.gw.Started)
	}

	bad := p
	bad.AmountMinor = 48001
	started := len(w.gw.Started)
	if _, err := w.pay.CreateSession(ctx, "shop", bad); err == nil {
		t.Fatal("lines that do not add up were accepted")
	}
	if len(w.gw.Started) != started {
		t.Fatal("the gateway was asked to start a session whose lines do not add up")
	}

	got, err := w.shopAPI.Reprice(ctx, "lines-1:quote-1", s.ID, nilda.PaymentReprice{AmountMinor: 48490})
	if err != nil {
		t.Fatal(err)
	}
	if got.Lines != nil || got.Breakdown != nil {
		t.Errorf("a re-price kept lines that describe the old amount: %+v %+v", got.Lines, got.Breakdown)
	}
}
