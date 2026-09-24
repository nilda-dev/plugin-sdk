package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"gitlab.com/nildalabs/nilda-sdk/plugin-sdk/contract"
)

// THE STATE MACHINE, written out move by move: every pair of statuses, and whether it may happen. Core holds
// its own table to this one (a test in Core, from the release built on this SDK), and nildatest refuses what
// it refuses — so a change here is a change to what every gateway and every consumer may see.
func TestThePaymentStateMachineIsTheOneWritten(t *testing.T) {
	t.Parallel()
	statuses := []string{PaymentCreated, PaymentRedirected, PaymentPending, PaymentResolved, PaymentRejected, PaymentExpired}
	allowed := map[string]bool{
		"created>redirected": true, "created>pending": true, "created>resolved": true, "created>rejected": true,
		"created>expired":    true,
		"redirected>pending": true, "redirected>resolved": true, "redirected>rejected": true, "redirected>expired": true,
		"pending>resolved": true, "pending>rejected": true, // a pending payment is the processor's to decide
		"expired>pending": true, "expired>resolved": true, // money after the expiry is still money
	}
	for _, from := range statuses {
		for _, to := range statuses {
			if got, want := PaymentCanMove(from, to), allowed[from+">"+to]; got != want {
				t.Errorf("PaymentCanMove(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	refunds := []string{RefundRequested, RefundPending, RefundResolved, RefundRejected}
	refundAllowed := map[string]bool{
		"requested>pending": true, "requested>resolved": true, "requested>rejected": true,
		"pending>resolved": true, "pending>rejected": true,
	}
	for _, from := range refunds {
		for _, to := range refunds {
			if got, want := RefundCanMove(from, to), refundAllowed[from+">"+to]; got != want {
				t.Errorf("RefundCanMove(%s, %s) = %v, want %v", from, to, got, want)
			}
		}
	}
	if PaymentCanMove("paid", PaymentResolved) || PaymentCanMove(PaymentCreated, "paid") {
		t.Error("a status outside the set moved")
	}
}

func validParams() PaymentSessionParams {
	return PaymentSessionParams{Gateway: "stripe", Method: "card", Reference: "order-1", AmountMinor: 1234,
		Currency: "EUR", ReturnURL: "https://site.test/shop/thanks"}
}

func TestPaymentSessionParamsRefuseWhatCoreRefuses(t *testing.T) {
	t.Parallel()
	if err := validParams().Validate(); err != nil {
		t.Fatalf("the valid params were refused: %v", err)
	}
	ok := func(name string, edit func(*PaymentSessionParams)) {
		t.Helper()
		p := validParams()
		edit(&p)
		if err := p.Validate(); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
	bad := func(name string, edit func(*PaymentSessionParams)) {
		t.Helper()
		p := validParams()
		edit(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok("a reference of 200 two-byte characters", func(p *PaymentSessionParams) { p.Reference = strings.Repeat("é", 200) })
	ok("a description of 500 characters", func(p *PaymentSessionParams) { p.Description = strings.Repeat("d", 500) })
	ok("a cancel url", func(p *PaymentSessionParams) { p.CancelURL = "https://site.test/shop/cart" })
	ok("an email", func(p *PaymentSessionParams) { p.Email = "payer@example.com" })
	ok("a region locale", func(p *PaymentSessionParams) { p.Locale = "pt-BR" })
	ok("a lower-case currency", func(p *PaymentSessionParams) { p.Currency = "eur" })

	bad("no gateway", func(p *PaymentSessionParams) { p.Gateway = " " })
	bad("no method", func(p *PaymentSessionParams) { p.Method = "" })
	bad("no reference", func(p *PaymentSessionParams) { p.Reference = "" })
	bad("a reference of 201 characters", func(p *PaymentSessionParams) { p.Reference = strings.Repeat("é", 201) })
	bad("a description of 501 characters", func(p *PaymentSessionParams) { p.Description = strings.Repeat("d", 501) })
	bad("a zero amount", func(p *PaymentSessionParams) { p.AmountMinor = 0 })
	bad("a negative amount", func(p *PaymentSessionParams) { p.AmountMinor = -1 })
	bad("gold", func(p *PaymentSessionParams) { p.Currency = "XAU" })
	bad("no currency", func(p *PaymentSessionParams) { p.Currency = "" })
	bad("no return url", func(p *PaymentSessionParams) { p.ReturnURL = "" })
	bad("a relative return url", func(p *PaymentSessionParams) { p.ReturnURL = "/shop/thanks" })
	bad("a javascript return url", func(p *PaymentSessionParams) { p.ReturnURL = "javascript:alert(1)" })
	bad("an ftp return url", func(p *PaymentSessionParams) { p.ReturnURL = "ftp://site.test/x" })
	bad("a return url past 2048 bytes", func(p *PaymentSessionParams) {
		p.ReturnURL = "https://site.test/" + strings.Repeat("a", 2048)
	})
	bad("a relative cancel url", func(p *PaymentSessionParams) { p.CancelURL = "/cart" })
	bad("a name in the email", func(p *PaymentSessionParams) { p.Email = "Payer <payer@example.com>" })
	bad("not an email", func(p *PaymentSessionParams) { p.Email = "payer" })
	bad("an email past 254 bytes", func(p *PaymentSessionParams) { p.Email = strings.Repeat("a", 250) + "@example.com" })
	bad("an underscore locale", func(p *PaymentSessionParams) { p.Locale = "en_US" })
	// 38 characters of well-formed subtags: only the length can refuse it.
	bad("a locale past 35", func(p *PaymentSessionParams) { p.Locale = "en" + strings.Repeat("-abcdefgh", 4) })
	bad("a subtag past 8", func(p *PaymentSessionParams) { p.Locale = "en-abcdefghi" })
	bad("an empty subtag", func(p *PaymentSessionParams) { p.Locale = "en--US" })
}

func TestTheGatewaysReportsRefuseWhatCoreRefuses(t *testing.T) {
	t.Parallel()
	if err := (PaymentResolution{AmountMinor: 1, Currency: "EUR", ProviderRef: "pi_1"}).Validate(); err != nil {
		t.Errorf("a valid resolution was refused: %v", err)
	}
	for name, r := range map[string]PaymentResolution{
		"no amount": {Currency: "EUR"}, "gold": {AmountMinor: 1, Currency: "XAU"},
		"a reference past 255 bytes": {AmountMinor: 1, Currency: "EUR", ProviderRef: strings.Repeat("r", 256)},
	} {
		if r.Validate() == nil {
			t.Errorf("resolution with %s accepted", name)
		}
	}
	for _, code := range []string{PaymentFailDeclined, PaymentFailCancelled, PaymentFailProviderUnavailable,
		PaymentFailInvalidRequest, PaymentFailExpired} {
		if err := (PaymentRejection{FailureCode: code}).Validate(); err != nil {
			t.Errorf("a gateway's own code %q was refused: %v", code, err)
		}
	}
	// Core's verdicts are Core's: a gateway cannot claim the consumer refused, or that amounts differed.
	for _, code := range []string{PaymentFailAmountMismatch, PaymentFailConsumerRefused, "", "card_declined"} {
		if (PaymentRejection{FailureCode: code}).Validate() == nil {
			t.Errorf("a gateway reporting %q was accepted", code)
		}
	}
	if (PaymentRejection{FailureCode: PaymentFailDeclined, FailureMessage: strings.Repeat("m", 501)}).Validate() == nil {
		t.Error("a failure message past 500 characters was accepted")
	}
	if (PaymentRefundParams{AmountMinor: 0}).Validate() == nil || (PaymentRefundParams{AmountMinor: 1,
		Reason: strings.Repeat("r", 501)}).Validate() == nil {
		t.Error("a refund of nothing, or with a reason past 500 characters, was accepted")
	}
	if err := (PaymentRefundParams{AmountMinor: 1, Reason: "returned"}).Validate(); err != nil {
		t.Errorf("a valid refund was refused: %v", err)
	}
}

// jsonKeys is every JSON key a value marshals to, at the top level.
func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestTheWireNamesAreTheContract pins every JSON name, because the names ARE the contract: Core and a gateway
// written in another language meet on them, and a renamed field unmarshals into a zero value on the other
// side with no error at all — an amount of 0, a session with no id.
func TestTheWireNamesAreTheContract(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	full := PaymentSession{ID: "i", Consumer: "c", Gateway: "g", Method: "m", Reference: "r", AmountMinor: 1,
		Currency: "EUR", Status: "s", Test: true, Description: "d", ReturnURL: "u", CancelURL: "u", RedirectURL: "u",
		ProviderRef: "p", FailureCode: "f", FailureMessage: "f", Email: "e", Locale: "l", ExpiresAt: &at,
		RefundedMinor: 1, CreatedAt: at, UpdatedAt: at}
	refund := PaymentRefund{ID: "i", Session: "s", AmountMinor: 1, Currency: "EUR", Status: "s", Reason: "r",
		ProviderRef: "p", FailureCode: "f", FailureMessage: "f", CreatedAt: at, UpdatedAt: at}
	for name, c := range map[string]struct {
		v    any
		want string
	}{
		"PaymentSession": {full, "amount_minor cancel_url consumer created_at currency description email expires_at " +
			"failure_code failure_message gateway id locale method provider_ref redirect_url reference refunded_minor " +
			"return_url status test updated_at"},
		"PaymentRefund": {refund, "amount_minor created_at currency failure_code failure_message id provider_ref " +
			"reason session status updated_at"},
		"PaymentMethod": {PaymentMethod{Key: "k", Label: "l", Description: "d", Currencies: []string{"EUR"},
			Refunds: true, Test: true}, "currencies description key label refunds test"},
		"PaymentOption": {PaymentOption{Gateway: "g", Method: "m", Label: "l", Description: "d", Refunds: true,
			Test: true}, "description gateway label method refunds test"},
		"PaymentSessionParams": {PaymentSessionParams{Gateway: "g", Method: "m", Reference: "r", AmountMinor: 1,
			Currency: "EUR", Description: "d", ReturnURL: "u", CancelURL: "u", Email: "e", Locale: "l"},
			"amount_minor cancel_url currency description email gateway locale method reference return_url"},
		"PaymentRefundParams": {PaymentRefundParams{AmountMinor: 1, Reason: "r"}, "amount_minor reason"},
		"PaymentResolution": {PaymentResolution{AmountMinor: 1, Currency: "EUR", ProviderRef: "p"},
			"amount_minor currency provider_ref"},
		"PaymentRejection": {PaymentRejection{FailureCode: "f", FailureMessage: "m", ProviderRef: "p"},
			"failure_code failure_message provider_ref"},
		"PaymentStartResult": {PaymentStartResult{Status: "s", RedirectURL: "u", ProviderRef: "p", ExpiresAt: &at,
			FailureCode: "f", FailureMessage: "m"}, "expires_at failure_code failure_message provider_ref redirect_url status"},
		"PaymentRefundResult": {PaymentRefundResult{Status: "s", ProviderRef: "p", FailureCode: "f",
			FailureMessage: "m"}, "failure_code failure_message provider_ref status"},
		"PaymentConfirmResult":    {PaymentConfirmResult{Proceed: true, Reason: "r"}, "proceed reason"},
		"PaymentDescribeResponse": {PaymentDescribeResponse{}, "methods"},
		"PaymentStartRequest":     {PaymentStartRequest{}, "session"},
		"PaymentRefundRequest":    {PaymentRefundRequest{}, "refund session"},
		"PaymentConfirmRequest":   {PaymentConfirmRequest{}, "session"},
		"PaymentSessionUpdate":    {PaymentSessionUpdate{}, "session"},
		"PaymentRefundUpdate":     {PaymentRefundUpdate{}, "refund session"},
	} {
		if got := strings.Join(jsonKeys(t, c.v), " "); got != c.want {
			t.Errorf("%s marshals to keys %q, want %q", name, got, c.want)
		}
	}
	// A session's amount and its refunded total are always written, zero included: a consumer reading
	// "refunded_minor" must not have to guess what a missing key means.
	if keys := strings.Join(jsonKeys(t, PaymentSession{}), " "); !strings.Contains(keys, "refunded_minor") ||
		!strings.Contains(keys, "amount_minor") {
		t.Errorf("an empty session drops its amounts: %s", keys)
	}
}

// recordingGateway is a gateway that says what it was asked.
type recordingGateway struct {
	calls   []string
	methods []PaymentMethod
	start   PaymentStartResult
	refund  PaymentRefundResult
	err     error
}

func (g *recordingGateway) DescribePayments(context.Context) ([]PaymentMethod, error) {
	g.calls = append(g.calls, "describe")
	return g.methods, g.err
}
func (g *recordingGateway) StartPayment(_ context.Context, s PaymentSession) (PaymentStartResult, error) {
	g.calls = append(g.calls, "start:"+s.ID)
	return g.start, g.err
}
func (g *recordingGateway) RefundPayment(_ context.Context, r PaymentRefund, _ PaymentSession) (PaymentRefundResult, error) {
	g.calls = append(g.calls, "refund:"+r.ID)
	return g.refund, g.err
}

func startPayload(t *testing.T, s PaymentSession) []byte {
	t.Helper()
	raw, err := json.Marshal(PaymentStartRequest{Session: s})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAGatewayIsAskedOnlyAboutASessionItCanCharge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	good := PaymentSession{ID: "s1", AmountMinor: 100, Currency: "EUR"}
	for name, s := range map[string]PaymentSession{
		"no id":       {AmountMinor: 100, Currency: "EUR"},
		"no amount":   {ID: "s1", Currency: "EUR"},
		"no currency": {ID: "s1", AmountMinor: 100},
		"gold":        {ID: "s1", AmountMinor: 100, Currency: "XAU"},
	} {
		g := &recordingGateway{start: PaymentStartResult{Status: PaymentPending}}
		if _, handled, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentStart, startPayload(t, s)); !handled || err == nil {
			t.Errorf("%s: dispatched (handled=%v err=%v)", name, handled, err)
		}
		if len(g.calls) != 0 {
			t.Errorf("%s: the gateway was asked anyway: %v", name, g.calls)
		}
	}
	g := &recordingGateway{start: PaymentStartResult{Status: PaymentPending}}
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentStart, startPayload(t, good)); err != nil ||
		len(g.calls) != 1 || g.calls[0] != "start:s1" {
		t.Errorf("a valid session: calls %v, err %v", g.calls, err)
	}
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentStart, []byte("{not json")); err == nil {
		t.Error("a payload that does not parse was dispatched")
	}
}

func TestAGatewaysAnswerIsOneCoreCanActOn(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := PaymentSession{ID: "s1", AmountMinor: 100, Currency: "EUR"}
	for _, c := range []struct {
		name string
		res  PaymentStartResult
		ok   bool
	}{
		{"redirected to a page", PaymentStartResult{Status: PaymentRedirected, RedirectURL: "https://pay.example/c/1"}, true},
		{"pending", PaymentStartResult{Status: PaymentPending}, true},
		{"resolved on the spot", PaymentStartResult{Status: PaymentResolved, ProviderRef: "pi_1"}, true},
		{"rejected as declined", PaymentStartResult{Status: PaymentRejected, FailureCode: PaymentFailDeclined}, true},
		{"redirected nowhere", PaymentStartResult{Status: PaymentRedirected}, false},
		{"redirected to a relative path", PaymentStartResult{Status: PaymentRedirected, RedirectURL: "/pay"}, false},
		{"redirected to javascript", PaymentStartResult{Status: PaymentRedirected, RedirectURL: "javascript:x"}, false},
		{"rejected with no code", PaymentStartResult{Status: PaymentRejected}, false},
		{"rejected with Core's code", PaymentStartResult{Status: PaymentRejected, FailureCode: PaymentFailConsumerRefused}, false},
		{"a status Core does not take", PaymentStartResult{Status: PaymentCreated}, false},
		{"no status", PaymentStartResult{}, false},
		{"a message past 500", PaymentStartResult{Status: PaymentRejected, FailureCode: PaymentFailDeclined,
			FailureMessage: strings.Repeat("m", 501)}, false},
	} {
		g := &recordingGateway{start: c.res}
		out, _, err := DispatchPaymentGatewayHook(ctx, nil, g, HookPaymentStart, startPayload(t, s))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
			continue
		}
		if c.ok {
			var back PaymentStartResult
			if json.Unmarshal(out, &back) != nil || !reflect.DeepEqual(back, c.res) {
				t.Errorf("%s: answered %s", c.name, out)
			}
		}
	}

	refund := func(res PaymentRefundResult, r PaymentRefund) error {
		raw, _ := json.Marshal(PaymentRefundRequest{Refund: r, Session: s})
		_, _, err := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{refund: res}, HookPaymentRefund, raw)
		return err
	}
	r := PaymentRefund{ID: "r1", AmountMinor: 50}
	for _, st := range []string{RefundPending, RefundResolved} {
		if err := refund(PaymentRefundResult{Status: st}, r); err != nil {
			t.Errorf("refund answered %s: %v", st, err)
		}
	}
	if err := refund(PaymentRefundResult{Status: RefundRejected, FailureCode: PaymentFailDeclined}, r); err != nil {
		t.Errorf("a declined refund: %v", err)
	}
	for name, res := range map[string]PaymentRefundResult{
		"rejected with no code": {Status: RefundRejected}, "requested": {Status: RefundRequested}, "nothing": {},
	} {
		if refund(res, r) == nil {
			t.Errorf("a refund answered %s was accepted", name)
		}
	}
	if refund(PaymentRefundResult{Status: RefundResolved}, PaymentRefund{AmountMinor: 50}) == nil ||
		refund(PaymentRefundResult{Status: RefundResolved}, PaymentRefund{ID: "r1"}) == nil {
		t.Error("a refund with no id or no amount reached the gateway")
	}

	// describe: a list, never null — and an error is the gateway's.
	out, _, err := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{}, HookPaymentDescribe, []byte(`{}`))
	if err != nil || string(out) != `{"methods":[]}` {
		t.Errorf("describe with no methods answered %s, %v", out, err)
	}
	card := PaymentMethod{Key: "card", Label: "Card", Currencies: []string{"EUR", "jpy"}}
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{methods: []PaymentMethod{card,
		{Key: "ideal", Label: "iDEAL"}}}, HookPaymentDescribe, nil); err != nil {
		t.Errorf("two good methods were refused: %v", err)
	}
	for name, methods := range map[string][]PaymentMethod{
		"a method with no key":          {{Label: "Card"}},
		"a method with no label":        {{Key: "card"}},
		"two methods, one key":          {card, {Key: "card", Label: "Card again"}},
		"a currency ISO does not price": {{Key: "gold", Label: "Gold", Currencies: []string{"XAU"}}},
	} {
		if _, _, err := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{methods: methods}, HookPaymentDescribe, nil); err == nil {
			t.Errorf("describe answering %s was accepted", name)
		}
	}
	boom := errors.New("no keys")
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{err: boom}, HookPaymentDescribe, nil); !errors.Is(err, boom) {
		t.Errorf("the gateway's own error was not returned: %v", err)
	}
	if _, handled, _ := DispatchPaymentGatewayHook(ctx, nil, &recordingGateway{}, "content.saved", nil); handled {
		t.Error("a hook that is not a gateway's was claimed")
	}
}

