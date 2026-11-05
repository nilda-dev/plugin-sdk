package nildatest

import (
	"context"
	"errors"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// DOOR D5 in the kit, held to Core's rules: a kept method's charge tells the gateway which kind of merchant-initiated
// charge it is, a decline of it is a result, and a till pays at a card reader and can call a payment off there.

// A subscription's renewal is recurring and a pre-order's charge at shipment unscheduled — what the GATEWAY is told,
// and what the consumer reads back. A decline with the payer absent is the session rejected, never an error.
func TestAKeptMethodsChargeSaysWhichKindItIs(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true,
		Supports: []string{nilda.SupportRefund, nilda.SupportSaveForLater}}}
	keep := func(key string, m *nilda.PaymentMandate) string {
		t.Helper()
		p := params()
		p.Save = m
		s, err := w.shopAPI.CreateSession(ctx, key, p)
		if err != nil {
			t.Fatal(err)
		}
		r, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR",
			Saved: &nilda.PaymentSaved{Token: "cus_1/pm_" + key, Label: "Visa •••• 4242"}})
		if err != nil || r.SavedMethod == "" {
			t.Fatalf("kept: %+v, %v", r, err)
		}
		if r.OffSession != "" {
			t.Errorf("a session the payer was present for says off_session %q", r.OffSession)
		}
		return r.SavedMethod
	}
	sub := keep("k-sub", mandate())
	pre := keep("k-pre", &nilda.PaymentMandate{Purpose: nilda.MandatePreOrder, Text: "€12.34 when it ships.",
		AmountMinor: 1234, Currency: "EUR"})

	w.pay.Now = func() time.Time { return time.Now().AddDate(0, 1, 0) }
	for _, c := range []struct{ method, key, want string }{
		{sub, "renewal-1", nilda.OffSessionRecurring},
		{pre, "shipped-1", nilda.OffSessionUnscheduled},
	} {
		s, err := w.shopAPI.ChargeSaved(ctx, c.key, nilda.PaymentChargeParams{SavedMethod: c.method, Reference: c.key,
			AmountMinor: 1234, Currency: "EUR"})
		if err != nil || s.OffSession != c.want {
			t.Fatalf("%s: the consumer reads off_session %q (%v), want %q", c.key, s.OffSession, err, c.want)
		}
		told := w.gw.SavedSessions[len(w.gw.SavedSessions)-1]
		if told.ID != s.ID || told.OffSession != c.want {
			t.Fatalf("%s: the gateway was told %q for %s, want %q", c.key, told.OffSession, told.ID, c.want)
		}
	}

	w.pay.Now = func() time.Time { return time.Now().AddDate(0, 2, 0) }
	w.gw.SavedStart = nilda.PaymentStartResult{Status: nilda.PaymentRejected, FailureCode: nilda.PaymentFailDeclined,
		FailureMessage: "insufficient funds"}
	declined, err := w.shopAPI.ChargeSaved(ctx, "renewal-2", nilda.PaymentChargeParams{SavedMethod: sub,
		Reference: "renewal-2", AmountMinor: 1234, Currency: "EUR"})
	if err != nil || declined.Status != nilda.PaymentRejected || declined.FailureCode != nilda.PaymentFailDeclined {
		t.Fatalf("a declined renewal: %+v, %v — a decline is a result, not an error", declined, err)
	}
}

// A till's world: the stub offers an online card and a card reader at the front counter, and starts a reader's payment
// pending — the payer decides at the reader.
func tillWorld(t *testing.T) world {
	t.Helper()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{
		{Key: "card", Label: "Card", Refunds: true},
		{Key: "card_present", Label: "Card reader", Supports: []string{nilda.SupportRefund, nilda.SupportCardPresent},
			Readers: []nilda.PaymentReader{{ID: "tmr_front", Label: "Front counter"}}},
	}
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_till"}
	return w
}

func atTill() nilda.PaymentSessionParams {
	return nilda.PaymentSessionParams{Gateway: "testpay", Method: "card_present", Reference: "till-1#1", AmountMinor: 450,
		Currency: "EUR", Reader: "tmr_front"}
}

