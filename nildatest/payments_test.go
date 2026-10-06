package nildatest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// The payment fake must refuse what Core refuses and allow what Core allows: a gateway or a consumer tested
// against a kinder fake passes here and fails on the first real site. Each rule below is one Core applies.

// shopConsumer is a consumer that records what it heard and can be made to fail.
type shopConsumer struct {
	heard   []string
	fail    error
	confirm nilda.PaymentConfirmResult
	calls   int
	// asked, when set, runs while the consumer is being asked to confirm — for what happens meanwhile.
	asked func(nilda.PaymentSession)
}

func (c *shopConsumer) ConfirmPayment(_ context.Context, s nilda.PaymentSession) (nilda.PaymentConfirmResult, error) {
	c.calls++
	if c.asked != nil {
		c.asked(s)
	}
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

// downAfterPaying is a StubGateway that stops answering describe when down is set — a gateway restarting.
type downAfterPaying struct {
	StubGateway
	down bool
}

func (g *downAfterPaying) DescribePayments(ctx context.Context) ([]nilda.PaymentMethod, error) {
	if g.down {
		return nil, errors.New("restarting")
	}
	return g.StubGateway.DescribePayments(ctx)
}

// The kit refuses and bounds what Core refuses and bounds (Core's 2026-09-24 whole-plan review, P-m7): a refund
// through a gateway that cannot be asked is UNAVAILABLE — ask again — not a VALIDATION; a processor's reference
// past 255 bytes is refused on a pending report and a refund resolve; a pending report on a pending session
// changes nothing, its reference included.
func TestTheKitRefusesWhatCoreRefuses(t *testing.T) {
	ctx := context.Background()
	pay := NewPayments()
	gw := &downAfterPaying{}
	pay.Gateway("testpay", gw)
	pay.Consumer("shop", &shopConsumer{confirm: nilda.PaymentConfirmResult{Proceed: true}})
	shopCore, _, s1 := pay.Core("shop", "payment_session")
	gwCore, _, s2 := pay.Core("testpay", "payment_gateway")
	t.Cleanup(s1.Close)
	t.Cleanup(s2.Close)
	shop, gwAPI := shopCore.API().Payments(), gwCore.API().Payments()

	s, err := shop.CreateSession(ctx, "k1", params())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gwAPI.MarkPending(ctx, s.ID, "pi_first"); err != nil {
		t.Fatal(err)
	}
	if again, err := gwAPI.MarkPending(ctx, s.ID, "pi_second"); err != nil || again.ProviderRef != "pi_first" {
		t.Fatalf("a pending report on a pending session changed it: %+v %v", again, err)
	}
	var out json.RawMessage
	long := strings.Repeat("r", 256)
	if err := gwCore.API().Post(ctx, "/payments/sessions/"+s.ID+"/pending", map[string]string{"provider_ref": long}, &out); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a 256-byte reference on a pending report answered %v, want 422", err)
	}
	if _, err := gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	rf, err := shop.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 100})
	if err != nil {
		t.Fatal(err)
	}
	pay.Deliver(ctx)
	if err := gwCore.API().Post(ctx, "/payments/refunds/"+rf.ID+"/resolve", map[string]string{"provider_ref": long}, &out); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a 256-byte reference on a refund resolve answered %v, want 422", err)
	}

	gw.down = true
	if _, err := shop.CreateRefund(ctx, "r2", s.ID, nilda.PaymentRefundParams{AmountMinor: 100}); httpStatus(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("a refund through a gateway that cannot be asked answered %v, want 503 — ask again later", err)
	}
	// Core asks whether the gateway can refund BEFORE it adds up the refunds: an over-refund while the gateway is
	// down is the same 503, not the 422 it will be once the gateway answers.
	if _, err := shop.CreateRefund(ctx, "r-over", s.ID, nilda.PaymentRefundParams{AmountMinor: 5000}); httpStatus(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("an over-refund while the gateway is down answered %v, want Core's 503", err)
	}
	// …and a 503 changed nothing, so the same key asks again once the gateway is back, instead of replaying it.
	gw.down = false
	if rf2, err := shop.CreateRefund(ctx, "r2", s.ID, nilda.PaymentRefundParams{AmountMinor: 100}); err != nil || rf2.Status != nilda.RefundRequested {
		t.Fatalf("the same key after the gateway came back answered %+v %v — the 503 was kept", rf2, err)
	}
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

