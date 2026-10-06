package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

// These tests are the other half of the template: how a gateway is tested with nothing running. The payment
// service is nildatest's — Core's rules in memory — and the processor is driven the way a real one arrives,
// through nildatest.Webhook.

const secret = "whsec_test"

// rig is one installed testpay, and a shop to pay it.
type rig struct {
	pay  *nildatest.Payments
	gw   *Gateway
	shop *nilda.Core
}

func newRig(t *testing.T, settings map[string]any) rig {
	t.Helper()
	pay := nildatest.NewPayments()
	core, _, srv := pay.Core("testpay", "payment_gateway", "route", "admin_page")
	t.Cleanup(srv.Close)
	core.RoutePrefix = "/testpay"
	nildatest.SetSettings(core, settings)
	gw := &Gateway{}
	if _, err := gw.Init(context.Background(), core); err != nil {
		t.Fatalf("Init: %v", err)
	}
	pay.Gateway("testpay", gw)
	shop, _, shopSrv := pay.Core("shop", "payment_session")
	t.Cleanup(shopSrv.Close)
	return rig{pay: pay, gw: gw, shop: shop}
}

func (r rig) start(t *testing.T, key string, amount int64, currency string) nilda.PaymentSession {
	t.Helper()
	s, err := r.shop.API().Payments().CreateSession(context.Background(), key, nilda.PaymentSessionParams{
		Gateway: "testpay", Method: "card", Reference: "order-1", AmountMinor: amount, Currency: currency,
		Description: "Order 1", ReturnURL: "https://site.test/shop/thanks", CancelURL: "https://site.test/shop/cart",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return s
}

// signed is a webhook as the processor would send it: the body, and its HMAC — computed here, not by the
// code under test.
func signed(t *testing.T, ev map[string]any) ([]byte, http.Header) {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(raw)
	return raw, http.Header{"Testpay-Signature": {hex.EncodeToString(m.Sum(nil))}}
}

func (r rig) status(t *testing.T, id string) nilda.PaymentSession {
	t.Helper()
	s, ok := r.pay.GetSession(id)
	if !ok {
		t.Fatalf("no session %s", id)
	}
	return s
}

// The whole purchase, the way a payer makes it: sent to the processor's page, pays there, and is sent back.
func TestAPayerPaysOnTheProcessorsPage(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 1500, "JPY")
	if s.Status != nilda.PaymentRedirected || !s.Test {
		t.Fatalf("the session should wait on the processor's page, marked test: %+v", s)
	}
	want := "https://site.test/testpay/checkout?session=" + url.QueryEscape(s.ID)
	if s.RedirectURL != want {
		t.Fatalf("the payer is sent to %q, want %q", s.RedirectURL, want)
	}

	page := httptest.NewRecorder()
	r.gw.routes.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/testpay/checkout?session="+s.ID, nil))
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, "1500 JPY") {
		t.Fatalf("the checkout page should show 1500 JPY (a yen has no subunit): %d %s", page.Code, body)
	}
	token := regexp.MustCompile(`name="outcome" value="paid">\s*<input type="hidden" name="token" value="([0-9a-f]+)"`).
		FindStringSubmatch(body)
	if token == nil {
		t.Fatalf("no Pay button on the page: %s", body)
	}

	form := url.Values{"session": {s.ID}, "outcome": {"paid"}, "token": {token[1]}}
	res := nildatest.Webhook(r.gw.routes, "/testpay/pay", []byte(form.Encode()),
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "https://site.test/shop/thanks" {
		t.Fatalf("the payer should be sent back to the shop: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if got := r.status(t, s.ID); got.Status != nilda.PaymentResolved || got.ProviderRef != "tp_"+s.ID {
		t.Fatalf("paying should resolve the session with the processor's reference: %+v", got)
	}
}

func TestADeclinedPaymentIsRejectedAndThePayerSentToTheCart(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 2500, "EUR")
	form := url.Values{"session": {s.ID}, "outcome": {"declined"}, "token": {r.gw.token(s.ID, "declined")}}
	res := nildatest.Webhook(r.gw.routes, "/testpay/pay", []byte(form.Encode()),
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}})
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "https://site.test/shop/cart" {
		t.Fatalf("a declined payer goes back to the cart: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if got := r.status(t, s.ID); got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailDeclined {
		t.Fatalf("declining should reject the session as declined: %+v", got)
	}
}

