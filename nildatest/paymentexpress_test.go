package nildatest

import (
	"context"
	"errors"
	"net/http"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// EXPRESS WALLET BUTTONS (v0.58.0, EXPERIMENTAL): the kit refuses what Core will refuse about them (plan 10.2, Core's
// X2b) — a re-price after the payer approved, of a session a credential started, through a method that cannot — and
// carries a start's client action only while its session waits on the payer's page.

// expressWorld is a world whose gateway offers PayPal-like buttons: an order kind, re-priced, its press answered
// redirected with the processor's order id for the buttons and no page.
func expressWorld(t *testing.T) world {
	t.Helper()
	w := newWorld(t)
	w.gw.Methods = []nilda.PaymentMethod{
		{Key: "paypal", Label: "PayPal", Test: true, Supports: []string{nilda.SupportRefund, nilda.SupportReprice},
			Express: &nilda.PaymentExpress{Kind: nilda.ExpressOrder, Script: "/buttons.js",
				Origins: nilda.PaymentExpressOrigins{Script: []string{"https://www.paypal.com"}, Frame: []string{"https://*.paypal.com"}},
				Config:  map[string]string{"client_id": "AYSq3RDGsmBLJE-otTkBtM"}}},
		{Key: "wallet", Label: "Apple Pay, Google Pay", Test: true, Supports: []string{nilda.SupportRefund, nilda.SupportReprice},
			CredentialTypes: []string{nilda.CredentialStripeConfirmationToken},
			Express: &nilda.PaymentExpress{Kind: nilda.ExpressToken, Script: "/express.js",
				Origins: nilda.PaymentExpressOrigins{Script: []string{"https://js.stripe.com"}},
				Config:  map[string]string{"publishable_key": "pk_test_51Hx"}}},
		{Key: "card", Label: "Card", Test: true, Supports: []string{nilda.SupportRefund}},
	}
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentRedirected, ClientAction: "5O190127TN364715T",
		ProviderRef: "5O190127TN364715T"}
	return w
}

func pressed(t *testing.T, w world, key string) nilda.PaymentSession {
	t.Helper()
	p := params()
	p.Method = "paypal"
	p.AmountMinor = 48000
	s, err := w.shopAPI.CreateSession(context.Background(), key, p)
	if err != nil || s.Status != nilda.PaymentRedirected || s.ClientAction != "5O190127TN364715T" || s.RedirectURL != "" {
		t.Fatalf("the press: %+v, %v", s, err)
	}
	return s
}

