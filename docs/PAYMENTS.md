# Taking payments — the contract

This is the whole payment contract, for both of its sides: the **gateway** plugin that talks to a processor
(Stripe, PayPal, a bank) and the **consumer** plugin that takes the money (a shop's order, a booking's
deposit, a donation). It is Nilda's decision D-84 (2026-09-23): Core owns the contract and every gateway is its
own plugin.

The Go surface is in this module from **plugin-sdk v0.10.0** (`payment.go`, `paymentapi.go`, `currency.go`).
Core accepts the two capabilities from the release built on v0.10.0; an older Core refuses a manifest that
declares either. **v0.10.4** adds, all additively (`paymentmore.go`): what a method supports, carried on the
session; the payer's addresses; disputes; refunds reversed after success and refunds made at the processor; the
`payment.sync` pull path; a gateway's own secrets kept by Core; and the site's address handed to every plugin
at `Init`. A route a Core before it does not have answers 404, and a hook nobody subscribed to is never sent.

## 1. The shape

```
 consumer (payment_session)          Core                         gateway (payment_gateway)
 ─────────────────────────           ────                         ─────────────────────────
 Payments.Methods  ───────────────►  asks every gateway ────────► payment.describe
 Payments.CreateSession ──────────►  stores the session ────────► payment.start ──► the processor
       ◄──── the session: send the payer to RedirectURL  ◄──────  answers with its page
                                                                          │
                                         the payer pays on the processor's page
                                                                          │
                                     ◄──── Payments.Resolve ◄──── the processor's webhook
 PaymentUpdated  ◄──── told, until it answers
```

- **Nobody talks to anybody but Core.** The consumer never learns which processor took the money; the
  gateway never learns what was bought. Plugins cannot call each other at all.
- **Starting is short; the outcome comes later.** That is how every processor works — Stripe Checkout, PayPal
  Orders, Mollie — and every platform on top of them (Shopify's payments apps, Saleor, Medusa). Nothing in the
  contract waits for a payer.
- **The contract holds no provider code, no keys, and makes no outbound call.** A gateway's keys live on its
  own settings page, or with Core through `SetSecret`, encrypted; its calls to the processor are its own. Core
  makes no payment call of its own: its Stripe button and the Stripe client behind it were retired on 2026-10-10
  (Core stays light — payments live only in gateway plugins). The page builder's PayPal button is a plain form the
  payer's browser posts to PayPal; Core makes no call and holds no key for it.

## 2. Money is an integer in the currency's smallest unit

Every amount is `AmountMinor`, an `int64` of ISO 4217 minor units: €12.34 is `1234`, ¥1,500 is `1500` (a
yen has no subunit), 1.250 KWD is `1250`. Never a float, never a formatted string, never "times a hundred"
— a shop that multiplies everything by a hundred charges ¥150,000 for a ¥1,500 kettle, and nothing along the
way can tell.

```go
digits, ok := nilda.MinorUnits("KWD")            // 3, true — and false for a code ISO 4217 does not price
minor, err := nilda.ParseMinor("12.34", "EUR")   // 1234 — "12.345" is REFUSED, never rounded
text, ok := nilda.FormatMinor(1500, "JPY")       // "1500"
```

The table is ISO 4217 List One as published on 2026-09-17: 165 currencies. The 13 with no minor unit —
gold, silver, fund units, the test code `XTS`, "no currency" `XXX` — are refused.

## 3. A session, a refund, and how each one ends

A **session** is one payment. Its `Status`, a closed set:

| status | means |
|---|---|
| `created` | Core has it; the gateway has not answered yet |
| `redirected` | the payer was sent to the processor's page; nothing is decided |
| `pending` | the processor has it and has not decided — a bank transfer, a review. Also: held for a PERSON (`amount_mismatch`) |
| `resolved` | the money is there |
| `rejected` | it ended without money; `FailureCode` says how |
| `expired` | nothing decided it before `ExpiresAt`, and Core closed it |

The moves allowed, one table in `payment.go` (`PaymentCanMove`), the same one Core and `nildatest` hold:

```
created    → redirected, pending, resolved, rejected, expired
redirected → pending, resolved, rejected, expired
pending    → resolved, rejected                    (never expired: the processor has it)
expired    → pending, resolved                     (a late payment is still money — never dropped)
resolved, rejected: final
```

- **A report of the status the session already has is a no-op** (200, the session unchanged) — the same
  report twice, and also a second `Reject` with a different code, or a second `Resolve` of the same amount with
  a different reference: the first one stands. A processor sends the same webhook more than once as a matter of
  course. A second `Resolve` with a DIFFERENT amount or currency is not the same report: on a resolved payment it
  is refused with 409.
- **A report that would MOVE the session somewhere the table does not allow is refused with 409.** A payment
  that ended one way does not end another way because a late webhook says so.

A **refund** gives money back from a resolved session: `requested → pending → resolved | rejected`
(`RefundCanMove`). Core refuses one that would take the session's refunds — resolved and still in flight —
past what was paid, and one the method could not carry out: **what a method can do is decided by the session's
own `supports`**, the list Core copied from the method when the payment was made (`SupportsOf`: the method's
`Supports`, or `refund` + `partial_refund` when it lists nothing and `Refunds` is true). `refund` is needed for
any refund, `partial_refund` for one of less than was paid. The gateway is never asked again — a gateway whose
key was removed made every old payment unrefundable while Core asked it. A gateway that is not RUNNING is a 503:
ask again later. `RefundedMinor` on the session is the sum of its resolved refunds. `CreateRefund` answers
with the refund **`requested`**: Core asks the gateway from its delivery job a moment later, never inside the
consumer's own call (an owner's refund button has five seconds; a processor may take fifteen), and the consumer
hears how it ends as `payment.refund.updated`.