// recordingConsumer is a consumer that says what it was told.
type recordingConsumer struct {
	seen    []string
	confirm PaymentConfirmResult
	err     error
}

func (c *recordingConsumer) ConfirmPayment(_ context.Context, s PaymentSession) (PaymentConfirmResult, error) {
	c.seen = append(c.seen, "confirm:"+s.ID)
	return c.confirm, c.err
}
func (c *recordingConsumer) PaymentUpdated(_ context.Context, s PaymentSession) error {
	c.seen = append(c.seen, "updated:"+s.ID+":"+s.Status)
	return c.err
}
func (c *recordingConsumer) RefundUpdated(_ context.Context, r PaymentRefund, s PaymentSession) error {
	c.seen = append(c.seen, "refund:"+r.ID+":"+r.Status+":"+s.ID)
	return c.err
}

func TestAConsumerIsToldAndItsAnswerCarried(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := PaymentSession{ID: "s1", Status: PaymentResolved}
	c := &recordingConsumer{confirm: PaymentConfirmResult{Proceed: false, Reason: "sold out"}}
	raw, _ := json.Marshal(PaymentConfirmRequest{Session: s})
	out, _, err := DispatchPaymentConsumerHook(ctx, nil, c, HookPaymentConfirm, raw)
	if err != nil || string(out) != `{"proceed":false,"reason":"sold out"}` {
		t.Errorf("confirm answered %s, %v", out, err)
	}
	raw, _ = json.Marshal(PaymentSessionUpdate{Session: s})
	if out, _, err := DispatchPaymentConsumerHook(ctx, nil, c, HookPaymentSessionUpdated, raw); err != nil || string(out) != `{}` {
		t.Errorf("session.updated answered %s, %v", out, err)
	}
	raw, _ = json.Marshal(PaymentRefundUpdate{Refund: PaymentRefund{ID: "r1", Status: RefundResolved}, Session: s})
	if _, _, err := DispatchPaymentConsumerHook(ctx, nil, c, HookPaymentRefundUpdated, raw); err != nil {
		t.Errorf("refund.updated: %v", err)
	}
	if want := []string{"confirm:s1", "updated:s1:resolved", "refund:r1:resolved:s1"}; !reflect.DeepEqual(c.seen, want) {
		t.Errorf("the consumer saw %v, want %v", c.seen, want)
	}
	// An error is what makes Core deliver again: it must reach Core as an error, never as an answer.
	boom := errors.New("database down")
	failing := &recordingConsumer{err: boom}
	for _, hook := range []string{HookPaymentConfirm, HookPaymentSessionUpdated, HookPaymentRefundUpdated} {
		if _, handled, err := DispatchPaymentConsumerHook(ctx, nil, failing, hook, []byte(`{}`)); !handled || !errors.Is(err, boom) {
			t.Errorf("%s: a consumer's error became handled=%v err=%v", hook, handled, err)
		}
	}
	if _, handled, _ := DispatchPaymentConsumerHook(ctx, nil, c, HookPaymentStart, nil); handled {
		t.Error("a gateway's hook was claimed by the consumer dispatch")
	}
}