// X1, the kit's side: a re-price is asked of the gateway while the payer is still choosing, and refused once the payer's
// approval reached Core (the gateway asked Confirm) — the amount they approved is the amount — and on a session a
// wallet's credential started, through a method that does not re-price, or after the payment moved on.
func TestRepriceOnlyBeforeThePayerApproves(t *testing.T) {
	w := expressWorld(t)
	ctx := context.Background()
	s := pressed(t, w, "press-1")

	options := []nilda.PaymentShippingOption{{ID: "dhl", Label: "DHL", AmountMinor: 490, Selected: true},
		{ID: "packstation", Label: "DHL Packstation", AmountMinor: 390}}
	got, err := w.shopAPI.Reprice(ctx, "press-1:quote-1", s.ID, nilda.PaymentReprice{AmountMinor: 48490, ShippingOptions: options})
	if err != nil || got.AmountMinor != 48490 || got.Status != nilda.PaymentRedirected || got.ClientAction != s.ClientAction {
		t.Fatalf("a re-price while the payer chooses: %+v, %v", got, err)
	}
	if len(w.gw.Repriced) != 1 || w.gw.Repriced[0].AmountMinor != 48490 || len(w.gw.Repriced[0].ShippingOptions) != 2 ||
		w.gw.Repriced[0].Session.AmountMinor != 48000 {
		t.Fatalf("the gateway was asked %+v", w.gw.Repriced)
	}
	// The same re-price again under its key is answered from the first, never asked twice.
	if again, err := w.shopAPI.Reprice(ctx, "press-1:quote-1", s.ID, nilda.PaymentReprice{AmountMinor: 48490, ShippingOptions: options}); err != nil ||
		again.AmountMinor != 48490 || len(w.gw.Repriced) != 1 {
		t.Fatalf("a repeated re-price: %+v, %v, asked %d times", again, err, len(w.gw.Repriced))
	}

	// The processor's refusal is a 409 and the amount stays; no answer is a 503 and nothing changed.
	w.gw.RepriceResult = nilda.PaymentRepriceResult{FailureMessage: "ORDER_ALREADY_APPROVED"}
	if _, err := w.shopAPI.Reprice(ctx, "press-1:quote-2", s.ID, nilda.PaymentReprice{AmountMinor: 48390}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("the processor's refusal: %v", err)
	}
	w.gw.RepriceResult = nilda.PaymentRepriceResult{}
	w.gw.RepriceErr = errors.New("paypal did not answer")
	if _, err := w.shopAPI.Reprice(ctx, "press-1:quote-3", s.ID, nilda.PaymentReprice{AmountMinor: 48390}); httpStatus(t, err) != http.StatusServiceUnavailable {
		t.Fatalf("no answer: %v", err)
	}
	w.gw.RepriceErr = nil
	if now, _ := w.pay.GetSession(s.ID); now.AmountMinor != 48490 {
		t.Fatalf("a refused or unanswered re-price moved the amount to %d", now.AmountMinor)
	}

	// The payer approves in PayPal's window: the gateway's finish path asks Confirm. From then on, no re-price.
	if res, err := w.gwAPI.Confirm(ctx, s.ID); err != nil || !res.Proceed {
		t.Fatalf("the approval: %+v, %v", res, err)
	}
	asked := len(w.gw.Repriced)
	if _, err := w.shopAPI.Reprice(ctx, "press-1:quote-4", s.ID, nilda.PaymentReprice{AmountMinor: 48390}); httpStatus(t, err) != http.StatusConflict {
		t.Fatalf("a re-price after the payer approved: %v", err)
	}
	if len(w.gw.Repriced) != asked {
		t.Fatal("the gateway was asked to re-price an approved payment")
	}
	// Paid: the client action is gone with the page that needed it, and a re-price is refused as for any ended payment.
	paid, err := w.gwAPI.Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 48490, Currency: "EUR", ProviderRef: "CAPTURE-1"})
	if err != nil || paid.Status != nilda.PaymentResolved || paid.ClientAction != "" {
		t.Fatalf("the capture: %+v, %v", paid, err)
	}

	// A session a wallet's credential started was approved in the sheet: never re-priced.
	p := params()
	p.Method = "wallet"
	p.Credential = &nilda.PaymentCredential{Type: nilda.CredentialStripeConfirmationToken, Token: "ctoken_1"}
	w.gw.CredentialStart = nilda.PaymentStartResult{Status: nilda.PaymentRedirected, ClientAction: "pi_3Q_secret_x"}
	tok, err := w.shopAPI.CreateSession(ctx, "complete-1", p)
	if err != nil || tok.Status != nilda.PaymentRedirected || tok.ClientAction != "pi_3Q_secret_x" {
		t.Fatalf("a token kind's complete that needs 3D Secure: %+v, %v", tok, err)
	}
	if _, err := w.shopAPI.Reprice(ctx, "complete-1:quote", tok.ID, nilda.PaymentReprice{AmountMinor: 100}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a re-price of a credential's session: %v", err)
	}
	// A method that does not re-price, and another consumer's session.
	card := params()
	cs, err := w.shopAPI.CreateSession(ctx, "card-1", card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.shopAPI.Reprice(ctx, "card-1:quote", cs.ID, nilda.PaymentReprice{AmountMinor: 100}); httpStatus(t, err) != http.StatusUnprocessableEntity {
		t.Fatalf("a re-price through a method that cannot: %v", err)
	}
	other, _, srv := w.pay.Core("other", "payment_session")
	defer srv.Close()
	open := pressed(t, w, "press-2")
	if _, err := other.API().Payments().Reprice(ctx, "k", open.ID, nilda.PaymentReprice{AmountMinor: 100}); httpStatus(t, err) != http.StatusNotFound {
		t.Fatalf("another consumer's session: %v", err)
	}
	// A gateway's test re-prices as its consumer would.
	if s, err := w.pay.Reprice(ctx, "shop", open.ID, nilda.PaymentReprice{AmountMinor: 48100}); err != nil || s.AmountMinor != 48100 {
		t.Fatalf("Payments.Reprice: %+v, %v", s, err)
	}
}

// A client action lives only while its session waits on the payer's page: a payment that moved on carries none.
func TestAClientActionIsKeptOnlyWhileRedirected(t *testing.T) {
	w := expressWorld(t)
	ctx := context.Background()
	s := pressed(t, w, "press-1")
	if got, err := w.shopAPI.GetSession(ctx, s.ID); err != nil || got.ClientAction != s.ClientAction {
		t.Fatalf("read back while redirected: %+v, %v", got, err)
	}
	pending, err := w.gwAPI.MarkPending(ctx, s.ID, "5O190127TN364715T")
	if err != nil || pending.Status != nilda.PaymentPending || pending.ClientAction != "" {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	// And a start that answers redirected with nowhere to go is no answer: the session closes, as Core closes it.
	w.gw.Start = nilda.PaymentStartResult{Status: nilda.PaymentRedirected}
	p := params()
	p.Method = "paypal"
	bad, err := w.shopAPI.CreateSession(ctx, "press-3", p)
	if err != nil || bad.Status != nilda.PaymentRejected || bad.FailureCode != nilda.PaymentFailProviderUnavailable {
		t.Fatalf("a redirect to nowhere: %+v, %v", bad, err)
	}
}

// The express describe is held as Core holds it: a consumer is offered each express method with its buttons, and a
// gateway whose describe answer carries a secret in its public configuration offers nothing at all.
func TestAnExpressMethodReachesTheConsumerOnlyWhenItsDescribeHolds(t *testing.T) {
	w := expressWorld(t)
	ctx := context.Background()
	opts, err := w.shopAPI.Methods(ctx, "EUR")
	if err != nil || len(opts) != 3 {
		t.Fatalf("methods: %+v, %v", opts, err)
	}
	if opts[0].Express == nil || opts[0].Express.Kind != nilda.ExpressOrder || opts[0].Express.Config["client_id"] == "" ||
		opts[1].Express == nil || opts[1].Express.Kind != nilda.ExpressToken || opts[2].Express != nil {
		t.Fatalf("the options' buttons: %+v", opts)
	}
	w.gw.Methods[1].Express.Config = map[string]string{"publishable_key": "sk_live_51Hx"}
	if opts, err := w.shopAPI.Methods(ctx, "EUR"); err != nil || len(opts) != 0 {
		t.Fatalf("a describe carrying a secret key still offered %+v (%v)", opts, err)
	}
}