**A refund can be reversed** after it went through — a bank sent the money back (Stripe: up to 30 days). The
gateway reports it (`ReverseRefund`); only a `resolved` refund can be, and it STAYS `resolved` (final): Core sets
its `reversed_at`, keeps the reason in `failure_code`/`failure_message`, and tells the consumer again through
`payment.refund.updated`. `RefundedMinor` is not changed — what was booked is the consumer's to correct.

**A refund made at the processor**, outside Nilda — by hand in its dashboard — is reported by the gateway
(`ReportExternalRefund`, keyed by the processor's refund id). Core records it as a refund that is `resolved` at
once and `external`, adds it to `RefundedMinor`, and tells the consumer, so the shop's books match the money.

**A dispute** — a payer's bank questioning a payment — is a SECOND object beside the session; the session's state
machine never moves for it (`resolved` stays final). The gateway reports each dispute as a snapshot of what the
processor holds now (`ReportDispute`; read it again from the processor on every event — they arrive out of
order). One dispute per processor id; at most 10 per payment.

| field | means |
|---|---|
| `stage` | `inquiry` (the bank asks, no money moved) → `chargeback` (the money is being taken back); never back |
| `status` | `needs_response` ⇄ `under_review` → `won`, `lost` or `closed` (ended without a verdict); the last three final (`DisputeCanMove`) |
| `funds_held`, `amount_minor`, `refundable`, `respond_by`, `url` | facts the processor reports; still updatable after the verdict (money comes back after "won") |

A snapshot equal to the stored one is a no-op; one that takes a verdict back, or a chargeback back to an
inquiry, is 409 — answer the processor 200, as for a session. The consumer hears every change as
`payment.dispute.updated` if it implements `nilda.PaymentDisputeConsumer`; one that does not is never sent it,
and Core shows the owner that it does not take dispute news. Nothing in the contract ANSWERS a dispute: the owner
does that at the processor, through `url`.

**Money on a payment already refused** — a bank transfer that cleared after the payment was rejected, a charge
made before a lost `payment.start` answer — is still a 409 to the gateway: rejected is final. Core records it for
the owner (the amount on Settings → Payments, "Money to refund", the reference in the audit log): the payer paid
for an order that is closed, and a person refunds it at the processor. Nothing a gateway or consumer reads
changes.

**FailureCode**, a closed set. The gateway reports the first five; the last two are Core's own verdicts, and a
gateway reporting one is refused:

| code | who | means |
|---|---|---|
| `declined` | gateway | the processor said no |
| `cancelled` | gateway, or a person | the payer walked away — not a problem to investigate; also what a person's reject of a pending payment records |
| `provider_unavailable` | gateway, or Core | the processor could not be reached; usually nothing was charged. Core records it too when the gateway does not answer `payment.start`, or answers something Core cannot act on — see "Core asks once" for the charge that can happen anyway |
| `invalid_request` | gateway | the processor cannot take this payment (a currency, a minimum) |
| `expired` | gateway | the processor's page expired before the payer paid |
| `amount_mismatch` | Core, then a person | the processor took a different amount or currency; held `pending` for a person — and kept as the code when that person rejects it |
| `consumer_refused` | Core | the consumer's confirm step said no |

**A person can settle a pending payment.** The site owner, in the admin, may resolve or reject a payment that
is `pending` — one held for `amount_mismatch`, or one a processor has sat on. Resolve moves it to `resolved`
and clears the failure code; a held mismatch is resolved **at the amount the processor took** — the session's
`AmountMinor` becomes that amount, so a consumer books the money that exists and a refund is bounded by it (a
mismatch in another CURRENCY cannot be resolved: it is refunded at the processor and rejected). Reject moves it
to `rejected` with `amount_mismatch` if that is why it was held, `cancelled` otherwise, and the person's note
becomes its `failure_message`. A `resolved` session carries no failure message; the note is in the audit log.
The consumer is told the change like any other.

## 4. Writing a gateway

`_examples/gateway` is a whole gateway with the processor played by its own route — copy it. Everything below
is in it, and its tests show how each piece is tested with nothing running.

### The manifest

```json
{
  "key": "stripe",
  "name": "Stripe",
  "version": "1.0.0",
  "sdk_version": "0.10.1",
  "capabilities": ["payment_gateway", "route", "admin_page"],
  "route_prefix": "/stripe",
  "webhook_paths": ["/webhook"],
  "network": ["api.stripe.com"],
  "admin_pages": [{
    "key": "settings", "label": "Settings", "kind": "settings",
    "fields": [
      { "key": "secret_key", "label": "Secret key", "type": "text", "secret": true, "required": true },
      { "key": "webhook_secret", "label": "Webhook signing secret", "type": "text", "secret": true, "required": true }
    ]
  }]
}
```

- `route` + `webhook_paths`: where the processor's webhook arrives — `/stripe/webhook`. A webhook path needs no
  CSRF token, and Core tells the plugin **nothing** about who called: the processor's signature is the only
  thing that makes a webhook genuine.
- `network`: the processor's API host, or every call to it is refused by Core's egress proxy. Make the calls
  with `nilda.HTTPClient`, which carries the plugin's identity to that proxy.
- `admin_page` with `secret: true` fields: the owner types the keys; Core stores them encrypted, shows them
  masked, and hands them to the plugin at `Init`. Saving them restarts the plugin.

### The code

A gateway is a `nilda.Handler` whose `Init` keeps the `*Core`, reads the keys and starts the webhook route,
and a `nilda.PaymentGateway` — three calls, none of which waits for a payer:

```go
type Gateway struct {
	core   *nilda.Core
	routes http.Handler // kept so a test can deliver a webhook to it (below)
}

func main() { nilda.ServePaymentGateway(&Gateway{}) }

func (g *Gateway) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
	g.core = core
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+core.Route("/webhook"), g.webhook)
	g.routes = mux
	addr, err := nilda.StartHTTP(mux)
	return nilda.InitResult{RouteAddr: addr}, err
}

// The rest of nilda.Handler. Serve answers the payment hooks before HandleHook sees them, and a gateway
// subscribes to nothing else, so both have nothing to do.
func (g *Gateway) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	return nil, fmt.Errorf("unexpected hook %q", hook)
}
func (g *Gateway) HandleEvent(ctx context.Context, eventType string, data []byte) error { return nil }

func (g *Gateway) DescribePayments(ctx context.Context) ([]nilda.PaymentMethod, error)
func (g *Gateway) StartPayment(ctx context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error)
func (g *Gateway) RefundPayment(ctx context.Context, r nilda.PaymentRefund, s nilda.PaymentSession) (nilda.PaymentRefundResult, error)
```

`ServePaymentGateway` is `Serve` with the compiler checking that your type IS a `nilda.PaymentGatewayPlugin`
(a Handler and a PaymentGateway): a method one character off would otherwise leave a plugin Serve cannot
recognise as a gateway. Serve subscribes the three hooks and answers them for you, through
`nilda.DispatchPaymentGatewayHook` — which also checks both directions: a session with no id, amount or
currency never reaches you, and an answer Core could not act on is an error.

**`DescribePayments`** lists what you offer, as configured right now: each `nilda.PaymentMethod` with a
`Key`, a `Label` the payer reads, the `Currencies` it takes (empty = any), whether it `Refunds`, and `Test`
while you hold the processor's TEST keys — Core shows that to the owner and stamps it on every session, so a
test order is never shipped as paid. With `Test`, give a `TestHint`: how a payer pays in test mode, by language
(`"en"` always, then any of fa, ar, de, es, fr) — `{"en": "Use card 4242 4242 4242 4242, any future date, any
CVC."}`. Core hands it to consumers on `PaymentOption.TestHint`, and a shop shows it beside the method in its own
language (`TestHintFor` falls back to English). **No keys yet: offer nothing**, not methods that fail.

**`StartPayment`** creates the processor's checkout and answers with its page:

```go
return nilda.PaymentStartResult{Status: nilda.PaymentRedirected, RedirectURL: checkout.URL, ProviderRef: checkout.ID}, nil
```

- **Core asks once.** An answer that does not arrive — a timeout, a crash, one Core cannot act on — closes
  the session as `rejected`, `provider_unavailable`, and the consumer may offer the payer another try, which is
  a new session. Usually no money has moved: no payer has seen a processor's page. But a processor that charges
  on the spot (a saved card) may have charged before your answer was lost — if yours did, report it anyway
  with `Resolve`: Core answers 409, since rejected is final, and records the money for the owner to refund. **`s.ID` is still
  your idempotency key at the processor**, so a retry inside your own `StartPayment` cannot open two
  checkouts for one session.
- Send the processor `s.ReturnURL` and `s.CancelURL` — absolute, on the site; Core fills CancelURL when the
  consumer gave none. Or send your OWN route as the processor's return address, finish there (PayPal's
  capture, say), and redirect the payer on to `s.ReturnURL`.
