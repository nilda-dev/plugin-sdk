package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// holdingGateway is a gateway that holds money: it answers payment.capture and payment.void with what a test sets.
type holdingGateway struct {
	recordingGateway
	capture PaymentCaptureResult
	void    PaymentVoidResult
	asked   []string
}

func (g *holdingGateway) CapturePayment(_ context.Context, r PaymentCaptureRequest) (PaymentCaptureResult, error) {
	g.asked = append(g.asked, "capture:"+r.IdempotencyKey)
	return g.capture, g.err
}
func (g *holdingGateway) VoidPayment(_ context.Context, r PaymentVoidRequest) (PaymentVoidResult, error) {
	g.asked = append(g.asked, "void:"+r.IdempotencyKey)
	return g.void, g.err
}

func captureOf(t *testing.T, s PaymentSession, amount int64, key string) []byte {
	t.Helper()
	raw, err := json.Marshal(PaymentCaptureRequest{Session: s, AmountMinor: amount, IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The dispatch checks both directions of a capture and a void, as it does a start: a request Core could not have sent
// never reaches the processor, and an answer Core could not act on is an error here.
func TestACaptureAndAVoidAreCheckedBothWays(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	held := PaymentSession{ID: "s1", AmountMinor: 1234, Currency: "EUR", Status: PaymentAuthorized, Capture: CaptureManual,
		AuthorizedMinor: 1234}
	g := &holdingGateway{capture: PaymentCaptureResult{Status: PaymentResolved, AmountMinor: 1000}}
	out, handled, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentCapture, captureOf(t, held, 1000, CaptureKeyPrefix+"s1"))
	if err != nil || !handled || !strings.Contains(string(out), `"amount_minor":1000`) {
		t.Fatalf("a good capture: %s, %v, %v", out, handled, err)
	}
	for name, payload := range map[string][]byte{
		"no amount": captureOf(t, held, 0, "k"),
		"no key":    captureOf(t, held, 1000, ""),
		"no id":     captureOf(t, PaymentSession{AmountMinor: 1, Currency: "EUR"}, 1, "k"),
	} {
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentCapture, payload); err == nil {
			t.Errorf("%s: the capture reached the processor", name)
		}
	}
	for name, res := range map[string]PaymentCaptureResult{
		"resolved without the amount taken": {Status: PaymentResolved},
		"rejected with Core's own verdict":  {Status: PaymentRejected, FailureCode: PaymentFailVoided},
		"rejected with no code":             {Status: PaymentRejected},
		"a status a capture does not have":  {Status: PaymentAuthorized},
		"a NUL in the message":              {Status: PaymentPending, FailureMessage: "a\x00b"},
	} {
		g.capture = res
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentCapture, captureOf(t, held, 1000, "k")); err == nil {
			t.Errorf("%s: an answer Core cannot act on went through", name)
		}
	}
	void, _ := json.Marshal(PaymentVoidRequest{Session: held, IdempotencyKey: VoidKeyPrefix + "s1"})
	g.void = PaymentVoidResult{Status: PaymentRejected}
	if _, handled, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentVoid, void); err != nil || !handled {
		t.Fatalf("a good void: %v, %v", handled, err)
	}
	for _, res := range []PaymentVoidResult{{Status: PaymentResolved}, {Status: PaymentPending}} {
		g.void = res
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentVoid, void); err == nil {
			t.Errorf("void answer %+v went through", res)
		}
	}
	noKey, _ := json.Marshal(PaymentVoidRequest{Session: held})
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentVoid, noKey); err == nil {
		t.Error("a void with no key reached the processor")
	}
	// The processor that could not be reached is the gateway's error, passed back: Core asks again.
	g.err = errors.New("timeout")
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentCapture, captureOf(t, held, 1000, "k")); err == nil {
		t.Error("an unanswered capture was answered")
	}
	// A gateway that holds nothing is never sent either hook: not handled.
	plain := &recordingGateway{}
	if _, handled, _ := DispatchPaymentGatewayHook(ctx, nil, plain, HookPaymentCapture, captureOf(t, held, 1000, "k")); handled {
		t.Error("a gateway that does not capture handled a capture")
	}
}

// Serve subscribes a gateway that holds money to the two hooks, and only such a gateway.
func TestAGatewayThatHoldsIsSubscribedToCaptureAndVoid(t *testing.T) {
	t.Parallel()
	type holder struct {
		plainOnly
		*holdingGateway
	}
	has := func(hooks []string, h string) bool {
		for _, x := range hooks {
			if x == h {
				return true
			}
		}
		return false
	}
	got := withProvidedHooks(holder{holdingGateway: &holdingGateway{}}, nil)
	if !has(got, HookPaymentCapture) || !has(got, HookPaymentVoid) {
		t.Errorf("a gateway that holds is subscribed to %v", got)
	}
	if got := withProvidedHooks(gatewayOnly{}, nil); has(got, HookPaymentCapture) || has(got, HookPaymentVoid) {
		t.Errorf("a gateway that holds nothing is subscribed to %v", got)
	}
}

// The consumer's two calls and the gateway's report go where Core serves them, and the client refuses what Core would.
func TestTheHoldCallsAreSentAsCoreReadsThem(t *testing.T) {
	t.Parallel()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Idempotency-Key")+" "+string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"s1","status":"authorized"}}`))
	}))
	t.Cleanup(srv.Close)
	pay := newAPI(srv.URL, "tok", nil).Payments()
	ctx := context.Background()
	if _, err := pay.Capture(ctx, "c1", "s1", 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := pay.Void(ctx, "v1", "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pay.Authorize(ctx, "s1", PaymentAuthorization{AmountMinor: 1234, Currency: " eur", ProviderRef: "pi_1"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /payments/sessions/s1/capture c1 {"amount_minor":1000}`,
		`POST /payments/sessions/s1/void v1 {}`,
		`POST /payments/sessions/s1/authorize  {"amount_minor":1234,"currency":"EUR","provider_ref":"pi_1"}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sent:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for name, call := range map[string]func() error{
		"capture with no key":   func() error { _, err := pay.Capture(ctx, "", "s1", 1); return err },
		"capture of nothing":    func() error { _, err := pay.Capture(ctx, "k", "s1", 0); return err },
		"capture of no session": func() error { _, err := pay.Capture(ctx, "k", " ", 1); return err },
		"void with no key":      func() error { _, err := pay.Void(ctx, "", "s1"); return err },
		"a hold of nothing":     func() error { _, err := pay.Authorize(ctx, "s1", PaymentAuthorization{Currency: "EUR"}); return err },
		"a hold in no currency": func() error { _, err := pay.Authorize(ctx, "s1", PaymentAuthorization{AmountMinor: 1}); return err },
		"a hold with a NUL ref": func() error {
			_, err := pay.Authorize(ctx, "s1", PaymentAuthorization{AmountMinor: 1, Currency: "EUR", ProviderRef: "a\x00"})
			return err
		},
		"a session asked later": func() error { p := validParams(); p.Capture = "later"; return p.Validate() },
		"a hold of a kept method": func() error {
			p := validParams()
			p.Capture, p.AmountMinor = CaptureManual, 0
			p.Save = &PaymentMandate{Purpose: MandatePreOrder, Text: "t", AmountMinor: 1, Currency: "EUR"}
			return p.Validate()
		},
	} {
		if call() == nil {
			t.Errorf("%s: not refused", name)
		}
	}
	if len(got) != 3 {
		t.Errorf("a refused call was sent: %v", got)
	}
	p := validParams()
	p.Capture = CaptureManual
	if err := p.Validate(); err != nil {
		t.Errorf("a manual session: %v", err)
	}
}