// heldStart is a StubGateway whose start waits until release is closed — a processor that is slow to answer.
// Only the first start is announced, so a kit that wrongly runs a second one fails at the test's assertion, not
// on a channel closed twice.
type heldStart struct {
	StubGateway
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *heldStart) StartPayment(ctx context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	g.once.Do(func() { close(g.entered) })
	<-g.release
	return g.StubGateway.StartPayment(ctx, s)
}

// A keyed request sent again while the first is still being answered is a 409, as in Core's idempotency layer
// (S-55 of Core's 2026-09-24 whole-plan review: the kit ran both) — whatever its body: Core looks for a running
// claim before it compares bodies, since a claim has no finished answer to compare with.
func TestARepeatWhileTheFirstIsRunningIsRefused(t *testing.T) {
	ctx := context.Background()
	pay := NewPayments()
	gw := &heldStart{entered: make(chan struct{}), release: make(chan struct{})}
	pay.Gateway("testpay", gw)
	pay.Consumer("shop", &shopConsumer{confirm: nilda.PaymentConfirmResult{Proceed: true}})
	shopCore, _, srv := pay.Core("shop", "payment_session")
	t.Cleanup(srv.Close)
	shop := shopCore.API().Payments()

	first := make(chan error, 1)
	go func() {
		_, err := shop.CreateSession(ctx, "k1", params())
		first <- err
	}()
	<-gw.entered
	_, err := shop.CreateSession(ctx, "k1", params())
	other := params()
	other.AmountMinor = 999
	_, otherErr := shop.CreateSession(ctx, "k1", other)
	close(gw.release)
	if httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("the same key while the first was running answered %v, want 409", err)
	}
	if httpStatus(t, otherErr) != http.StatusConflict {
		t.Fatalf("the same key with another body while the first was running answered %v, want Core's 409", otherErr)
	}
	if err := <-first; err != nil {
		t.Fatalf("the first request: %v", err)
	}
}

// panickingStart is a gateway whose own code panics on a start — an author's bug.
type panickingStart struct{ StubGateway }

func (panickingStart) StartPayment(context.Context, nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	panic("an author's bug")
}

// A panic while a keyed request runs is recorded as the 500 it is, as Core's idempotency layer records it: left
// claimed, every retry with that key answered 409 "still being processed" for good.
func TestAPanicUnderAKeyIsA500NotAClaimForever(t *testing.T) {
	ctx := context.Background()
	pay := NewPayments()
	pay.Gateway("testpay", &panickingStart{})
	shopCore, _, srv := pay.Core("shop", "payment_session")
	t.Cleanup(srv.Close)
	shop := shopCore.API().Payments()
	if _, err := shop.CreateSession(ctx, "k1", params()); err == nil {
		t.Fatal("a start that panicked answered no error")
	}
	if _, err := shop.CreateSession(ctx, "k1", params()); httpStatus(t, err) != http.StatusInternalServerError {
		t.Fatalf("the same key after the panic answered %v, want the recorded 500", err)
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
	// A different mismatch is news; the same one again is not (Core's rule: the processor's redelivery). What
	// the processor reported is the owner's to read in Core — the wire carries no failure message for it.
	before := len(w.pay.Deliveries())
	w.pay.Deliver(ctx)
	heard := len(w.pay.Deliveries())
	got, err = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "USD"})
	if err != nil || got.Status != nilda.PaymentPending || got.FailureCode != nilda.PaymentFailAmountMismatch || got.FailureMessage != "" {
		t.Fatalf("a payment in another currency: %+v %v", got, err)
	}
	if again, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "USD"}); err != nil || again.UpdatedAt != got.UpdatedAt {
		t.Fatalf("the same mismatch reported again changed the session: %+v %v", again, err)
	}
	w.pay.Deliver(ctx)
	if n := len(w.pay.Deliveries()) - heard; n != 1 || heard <= before {
		t.Fatalf("the consumer heard %d deliveries for a new mismatch and its redelivery, want exactly 1", n)
	}
	got, err = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
	if err != nil || got.Status != nilda.PaymentResolved || got.FailureCode != "" {
		t.Fatalf("the right amount afterwards resolves and clears the hold: %+v %v", got, err)
	}
}