- `s.Reference` (the consumer's order id, at most 200 characters — Stripe's own bound on
  `client_reference_id`), `s.Description`, `s.Email` and `s.Locale` are there to hand on.
- Report `ExpiresAt` if the processor's page expires; Core closes the session once that time has passed (24
  hours after it was created if you say nothing — Stripe Checkout's own default). The closing is a sweep that
  runs every five minutes, so a session can stay open up to five minutes past its time, and only one nothing
  has decided (`created`, `redirected`) is closed — a `pending` one is the processor's to decide.
- **A processor you cannot reach is a RESULT, not an error**: `Status: rejected, FailureCode:
  provider_unavailable`. An error counts against the plugin, and enough of them switch it off — a processor's
  bad afternoon would become a site with no checkout until somebody switched it back on.

### What else a gateway may do (v0.10.4)

- **Say what each method supports** (`Supports`: `refund`, `partial_refund`, `disputes`, `sync`). Core copies it
  onto every session paid that way and decides refunds by it.
- **Read the payer's addresses** from the session (`Fulfilment`, `BillingAddress`, `ShippingAddress`) when the
  consumer sent them — for the processor's fraud checks and seller protection; never required.
- **Know where you are**: `core.SiteURL` is the site's public address, so your webhook is `core.SiteURL +
  core.Route("/webhook")`. Core restarts a plugin that has a route when the address changes.
- **Keep a secret your processor shows once** (a webhook signing secret): `Payments.SetSecret(ctx, name, value)`
  and `GetSecret`. Core keeps it encrypted; it survives a restart and a lost kv.
- **Report disputes** (`ReportDispute`), **reversed refunds** (`ReverseRefund`) and **refunds made in the
  processor's dashboard** (`ReportExternalRefund`) — see §3.
- **Answer `payment.sync`** by implementing `nilda.PaymentSyncer`: look the session up at the processor and
  report what you find with the ordinary calls. Core asks when a payer is back on the return page and the session
  is still open, and in a sweep of sessions nothing has decided for a while — so a webhook the processor gave up
  on is not a payment lost. An error means "could not look now".

### The webhook

```go
func (g *Gateway) webhook(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))   // the RAW bytes, read once
	if err != nil || !g.signedByProcessor(raw, r.Header) {   // checked BEFORE they are parsed
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var ev processorEvent
	if err := json.Unmarshal(raw, &ev); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	_, err = g.core.API().Payments().Resolve(r.Context(), ev.SessionID, nilda.PaymentResolution{
		AmountMinor: ev.AmountReceived, Currency: ev.Currency, ProviderRef: ev.PaymentID,
	})
	w.WriteHeader(answerProcessor(err))
}
```

- **Verify over the raw body, before parsing.** A body decoded and encoded again is not the one that was
  signed; Stripe's own libraries refuse it, and so must yours.
- **The amount you resolve with is the PROCESSOR's**, never the session's copied back. Core compares them and a
  difference holds the session `pending` (`amount_mismatch`) for a person.
- `Payments.Reject` with one of your five codes; `Payments.MarkPending` when the processor has it and has not
  decided.
- **Answering the processor.** These reports carry no idempotency key and the client never retries them — the
  processor's redelivery IS the retry, and the same report twice is a no-op. So: a failure Core may get past
  later (5xx, 429, `nilda.APIError.Retryable()`) → answer the processor **500**, and it sends the webhook again.
  Anything else (a 409: the session already ended another way; a 404: not a session of yours — a processor
  account shared by two sites sends you both) → answer **200** and log it, because sending it again would be
  refused again forever.
- Webhooks arrive out of order. The state machine makes that safe: a stale `pending` after `resolved` is a 409.

### The confirm step (optional)

A processor that separates authorising from capturing (card holds, PayPal's approve → capture) gives the
consumer one last say before money moves:

```go
res, err := g.core.API().Payments().Confirm(ctx, sessionID)
var apiErr *nilda.APIError
switch {
case errors.As(err, &apiErr) && !apiErr.Retryable():
	// FINAL: 409, the session expired or already ended; 404, not a session of yours. Asking again gets
	// the same answer — void the authorisation.
case err != nil:
	// A transport error, a 429 or a 5xx — 503 is Core saying it could not ask the consumer. Ask again
	// later: an authorisation holds for days, and capturing unasked is the one wrong move.
case !res.Proceed:
	// Core has ALREADY rejected the session (consumer_refused) — unless it had ended another way while the
	// consumer was asked. Void the authorisation either way.
default:
	// capture, then Resolve with what the processor captured
}
```

### Refunds

`RefundPayment(r, s)` asks the processor to give `r.AmountMinor` back — **`r.ID` is the idempotency key**.
Answer `resolved`, `rejected` (with a code), or `pending` and report the end later with
`Payments.ResolveRefund` / `Payments.RejectRefund` from the webhook. A refund whose answer was lost stays
`requested` and Core asks again — never fails it, since the processor may already have paid it out.

**When you do not know whether the processor paid it** — it could not be reached, it refused your key — return an
ERROR: the refund stays `requested` and Core asks again with a backoff. That error is an ANSWER, not a failure:
Core does not count it toward switching your plugin off (a timeout or a crash still counts), so a processor's
outage during a few refunds never takes the site's checkout down with it. Before creating a refund at the
processor, look for one you already made for `r.ID` (a processor may forget an idempotency key after a day, and
Core asks for longer than that).

### What Core gives a gateway

- **15 seconds** for each payment hook, instead of the ordinary plugin call's 5 — a processor's API on a
  slow day, not a plugin that is broken. Precisely: at most the longer of 15 seconds and the site's
  `PLUGIN_CALL_TIMEOUT`, so an operator who raised that past 15 is never given less. At most, because
  `payment.start` and `payment.describe` run inside the request that asked — the payer is waiting — and keep
  its deadline: a consumer asking from its own 5-second hook, or from a page with 1.5 seconds for every
  plugin, gives you that much. `payment.refund` runs from Core's delivery job and has the whole budget.
- Its own sessions only: every other session answers 404, to reads and to reports alike.

### Testing a gateway with nothing running

```go
pay := nildatest.NewPayments()
core, _, srv := pay.Core("stripe", "payment_gateway", "route", "admin_page")
defer srv.Close()
core.RoutePrefix = "/stripe"
nildatest.SetSettings(core, map[string]any{"webhook_secret": "whsec_test"})
g := &Gateway{}
if _, err := g.Init(ctx, core); err != nil {
	t.Fatal(err)
}
pay.Gateway("stripe", g)