// A processor sends the same webhook more than once as a matter of course; the second changes nothing and is
// acknowledged, so the processor stops.
func TestTheSameWebhookTwiceChangesNothing(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 1234, "EUR")
	raw, h := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1234,
		"currency": "EUR", "ref": "tp_1"})
	for i := 1; i <= 2; i++ {
		if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", raw, h); res.StatusCode != http.StatusOK {
			t.Fatalf("delivery %d answered %d; the processor would keep sending it", i, res.StatusCode)
		}
	}
	got := r.status(t, s.ID)
	if got.Status != nilda.PaymentResolved {
		t.Fatalf("the session should be resolved once: %+v", got)
	}
}

// Anyone can post to a webhook path — Core lets it through with no identity — so the signature is the only
// thing that makes a webhook the processor's.
func TestAWebhookWithoutTheProcessorsSignatureChangesNothing(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 1234, "EUR")
	raw, _ := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1234,
		"currency": "EUR"})
	for name, h := range map[string]http.Header{
		"none":   {},
		"forged": {"Testpay-Signature": {strings.Repeat("ab", 32)}},
	} {
		if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", raw, h); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s signature: answered %d, want 400", name, res.StatusCode)
		}
	}
	// A body changed after signing: the signature was over other bytes.
	_, h := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1234, "currency": "EUR"})
	tampered := []byte(strings.Replace(string(raw), "1234", "9999", 1))
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", tampered, h); res.StatusCode != http.StatusBadRequest {
		t.Errorf("a tampered body: answered %d, want 400", res.StatusCode)
	}
	if got := r.status(t, s.ID); got.Status != nilda.PaymentRedirected {
		t.Fatalf("an unsigned webhook moved the session to %s", got.Status)
	}
	form := url.Values{"session": {s.ID}, "outcome": {"paid"}, "token": {"00"}}
	if res := nildatest.Webhook(r.gw.routes, "/testpay/pay", []byte(form.Encode()),
		http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}); res.StatusCode != http.StatusForbidden {
		t.Errorf("a pay button with a wrong token: answered %d, want 403", res.StatusCode)
	}
}

// A success reported after the session ended another way is refused by Core (409) — and acknowledged to the
// processor, because sending it again would be refused again forever.
func TestAReportCoreRefusesIsNotSentAgain(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 1234, "EUR")
	failed, h := signed(t, map[string]any{"type": "payment.failed", "session": s.ID})
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", failed, h); res.StatusCode != http.StatusOK {
		t.Fatalf("payment.failed answered %d", res.StatusCode)
	}
	late, h := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1234, "currency": "EUR"})
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", late, h); res.StatusCode != http.StatusOK {
		t.Fatalf("a report Core refuses (409) must be acknowledged, got %d", res.StatusCode)
	}
	if got := r.status(t, s.ID); got.Status != nilda.PaymentRejected {
		t.Fatalf("a rejected session was moved to %s", got.Status)
	}
}

// The processor says it took a different amount: Core holds the session for a person instead of calling it
// paid — which is why the amount a gateway reports must be the PROCESSOR's.
func TestAPaymentOfTheWrongAmountIsHeldForAPerson(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.start(t, "order-1#1", 1234, "EUR")
	raw, h := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1000, "currency": "EUR"})
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", raw, h); res.StatusCode != http.StatusOK {
		t.Fatalf("answered %d", res.StatusCode)
	}
	got := r.status(t, s.ID)
	if got.Status != nilda.PaymentPending || got.FailureCode != nilda.PaymentFailAmountMismatch {
		t.Fatalf("a short payment should be held pending as amount_mismatch: %+v", got)
	}
}

