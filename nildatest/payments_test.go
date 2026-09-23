package nildatest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
)

// The payment fake must refuse what Core refuses and allow what Core allows: a gateway or a consumer tested
// against a kinder fake passes here and fails on the first real site. Each rule below is one Core applies.

// shopConsumer is a consumer that records what it heard and can be made to fail.
type shopConsumer struct {
	heard   []string
	fail    error
	confirm nilda.PaymentConfirmResult
	calls   int
}

func (c *shopConsumer) ConfirmPayment(context.Context, nilda.PaymentSession) (nilda.PaymentConfirmResult, error) {
	c.calls++
	return c.confirm, c.fail
}
func (c *shopConsumer) PaymentUpdated(_ context.Context, s nilda.PaymentSession) error {
	c.heard = append(c.heard, "session:"+s.Status)
	return c.fail
}
func (c *shopConsumer) RefundUpdated(_ context.Context, r nilda.PaymentRefund, _ nilda.PaymentSession) error {
	c.heard = append(c.heard, "refund:"+r.Status)
	return c.fail
}

type world struct {
	pay     *Payments
	gw      *StubGateway
	shop    *shopConsumer
	shopAPI *nilda.Payments
	gwAPI   *nilda.Payments
}

func newWorld(t *testing.T) world {
	t.Helper()
	pay := NewPayments()
	gw := &StubGateway{}
	pay.Gateway("testpay", gw)
	shop := &shopConsumer{confirm: nilda.PaymentConfirmResult{Proceed: true}}
	pay.Consumer("shop", shop)
	shopCore, _, s1 := pay.Core("shop", "payment_session")
	gwCore, _, s2 := pay.Core("testpay", "payment_gateway")
	t.Cleanup(s1.Close)
	t.Cleanup(s2.Close)
	return world{pay: pay, gw: gw, shop: shop, shopAPI: shopCore.API().Payments(), gwAPI: gwCore.API().Payments()}
}

func params() nilda.PaymentSessionParams {
	return nilda.PaymentSessionParams{Gateway: "testpay", Method: "card", Reference: "order-1", AmountMinor: 1234,
		Currency: "EUR", ReturnURL: "https://site.test/shop/thanks"}
}

func httpStatus(t *testing.T, err error) int {
	t.Helper()
	var ae *nilda.APIError
	if !errors.As(err, &ae) {
		t.Fatalf("want an APIError, got %v", err)
	}
	return ae.Status
}

func TestASessionIsStartedAndItsConsumerToldFromAJob(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != nilda.PaymentRedirected || s.RedirectURL != "https://pay.test/"+s.ID || s.CancelURL != s.ReturnURL ||
		!s.Test || s.ExpiresAt == nil || len(s.ID) != 36 {
		t.Fatalf("the started session is not what Core returns: %+v", s)
	}
	if len(w.shop.heard) != 0 {
		t.Fatalf("the consumer was told inside its own call: %v — Core tells it from a job", w.shop.heard)
	}
	if owed := w.pay.Deliver(ctx); owed != 0 || strings.Join(w.shop.heard, ",") != "session:redirected" {
		t.Fatalf("Deliver: owed %d, heard %v", owed, w.shop.heard)
	}
}

func TestTheSameKeyGetsTheSameSessionAndAnotherRequestIsRefused(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	a, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	b, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil || b.ID != a.ID {
		t.Fatalf("the same key made a second session: %s then %s (%v)", a.ID, b.ID, err)
	}
	other := params()
	other.AmountMinor = 1
	if _, err := w.shopAPI.CreateSession(ctx, "k1", other); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("the same key on a different request was not refused 422")
	}
	if n := len(w.pay.Sessions()); n != 1 {
		t.Fatalf("%d sessions exist; the key should have allowed one", n)
	}
	// Without a key at all — only another language's client can do that, the SDK refuses first.
	_, _, srv := w.pay.Core("raw", "payment_session")
	defer srv.Close()
	res, err := http.Post(srv.URL+"/payments/sessions", "application/json", strings.NewReader(`{}`))
	if err != nil || res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("a keyless create answered %v %v, want 422", res.StatusCode, err)
	}
}