s, err := pay.CreateSession(ctx, "shop", nilda.PaymentSessionParams{Gateway: "stripe", Method: "card",
	Reference: "order-1", AmountMinor: 1234, Currency: "EUR", ReturnURL: "https://site.test/thanks"})
res := nildatest.Webhook(g.routes, "/stripe/webhook", signedBody, header)   // what the processor sends
got, _ := pay.GetSession(s.ID)                                               // resolved?
```

`nildatest.Payments` is Core's side in memory — the state machine, who may touch what, the idempotency
layer, the confirm step, refunds — and it drives your gateway through the same dispatch Serve does. Its
`Core` gives your plugin an API that lands there, under your plugin's own identity. `nildatest.Webhook`
delivers a request as Core's proxy does on a webhook path: the body byte for byte, the caller's headers, and
no `X-Nilda-*` header.

## 5. Taking payments (a consumer)

Declare `payment_session`. A consumer implements `nilda.PaymentConsumer`, and Serve answers its three hooks:

```go
func (s *Shop) ConfirmPayment(ctx context.Context, p nilda.PaymentSession) (nilda.PaymentConfirmResult, error)
func (s *Shop) PaymentUpdated(ctx context.Context, p nilda.PaymentSession) error
func (s *Shop) RefundUpdated(ctx context.Context, r nilda.PaymentRefund, p nilda.PaymentSession) error
```

**Checkout:**

```go
pay := core.API().Payments()
opts, err := pay.Methods(ctx, "EUR")   // what the payer may choose — empty: this site cannot take EUR
session, err := pay.CreateSession(ctx, order.ID+"#"+strconv.Itoa(order.Attempt), nilda.PaymentSessionParams{
	Gateway: choice.Gateway, Method: choice.Method, Reference: order.ID,
	AmountMinor: order.TotalMinor, Currency: "EUR", Description: "Order " + order.Number,
	ReturnURL: siteURL + "/shop/orders/" + order.ID, Email: order.Email, Locale: order.Locale,
})
if session.Status == nilda.PaymentRedirected {
	http.Redirect(w, r, session.RedirectURL, http.StatusSeeOther)
}
```

- **Create the order BEFORE the session** (pending payment), so the outcome has something to land on.
- **The idempotency key names the attempt** (`order-123#2`): sent again it returns the same session, so a retry
  after a lost answer is safe; a payer's second try after a declined card is a new attempt, a new key.
