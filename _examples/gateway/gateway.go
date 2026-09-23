// Command testpay is a payment gateway plugin with no processor behind it: the template a real gateway —
// Stripe, PayPal — starts from, and the gateway Nilda's own end-to-end test pays with.
//
// It has everything a real gateway has, in the same places, with the processor played by its own route:
//
//	DescribePayments  one method, "card", any currency, able to refund — marked Test, because no money moves
//	StartPayment      answers with a page to send the payer to, as a processor's hosted checkout does
//	GET  /checkout    that page: the amount, a Pay button and a Decline button
//	POST /pay         what the processor does once the payer has answered: it sends its webhook
//	POST /webhook     the webhook — signature checked over the RAW body, then parsed, then reported to Core
//	RefundPayment     settles on the spot, or later by webhook when "Settle refunds by webhook" is on
//
// To write a real one, copy this directory and replace the three places marked PROCESSOR with calls to yours.
// docs/PAYMENTS.md is the contract this implements.
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
)

func main() { nilda.ServePaymentGateway(&Gateway{}) }

// Gateway is the plugin: a Handler (Init keeps the Core, reads the keys and starts the route) and a
// nilda.PaymentGateway (the three calls Core makes).
type Gateway struct {
	core   *nilda.Core
	secret []byte // signs the webhook and the checkout page's buttons; a real gateway's is the processor's
	later  bool   // refunds settle by webhook rather than on the spot
	routes http.Handler
}

// Init runs once. The keys come from the owner's settings page (plugin.json declares it, `secret: true`),
// and the route is where the processor's webhook arrives.
func (g *Gateway) Init(_ context.Context, core *nilda.Core) (nilda.InitResult, error) {
	if !core.HasCapability("payment_gateway") || !core.HasAPI() {
		return nilda.InitResult{}, fmt.Errorf("testpay needs the payment_gateway capability — add it to plugin.json")
	}
	g.core = core
	g.secret = []byte(core.Setting("signing_secret"))
	g.later = core.SettingBool("refunds_later")

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+core.Route("/checkout"), g.checkout)
	mux.HandleFunc("POST "+core.Route("/pay"), g.pay)
	mux.HandleFunc("POST "+core.Route("/webhook"), g.webhook)
	g.routes = mux
	addr, err := nilda.StartHTTP(mux)
	if err != nil {
		return nilda.InitResult{}, err
	}
	// No hooks to list: Serve subscribes and answers the payment hooks for any Handler that is a
	// nilda.PaymentGateway.
	return nilda.InitResult{RouteAddr: addr}, nil
}

// HandleHook sees only what Serve did not answer, and a gateway subscribes to nothing else.
func (g *Gateway) HandleHook(_ context.Context, hook string, _ []byte) ([]byte, error) {
	return nil, fmt.Errorf("testpay: unexpected hook %q", hook)
}

// HandleEvent is unused: a gateway hears about payments through its processor, not through events.
func (g *Gateway) HandleEvent(context.Context, string, []byte) error { return nil }

// DescribePayments offers the one method — and nothing while there is no secret to check a webhook with,
// because a gateway that cannot tell a real webhook from a forged one must not be taking payments.
func (g *Gateway) DescribePayments(context.Context) ([]nilda.PaymentMethod, error) {
	if len(g.secret) == 0 {
		return nil, nil
	}
	return []nilda.PaymentMethod{{
		Key: "card", Label: "Test card", Description: "No money moves: this is a test gateway.",
		Refunds: true, Test: true,
	}}, nil
}

// StartPayment answers with the page to send the payer to.
func (g *Gateway) StartPayment(_ context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	// PROCESSOR: create the processor's checkout for s, with s.ID as its idempotency key, and answer with its
	// page. A processor that cannot be reached is a result (rejected, provider_unavailable), not an error.
	site, err := url.Parse(s.ReturnURL)
	if err != nil {
		return nilda.PaymentStartResult{Status: nilda.PaymentRejected, FailureCode: nilda.PaymentFailInvalidRequest}, nil
	}
	page := url.URL{Scheme: site.Scheme, Host: site.Host, Path: g.core.Route("/checkout"),
		RawQuery: url.Values{"session": {s.ID}}.Encode()}
	return nilda.PaymentStartResult{Status: nilda.PaymentRedirected, RedirectURL: page.String(),
		ProviderRef: "tp_" + s.ID}, nil
}