func TestEachSideTouchesOnlyItsOwnSessions(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())

	otherShop, _, s1 := w.pay.Core("blog", "payment_session")
	otherGw, _, s2 := w.pay.Core("othergw", "payment_gateway")
	defer s1.Close()
	defer s2.Close()
	if _, err := otherShop.API().Payments().GetSession(ctx, s.ID); httpStatus(t, err) != http.StatusNotFound {
		t.Error("another consumer read the session")
	}
	if _, err := otherGw.API().Payments().Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); httpStatus(t, err) != http.StatusNotFound {
		t.Error("another gateway resolved the session")
	}
	if got, err := w.gwAPI.GetSession(ctx, s.ID); err != nil || got.ID != s.ID {
		t.Errorf("the session's own gateway could not read it: %v", err)
	}
	// And each side only its own verbs.
	if _, err := w.shopAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); httpStatus(t, err) != http.StatusForbidden {
		t.Error("a consumer resolved its own payment")
	}
	if _, err := w.gwAPI.CreateSession(ctx, "k", params()); httpStatus(t, err) != http.StatusForbidden {
		t.Error("a gateway created a session")
	}
}

func TestTheStateMachineDecidesEveryReport(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	paid := nilda.PaymentResolution{AmountMinor: 1234, Currency: "eur", ProviderRef: "pi_1"}
	if got, err := w.gwAPI.Resolve(ctx, s.ID, paid); err != nil || got.Status != nilda.PaymentResolved || got.ProviderRef != "pi_1" {
		t.Fatalf("resolve: %+v %v", got, err)
	}
	if got, err := w.gwAPI.Resolve(ctx, s.ID, paid); err != nil || got.Status != nilda.PaymentResolved {
		t.Fatalf("the same resolve again must be a no-op: %+v %v", got, err)
	}
	if _, err := w.gwAPI.Reject(ctx, s.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); httpStatus(t, err) != http.StatusConflict {
		t.Error("a rejection after the money arrived was not refused 409")
	}
	if _, err := w.gwAPI.MarkPending(ctx, s.ID, ""); httpStatus(t, err) != http.StatusConflict {
		t.Error("pending after resolved was not refused 409")
	}
	w.pay.Deliver(ctx)
	if strings.Join(w.shop.heard, ",") != "session:resolved" {
		t.Errorf("the consumer should hear the latest state once, heard %v", w.shop.heard)
	}
}

// A report Core refuses changes nothing at all — not the status, not the reason, not the processor's reference.
func TestARefusedReportChangesNothing(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if _, err := w.gwAPI.Reject(ctx, s.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined, ProviderRef: "pi_1"}); err != nil {
		t.Fatal(err)
	}
	before, _ := w.pay.GetSession(s.ID)
	for name, r := range map[string]nilda.PaymentResolution{
		"the right amount":   {AmountMinor: 1234, Currency: "EUR", ProviderRef: "pi_late"},
		"a different amount": {AmountMinor: 1000, Currency: "EUR", ProviderRef: "pi_late"},
	} {
		if _, err := w.gwAPI.Resolve(ctx, s.ID, r); httpStatus(t, err) != http.StatusConflict {
			t.Errorf("%s: a resolve after the rejection was not refused 409", name)
		}
	}
	if after, _ := w.pay.GetSession(s.ID); after.ProviderRef != before.ProviderRef ||
		after.FailureCode != before.FailureCode || after.Status != before.Status || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Fatalf("a refused report changed the session:\nbefore %+v\nafter  %+v", before, after)
	}
}

// barrierGateway holds DescribePayments, once armed, until two callers are inside it.
type barrierGateway struct {
	StubGateway
	armed   atomic.Bool
	arrived chan struct{}
	release chan struct{}
}

func (g *barrierGateway) DescribePayments(ctx context.Context) ([]nilda.PaymentMethod, error) {
	if g.armed.Load() {
		g.arrived <- struct{}{}
		<-g.release
	}
	return g.StubGateway.DescribePayments(ctx)
}

// Two refunds that each fit alone, asked for at the same moment, must not both be written: 1000 + 1000 of a
// 1234 payment gives back more than was paid.
func TestTwoRefundsThatEachFitCannotBothBeWritten(t *testing.T) {
	w := newWorld(t)
	g := &barrierGateway{arrived: make(chan struct{}, 2), release: make(chan struct{})}
	w.pay.Gateway("testpay", g)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if _, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	g.armed.Store(true)
	errs := make(chan error, 2)
	for _, key := range []string{"r1", "r2"} {
		go func() {
			_, err := w.shopAPI.CreateRefund(ctx, key, s.ID, nilda.PaymentRefundParams{AmountMinor: 1000})
			errs <- err
		}()
	}
	<-g.arrived // both have passed the first check and are asking the gateway about the method
	<-g.arrived
	g.armed.Store(false)
	close(g.release)
	written, refused := 0, 0
	for range 2 {
		if err := <-errs; err == nil {
			written++
		} else if httpStatus(t, err) == http.StatusUnprocessableEntity {
			refused++
		}
	}
	if written != 1 || refused != 1 {
		t.Fatalf("%d refunds written and %d refused; one fits, the second would pass what was paid", written, refused)
	}
}

