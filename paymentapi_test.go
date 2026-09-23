package nilda

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// callLog is a stand-in for Core that records each request and answers with one canned response.
type callLog struct {
	mu       sync.Mutex
	requests []string // "METHOD path?query key=<Idempotency-Key> body"
	status   int
	answer   string
}

func (c *callLog) server(t *testing.T) *API {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		line := r.Method + " " + r.URL.EscapedPath()
		if r.URL.RawQuery != "" {
			line += "?" + r.URL.RawQuery
		}
		c.requests = append(c.requests, line+" key="+r.Header.Get("Idempotency-Key")+" "+string(body))
		status, answer := c.status, c.answer
		c.mu.Unlock()
		if status == 0 {
			status = http.StatusOK
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, answer)
	}))
	t.Cleanup(srv.Close)
	return newAPI(srv.URL, "tok", []string{"read:payments", "write:payments"})
}

// TestThePaymentCallsGoWhereCoreServesThem pins each call's method, path, key and body — the other half of the
// wire contract, which Core's routes are built to.
func TestThePaymentCallsGoWhereCoreServesThem(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := &callLog{answer: `{"data":{"id":"s/1","status":"redirected","amount_minor":1234}}`}
	pay := log.server(t).Payments()

	s, err := pay.CreateSession(ctx, "order-1#1", PaymentSessionParams{Gateway: "stripe", Method: "card",
		Reference: "order-1", AmountMinor: 1234, Currency: " eur ", ReturnURL: "https://site.test/thanks"})
	if err != nil || s.ID != "s/1" || s.Status != PaymentRedirected || s.AmountMinor != 1234 {
		t.Fatalf("CreateSession did not read Core's data envelope: %+v, %v", s, err)
	}
	_, _ = pay.GetSession(ctx, "s/1")
	_, _ = pay.CreateRefund(ctx, "refund-1", "s/1", PaymentRefundParams{AmountMinor: 500, Reason: "returned"})
	_, _ = pay.GetRefund(ctx, "r1")
	_, _ = pay.Resolve(ctx, "s/1", PaymentResolution{AmountMinor: 1234, Currency: "eur", ProviderRef: "pi_1"})
	_, _ = pay.Reject(ctx, "s/1", PaymentRejection{FailureCode: PaymentFailDeclined, FailureMessage: "declined"})
	_, _ = pay.MarkPending(ctx, "s/1", "pi_1")
	_, _ = pay.Confirm(ctx, "s/1")
	_, _ = pay.ResolveRefund(ctx, "r1", "re_1")
	_, _ = pay.RejectRefund(ctx, "r1", PaymentRejection{FailureCode: PaymentFailProviderUnavailable})

	want := []string{
		`POST /payments/sessions key=order-1#1 {"gateway":"stripe","method":"card","reference":"order-1","amount_minor":1234,"currency":"EUR","return_url":"https://site.test/thanks"}`,
		`GET /payments/sessions/s%2F1 key= `,
		`POST /payments/sessions/s%2F1/refunds key=refund-1 {"amount_minor":500,"reason":"returned"}`,
		`GET /payments/refunds/r1 key= `,
		`POST /payments/sessions/s%2F1/resolve key= {"amount_minor":1234,"currency":"EUR","provider_ref":"pi_1"}`,
		`POST /payments/sessions/s%2F1/reject key= {"failure_code":"declined","failure_message":"declined"}`,
		`POST /payments/sessions/s%2F1/pending key= {"provider_ref":"pi_1"}`,
		`POST /payments/sessions/s%2F1/confirm key= `,
		`POST /payments/refunds/r1/resolve key= {"provider_ref":"re_1"}`,
		`POST /payments/refunds/r1/reject key= {"failure_code":"provider_unavailable"}`,
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.requests) != len(want) {
		t.Fatalf("made %d requests, want %d:\n%s", len(log.requests), len(want), strings.Join(log.requests, "\n"))
	}
	for i := range want {
		if log.requests[i] != want[i] {
			t.Errorf("request %d:\n got %s\nwant %s", i, log.requests[i], want[i])
		}
	}
}

func TestMethodsAsksForOneCurrency(t *testing.T) {
	t.Parallel()
	log := &callLog{answer: `{"data":null}`}
	opts, err := log.server(t).Payments().Methods(context.Background(), "jpy")
	if err != nil || opts == nil || len(opts) != 0 {
		t.Fatalf("an empty answer should be an empty list, not nil: %v %v", opts, err)
	}
	if got := log.requests[0]; got != "GET /payments/methods?currency=JPY key= " {
		t.Errorf("asked %q", got)
	}
	if _, err := log.server(t).Payments().Methods(context.Background(), "XAU"); err == nil {
		t.Error("a currency with no minor unit was asked about")
	}
}