// RefundPayment gives money back.
func (g *Gateway) RefundPayment(_ context.Context, r nilda.PaymentRefund, _ nilda.PaymentSession) (nilda.PaymentRefundResult, error) {
	// PROCESSOR: ask the processor to refund r.AmountMinor, with r.ID as its idempotency key.
	if g.later {
		// This processor decides later and says so by webhook: refund.succeeded or refund.failed.
		return nilda.PaymentRefundResult{Status: nilda.RefundPending, ProviderRef: "tpr_" + r.ID}, nil
	}
	return nilda.PaymentRefundResult{Status: nilda.RefundResolved, ProviderRef: "tpr_" + r.ID}, nil
}

// event is what testpay's processor sends to /webhook. Its shape is testpay's own, as Stripe's is Stripe's.
type event struct {
	Type        string `json:"type"` // payment.succeeded | payment.failed | refund.succeeded | refund.failed
	Session     string `json:"session,omitempty"`
	Refund      string `json:"refund,omitempty"`
	AmountMinor int64  `json:"amount_minor,omitempty"`
	Currency    string `json:"currency,omitempty"`
	Ref         string `json:"ref,omitempty"`
}

// webhook is the processor calling back. plugin.json lists it in webhook_paths: Core lets it through without
// a CSRF token and tells it nothing about who is calling, so the SIGNATURE is the only thing that says the
// processor sent it.
func (g *Gateway) webhook(w http.ResponseWriter, r *http.Request) {
	// Read the body ONCE, as bytes, and check the signature over exactly those bytes BEFORE parsing them. A
	// body decoded and encoded again is not the one that was signed; Stripe's own libraries refuse it, and so
	// must yours.
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	w.WriteHeader(g.receive(r.Context(), raw, r.Header.Get("Testpay-Signature")))
}

// receive checks one webhook and reports it to Core, answering with the status the processor should get:
// 200 when there is nothing to send again, 500 when a later delivery could still succeed.
func (g *Gateway) receive(ctx context.Context, raw []byte, signature string) int {
	if !g.signedBy(raw, signature) {
		return http.StatusBadRequest
	}
	var ev event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return http.StatusBadRequest
	}
	pay := g.core.API().Payments()
	switch ev.Type {
	case "payment.succeeded":
		// The amount and currency are the PROCESSOR's, never the session's copied back: Core compares
		// them, and a difference is held for a person.
		_, err := pay.Resolve(ctx, ev.Session, nilda.PaymentResolution{AmountMinor: ev.AmountMinor,
			Currency: ev.Currency, ProviderRef: ev.Ref})
		return g.answer(err, ev)
	case "payment.failed":
		_, err := pay.Reject(ctx, ev.Session, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined,
			FailureMessage: "The test card was declined.", ProviderRef: ev.Ref})
		return g.answer(err, ev)
	case "refund.succeeded":
		_, err := pay.ResolveRefund(ctx, ev.Refund, ev.Ref)
		return g.answer(err, ev)
	case "refund.failed":
		_, err := pay.RejectRefund(ctx, ev.Refund, nilda.PaymentRejection{FailureCode: nilda.PaymentFailDeclined,
			FailureMessage: "The test processor refused the refund.", ProviderRef: ev.Ref})
		return g.answer(err, ev)
	}
	return http.StatusOK // an event this gateway does not act on: acknowledged, so it is not sent again
}

// answer turns Core's reply into the processor's. The same report twice is a no-op in Core, so a processor
// sending a webhook again is always safe — which is why a failure Core may get past later (5xx, 429, Core
// restarting) answers 500: the processor delivers again, and that IS the retry. Anything else — a session
// that already ended another way (409), one that is not this gateway's (404) — would be refused again
// forever, so the processor is told to stop, and the operator's log says why.
func (g *Gateway) answer(err error, ev event) int {
	if err == nil {
		return http.StatusOK
	}
	var ae *nilda.APIError
	if errors.As(err, &ae) && !ae.Retryable() {
		g.core.Log().Warn("testpay: Core refused a webhook's report; it will not be sent again",
			"event", ev.Type, "session", ev.Session, "refund", ev.Refund, "status", ae.Status, "error", err)
		return http.StatusOK
	}
	g.core.Log().Error("testpay: could not report a webhook to Core; the processor will send it again",
		"event", ev.Type, "session", ev.Session, "refund", ev.Refund, "error", err)
	return http.StatusInternalServerError
}