- `nilda.PaymentSessionParams.Validate` is what Core checks without asking anything; Core also checks the
  gateway offers that method in that currency and that the URLs are on this site (another host is refused: a
  payment flow that returns the payer anywhere is an open redirect).
- **"This site" is the site's public address**: the owner's Settings → General address (`site.base_url`), or
  the server's `APP_BASE_URL` when none is set. `GET /site` answers `base_url` from the setting alone, so a
  consumer that builds its return address from it offers no online payment until the owner sets one, and
  says why, rather than offering methods Core will refuse. When neither is a readable `http(s)://host`
  address, Core refuses the payment (422, "this site has no public address") rather than check nothing.

**`PaymentUpdated` is the one place an order's payment state changes.** Core tells you every change of a
session you created — including the one `CreateSession` already returned — and repeats it until you return
nil. Returning an error IS the way to say "not now": Core does not count it against your plugin (a timeout or a
crash still counts), so a database that blinks during a busy delivery pass does not switch your shop off. So:

- key what you do by `(p.ID, p.Status)`, never by arrival: the same news can come twice;
- you are told the LATEST state, and may never see the ones in between (`redirected → pending → resolved`
  while you were down arrives as `resolved`);
- `resolved` after `expired` is a late payment — the money is real; decide what it means for an order you may
  already have cancelled (reinstate it, or refund it);