func TestALatePaymentIsHeardAndAPendingOneIsNotExpired(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if err := w.pay.Expire(s.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil ||
		got.Status != nilda.PaymentResolved {
		t.Fatalf("money after the expiry must be heard: %+v %v", got, err)
	}
	s2, _ := w.shopAPI.CreateSession(ctx, "k2", params())
	if _, err := w.gwAPI.MarkPending(ctx, s2.ID, "pi_2"); err != nil {
		t.Fatal(err)
	}
	if err := w.pay.Expire(s2.ID); err == nil {
		t.Error("a pending payment was expired; the processor has it")
	}
}

func TestAWrongAmountIsHeldForAPersonAndTheRightOneResolves(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	got, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1000, Currency: "EUR"})
	if err != nil || got.Status != nilda.PaymentPending || got.FailureCode != nilda.PaymentFailAmountMismatch {
		t.Fatalf("a short payment: %+v %v", got, err)
	}
	got, err = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "USD"})
	if err != nil || got.Status != nilda.PaymentPending || !strings.Contains(got.FailureMessage, "USD") {
		t.Fatalf("a payment in another currency: %+v %v", got, err)
	}
	got, err = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	if err != nil || got.Status != nilda.PaymentResolved || got.FailureCode != "" {
		t.Fatalf("the right amount afterwards resolves and clears the hold: %+v %v", got, err)
	}
}

func TestReturnAddressesStayOnTheSite(t *testing.T) {
	w := newWorld(t)
	p := params()
	p.ReturnURL = "https://evil.example/phish"
	if _, err := w.shopAPI.CreateSession(context.Background(), "k1", p); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a return address on another host was accepted")
	}
	p = params()
	p.CancelURL = "http://site.test/cart" // another scheme is another origin
	if _, err := w.shopAPI.CreateSession(context.Background(), "k2", p); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a cancel address on another origin was accepted")
	}
}

func TestOnlyWhatAGatewayOffersCanBePaidWith(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Methods = []nilda.PaymentMethod{{Key: "ideal", Label: "iDEAL", Currencies: []string{"EUR"}}}
	opts, err := w.shopAPI.Methods(ctx, "USD")
	if err != nil || len(opts) != 0 {
		t.Fatalf("a EUR-only method was offered for USD: %v %v", opts, err)
	}
	if opts, _ := w.shopAPI.Methods(ctx, "eur"); len(opts) != 1 || opts[0].Gateway != "testpay" || opts[0].Method != "ideal" {
		t.Fatalf("Methods(EUR) = %+v", opts)
	}
	p := params()
	p.Method = "card"
	if _, err := w.shopAPI.CreateSession(ctx, "k1", p); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a method the gateway does not offer was accepted")
	}
	// A gateway that lost its capability is not asked, and offers nothing.
	_, host, srv := w.pay.Core("testpay", "payment_gateway")
	defer srv.Close()
	host.Revoke("payment_gateway")
	if opts, _ := w.shopAPI.Methods(ctx, "EUR"); len(opts) != 0 {
		t.Errorf("a gateway without payment_gateway was offered: %+v", opts)
	}
}

func TestAStartThatFailsClosesTheSessionAndAStartThatRefusesSaysWhy(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	w.gw.Start = nilda.PaymentStartResult{Status: "created"} // an answer Core cannot act on
	s, err := w.shopAPI.CreateSession(ctx, "k1", params())
	if err != nil || s.Status != nilda.PaymentRejected || s.FailureCode != nilda.PaymentFailProviderUnavailable {
		t.Fatalf("a start with no usable answer: %+v %v", s, err)
	}
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentRejected, FailureCode: nilda.PaymentFailInvalidRequest,
		FailureMessage: "below the minimum"}
	s, err = w.shopAPI.CreateSession(ctx, "k2", params())
	if err != nil || s.Status != nilda.PaymentRejected || s.FailureMessage != "below the minimum" {
		t.Fatalf("a refused start: %+v %v", s, err)
	}
}