func TestATillPaysAtOneOfTheMethodsReaders(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	opts, err := w.shopAPI.Methods(ctx, "EUR")
	if err != nil || len(opts) != 2 || len(opts[1].Readers) != 1 || opts[1].Readers[0].Label != "Front counter" {
		t.Fatalf("the till cannot see its readers: %+v, %v", opts, err)
	}
	online := atTill()
	online.Reader, online.ReturnURL = "", "https://site.test/shop/thanks"
	elsewhere := atTill()
	elsewhere.Reader = "tmr_back"
	onCard := atTill()
	onCard.Method = "card"
	for name, p := range map[string]nilda.PaymentSessionParams{
		"an online session on the reader method": online, "a reader the method does not list": elsewhere,
		"a reader on a method that takes no card in person": onCard,
	} {
		if _, err := w.shopAPI.CreateSession(ctx, "k-"+name, p); err == nil || httpStatus(t, err) != 422 {
			t.Errorf("%s: %v", name, err)
		}
	}
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil || s.Status != nilda.PaymentPending || s.Reader != "tmr_front" || s.ReturnURL != "" {
		t.Fatalf("the till's payment: %+v, %v", s, err)
	}
	paid, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 450, Currency: "EUR", ProviderRef: "pi_till",
		Instrument: nilda.InstrumentCard})
	if err != nil || paid.Status != nilda.PaymentResolved {
		t.Fatalf("paid at the reader: %+v, %v", paid, err)
	}
}

// The till's Cancel: a payment still waiting at its reader is voided — asked of the gateway under the void key — and
// ends rejected, voided. An online payment the processor is deciding cannot be voided; it was never a hold.
func TestATillCallsOffAPaymentWaitingAtItsReader(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil {
		t.Fatal(err)
	}
	off, err := w.shopAPI.Void(ctx, "cancel-till-1", s.ID)
	if err != nil || off.Status != nilda.PaymentRejected || off.FailureCode != nilda.PaymentFailVoided ||
		len(w.gw.Voided) != 1 || w.gw.Voided[0].IdempotencyKey != nilda.VoidKeyPrefix+s.ID {
		t.Fatalf("called off: %+v, %v, asked %+v", off, err, w.gw.Voided)
	}
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentPending, ProviderRef: "pi_web"}
	web, err := w.shopAPI.CreateSession(ctx, "web-1", params())
	if err != nil || web.Status != nilda.PaymentPending {
		t.Fatalf("the online payment: %+v, %v", web, err)
	}
	if _, err := w.shopAPI.Void(ctx, "cancel-web-1", web.ID); err == nil || httpStatus(t, err) != 409 {
		t.Fatalf("an online pending payment was voided: %v", err)
	}
}

// lostAtReader is a reader gateway whose start answer never arrives.
type lostAtReader struct{ StubGateway }

func (g *lostAtReader) StartPayment(context.Context, nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	return nilda.PaymentStartResult{}, errors.New("timeout")
}

// A reader's start whose answer was lost may have the payer's card on it: held pending, never closed as nothing
// charged. A start that answers with a page sent nothing to the reader: rejected, invalid_request.
func TestAReadersStartIsNeverGuessed(t *testing.T) {
	w := tillWorld(t)
	ctx := context.Background()
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentRedirected, RedirectURL: "https://pay.test/x"}
	paged, err := w.shopAPI.CreateSession(ctx, "till-1#1", atTill())
	if err != nil || paged.Status != nilda.PaymentRejected || paged.FailureCode != nilda.PaymentFailInvalidRequest ||
		paged.RedirectURL != "" {
		t.Fatalf("a reader's start answered with a page: %+v, %v", paged, err)
	}

	lost := &lostAtReader{StubGateway: StubGateway{Methods: w.gw.Methods}}
	w.pay.Gateway("testpay", lost)
	held, err := w.shopAPI.CreateSession(ctx, "till-2#1", atTill())
	if err != nil || held.Status != nilda.PaymentPending {
		t.Fatalf("a reader's lost start: %+v, %v", held, err)
	}
}