- `pending` with `FailureCode == amount_mismatch` is held for a person: ship nothing;
- `resolved` with an `AmountMinor` other than what you asked is a mismatch a person accepted: that is what
  the processor took, and what you book and refund against.

On the payer's return page, read `Payments.GetSession`: they are often back before the processor's webhook.

**`ConfirmPayment`** answers from your own state, fast — stock, price. `Proceed: false` rejects the session
with your `Reason` (shown to the payer); an ERROR means you could not decide: Core answers the gateway 503,
and a gateway that follows §4 asks again later.

**Refunds:** `pay.CreateRefund(ctx, key, session.ID, nilda.PaymentRefundParams{AmountMinor: 500, Reason:
"one item returned"})`, keyed like a session — repeating a refund must not give the money back twice. It answers
`requested`; its end arrives in `RefundUpdated` (Core asks the gateway from its job, and the news may reach you
before `CreateRefund`'s own answer does — record what you asked first, or recognise the refund by its id). A 503
means the gateway is not running or could not be asked: nothing was recorded, and asking again with the same key
runs again.

**The payer, if you know them:** `Fulfilment` (`physical`, `digital`, `none`), `BillingAddress` and
`ShippingAddress` (`PaymentAddress`, country as ISO 3166-1 alpha-2) on the session's parameters. A processor's
fraud checks read them, and PayPal's seller protection needs the shipping address of physical goods. They are
the gateway's to hand on; Core only carries them.

**Disputes:** implement `nilda.PaymentDisputeConsumer` to hear `payment.dispute.updated` — same promise as
`PaymentUpdated`, key it by `(d.ID, d.Stage, d.Status, d.FundsHeld)`. Without it you are never sent one, and the
owner sees that your plugin does not take dispute news. Read a payment's disputes with `ListDisputes`.

**A refund you did not ask for** can reach `RefundUpdated`: `r.External` is a refund made at the processor
outside Nilda — book it; and a refund you were told was `resolved` can come again with `r.ReversedAt` set — the
money went back to the merchant.

**Testing a consumer** — `nildatest.StubGateway` is a gateway whose answers you set, and `Deliver` runs the job
that tells the consumer (Core tells it from a job, never inside the call that changed the session):

```go
pay := nildatest.NewPayments()
pay.Gateway("testpay", &nildatest.StubGateway{})
core, _, srv := pay.Core("shop", "payment_session")
defer srv.Close()
shop := &Shop{}
if _, err := shop.Init(ctx, core); err != nil {
	t.Fatal(err)
}
pay.Consumer("shop", shop)
gw, _, gwSrv := pay.Core("testpay", "payment_gateway")   // to report outcomes as the gateway would
defer gwSrv.Close()

s, _ := core.API().Payments().CreateSession(ctx, "order-1#1", params)
_, _ = gw.API().Payments().Resolve(ctx, s.ID, nilda.PaymentResolution{AmountMinor: 1234, Currency: "EUR"})
owed := pay.Deliver(ctx)   // 0 once the shop has heard; pay.Deliveries() shows every attempt
```

`Deliver` is also what asks a gateway about a refund: `CreateRefund` answers `requested` there as in Core, and
the next `Deliver` sends `payment.refund` and then tells the consumer how it ended.

## 6. The wire — for a plugin not written in Go

Hooks arrive over the plugin protocol (docs/ANY_LANGUAGE.md) with these JSON payloads:

| hook | to | payload in | answer |
|---|---|---|---|
| `payment.describe` | gateway | `{}` | `{"methods":[{"key","label","description","currencies","refunds","test","test_hint","supports"}]}` |
| `payment.start` | gateway | `{"session":{…}}` | `{"status","redirect_url","provider_ref","expires_at","failure_code","failure_message"}` |
| `payment.refund` | gateway | `{"refund":{…},"session":{…}}` | `{"status","provider_ref","failure_code","failure_message"}` |
| `payment.session.confirm` | consumer | `{"session":{…}}` | `{"proceed","reason"}` |
| `payment.session.updated` | consumer | `{"session":{…}}` | `{}` — an error means "tell me again" |
| `payment.refund.updated` | consumer | `{"refund":{…},"session":{…}}` | `{}` — the same |
| `payment.sync` | gateway (a `PaymentSyncer`) | `{"session":{…}}` | `{}` — what it found is reported through the calls below |
| `payment.dispute.updated` | consumer (a `PaymentDisputeConsumer`) | `{"dispute":{…},"session":{…}}` | `{}` — an error means "tell me again" |

A session is `{"id","consumer","gateway","method","reference","amount_minor","currency","status","test",
"description","return_url","cancel_url","redirect_url","provider_ref","failure_code","failure_message",
"email","locale","supports","fulfilment","billing_address","shipping_address","expires_at","refunded_minor",
"created_at","updated_at"}`; a refund is `{"id","session","amount_minor","currency","status","reason",
"provider_ref","failure_code","failure_message","external","reversed_at","created_at","updated_at"}`; an address
is `{"name","line1","line2","city","postal_code","region","country"}`; a dispute is `{"id","session",
"provider_ref","stage","status","reason","amount_minor","currency","respond_by","evidence_submitted_at",
"funds_held","refundable","url","opened_at","closed_at","created_at","updated_at"}`.

Calls into Core, under `/api/rest/v1`, with the plugin's token; answers inside `{"data": …}`, refusals as
`{"error":{"code","message"}}`:

| call | who | body | answer |
|---|---|---|---|
| `GET /payments/methods?currency=EUR` | consumer | — | `[{"gateway","method","label","description","refunds","test","test_hint","supports"}]` |
| `POST /payments/sessions` + `Idempotency-Key` | consumer | `{"gateway","method","reference","amount_minor","currency","description","return_url","cancel_url","email","locale","fulfilment","billing_address","shipping_address"}` | 201, the session |
| `GET /payments/sessions/{id}` | either side of it | — | the session |
| `POST /payments/sessions/{id}/refunds` + `Idempotency-Key` | consumer | `{"amount_minor","reason"}` | 201, the refund |
| `GET /payments/refunds/{id}` | either side of it | — | the refund |
| `POST /payments/sessions/{id}/resolve` | gateway | `{"amount_minor","currency","provider_ref"}` | the session |
| `POST /payments/sessions/{id}/reject` | gateway | `{"failure_code","failure_message","provider_ref"}` | the session |
| `POST /payments/sessions/{id}/pending` | gateway | `{"provider_ref"}` | the session |
| `POST /payments/sessions/{id}/confirm` | gateway | — | `{"proceed","reason"}` |
| `POST /payments/refunds/{id}/resolve` | gateway | `{"provider_ref"}` | the refund |
| `POST /payments/refunds/{id}/reject` | gateway | `{"failure_code","failure_message","provider_ref"}` | the refund |
| `POST /payments/refunds/{id}/reverse` | gateway | `{"failure_code","failure_message","provider_ref"}` | the refund, `reversed_at` set |
| `POST /payments/sessions/{id}/external-refunds` | gateway | `{"amount_minor","currency","provider_ref","reason"}` | the refund, `external` |
| `POST /payments/sessions/{id}/disputes` | gateway | `{"provider_ref","stage","status","reason","amount_minor","currency","respond_by","evidence_submitted_at","funds_held","refundable","url","opened_at"}` | the dispute |
| `GET /payments/sessions/{id}/disputes` | either side of it | — | `[dispute]`, oldest first |
| `GET /payments/disputes/{id}` | either side of it | — | the dispute |
| `PUT /payments/secrets/{name}` | gateway | `{"value"}` | `{}` |
| `GET /payments/secrets/{name}` | gateway | — | `{"value"}`; 404 when none is set |

Refusals: **404** a session that is not yours; **409** a report the session has moved past, a refund of an
unpaid session, or a keyed request sent again while the first is still being answered; **422** what `Validate`
checks, a method the gateway does not offer, an address off the site (or no site address to check it
against), a refund past what was paid, a reused key on a different request; **403** a missing capability;
**503** from `confirm` when the consumer could not be asked, and from a refund whose gateway is not running or
could not be asked. A 503 changed nothing, so Core gives its Idempotency-Key back: the same request with the
same key runs again (any other 5xx is kept and answered again for 24 hours, since it may have come after a
write). The token's scopes: `payment_session` → `read:payments write:payments`, `payment_gateway` →
`read:payments report:payments`.

## 7. The bounds

| what | bound | why this number |
|---|---|---|
| `reference` | 200 characters | Stripe's bound on `client_reference_id`, so a gateway can hand it on unchanged |
| `description`, refund `reason`, `failure_message` | 500 characters | Nilda's choice |
| `provider_ref` | 255 bytes | Nilda's choice |
| `return_url`, `cancel_url`, `redirect_url` | 2,048 bytes, absolute http(s) | Nilda's choice |
| `email` | 254 bytes, a bare address | RFC 5321's longest address |
| `locale` | 35 characters, a language tag | RFC 5646's minimum a reader must support |
| an address's `name`, `line1`, `line2` | 200 characters | Nilda's choice |
| an address's `city`, `region` | 100 characters | Nilda's choice |
| an address's `postal_code` | 20 characters | Nilda's choice |
| an address's `country` | 2 capital letters | ISO 3166-1 alpha-2 |
| a dispute's `reason` | 100 characters | the processor's own code, verbatim |
| disputes on one payment | 10 | a split dispute is rare; a loop is not |
| a secret's name | 1–64 of `a-z 0-9 _` | Nilda's choice |
| a secret's value | 4,096 bytes | a signing secret is under a hundred |
| `Idempotency-Key` | 255 bytes | Core's, which is Stripe's |
| a session nothing decided | 24 hours, unless the gateway says | Stripe Checkout's own default |
| a gateway's hook | at most 15 seconds, or `PLUGIN_CALL_TIMEOUT` if that is longer | Core's budget for a processor's API |
| a consumer's hook | 5 seconds, the default of `PLUGIN_CALL_TIMEOUT` | the ordinary plugin call |

## 8. What the contract does not do

- **No line items.** A hosted checkout collects what its processor needs; the consumer sends one amount, a
  description and — when it knows them — the payer's addresses. A gateway that needs more asks the payer on the
  processor's page.
- **No answering a dispute.** Disputes are REPORTED (v0.10.4); the owner answers one at the processor.
- **No saved cards, no subscriptions, no capture/void.** The names are reserved so the door stays additive:
  `credential` and `credential_types` (a delegated payment token for agentic checkout, UCP/ACP — never stored,
  logged or echoed when it is built), and the `supports` tokens `capture`, `partial_capture` and `void`. A guard on
  each side fails if anything takes one of these names before the feature is built.
- **No money arithmetic in Core** beyond comparing amounts and summing refunds. Prices, tax and rounding stay
  the consumer's.