func TestAConsumerThatFailsIsOwedUntilItHears(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	w.shop.fail = errors.New("database down")
	if owed := w.pay.Deliver(ctx); owed != 1 {
		t.Fatalf("a failed delivery must stay owed, owed %d", owed)
	}
	if owed := w.pay.Deliver(ctx); owed != 1 {
		t.Fatalf("still failing, still owed: %d", owed)
	}
	w.shop.fail = nil
	if owed := w.pay.Deliver(ctx); owed != 0 {
		t.Fatalf("once it answers it is not owed: %d", owed)
	}
	d := w.pay.Deliveries()
	if len(d) != 3 || d[0].Err == nil || d[2].Err != nil || d[2].Session.Status != nilda.PaymentResolved {
		t.Fatalf("deliveries: %+v", d)
	}
}

func TestTheConfirmStepAsksTheConsumer(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if res, err := w.gwAPI.Confirm(ctx, s.ID); err != nil || !res.Proceed {
		t.Fatalf("confirm: %+v %v", res, err)
	}
	w.shop.confirm = nilda.PaymentConfirmResult{Proceed: false, Reason: "sold out"}
	res, err := w.gwAPI.Confirm(ctx, s.ID)
	if err != nil || res.Proceed || res.Reason != "sold out" {
		t.Fatalf("a refusing consumer: %+v %v", res, err)
	}
	got, _ := w.pay.GetSession(s.ID)
	if got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailConsumerRefused {
		t.Fatalf("a refused confirm must reject the session: %+v", got)
	}
	if _, err := w.gwAPI.Confirm(ctx, s.ID); httpStatus(t, err) != http.StatusConflict {
		t.Error("confirming a finished session was not refused 409")
	}
	s2, _ := w.shopAPI.CreateSession(ctx, "k2", params())
	w.shop.fail = errors.New("database down")
	var ae *nilda.APIError
	if _, err := w.gwAPI.Confirm(ctx, s2.ID); !errors.As(err, &ae) || !ae.Retryable() {
		t.Errorf("a consumer that could not decide must be a retryable answer, got %v", err)
	}
}

func TestARefundIsBoundedByWhatWasPaid(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if _, err := w.shopAPI.CreateRefund(ctx, "r0", s.ID, nilda.PaymentRefundParams{AmountMinor: 1}); httpStatus(t, err) != http.StatusConflict {
		t.Error("a refund of an unpaid session was not refused 409")
	}
	_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	first, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1000})
	if err != nil || first.Status != nilda.RefundPending {
		t.Fatalf("refund: %+v %v", first, err)
	}
	// 1000 is still in flight: 1000 + 235 would pass what was paid, even though nothing is refunded yet.
	if _, err := w.shopAPI.CreateRefund(ctx, "r2", s.ID, nilda.PaymentRefundParams{AmountMinor: 235}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a refund past what was paid, counting the one in flight, was accepted")
	}
	if _, err := w.gwAPI.ResolveRefund(ctx, first.ID, "re_1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.pay.GetSession(s.ID); got.RefundedMinor != 1000 {
		t.Fatalf("refunded %d, want 1000", got.RefundedMinor)
	}
	if _, err := w.gwAPI.RejectRefund(ctx, first.ID, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined}); httpStatus(t, err) != http.StatusConflict {
		t.Error("rejecting a resolved refund was not refused 409")
	}
	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card"}} // no refunds
	if _, err := w.shopAPI.CreateRefund(ctx, "r3", s.ID, nilda.PaymentRefundParams{AmountMinor: 1}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a refund through a method that cannot refund was accepted")
	}
}

// failingRefunds is a gateway whose refund call fails until told otherwise.
type failingRefunds struct {
	StubGateway
	down bool
}

func (g *failingRefunds) RefundPayment(ctx context.Context, r nilda.PaymentRefund, s nilda.PaymentSession) (nilda.PaymentRefundResult, error) {
	if g.down {
		return nilda.PaymentRefundResult{}, errors.New("processor unreachable")
	}
	return nilda.PaymentRefundResult{Status: nilda.RefundResolved}, nil
}

// settledByWebhook is a gateway whose processor's webhook settles a refund before the refund call answers.
type settledByWebhook struct {
	StubGateway
	api    *nilda.Payments
	answer nilda.PaymentRefundResult
}

func (g *settledByWebhook) RefundPayment(ctx context.Context, r nilda.PaymentRefund, _ nilda.PaymentSession) (nilda.PaymentRefundResult, error) {
	if _, err := g.api.ResolveRefund(ctx, r.ID, "re_webhook"); err != nil {
		return nilda.PaymentRefundResult{}, err
	}
	return g.answer, nil
}