// A person's decision, as Core's Decide (admin.go): a held mismatch is resolved AT THE PROCESSOR'S AMOUNT — the
// consumer books what was taken, and a refund in full is bounded by it — one in another currency is refused, and a
// rejection keeps amount_mismatch as its reason with the note as its message. Only a pending payment is decided.
func TestAPersonDecidesAPendingPaymentAsInCore(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()

	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	if _, err := w.pay.Decide(s.ID, "resolve", ""); err == nil {
		t.Fatal("a redirected payment was decided by a person — Core decides only a pending one")
	}
	_, _ = w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1200, Currency: "EUR"})
	w.pay.Deliver(ctx)
	got, err := w.pay.Decide(s.ID, "resolve", "the payer's bank took a fee")
	if err != nil || got.Status != nilda.PaymentResolved || got.AmountMinor != 1200 || got.FailureCode != "" || got.FailureMessage != "" {
		t.Fatalf("resolving a held 1200 of 1234: %+v %v — want resolved at the processor's 1200, no failure fields", got, err)
	}
	w.pay.Deliver(ctx)
	if d := w.pay.Deliveries(); d[len(d)-1].Session.Status != nilda.PaymentResolved || d[len(d)-1].Session.AmountMinor != 1200 {
		t.Fatalf("the consumer was told %+v, want resolved at 1200", d[len(d)-1].Session)
	}
	if _, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1234}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Error("a refund of the amount asked — past the 1200 taken — was accepted")
	}

	other, _ := w.shopAPI.CreateSession(ctx, "k2", params())
	_, _ = w.gwAPI.Resolve(ctx, other.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "USD"})
	if _, err := w.pay.Decide(other.ID, "resolve", ""); err == nil {
		t.Fatal("a payment the processor took in another currency was resolved — Core refuses to book it")
	}
	got, err = w.pay.Decide(other.ID, "reject", "refunded at the processor")
	if err != nil || got.Status != nilda.PaymentRejected || got.FailureCode != nilda.PaymentFailAmountMismatch ||
		got.FailureMessage != "refunded at the processor" {
		t.Fatalf("rejecting a held mismatch: %+v %v", got, err)
	}

	sat, _ := w.shopAPI.CreateSession(ctx, "k3", params())
	_, _ = w.gwAPI.MarkPending(ctx, sat.ID, "pi_1")
	if got, err := w.pay.Decide(sat.ID, "reject", ""); err != nil || got.FailureCode != nilda.PaymentFailCancelled {
		t.Fatalf("rejecting a payment a processor sat on: %+v %v — want cancelled", got, err)
	}
	// Both refusals on a payment that is still pending, so neither is the pending rule answering for them.
	last, _ := w.shopAPI.CreateSession(ctx, "k4", params())
	_, _ = w.gwAPI.MarkPending(ctx, last.ID, "")
	if _, err := w.pay.Decide(last.ID, "reject", strings.Repeat("é", 501)); err == nil {
		t.Error("a 501-character note was accepted — Core's bound is 500")
	}
	if _, err := w.pay.Decide(last.ID, "maybe", ""); err == nil {
		t.Error("a decision that is neither resolve nor reject was accepted")
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

	// A gateway that could not be asked at all: closed as provider_unavailable with NO message, as Core writes
	// none — a consumer that shows FailureMessage must not show text here that a real site never sends.
	pay := NewPayments()
	pay.Gateway("testpay", &failingStart{})
	shopCore, _, srv := pay.Core("shop", "payment_session")
	t.Cleanup(srv.Close)
	s, err = shopCore.API().Payments().CreateSession(ctx, "k3", params())
	if err != nil || s.Status != nilda.PaymentRejected || s.FailureCode != nilda.PaymentFailProviderUnavailable || s.FailureMessage != "" {
		t.Fatalf("a start the gateway could not answer: %+v %v", s, err)
	}
}

// failingStart is a gateway whose start call fails — its processor timed out.
type failingStart struct{ StubGateway }