// What the client refuses itself never reaches Core: no request is made at all.
func TestWhatTheClientRefusesIsNeverSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := &callLog{answer: `{"data":{}}`}
	pay := log.server(t).Payments()
	ok := PaymentSessionParams{Gateway: "g", Method: "m", Reference: "r", AmountMinor: 1, Currency: "EUR",
		ReturnURL: "https://site.test/x"}
	for name, call := range map[string]func() error{
		"a session with no key": func() error { _, err := pay.CreateSession(ctx, " ", ok); return err },
		"a session with a key past 255 bytes": func() error {
			_, err := pay.CreateSession(ctx, strings.Repeat("k", 256), ok)
			return err
		},
		"a session Validate refuses": func() error {
			bad := ok
			bad.AmountMinor = 0
			_, err := pay.CreateSession(ctx, "k", bad)
			return err
		},
		"a refund with no key": func() error {
			_, err := pay.CreateRefund(ctx, "", "s1", PaymentRefundParams{AmountMinor: 1})
			return err
		},
		"a refund of nothing": func() error { _, err := pay.CreateRefund(ctx, "k", "s1", PaymentRefundParams{}); return err },
		"a refund of no session": func() error {
			_, err := pay.CreateRefund(ctx, "k", "", PaymentRefundParams{AmountMinor: 1})
			return err
		},
		"reading no session":       func() error { _, err := pay.GetSession(ctx, ""); return err },
		"reading no refund":        func() error { _, err := pay.GetRefund(ctx, " "); return err },
		"resolving with no amount": func() error { _, err := pay.Resolve(ctx, "s1", PaymentResolution{Currency: "EUR"}); return err },
		"resolving no session": func() error {
			_, err := pay.Resolve(ctx, "", PaymentResolution{AmountMinor: 1, Currency: "EUR"})
			return err
		},
		"rejecting with Core's code": func() error {
			_, err := pay.Reject(ctx, "s1", PaymentRejection{FailureCode: PaymentFailConsumerRefused})
			return err
		},
		"a pending reference past 255 bytes": func() error { _, err := pay.MarkPending(ctx, "s1", strings.Repeat("r", 256)); return err },
		"confirming no session":              func() error { _, err := pay.Confirm(ctx, ""); return err },
		"resolving no refund":                func() error { _, err := pay.ResolveRefund(ctx, "", ""); return err },
		"rejecting a refund with no code":    func() error { _, err := pay.RejectRefund(ctx, "r1", PaymentRejection{}); return err },
	} {
		if call() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if len(log.requests) != 0 {
		t.Errorf("refused calls reached Core: %v", log.requests)
	}
}

// A gateway's report carries no key and is never retried by the client: its PROCESSOR retries it, by sending
// the webhook again. A consumer's keyed write is retried, because its key makes that safe.
func TestAReportIsNotRetriedAndAKeyedWriteIs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	log := &callLog{status: http.StatusServiceUnavailable, answer: `{"error":{"code":"INTERNAL","message":"down"}}`}
	api := log.server(t).WithMaxRetries(1)
	_, err := api.Payments().Resolve(ctx, "s1", PaymentResolution{AmountMinor: 1, Currency: "EUR"})
	var ae *APIError
	if !errors.As(err, &ae) || !ae.Retryable() {
		t.Fatalf("a 503 should come back as a retryable APIError, got %v", err)
	}
	log.mu.Lock()
	if len(log.requests) != 1 {
		t.Errorf("a report was sent %d times; the processor's redelivery is its retry", len(log.requests))
	}
	log.requests = nil
	log.mu.Unlock()

	_, _ = api.Payments().CreateSession(ctx, "k1", PaymentSessionParams{Gateway: "g", Method: "m", Reference: "r",
		AmountMinor: 1, Currency: "EUR", ReturnURL: "https://site.test/x"})
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.requests) != 2 {
		t.Errorf("a keyed create was sent %d times; with a key a retry is safe and should happen", len(log.requests))
	}
}

func TestARefusalArrivesAsAnAPIError(t *testing.T) {
	t.Parallel()
	log := &callLog{status: http.StatusConflict,
		answer: `{"error":{"code":"CONFLICT","message":"this payment is already rejected"}}`}
	_, err := log.server(t).Payments().Resolve(context.Background(), "s1", PaymentResolution{AmountMinor: 1, Currency: "EUR"})
	var ae *APIError
	if !errors.As(err, &ae) || !ae.Conflict() || ae.Code != "CONFLICT" {
		t.Fatalf("a 409 should be an APIError that says Conflict, got %v", err)
	}
	var none *API
	if _, err := none.Payments().GetSession(context.Background(), "s1"); !errors.Is(err, errNoAPI) {
		t.Errorf("a plugin with no API access should get the client's own error, got %v", err)
	}
}