func TestARefundIsSettledOnTheSpot(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret})
	s := r.paid(t)
	asked, err := r.shop.API().Payments().CreateRefund(context.Background(), "refund-1", s.ID,
		nilda.PaymentRefundParams{AmountMinor: 500, Reason: "one item returned"})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	// Core asks the gateway from its delivery job, not inside the consumer's call; Deliver runs that job.
	r.pay.Deliver(context.Background())
	rf, _ := r.pay.GetRefund(asked.ID)
	if rf.Status != nilda.RefundResolved || rf.ProviderRef != "tpr_"+rf.ID {
		t.Fatalf("the refund should settle on the spot: %+v", rf)
	}
	if got := r.status(t, s.ID); got.RefundedMinor != 500 {
		t.Fatalf("the session should record 500 refunded, has %d", got.RefundedMinor)
	}
}

func TestARefundTheProcessorSettlesLaterArrivesByWebhook(t *testing.T) {
	r := newRig(t, map[string]any{"signing_secret": secret, "refunds_later": true})
	s := r.paid(t)
	asked, err := r.shop.API().Payments().CreateRefund(context.Background(), "refund-1", s.ID,
		nilda.PaymentRefundParams{AmountMinor: 1234})
	if err != nil {
		t.Fatalf("CreateRefund: %v", err)
	}
	r.pay.Deliver(context.Background()) // Core's delivery job asks the gateway
	rf, _ := r.pay.GetRefund(asked.ID)
	if rf.Status != nilda.RefundPending {
		t.Fatalf("the refund should wait on the processor: %+v", rf)
	}
	raw, h := signed(t, map[string]any{"type": "refund.succeeded", "refund": rf.ID, "ref": "tpr_x"})
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", raw, h); res.StatusCode != http.StatusOK {
		t.Fatalf("refund.succeeded answered %d", res.StatusCode)
	}
	if got, _ := r.pay.GetRefund(rf.ID); got.Status != nilda.RefundResolved {
		t.Fatalf("the refund should be resolved by the webhook: %+v", got)
	}
	if got := r.status(t, s.ID); got.RefundedMinor != 1234 {
		t.Fatalf("the session should record the whole amount refunded, has %d", got.RefundedMinor)
	}
}

// With no secret there is no telling a real webhook from a forged one, so the gateway offers nothing and
// Core cannot start a payment with it.
func TestAGatewayWithNoSecretOffersNothing(t *testing.T) {
	r := newRig(t, map[string]any{})
	opts, err := r.shop.API().Payments().Methods(context.Background(), "EUR")
	if err != nil || len(opts) != 0 {
		t.Fatalf("a gateway with no secret should offer nothing: %v %v", opts, err)
	}
	_, err = r.shop.API().Payments().CreateSession(context.Background(), "k", nilda.PaymentSessionParams{
		Gateway: "testpay", Method: "card", Reference: "o", AmountMinor: 100, Currency: "EUR",
		ReturnURL: "https://site.test/x"})
	var ae *nilda.APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnprocessableEntity {
		t.Fatalf("a payment with a gateway that offers nothing should be refused 422, got %v", err)
	}
}

// paid is a session the processor has already paid.
func (r rig) paid(t *testing.T) nilda.PaymentSession {
	t.Helper()
	s := r.start(t, "order-1#1", 1234, "EUR")
	raw, h := signed(t, map[string]any{"type": "payment.succeeded", "session": s.ID, "amount_minor": 1234,
		"currency": "EUR", "ref": "tp_" + s.ID})
	if res := nildatest.Webhook(r.gw.routes, "/testpay/webhook", raw, h); res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("paying answered %d: %s", res.StatusCode, body)
	}
	return r.status(t, s.ID)
}