func (failingStart) StartPayment(context.Context, nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	return nilda.PaymentStartResult{}, errors.New("processor timeout")
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

// A consumer's "no" is written only onto a session that can still be refused (Core's Confirm): the processor's
// webhook that settles it while the consumer is being asked stands, with no refusal's code on a paid session.
// And the reason is cut to Core's 500 characters, stored and answered alike.
func TestAConfirmRefusalDoesNotOverwriteWhatHappenedMeanwhile(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	w.shop.confirm = nilda.PaymentConfirmResult{Proceed: false, Reason: "sold out"}
	w.shop.asked = func(s nilda.PaymentSession) {
		if _, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"}); err != nil {
			t.Errorf("the webhook's resolve: %v", err)
		}
	}
	if res, err := w.gwAPI.Confirm(ctx, s.ID); err != nil || res.Proceed {
		t.Fatalf("confirm: %+v %v", res, err)
	}
	got, _ := w.pay.GetSession(s.ID)
	if got.Status != nilda.PaymentResolved || got.FailureCode != "" || got.FailureMessage != "" {
		t.Fatalf("a refusal after the payment settled was written onto it: %+v", got)
	}

	w.shop.asked = nil
	w.shop.confirm = nilda.PaymentConfirmResult{Proceed: false, Reason: strings.Repeat("é", 501)}
	s2, _ := w.shopAPI.CreateSession(ctx, "k2", params())
	res, err := w.gwAPI.Confirm(ctx, s2.ID)
	got, _ = w.pay.GetSession(s2.ID)
	if err != nil || res.Reason != strings.Repeat("é", 500) || got.FailureMessage != res.Reason {
		t.Fatalf("a 501-character reason: answered %d characters, stored %d (%v), want 500 both",
			len([]rune(res.Reason)), len([]rune(got.FailureMessage)), err)
	}
}

// Core reads a report's body before it looks for the session: a malformed report is 422 whether or not the
// session exists, and a currency is compared the way Core normalises it (" eur" is EUR).
func TestAReportIsReadBeforeItsSessionAsInCore(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	gwCore, _, srv := w.pay.Core("testpay", "payment_gateway")
	t.Cleanup(srv.Close)
	var out json.RawMessage
	for _, c := range []struct{ path, body string }{
		{"/payments/sessions/nope/resolve", `{"amount_minor":0,"currency":"EUR"}`},
		{"/payments/sessions/nope/reject", `{"failure_code":"not-a-code"}`},
		{"/payments/sessions/nope/pending", `{"provider_ref":"` + strings.Repeat("r", 256) + `"}`},
		{"/payments/refunds/nope/resolve", `{"provider_ref":"` + strings.Repeat("r", 256) + `"}`},
		{"/payments/refunds/nope/reject", `{"failure_code":"not-a-code"}`},
	} {
		if err := gwCore.API().Post(ctx, c.path, json.RawMessage(c.body), &out); httpStatus(t, err) != http.StatusUnprocessableEntity {
			t.Errorf("POST %s %s on no session answered %v, want Core's 422", c.path, c.body, err)
		}
	}
	s, _ := w.shopAPI.CreateSession(ctx, "k1", params())
	var got struct {
		Data nilda.PaymentSession `json:"data"`
	}
	if err := gwCore.API().Post(ctx, "/payments/sessions/"+s.ID+"/resolve", json.RawMessage(`{"amount_minor":1234,"currency":" eur"}`), &got); err != nil || got.Data.Status != nilda.PaymentResolved {
		t.Fatalf(`a resolve in " eur" of an EUR session: %+v %v — Core normalises it and resolves`, got.Data, err)
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
	// REQUESTED: as in Core, the gateway is asked by the delivery job, never inside the consumer's call.
	first, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 1000})
	if err != nil || first.Status != nilda.RefundRequested {
		t.Fatalf("refund: %+v %v", first, err)
	}
	w.pay.Deliver(ctx)
	if got, _ := w.pay.GetRefund(first.ID); got.Status != nilda.RefundPending {
		t.Fatalf("after Deliver the refund is %q, want the gateway's answer (pending)", got.Status)
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
		created, err := w.shopAPI.CreateRefund(ctx, "r1", s.ID, nilda.PaymentRefundParams{AmountMinor: 500})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		w.pay.Deliver(ctx) // the job asks the gateway, as Core's does
		rf, _ := w.pay.GetRefund(created.ID)
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