// signedBy reports whether signature is this gateway's HMAC-SHA256 of raw, compared in constant time.
func (g *Gateway) signedBy(raw []byte, signature string) bool {
	if len(g.secret) == 0 {
		return false
	}
	got, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(got, g.mac(raw))
}

func (g *Gateway) mac(parts ...[]byte) []byte {
	m := hmac.New(sha256.New, g.secret)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// ---- the processor's side, which a real gateway does not have ----------------------------------------------

var checkoutPage = template.Must(template.New("checkout").Parse(`<!doctype html>
<html lang="{{.Lang}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Test payment</title></head>
<body>
<main>
<h1>Test payment</h1>
{{if .Open}}
<p>{{.Description}}</p>
<p><strong>{{.Amount}} {{.Currency}}</strong></p>
<p>No money moves: this is a test gateway.</p>
<form method="post" action="{{.Action}}">
<input type="hidden" name="session" value="{{.Session}}"><input type="hidden" name="outcome" value="paid">
<input type="hidden" name="token" value="{{.PayToken}}"><button type="submit">Pay</button></form>
<form method="post" action="{{.Action}}">
<input type="hidden" name="session" value="{{.Session}}"><input type="hidden" name="outcome" value="declined">
<input type="hidden" name="token" value="{{.DeclineToken}}"><button type="submit">Decline</button></form>
{{else}}
<p>This payment is already {{.Status}}.</p>
<p><a href="{{.Back}}">Back to the site</a></p>
{{end}}
</main>
</body></html>`))

// checkout is the processor's hosted page. A real processor knows who its payer is; this page lets whoever
// holds the link answer for the payment — acceptable only because testpay moves no money.
func (g *Gateway) checkout(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("session")
	s, err := g.core.API().Payments().GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, "No such payment.", http.StatusNotFound)
		return
	}
	amount, _ := nilda.FormatMinor(s.AmountMinor, s.Currency)
	lang := s.Locale
	if lang == "" {
		lang = "en"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // the page carries the buttons' tokens
	_ = checkoutPage.Execute(w, map[string]any{
		"Lang": lang, "Open": s.Status == nilda.PaymentRedirected, "Status": s.Status, "Back": s.ReturnURL,
		"Description": s.Description, "Amount": amount, "Currency": s.Currency, "Session": s.ID,
		"Action":   g.core.Route("/pay"),
		"PayToken": g.token(s.ID, "paid"), "DeclineToken": g.token(s.ID, "declined"),
	})
}

// pay is the payer answering the checkout page. It is in webhook_paths only because a browser form cannot
// carry Nilda's CSRF header: the token is what authorises it, and it names one outcome for one session.
func (g *Gateway) pay(w http.ResponseWriter, r *http.Request) {
	id, outcome := r.PostFormValue("session"), r.PostFormValue("outcome")
	want, _ := hex.DecodeString(r.PostFormValue("token"))
	if (outcome != "paid" && outcome != "declined") || len(g.secret) == 0 ||
		!hmac.Equal(want, g.mac([]byte("pay|"+id+"|"+outcome))) {
		http.Error(w, "This payment link is not valid.", http.StatusForbidden)
		return
	}
	s, err := g.core.API().Payments().GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, "No such payment.", http.StatusNotFound)
		return
	}
	// PROCESSOR: a real processor takes the money here and then sends its webhook, over the network, to the
	// route above. testpay sends it to itself — the same bytes, the same signature, the same checks.
	ev := event{Type: "payment.succeeded", Session: s.ID, AmountMinor: s.AmountMinor, Currency: s.Currency,
		Ref: "tp_" + s.ID}
	back := s.ReturnURL
	if outcome == "declined" {
		ev = event{Type: "payment.failed", Session: s.ID, Ref: "tp_" + s.ID}
		back = s.CancelURL
	}
	raw, _ := json.Marshal(ev)
	if code := g.receive(r.Context(), raw, hex.EncodeToString(g.mac(raw))); code != http.StatusOK {
		http.Error(w, "The site could not record the payment. Go back and try again.", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// token authorises one outcome for one session on the checkout page.
func (g *Gateway) token(session, outcome string) string {
	return hex.EncodeToString(g.mac([]byte("pay|" + session + "|" + outcome)))
}