// The refund call's answer, arriving after the webhook settled the refund, describes a moment already past and
// changes nothing — whatever it says.
func TestARefundAnswerAfterItsWebhookChangesNothing(t *testing.T) {
	for name, answer := range map[string]nilda.PaymentRefundResult{
		"resolved": {Status: nilda.RefundResolved, ProviderRef: "re_answer"},
		"rejected": {Status: nilda.RefundRejected, FailureCode: nilda.PaymentFailDeclined, ProviderRef: "re_answer"},
	} {
		w := newWorld(t)
		ctx := context.Background()
		w.pay.Gateway("testpay", &settledByWebhook{api: w.gwAPI, answer: answer})
		s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
		_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
		rf, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 500})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if rf.Status != nilda.RefundResolved || rf.ProviderRef != "re_webhook" || rf.FailureCode != "" {
			t.Errorf("an answer of %s after the webhook changed the refund: %+v", name, rf)
		}
		if got, _ := w.pay.GetSession(s.ID); got.RefundedMinor != 500 {
			t.Errorf("%s: refunded %d, want 500 counted once", name, got.RefundedMinor)
		}
	}
}

// A refund whose answer was lost may have happened at the processor, so it is asked again — never failed.
func TestAnUnansweredRefundIsAskedAgainNotFailed(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	g := &failingRefunds{down: true}
	w.pay.Gateway("testpay", g)
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	rf, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 500})
	if err != nil || rf.Status != nilda.RefundRequested {
		t.Fatalf("an unanswered refund stays requested: %+v %v", rf, err)
	}
	g.down = false
	w.pay.Deliver(ctx)
	if got, _ := w.pay.GetRefund(rf.ID); got.Status != nilda.RefundResolved {
		t.Fatalf("the delivery job should ask again: %+v", got)
	}
}

// settlesAtOnce is a gateway whose processor reports the money before start has even answered.
type settlesAtOnce struct {
	StubGateway
	api *nilda.Payments
}

func (g *settlesAtOnce) StartPayment(ctx context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	if _, err := g.api.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: s.AmountMinor, Currency: s.Currency}); err != nil {
		return nilda.PaymentStartResult{}, err
	}
	return nilda.PaymentStartResult{Status: nilda.PaymentRedirected, RedirectURL: "https://pay.test/late"}, nil
}

// The report that arrived first is the truth: a start answer describing an earlier moment does not undo it.
func TestAnOutcomeBeforeTheStartAnswerStands(t *testing.T) {
	w := newWorld(t)
	w.pay.Gateway("testpay", &settlesAtOnce{api: w.gwAPI})
	s, err := w.shopAPI.CreateSession(context.Background(), "k1", params())
	if err != nil || s.Status != nilda.PaymentResolved || s.RedirectURL != "" {
		t.Fatalf("the session should stay resolved, with no page to send the payer to: %+v %v", s, err)
	}
}

// Core sends a consumer's hooks only to a plugin holding payment_session.
func TestAConsumerWithoutTheCapabilityIsNotAsked(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	_, host, srv := w.pay.Core("shop", "payment_session")
	defer srv.Close()
	host.Revoke("payment_session")
	if owed := w.pay.Deliver(ctx); owed != 1 || len(w.shop.heard) != 0 {
		t.Fatalf("a consumer without payment_session was told (heard %v, owed %d)", w.shop.heard, owed)
	}
	if _, err := w.gwAPI.Confirm(ctx, s.ID); httpStatus(t, err) != http.StatusServiceUnavailable || w.shop.calls != 0 {
		t.Fatalf("a consumer without payment_session was asked to confirm (calls %d)", w.shop.calls)
	}
}

func TestAWebhookArrivesAsCoresProxySendsIt(t *testing.T) {
	var gotBody []byte
	var gotHeader http.Header
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotHeader = r.Header
	})
	body := []byte(`{"b":1,  "a":2}`) // spacing a re-encode would not keep
	res := Webhook(h, "/stripe/webhook", body, http.Header{
		"Stripe-Signature": {"t=1,v1=abc"}, "X-Nilda-User": {"admin"}, "X-Nilda-Plugin": {"x"},
	})
	if res.StatusCode != http.StatusOK || !bytes.Equal(gotBody, body) {
		t.Fatalf("the body must arrive byte for byte: %q", gotBody)
	}
	if gotHeader.Get("Stripe-Signature") != "t=1,v1=abc" || gotHeader.Get("X-Nilda-User") != "" || gotHeader.Get("X-Nilda-Plugin") != "" {
		t.Fatalf("headers: the caller's kept, every X-Nilda-* dropped — got %v", gotHeader)
	}
}