// paymentPlugin is a Handler that is both a gateway and a consumer, as Serve sees one.
type paymentPlugin struct {
	recordingHandler
	recordingGateway
	recordingConsumer
}

// TestServeAnswersThePaymentHooksItself drives pluginServer — the dispatcher every hook goes through. A plugin
// that implements the interfaces is subscribed AND answered; its own HandleHook never sees a payment hook.
func TestServeAnswersThePaymentHooksItself(t *testing.T) {
	t.Parallel()
	p := &paymentPlugin{recordingGateway: recordingGateway{start: PaymentStartResult{Status: PaymentPending}}}
	s := &pluginServer{handler: p}
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: HookPaymentStart,
		Payload: startPayload(t, PaymentSession{ID: "s1", AmountMinor: 1, Currency: "EUR"})})
	if err != nil || !strings.Contains(string(res.GetPayload()), `"status":"pending"`) {
		t.Fatalf("payment.start answered %s, %v", res.GetPayload(), err)
	}
	raw, _ := json.Marshal(PaymentSessionUpdate{Session: PaymentSession{ID: "s1", Status: PaymentResolved}})
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: HookPaymentSessionUpdated, Payload: raw}); err != nil {
		t.Fatalf("payment.session.updated: %v", err)
	}
	if len(p.recordingHandler.seen) != 0 {
		t.Errorf("the author's HandleHook saw %v; the payment hooks are answered before it", p.recordingHandler.seen)
	}
	if want := []string{"start:s1"}; !reflect.DeepEqual(p.recordingGateway.calls, want) {
		t.Errorf("the gateway saw %v", p.recordingGateway.calls)
	}
	if want := []string{"updated:s1:resolved"}; !reflect.DeepEqual(p.recordingConsumer.seen, want) {
		t.Errorf("the consumer saw %v", p.recordingConsumer.seen)
	}
}

// TestTheKitsBoundsAreTheSDKs — nildatest cannot see this package's unexported bounds, so it keeps a copy; the
// copy is held here, where both are visible, so the kit refuses exactly what the SDK and Core refuse.
func TestTheKitsBoundsAreTheSDKs(t *testing.T) {
	kit := strings.Join(strings.Fields(readText(t, "nildatest/payments.go")), " ")
	for _, want := range []string{
		fmt.Sprintf("maxProviderRef = %d", maxProviderRef),
		fmt.Sprintf("maxPaymentText = %d", maxPaymentText),
	} {
		if !strings.Contains(kit, want) {
			t.Errorf("nildatest/payments.go does not say %q — the kit's bound has left the SDK's (payment.go)", want)
		}
	}
}
