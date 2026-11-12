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
at `Init`. **v0.58.0** adds holding the money and taking it later — authorise, then capture or void
(`paymentcapture.go`; §4 "Holding the money", §5 "Take the money later") — and commerce's door D5: the kind of
merchant-initiated charge a kept method's charge is (`OffSession`, `paymentsaved.go`) and, EXPERIMENTAL, a card taken in
person at a reader (`paymentinperson.go`; §4 "A card at a reader", §5 "In person, and with nobody there") — and, EXPERIMENTAL,
express wallet buttons: a method's buttons, a start answered with a client action, a credential from the wallet's sheet
and re-pricing an open session before its payer approves (`paymentexpress.go`; §4 "Express wallet buttons", §5 "Express
checkout and re-pricing"). A route a Core before it does not have
answers 404, and a hook nobody subscribed to is never sent.

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
| `redirected` | the payer was sent to the processor's page — or waits on the site's page, where the gateway's own script goes on with the session's `client_action` (v0.58.0); nothing is decided |
| `pending` | the processor has it and has not decided — a bank transfer, a review. Also: held for a PERSON (`amount_mismatch`) |
| `authorized` | the processor HOLDS the money and has not taken it (v0.58.0): a session asked with `capture` `manual`. Not paid |
| `resolved` | the money is there |
| `rejected` | it ended without money; `FailureCode` says how |
| `expired` | nothing decided it before `ExpiresAt`, and Core closed it |

The moves allowed, one table in `payment.go` (`PaymentCanMove`), the same one Core and `nildatest` hold:

```
created    → redirected, pending, authorized, resolved, rejected, expired
redirected → pending, authorized, resolved, rejected, expired
pending    → authorized, resolved, rejected        (never expired: the processor has it)
authorized → pending, resolved, rejected           (captured slowly, captured, let go or lapsed — never expired:
                                                     its deadline is the processor's, and Core asks before closing it)
expired    → pending, authorized, resolved         (a late payment is still money — never dropped; so is a late hold)
resolved, rejected: final
```

- **A report of the status the session already has is a no-op** (200, the session unchanged) — the same
  report twice, and also a second `Reject` with a different code, or a second `Resolve` of the same amount with
  a different reference: the first one stands. A processor sends the same webhook more than once as a matter of
  course. A second `Resolve` with a DIFFERENT amount or currency is not the same report: on a resolved payment it
  is refused with 409.
- **A report that would MOVE the session somewhere the table does not allow is refused with 409.** A payment
  that ended one way does not end another way because a late webhook says so.

A **refund** gives money back from a resolved session — never from an `authorized` one, which holds money nobody
has taken: that one is voided (409 otherwise): `requested → pending → resolved | rejected`
(`RefundCanMove`). Core refuses one that would take the session's refunds — resolved and still in flight —
past what was paid, and one the method could not carry out: **what a method can do is decided by the session's
own `supports`**, the list Core copied from the method when the payment was made (`SupportsOf`: the method's
`Supports`, or `refund` + `partial_refund` when it lists nothing and `Refunds` is true). `refund` is needed for
any refund, `partial_refund` for one of less than was paid. The gateway is never asked again — a gateway whose
key was removed made every old payment unrefundable while Core asked it. A gateway that is not RUNNING is a 503:
ask again later. `refunded_minor` (`RefundedMinor`) on a session is what its resolved refunds gave back, **less
any reversed** (`reversed_at` set): a reversed refund no longer counts, and the payment can be refunded again up
to what was paid. `CreateRefund` answers
with the refund **`requested`**: Core asks the gateway from its delivery job a moment later, never inside the
consumer's own call (an owner's refund button has five seconds; a processor may take fifteen), and the consumer
hears how it ends as `payment.refund.updated`.

**A refund can be reversed** after it went through — a bank sent the money back (Stripe: up to 30 days). The
gateway reports it (`ReverseRefund`); only a `resolved` refund can be, and it STAYS `resolved` (final): Core sets
its `reversed_at`, keeps the reason in `failure_code`/`failure_message`, and tells the consumer again through
`payment.refund.updated`. It no longer counts in `RefundedMinor`: the money is with the merchant again, and the
payment can be refunded again up to what was paid.

**A refund made at the processor**, outside Nilda — by hand in its dashboard — is reported by the gateway
(`ReportExternalRefund`, keyed by the processor's refund id). Core records it as a refund that is `resolved` at
once and `external`, adds it to `RefundedMinor`, and tells the consumer, so the shop's books match the money.

**An unmatched refund.** `ReportExternalRefund` may answer with a refund whose `failure_code` is `"unmatched"`
(`nilda.FailureUnmatched`; `PaymentRefund.Unmatched()` reports it). That is NOT a refund of the payment: the
processor gave back money Nilda holds on no payment (a payment after a refusal, a second payment of the order, more
than was paid, or one beside refunds still pending at the bank). Nothing of the order changed and the consumer is
not told; the owner settles it. Its `id` can be read with `GetRefund` and reported reversed with `ReverseRefund`
(both answer 200). An external refund that passes the cap only because a Nilda refund is still being asked of its
gateway is refused with **503**: report it again shortly; a 503 is never final.

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

**FailureCode**, a closed set. The gateway reports the first six; the last three are Core's own verdicts, and a
gateway reporting one is refused:

| code | who | means |
|---|---|---|
| `declined` | gateway | the processor said no |
| `cancelled` | gateway, or a person | the payer walked away — not a problem to investigate; also what a person's reject of a pending payment records |
| `provider_unavailable` | gateway, or Core | the processor could not be reached; usually nothing was charged. Core records it too when the gateway does not answer `payment.start`, or answers something Core cannot act on — see "Core asks once" for the charge that can happen anyway |
| `invalid_request` | gateway | the processor cannot take this payment (a currency, a minimum) |
| `expired` | gateway | the processor's page expired before the payer paid |
| `authentication_required` | gateway | a kept method's charge the payer's bank takes only with the payer present: send them a link to pay on-session (v0.30.0) |
| `amount_mismatch` | Core, then a person | the processor took a different amount or currency; held `pending` for a person — and kept as the code when that person rejects it |
| `consumer_refused` | Core | the consumer's confirm step said no |
| `voided` | Core | the consumer let a hold go (`Payments.Void`, v0.58.0). A hold the PROCESSOR dropped is the gateway's `cancelled` or `expired`, and a hold that passed its deadline uncaptured is Core's `expired` |

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
- **Name each method's acceptance marks** (v0.58.0, `paymentmarks.go`): `Marks` — a card method the networks it takes
  (`visa`, `mastercard`, `amex`, `maestro`), a wallet or a local method its own (`paypal`, `klarna`, `apple-pay`,
  `google-pay`, `sepa`, `ideal`, `bancontact`, `eps`); each one of `nilda.PaymentMarks()`, none twice — a describe naming
  any other is refused. Read them from the account's settings, never from a live probe (`DescribePayments` reads the
  settings only). Core passes them to consumers on `PaymentOption.Marks`; a shop names the marks of the options it offers,
  and of its own offline methods (`bank-transfer`, `cash-on-delivery`), on `CommerceEndpoints.PaymentMarks`, which is all
  Core's payment-marks strip draws (COMMERCE.md §8). The list is closed because Core draws each mark from its own files,
  drawn to the brand's acceptance-mark rules.

### Holding the money: capture and void (v0.58.0)

A consumer may ask for a session with `Capture: nilda.CaptureManual` — "take the money when the order ships" — on a
method whose `Supports` lists **`manual_capture`** (`nilda.SupportManualCapture`). List it only for a method your
processor can hold: cards, Klarna and PayPal can; a SEPA debit, iDEAL or a bank transfer cannot, and Core refuses a
manual session on a method that does not list it, so one checkout can still offer SEPA paid at once. A gateway that
lists it implements **`nilda.PaymentCapturer`**, and Serve subscribes it to `payment.capture` and `payment.void`;
`DispatchPaymentGatewayHook` refuses a describe answer listing `manual_capture` from a gateway that does not.

1. **Hold, then report it.** For a manual session ask the processor to hold rather than take (Stripe:
   `capture_method=manual`; PayPal: `intent=AUTHORIZE`). When it holds, report **`Payments.Authorize(ctx, id,
   nilda.PaymentAuthorization{AmountMinor, Currency, ProviderRef, ExpiresAt})`** — the amount the PROCESSOR holds and
   its deadline to capture (Stripe's `capture_before`, PayPal's 29 days). Core moves the session to `authorized`,
   keeps `authorized_minor` and `authorization_expires_at` (seven days when you say none, thirty at most, and **now
   when the deadline you give has already passed** — the hold lapses at the next sweep, which asks you to look first).
   A hold of a different amount or currency is refused (422) and nothing moves — release it at the processor — on a
   session already `authorized` as on one not yet held; a hold reported on a session asked to take the money at once
   is refused too. A hold made on the spot (a kept card, a wallet) is the same: answer the start `pending` and report
   `Authorize`.
   - **The same hold again** is a no-op — unless it names a deadline other than the one Core keeps and no capture or
     void was asked yet: then Core keeps the newer deadline, and the consumer hears it. So report the processor's real
     deadline when you learn it, even after a report that named none; a report that names none changes nothing.
   - **Once a capture or void was asked, a hold report moves nothing**: while the session is still `authorized` it is
     the same hold (its deadline included), and once the ask moved it — a slow capture is `pending` — it is refused
     (409). The processor's "authorized" webhook redelivered after its capture event is the usual case; acknowledge it.
   - **A session held for a person** (`pending`, `amount_mismatch`: the processor reported taking another amount) is
     money taken: a hold on it is refused (409), and the amount reported stays for the person who decides it.
2. **Capture.** `CapturePayment(ctx, nilda.PaymentCaptureRequest{Session, AmountMinor, IdempotencyKey})` takes
   `AmountMinor` — at most the hold; less, and the processor releases the rest. **Pass `IdempotencyKey` to the
   processor unchanged** (`nilda-capture-<session id>`, the same every time Core asks about this session). Answer
   `resolved` with the amount the processor says it took, `pending` when it will say later (report it then with
   `Resolve` or `Reject`), or `rejected` with a code when the money will not come (the hold expired or was cancelled).
3. **Void.** `VoidPayment(ctx, nilda.PaymentVoidRequest{Session, IdempotencyKey})` lets the hold go (Stripe: cancel
   the PaymentIntent; PayPal: void the authorisation), under `nilda-void-<session id>`. Answer `rejected` — released,
   or already lapsed; Core closes the session with Core's code `voided` — or `resolved` with the amount when the
   processor had already taken it.

**Core asks while it holds the session's lock**, so two captures asked at once are one capture. Answer from the
processor's answer, and do not report on that session from inside the hook (`Resolve`, `Reject`, `MarkPending`): the
report waits on the lock until the hook's time runs out. The answer IS the report; the processor's webhook for the
same capture arrives afterwards and changes nothing. **An answer you do not have is an ERROR**, never `rejected`: the
processor could not be reached, or did not say. Core keeps the capture (or void) asked and asks again, with the same
key, until it is answered — the error is an answer, not counted against your plugin, as for a refund.

**A void whose answer was lost.** When the processor's own word that the hold is gone arrives first (Stripe's
`payment_intent.canceled`, PayPal's `PAYMENT.AUTHORIZATION.VOIDED`), report it as you would any cancel —
`Reject` with `cancelled`, or `expired` for a lapse. On an `authorized` session whose void was asked, Core ends it as
the void's answer would: `rejected`, `voided`, and the void is no longer owed — so the consumer that let the hold go
hears that it was voided, never that the payer cancelled.

**A hold past its deadline.** When `authorization_expires_at` passes and nobody captured, Core asks the gateway to
look first (`payment.sync`, if you implement `PaymentSyncer`): a capture made at the processor is money, so report
it. A session still `authorized` afterwards ends `rejected`, `expired`. While the gateway cannot look, Core waits and
asks again — for at most three days past the deadline, after which the processor has let the hold go anyway.

### The processor's risk verdict (v0.58.0, EXPERIMENTAL)

A processor that screens payments for fraud says how risky it found one. Report it beside the money — with the hold
(`PaymentAuthorization.Risk`) or the resolution (`PaymentResolution.Risk`) — as a `nilda.PaymentRisk`:

```go
_, err := pay.Resolve(ctx, sessionID, nilda.PaymentResolution{AmountMinor: 1200, Currency: "EUR", ProviderRef: pi.ID,
	Risk: &nilda.PaymentRisk{Level: nilda.RiskElevated, Score: &score, Reason: charge.Outcome.Reason}})
```

- **`Level`** is one of five, Stripe Radar's words (`outcome.risk_level`, docs.stripe.com/api/charges/object, read
  2026-11-05): `normal`, `elevated`, `highest` for a payment the processor assessed; `not_assessed` when it did not (a bank
  debit, a wallet, a payment from before it scored); `unknown` when its assessment failed. Another processor maps its
  own verdict onto them. Anything else is refused (`PaymentRisk.Validate`, run by the client before it sends and by Core).
- **`Score`** is the processor's 0 (least risky) to 100, when it gives one — Stripe's `outcome.risk_score`, which only
  Radar for Fraud Teams returns. A score of 0 is a score; nil is none. Never beside `not_assessed` or `unknown`.
- **`Reason`** is the processor's own word for its verdict (Stripe's `outcome.reason`: `elevated_risk_level`, `rule`), one
  plain line of at most 200 characters, for the owner. Never the payer's data.
- **No risk at all** (nil) means the gateway does not say. It is not `not_assessed`, which is the processor saying it did
  not look — the shop shows the two differently.
- **Core keeps the first verdict reported** on the session (`PaymentSession.Risk`): from the hold if it carried one,
  else from the resolution. A later report never moves it and never clears it; one that arrives after the money (the
  charge read later) is kept then, and the consumer is told. A report Core refuses keeps nothing of it.

### A card at a reader (v0.58.0, EXPERIMENTAL)

A method whose `Supports` lists **`card_present`** (`nilda.SupportCardPresent`) takes a card in person, at one of the
card readers it lists in **`Readers`** (`nilda.PaymentReader{ID, Label}`: the processor's id, "tmr_…", and the name the
staff know, "Front counter"). List the readers your owner registered with you — from your settings, never a live probe at
describe — and list the method only once there is one: Core refuses a `card_present` method with no reader and readers on
any other method. A gateway that lists it implements **`nilda.PaymentCapturer`**: the till's Cancel is a void, and
`DispatchPaymentGatewayHook` refuses a describe answer listing `card_present` from a gateway that is not one.

1. **Start.** An in-person session names its reader (`s.Reader`) and has no return address. Send the amount to that
   reader (Stripe Terminal, server-driven: a PaymentIntent with `card_present`, then
   `POST /v1/terminal/readers/{reader}/process_payment_intent`) and answer **`pending`** with your reference: the payer
   decides at the reader. A reader offline or busy is `rejected` (`provider_unavailable`). An answer with a page is
   refused: Core closes the session `rejected`, `invalid_request` — nothing reached the reader. An answer that never
   arrives may have the payer's card on the reader, so Core holds the session `pending` and looks it up (`payment.sync`),
   never closes it as "nothing was charged".
2. **The outcome**, from your webhook as for any session (Stripe: `terminal.reader.action_succeeded` / `action_failed`,
   and the PaymentIntent's own events): `Resolve`, `Reject` (`declined` for a refused card — cancel that PaymentIntent at
   the processor; the till starts a new session for another card — `cancelled` for the payer's cancel on the reader), or
   `Authorize` for a session asked with `capture` `manual` (Stripe: `capture_method=manual`; capture within two days).
3. **Void** (`payment.void`) calls off a payment still waiting at its reader: cancel the reader's action and the payment
   at the processor (Stripe: `cancel_action`, then cancel the PaymentIntent) and answer `rejected` — or `resolved` with
   the amount if the payer had already paid. Your own word that the reader's action was cancelled, arriving first, is
   that void (`Reject` with `cancelled`): Core ends the session `voided`. A void may come before your start answer has
   reached Core (the till's Cancel pressed at once): Core keeps it and asks it the moment your start answers `pending`.
4. **A reader action that timed out is a `Reject`.** When the processor says the reader gave up (no card presented, the
   reader's own timeout, Stripe's `terminal.reader.action_failed`), report it — `cancelled` for the payer's or the reader's
   cancel, `provider_unavailable` for a reader that lost its connection, after you looked the PaymentIntent up (Stripe:
   "fetch the PaymentIntent status to verify if the payment is successfully authorized"). Never leave it pending: a
   reader can be left "in_progress" with no webhook at all (Stripe, "Missing webhooks"), so Core gives every reader's
   payment a **deadline** — your start answer's `ExpiresAt`, at most 30 minutes away, or five minutes when you name none —
   and past it asks you to look (`payment.sync`), then sends `payment.void` itself. A void you leave unanswered eight
   times (about two hours, with the usual backoff) is left `pending` for a person on Settings → Payments, logged as an
   error; a person's reject of a payment still waiting at its reader asks the void first, too.

A reader's `ID` and `Label` are one plain line: no control character (a newline forges a log line) and no
bidirectional override or isolate (U+202A–U+202E, U+2066–U+2069), which would show staff a name that is not the
reader's. Core and `DispatchPaymentGatewayHook` refuse a describe answer with one, and a session naming one.

Not in it: keeping a card taken at a till for later, a delegated credential, a reader the till device drives itself
through the processor's own SDK (Stripe's Tap to Pay — your route may hand the till app a connection token; nothing of it
crosses Core), and payments a reader stores while offline.

### Express wallet buttons (v0.58.0, EXPERIMENTAL)

Apple Pay, Google Pay, Link and PayPal buttons on a product page, the cart and the checkout. A method that has them says
how they are drawn, **`PaymentMethod.Express`** (`nilda.PaymentExpress`):

| field | what it is |
|---|---|
| `kind` | `token` (`nilda.ExpressToken`) or `order` (`nilda.ExpressOrder`) — below |
| `script` | your own script that draws the buttons, a path on your route RELATIVE to its prefix, as a webhook path is (`"/express.js"`; Core loads `/stripe/express.js`). Load the processor's library from the processor from inside it — Stripe.js "directly from js.stripe.com", PayPal's SDK "directly from https://www.paypal.com/sdk/js" — never a copy on your route |
| `origins` | `{"script","frame","connect","img"}`: the processor's origins the page's Content-Security-Policy must allow, each `https://host` with at most one leading `*.` label. Stripe.js needs `script` `https://js.stripe.com` `https://*.js.stripe.com`, `frame` the same and `https://hooks.stripe.com`, `connect` `https://api.stripe.com` (docs.stripe.com/security/guide, read 2026-11-05). Core opens them on its public page responses only — never on the admin's or an API's — while a gateway offers a switched-on express method and the shop has its Express endpoint |
| `config` | what your script reads, by snake-case key: a publishable key, a client id, the merchant's country. **Public** — written into a page every visitor is served — so a secret is refused, below |

**The two kinds**, because the processors work two ways (read 2026-11-05):

- **`token`** — Stripe's Express Checkout Element (docs.stripe.com/elements/express-checkout-element/accept-a-payment;
  docs.stripe.com/payments/finalize-payments-on-the-server). Nothing exists at the processor until the payer confirms in
  the sheet; the page then makes a ConfirmationToken and the consumer creates the session with it as its `credential`,
  type **`stripe.confirmation_token`** (`nilda.CredentialStripeConfirmationToken`), which the method lists in
  `credential_types`. Your `StartPaymentWithCredential` creates and confirms one PaymentIntent with it (`confirmation_token`,
  `confirm=true`, the capture method from the session's `capture`). Stripe: "You must immediately use the created
  ConfirmationToken to confirm a PaymentIntent; if unused, it expires after 12 hours." When the PaymentIntent answers
  `requires_action` (3D Secure, a redirect), answer **`redirected` with a `client_action`** — its client secret — and your
  script hands it to `stripe.handleNextAction`; the outcome arrives by your webhook as for any session.
- **`order`** — PayPal's buttons (developer.paypal.com/sdk/js/reference/). The processor's order exists from the press:
  the consumer starts the session then, and you answer **`redirected` with a `client_action`** — the PayPal order id the
  buttons' order promise resolves to (a `redirect_url` beside it is allowed, never required). While the payer changes
  address or delivery in PayPal's window (`onShippingAddressChange`, `onShippingOptionsChange`) the consumer re-prices the
  session and Core asks you **`payment.reprice`**: PATCH the order (PayPal: "Updates an order with a `CREATED` or
  `APPROVED` status", developer.paypal.com/docs/api/orders/v2/) to `amount_minor`, with the `shipping_options` listed and
  the `selected` one in the amount. The method lists **`reprice`** (`nilda.SupportReprice`) and your gateway implements
  **`nilda.PaymentRepricer`** — `DispatchPaymentGatewayHook` refuses a describe answer listing `reprice` from one that
  does not, and Serve subscribes it. Approval goes to your existing finish path: `Payments.Confirm`, then capture or
  authorise.

**`payment.reprice`** (`RepricePayment(ctx, nilda.PaymentRepriceRequest{Session, AmountMinor, ShippingOptions})`): answer
`{"repriced": true}` when the processor's order now asks the new amount, or `{"repriced": false, "failure_message": …}`
when the processor refused (the order was approved meanwhile, it is gone) — Core answers the consumer 409 and the session
keeps its amount. A processor you could not reach is an ERROR: nothing changed, Core answers the consumer 503. Setting an
amount is idempotent by nature, so there is no key. Core sends it only for a session that is `created` or `redirected`,
whose payer's approval has not reached Core (no `Confirm` yet), that no credential, kept method or reader started.

**A client action** (`nilda.ClientAction`) is the payer's: a Stripe client secret lets its holder read the PaymentIntent.
It is one line of visible ASCII, at most 1,024 bytes, only on a `redirected` answer; Core hands it only to the browser
that pressed the button, keeps it only while the session is `redirected`, never logs it, never puts it in a URL — and its
`String` prints `[client action redacted]`. A `redirected` answer with neither a `redirect_url` nor a `client_action` is
refused: the payer would have nowhere to go.

**The public configuration refuses a secret**, by its value or by its key's name (`PaymentExpress.Validate`, which
`DispatchPaymentGatewayHook` runs on every describe answer): a value starting `sk_` (a Stripe secret, test, live or
organisation key), `rk_` (a restricted key — "Safe to expose: No", docs.stripe.com/keys), `whsec_` (a webhook signing
secret), `A21AA` (a PayPal OAuth access token, its sample in developer.paypal.com/api/rest/authentication/), `-----BEGIN`
(a PEM key), `Bearer ` (an Authorization header), a value holding `_secret_` (a PaymentIntent's or SetupIntent's client
secret — per payer, a `client_action`, never configuration) and a JSON Web Token; and a key one of whose snake-case words is
`secret`, `password`, `passwd`, `private`, `token`, `signing` or `credential`. **PayPal's client secret has no shape of its
own** — PayPal publishes no prefix for it — so it is refused by its NAME: never put it in `config` under any name. Its client
id is public (PayPal's SDK takes it in the page's script URL, `client-id`) and passes. Stripe's publishable `pk_…` passes:
"You can include it in front-end code" (docs.stripe.com/keys). Also refused: a script that is not a path on your route
(another host, `//host`, `..`, a query), an origin that is not one https host (`http:`, a bare `*`, `*.com`, a path, a
keyword such as `'unsafe-inline'`), and express on a method that takes cards at a reader.

**The page bridge** — what your `script` is written against (Core's `express_checkout` widget,
`internal/pagebuilder/expresswidget.go`). Core draws each placement HIDDEN, asks the shop a first quote and, when the shop
answers `available`, adds one element per express method and loads your script once per page. Register your gateway under
its plugin key and say so:

```js
var N = window.nilda = window.nilda || {}, X = N.express = N.express || {};
(X.gateways = X.gateways || {})['stripe'] = { mount: function (host) { /* draw into host.element */ return { destroy: function () {} }; } };
document.dispatchEvent(new CustomEvent('nilda:express-gateway', { detail: { gateway: 'stripe' } }));
```

So `window.nilda.express.gateways.<key> = {mount}`, then the `nilda:express-gateway` event: Core mounts every waiting
placement of that gateway, once each, at once if you registered before it looked. `mount(host)` runs once per method and
placement; a throw hides that method. What it returns Core keeps and calls nothing on today. The host is `{element, config,
locale, gateway, method, kind, quote, start, complete, done, available}`:

| member | what it is |
|---|---|
| `element` | the element to draw this method's button in |
| `config` | the method's public `config`, as you described it |
| `locale` | the page's language (`<html lang>`), or undefined |
| `gateway`, `method`, `kind` | which method this is, and `token` or `order` |
| `quote(req)` | `req` `{address?, shipping_option?}` (a PARTIAL address only) → a promise of the shop's `ExpressAnswer`. The bridge adds the step, gateway, method, source and — on a product page — the product, the shopper's variation answers and quantity from the page's add-to-cart row; after a `start`, the start's `session` and `press`. An available answer's total and terms line are written beside the buttons |
| `start(req)` | kind `order`: the payer pressed. Same `req` → the answer with the `session` and its `client_action` (the order id your buttons' order promise resolves to). Mints a NEW press and forgets any earlier session |
| `complete(req)` | the payer confirmed: `req` `{payer, credential?, shipping_option, shown_total}` — the `ExpressPayer`, kind `token`'s wallet token (`{"type": "stripe.confirmation_token", "token"}`), the delivery option the payer chose and the total the sheet showed them (`{amount_minor, currency}`). Kind `token`: every call is a NEW press; kind `order`: the start's press and session. An answer `code: "total_changed"` charged nothing: end the sheet unpaid, call `done` with it, and `quote` again so the new total stands beside the buttons. |
| `done(outcome)` | the payment ended in the browser, `{ok, status, message, order_url}`: `ok` sends the payer to `order_url` (this site only); otherwise `message`, or the widget's own error text, is said in the placement's live region, and a `token` kind's next `complete` is a new press |
| `available(on)` | whether you draw a button here (the wallet may have none on this device); when no method of a placement is on, the placement hides |

**A press** is 128 random bits the bridge mints per press of a button — a `start`, or a `token` kind's `complete` — and
the shop's key for one order and one session. The bridge POSTs to the shop's endpoint from the page (same origin, its
cookie, the CSRF header) and, when no answer came (the network, a 5xx), sends the SAME body again, up to three tries in
all: a retry reuses the press, so it is the same order and the same charge. A 4xx is the shop's answer and reaches your
script as it is.

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

### One stable processor reference per payment

A gateway reports a payment (`Resolve`, `Authorize`, a capture's answer) under ONE reference for its whole life —
the charge (Stripe: the `ch_…`/latest charge id, never the PaymentIntent `pi_…` in one report and the charge in
another). Core treats money reported on a paid payment under a different reference as a SECOND payment of the
order, even of the same amount: it is kept as money no payment holds, and a refund made at the processor while it
is open is answered as `Unmatched()` and kept for the owner — never booked as the order's refund. A gateway that
names one payment by two references makes the owner settle its refunds by hand.

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
  plugin, gives you that much. `payment.refund` and `payment.saved.forget` run from Core's delivery job and have
  the whole budget; an error from either means "not done", and Core asks again.
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
// Why it cannot, for the owner (v0.42.0): no gateway installed, one installed and switched off, or none of the
// switched-on methods takes EUR — and Test when a switched-on method takes no real money.
ready, err := pay.Readiness(ctx, "EUR")
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
- `nilda.PaymentSessionParams.Validate` is what Core checks without asking anything — a NUL character in any text
  included, which every payment `Validate` refuses as Core does ("text in a payment may not hold a NUL character":
  Postgres stores none, so it would come back as a 500); Core also checks the
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

### Item lines: what the payer is buying (v0.58.0; commerce plan 10.15)

`PaymentSessionParams.Lines` (a `PaymentLine` each: `name`, `quantity`, `unit_amount_minor`, `total_minor`, `tax_minor`,
optional `sku`, `url`, `image_url`, `fulfilment`) and `PaymentSessionParams.Breakdown` (a `PaymentBreakdown`:
`shipping_minor`, `discount_minor`, `tax_minor`, `tax_included`) say what the payer is buying, so the processor's page
can list it — Stripe Checkout's `line_items`, PayPal's `purchase_units[].items` — instead of one "Order NLD-1042" line.
Optional: a consumer built before sends neither, and the gateway shows the one amount as before. `Validate` (run by the
client before it sends, and by Core) holds them to the amount EXACTLY:

```
sum(lines' total_minor) + shipping_minor − discount_minor + (tax_minor unless tax_included) = amount_minor
```

- each line's `total_minor` is its `unit_amount_minor` × `quantity` exactly (Stripe and PayPal both multiply; a line
  discount that does not divide by the quantity goes into `discount_minor`, never into a unit price), nothing is
  negative, `tax_minor` is ALL the tax in the amount (the lines' and the delivery's) and a line's tax is at most its
  total when included;
- bounds: at most `MaxPaymentLines` (100, Stripe Checkout's payment-mode maximum) lines, a `name` of one plain line up to
  `MaxPaymentLineName` (127, PayPal's item name) characters, `sku` up to `MaxPaymentLineSKU` (127), a `quantity` of 1 to
  `MaxPaymentLineQuantity` (999,999), `url` and `image_url` absolute http(s) up to 2,048 bytes; `breakdown` comes with
  lines and only with them;
- **the lines describe the amount; they never set it.** `amount_minor` is the one figure charged. Core keeps the lines on
  the session (`PaymentSession.Lines`, `Breakdown`) and the gateway reads them from the session `payment.start` carries.
  A re-price (`Payments.Reprice`) moves the amount and DROPS the lines: they described the amount before it.

What a gateway does with them (read 2026-11-05):

- **Stripe Checkout** (docs.stripe.com/api/checkout/sessions/create) takes no total in payment mode: it adds
  `line_items[].price_data.unit_amount` × `quantity`, the chosen `shipping_options` rate, and tax only through `tax_rates`
  or automatic tax, then subtracts at most one `discounts[]` coupon that must exist beforehand. So a tax-included basket
  with no discount maps one to one (lines → `line_items`, `product_data.name`, `images` from `image_url`; delivery → a
  `shipping_rate_data.fixed_amount`); a discount needs a coupon created for the amount, and tax on top needs a line of its
  own or a tax rate. A gateway that cannot reproduce the amount exactly sends the one line it sent before — never a
  total that differs from `amount_minor`.
- **PayPal Orders v2** (schema 2.36, `item_request`, `amount_breakdown`): `items[]` with `name`, `quantity` (a string),
  `unit_amount`, `tax` PER UNIT, `sku`, `url`, `image_url`, `category`; `amount.breakdown` with `item_total` (= the
  sum of unit × quantity), `tax_total`, `shipping`, `discount`, and `amount = item_total + tax_total + shipping −
  discount`. Tax included: the lines carry it and `tax_total` is left out. Tax on top: `tax_total` is `tax_minor`, and a
  line's `tax` is sent only when its `tax_minor` divides by its quantity (else the item has none and the total stands).
  `fulfilment` maps to `category`: physical → `PHYSICAL_GOODS`, digital → `DIGITAL_GOODS`, none → `DONATION` only for a
  donation, else left out.

### Take the money later (v0.58.0)

The market's default is to take the money at checkout, and so is Nilda's: a session with no `Capture` (or
`automatic`) does exactly what every session did before. A consumer that ships later — a wheel built to order — may
ask the processor to HOLD the money and take it when the order ships:

```go
opts, _ := pay.Methods(ctx, "EUR")
// offer "held until it ships" only through a method that can hold — the others take the money at once
canHold := nilda.Supports(opt.Supports, nilda.SupportManualCapture)
s, err := pay.CreateSession(ctx, key, nilda.PaymentSessionParams{ /* … */ Capture: nilda.CaptureManual})
// … PaymentUpdated: s.Status == nilda.PaymentAuthorized — held: s.AuthorizedMinor until s.AuthorizationExpiresAt
s, err = pay.Capture(ctx, "ship-"+order.ID, s.ID, order.TotalMinor-cancelledLines) // when it ships
s, err = pay.Void(ctx, "cancel-"+order.ID, s.ID)                                   // when it is cancelled instead
```

- **Authorized is not paid.** Confirm the order, ship against it if you will; book no money, issue no invoice, unlock
  no download until the session is `resolved`.
- **One capture, of at most what is held.** Less takes that much and the processor releases the rest; the session
  resolves at what was taken — its `AmountMinor` becomes it, `capture_requested_minor` says what you asked, and a
  refund is bounded by it. Asked again with the same amount it is the same capture, answered as it stands (or asked
  of the gateway again when its first answer was lost); with another amount, 409. More than is held is 422; a
  session that is not `authorized` is 409. The answer is the session as the gateway left it: `resolved`, `pending`
  (the processor decides; you hear it in `PaymentUpdated`) or `rejected` (the hold was gone).
- **A 503 means the gateway did not answer.** Core keeps the capture asked and asks again, with the same key, until
  it is answered — you hear the end in `PaymentUpdated`. The same call may be sent again under the same key.
- **Void** lets the hold go: the session ends `rejected` with `voided` (or `resolved`, if the processor had already
  taken it). A session whose capture was asked cannot be voided; voiding twice is no error. `void_requested` is set
  while the gateway has not yet said it let go.
- **A refund of an authorized session is refused (409)**: nothing was taken. Void it.
- **A hold nobody captured** ends `rejected`, `expired` after its deadline (`authorization_expires_at`), once Core
  has asked the gateway whether it was captured at the processor after all. Warn the owner before it does.
- **A late hold** on an order you already closed (`expired → authorized`) is yours to void and note, as you refund
  late money.

`nildatest.Payments` holds every one of these rules, `StubGateway` answers `payment.capture` and `payment.void`
(`CaptureResult`, `CaptureErr`, `VoidResult`, `VoidErr`; `Captured` and `Voided` record what it was asked), and
`ExpireHold` runs Core's expiry of one hold.

### Reading the processor's risk verdict (v0.58.0, EXPERIMENTAL)

`PaymentSession.Risk` is the verdict the gateway reported (§4 "The processor's risk verdict"): `Level` normal,
elevated, highest, not_assessed or unknown, an optional `Score` 0–100 and the processor's `Reason`. Read it in
`PaymentUpdated`: a shop holds an `elevated` order from shipping until a person looks, and shows `highest` louder still —
Radar blocks those by default, so one that was paid got through a rule. Nil means the gateway did not say: hold nothing
on it, and say "not reported", never "safe". The verdict never changes the session's status: a risky payment that
resolved is paid, and refunding it is the consumer's decision.

### In person, and with nobody there

- **With nobody there** — a merchant-initiated charge, the payer absent — is the kept method's charge
  (`Payments.ChargeSaved`, below, v0.30.0): Core holds the token, the consumer holds only the kept method's id (the
  mandate's reference), and the charge must stay inside the mandate the payer accepted. Each charge carries your
  idempotency key and says its kind (`OffSession`, v0.58.0); a decline is the session `rejected`, never an error.
- **In person** — a card at a till (v0.58.0, EXPERIMENTAL: inside `payments/v1`, outside its no-breaking promise until
  a till built by Nilda ships green on it). Offer, at the till and never online, a method whose `Supports` lists
  `card_present`; its `Readers` are the card readers you may name. Start a session with `Reader` set and no return
  address — Core refuses one at a reader the method does not list, an online session on such a method, and `Save` or
  `Credential` beside a reader:

```go
s, err := pay.CreateSession(ctx, sale.ID+"#"+strconv.Itoa(sale.Attempt), nilda.PaymentSessionParams{
	Gateway: opt.Gateway, Method: opt.Method, Reference: sale.ID, AmountMinor: sale.TotalMinor, Currency: "EUR",
	Reader: opt.Readers[0].ID,
})
// s.Status == nilda.PaymentPending: the amount is on the reader. PaymentUpdated says how it ends.
s, err = pay.Void(ctx, "cancel-"+sale.ID, s.ID) // the till's Cancel, while it waits there: rejected, voided
```

  A declined card ends the session `rejected`; another card is another attempt, a new key. A void answers as for a hold:
  `rejected` with `voided`, or `resolved` if the payer had paid a moment before. A void sent while the start is still on
  its way answers the session `created` with `void_requested`: Core hands it to the reader once the gateway answers, and
  `PaymentUpdated` says how it ends. A payment nobody finishes at the reader is called off by Core after a few minutes
  (`rejected`, `voided`); `void_requested` is never left beside a payment that ended another way.

### Express checkout and re-pricing (v0.58.0, EXPERIMENTAL)

`Payments.Methods` hands each option its method's buttons (`PaymentOption.Express`): offer an express checkout only
through such an option, and create its sessions as the kind says. The shop's side of the buttons — the one endpoint Core's
widget calls, the partial address, the press — is `docs/COMMERCE.md` §6.

- **`token`** (Stripe): no session before the payer confirms in the sheet. Then create one with the wallet's token as its
  credential — `Credential: &nilda.PaymentCredential{Type: nilda.CredentialStripeConfirmationToken, Token: …}` — keyed by
  the press, so the same press is the same session. It answers `resolved`, `authorized` (a manual capture), `pending`,
  `rejected`, or `redirected` with a `client_action` for the payer's browser (3D Secure).
- **`order`** (PayPal): create the session at the press, keyed by the press; it answers `redirected` with the
  `client_action` the buttons need. While the payer changes address or delivery, re-price it:

```go
s, err := pay.Reprice(ctx, press+":"+quoteID, s.ID, nilda.PaymentReprice{AmountMinor: quote.TotalMinor,
	ShippingOptions: []nilda.PaymentShippingOption{{ID: "dhl", Label: "DHL", AmountMinor: 490, Selected: true}}})
```

  Core asks the gateway once and answers with the session at its new amount. **Only before the payer approves**: a session
  that is not `created` or `redirected`, or whose approval reached Core (the gateway asked `Confirm`), is 409; one a
  credential, a kept method or a reader started, or whose method does not list `reprice`, is 422; the processor's refusal
  is 409 with its reason; a 503 changed nothing — ask again with the same key. At most ten delivery options, exactly one
  `selected`, its price in the amount. Your `ConfirmPayment` then checks the order as the payer approved it, as always.

`nildatest.Payments` holds the same rules (`Reprice` for a gateway's test), keeps a start's `client_action` only while its
session is `redirected`, and `StubGateway` answers `payment.reprice` (`RepriceResult`, `RepriceErr`, `Repriced`).

### An agent paying for its buyer (v0.20.0)

An AI agent may carry a DELEGATED payment credential its buyer's wallet granted it — a Stripe Shared Payment Token
(`spt_…`), limited to this seller, an amount and a time. A consumer that lets an `AgentBuy` agent complete an order
sends it as `PaymentSessionParams.Credential` — `nilda.PaymentCredential{Type: nilda.CredentialStripeSPT, Token: …}` —
on a method whose `CredentialTypes` lists that type. Core hands it to the gateway ONCE, in the start request
(`PaymentStartRequest.Credential`), and keeps nothing of it: not in the session, not in the audit trail, not in a log.
The gateway charges it on the spot and answers `resolved`, `pending` or `rejected` — there is no page to send anyone
to. The same idempotency key gives the same session, never a second charge. A credential's `String` shows only its type.

### A method kept for later (v0.30.0)

A subscription's next charge, a pre-order charged when it ships, a card the payer replaces: the payer is present
ONCE, and later charges are merchant-initiated. This is how the market does it (Shopify's customer payment
methods, WooCommerce's tokens; Stripe's `setup_future_usage=off_session`, PayPal's vault), and what PSD2 asks for:
strong customer authentication when the method is kept, later charges out of its scope.

1. **Keep it.** Send a session with `Save` — a `nilda.PaymentMandate`: its purpose (`subscription`, `pre_order`,
   `update`), the agreement exactly as the payer read it, each charge's amount (or the rule that sets it and the most
   one charge may be, `max_amount_minor`), the currency and, for a subscription, the interval. The method must list `save_for_later` in its `Supports`. With
   `Save`, `AmountMinor` may be `0`: the payer only sets the method up. The gateway asks its processor to keep the
   method and reports it with the resolution (`PaymentResolution.Saved`: the processor's token and a label a
   person recognises, "Visa •••• 4242, 12/28"). Core keeps the token, encrypted, with the mandate, and the resolved
   session carries `SavedMethod`, the id Core gave it. A Save session resolved without `Saved` keeps nothing.
2. **Charge it.** `Payments.ChargeSaved(ctx, key, nilda.PaymentChargeParams{SavedMethod, Reference, AmountMinor,
   Currency, …})`: no page, no return address. Core checks the method is yours, not forgotten and still offered, and
   that the charge is inside the mandate: its currency, at most its amount (or `max_amount_minor`), a pre-order's one
   charge once, a subscription's no sooner than its interval after the last (two days' grace, for month ends); creates a session with `ChargedSaved` set; and hands the token to the gateway in the
   start request (`PaymentStartRequest.Saved`) — never to you. The session says which kind of merchant-initiated charge it
   is, **`OffSession`**, from what the mandate is FOR: `recurring` for a subscription's renewal, `unscheduled` for a
   pre-order's charge at shipment, and for an update the kind of the agreement it replaces — Core sets it from the
   mandate it keeps (`nilda.OffSessionOf`), never you, and never from an interval you wrote. A pre-order's mandate names
   no interval; an update's names, in **`Replaces`**, the kept method (its `SavedMethod`) whose agreement it replaces —
   one of yours — and an interval exactly when that agreement is a recurring one. A kept method is never charged on a
   method that now lists `card_present` (422): nobody stands at the till. The
   gateway tells its processor (Stripe: `off_session` `recurring` or `one_off`; PayPal, for a card: its stored credential's
   `payment_type` `RECURRING` or `UNSCHEDULED` — a PayPal wallet's stored credential has no `payment_type`, only
   `payment_initiator`, `usage` and a `usage_pattern` that must match its setup token, so for a wallet the kind is not sent
   yet, plugin-paypal's open item P-13), which is how the charge is flagged out of strong customer authentication's scope,
   charges with the payer absent and answers `resolved`, `pending` or `rejected`. `authentication_required` is not a decline: the payer's bank wants
   them present, so send them a link to pay on-session and keep the method. A charge whose answer was lost may have
   been made, so it is held `pending` — never closed as "nothing was charged" — and Core asks the gateway again, the
   same start for the same session, for up to 20 hours; the gateway makes the charge idempotent by the session's id,
   so the processor answers with the first charge if there was one. Do not charge again while one is pending.
3. **Forget it.** `Payments.ForgetSavedMethod` — when the last subscription on it ends and the pre-order is paid.
   Core asks the gateway to let go at the processor (`payment.saved.forget`) and drops the token; it charges nothing
   again. Forgetting twice is no error.

A gateway that keeps methods implements `nilda.SavedMethodPayer`: `StartPaymentWithSaved` for the charge and
`ForgetSaved` for the release. A token is never logged, echoed or shown: its types print `[token redacted]`.

## 6. The wire — for a plugin not written in Go

Hooks arrive over the plugin protocol (docs/ANY_LANGUAGE.md) with these JSON payloads:

| hook | to | payload in | answer |
|---|---|---|---|
| `payment.describe` | gateway | `{}` | `{"methods":[{"key","label","description","currencies","refunds","test","test_hint","supports","credential_types","readers","express","marks"}]}` — a reader is `{"id","label"}`; express is `{"kind","script","origins":{"script","frame","connect","img"},"config"}` |
| `payment.start` | gateway | `{"session":{…}}` | `{"status","redirect_url","client_action","provider_ref","expires_at","failure_code","failure_message"}` |
| `payment.refund` | gateway | `{"refund":{…},"session":{…}}` | `{"status","provider_ref","failure_code","failure_message"}` |
| `payment.session.confirm` | consumer | `{"session":{…}}` | `{"proceed","reason"}` |
| `payment.session.updated` | consumer | `{"session":{…}}` | `{}` — an error means "tell me again" |
| `payment.refund.updated` | consumer | `{"refund":{…},"session":{…}}` | `{}` — the same |
| `payment.sync` | gateway (a `PaymentSyncer`) | `{"session":{…}}` | `{}` — what it found is reported through the calls below |
| `payment.dispute.updated` | consumer (a `PaymentDisputeConsumer`) | `{"dispute":{…},"session":{…}}` | `{}` — an error means "tell me again" |
| `payment.saved.forget` | gateway (a `SavedMethodPayer`) | `{"token"}` | `{}` — let go of the method at the processor; an unknown token is forgotten already |
| `payment.capture` | gateway (a `PaymentCapturer`) | `{"session":{…},"amount_minor","idempotency_key"}` | `{"status","amount_minor","provider_ref","failure_code","failure_message"}` — an error means "not answered, ask again" (v0.58.0) |
| `payment.void` | gateway (a `PaymentCapturer`) | `{"session":{…},"idempotency_key"}` | `{"status","amount_minor","provider_ref"}` — the same |
| `payment.reprice` | gateway (a `PaymentRepricer`) | `{"session":{…},"amount_minor","shipping_options":[{"id","label","amount_minor","selected"}]}` | `{"repriced","failure_message"}` — an error means "not answered, nothing changed" (v0.58.0, EXPERIMENTAL; from the Core of plan 10.2's X2b) |

A session is `{"id","consumer","gateway","method","reference","amount_minor","currency","status","test",
"description","return_url","cancel_url","redirect_url","client_action","provider_ref","failure_code","failure_message",
"email","locale","supports","fulfilment","billing_address","shipping_address","reader","expires_at","save","saved_method","charged_saved","off_session","instrument","resolved_at","risk",
"lines","breakdown",
"capture","authorized_minor","authorization_expires_at","capture_requested_minor","void_requested","refunded_minor",
"created_at","updated_at"}`; a refund is `{"id","session","amount_minor","currency","status","reason",
"provider_ref","failure_code","failure_message","external","reversed_at","created_at","updated_at"}`; an address
is `{"name","line1","line2","city","postal_code","region","country"}`; a dispute is `{"id","session",
"provider_ref","stage","status","reason","amount_minor","currency","respond_by","evidence_submitted_at",
"funds_held","refundable","url","opened_at","closed_at","created_at","updated_at"}`.

Calls into Core, under `/api/rest/v1`, with the plugin's token; answers inside `{"data": …}`, refusals as
`{"error":{"code","message"}}`:

| call | who | body | answer |
|---|---|---|---|
| `GET /payments/methods?currency=EUR` | consumer | — | `[{"gateway","method","label","description","refunds","test","test_hint","supports","credential_types","readers","express","marks"}]` |
| `GET /payments/readiness?currency=EUR` | consumer | — | `{"gateways":[{"key","name","enabled"}],"methods_on","test"}` (v0.42.0) |
| `POST /payments/sessions` + `Idempotency-Key` | consumer | `{"gateway","method","reference","amount_minor","currency","description","return_url","cancel_url","email","locale","fulfilment","billing_address","shipping_address","reader","credential","save","capture"}` | 201, the session |
| `POST /payments/sessions/{id}/capture` + `Idempotency-Key` | consumer | `{"amount_minor"}` | the session as the gateway left it (v0.58.0) |
| `POST /payments/sessions/{id}/void` + `Idempotency-Key` | consumer | — | the session as the gateway left it (v0.58.0) |
| `POST /payments/sessions/{id}/reprice` + `Idempotency-Key` | consumer | `{"amount_minor","shipping_options"}` | the session at its new amount (v0.58.0, EXPERIMENTAL; from the Core of plan 10.2's X2b) |
| `GET /payments/sessions/{id}` | either side of it | — | the session |
| `POST /payments/sessions/{id}/refunds` + `Idempotency-Key` | consumer | `{"amount_minor","reason"}` | 201, the refund |
| `GET /payments/refunds/{id}` | either side of it | — | the refund |
| `POST /payments/sessions/{id}/resolve` | gateway | `{"amount_minor","currency","provider_ref","saved","instrument","risk"}` — a risk is `{"level","score","reason"}` | the session |
| `POST /payments/sessions/{id}/reject` | gateway | `{"failure_code","failure_message","provider_ref"}` | the session |
| `POST /payments/sessions/{id}/pending` | gateway | `{"provider_ref"}` | the session |
| `POST /payments/sessions/{id}/authorize` | gateway | `{"amount_minor","currency","provider_ref","expires_at","risk"}` | the session, `authorized` (v0.58.0) |
| `POST /payments/sessions/{id}/confirm` | gateway | — | `{"proceed","reason"}` |
| `POST /payments/refunds/{id}/resolve` | gateway | `{"provider_ref"}` | the refund |
| `POST /payments/refunds/{id}/reject` | gateway | `{"failure_code","failure_message","provider_ref"}` | the refund |
| `POST /payments/refunds/{id}/reverse` | gateway | `{"failure_code","failure_message","provider_ref"}` | the refund, `reversed_at` set |
| `POST /payments/sessions/{id}/external-refunds` | gateway | `{"amount_minor","currency","provider_ref","reason"}` | the refund, `external` — or one with `failure_code` `"unmatched"`, kept for the owner; 503: report it again shortly |
| `POST /payments/sessions/{id}/disputes` | gateway | `{"provider_ref","stage","status","reason","amount_minor","currency","respond_by","evidence_submitted_at","funds_held","refundable","url","opened_at"}` | the dispute |
| `GET /payments/sessions/{id}/disputes` | either side of it | — | `[dispute]`, oldest first |
| `GET /payments/disputes/{id}` | either side of it | — | the dispute |
| `PUT /payments/secrets/{name}` | gateway | `{"value"}` | `{}` |
| `GET /payments/secrets/{name}` | gateway | — | `{"value"}`; 404 when none is set |
| `POST /payments/charges` + `Idempotency-Key` | consumer | `{"saved_method","reference","amount_minor","currency","description","fulfilment","shipping_address"}` | 201, the session |
| `GET /payments/saved-methods/{id}` | consumer | — | the kept method, never its token |
| `POST /payments/saved-methods/{id}/forget` | consumer | — | the kept method, `forgotten_at` set |

Refusals: **404** a session that is not yours; **409** a report the session has moved past, a refund of an
unpaid session, or a keyed request sent again while the first is still being answered; **422** what `Validate`
checks, a method the gateway does not offer, an address off the site (or no site address to check it
against), a refund past what was paid, a reused key on a different request; **403** a missing capability;
**503** from `confirm` when the consumer could not be asked, from a refund whose gateway is not running or
could not be asked, and from a capture or void the gateway did not answer (Core keeps it asked). A 503 changed nothing, so Core gives its Idempotency-Key back: the same request with the
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
| a mandate's `text` | 4,000 characters | Nilda's choice: the whole agreement fits |
| a kept method's `token` | 1,024 bytes | Nilda's choice: a processor's ids are under a hundred |
| a kept method's `label` | 100 characters | Nilda's choice |
| a mandate's `interval_count` | 1–365 | Nilda's choice |
| a method's `readers` | at most 100, each id at most 255 bytes, each label 1–100 characters | Nilda's choice: a shop with a hundred tills runs more than one site |
| a reader's payment waits | its start answer's `expires_at`, at most 30 minutes; 5 minutes when none; then looked up and voided, a void asked at most 8 times | Nilda's choice: past a payer's seconds and Stripe's two-minute offline mark |
| `Idempotency-Key` | 255 bytes | Core's, which is Stripe's |
| a `client_action` | 1,024 bytes of visible ASCII, one line | Nilda's choice: a client secret or an order id is under a hundred |
| an express method's `script` | 255 bytes, a path on the gateway's route | Nilda's choice, as a `provider_ref` |
| an express method's `origins` | at most 16 a directive, each one `https://host` | Nilda's choice: Stripe.js needs five across three |
| an express method's `config` | at most 32 keys, a key 64 bytes of snake case, a value 1,024 bytes on one line | Nilda's choice |
| a re-price's `shipping_options` | at most 10, an id 64 bytes, a label 100 characters, exactly one `selected` | "PayPal will display only the first 10 shipping rates provided" (Stripe's `shippingaddresschange` page) |
| any payment text — a reference, a reason, an address, a label, a `provider_ref`, a key | no NUL character | Postgres keeps one in neither text nor jsonb |
| a session nothing decided | 24 hours, unless the gateway says | Stripe Checkout's own default |
| a hold the gateway gives no deadline | 7 days | a card's hold online (Stripe), the shortest of the common ones |
| a hold's deadline | at most 30 days | past PayPal's 29, its longest |
| a hold past its deadline whose gateway cannot look | 3 days, then closed `expired` | the processor has let it go by then |
| a gateway's hook | at most 15 seconds, or `PLUGIN_CALL_TIMEOUT` if that is longer | Core's budget for a processor's API |
| a consumer's hook | 5 seconds, the default of `PLUGIN_CALL_TIMEOUT` | the ordinary plugin call |

## 8. What the contract does not do

- **No line items.** A hosted checkout collects what its processor needs; the consumer sends one amount, a
  description and — when it knows them — the payer's addresses. A gateway that needs more asks the payer on the
  processor's page.
- **No answering a dispute.** Disputes are REPORTED (v0.10.4); the owner answers one at the processor.
- **No subscriptions here.** A method kept for later (§5, v0.30.0) is the door a consumer builds subscriptions on;
  the schedule, the retries and the portal are the consumer's.
- **One capture per hold.** Authorise then capture or void is in the contract (v0.58.0); taking one hold in several
  parts (multicapture) is not. The name `partial_capture` stays reserved for it, so the door stays additive; a guard
  fails if anything takes it before the feature is built. (`credential` and `credential_types` were reserved the same
  way until v0.20.0 built them, and `capture` and `void` until v0.58.0.)
- **No money arithmetic in Core** beyond comparing amounts and summing refunds. Prices, tax and rounding stay
  the consumer's.

### Door D5's decisions (commerce plan 2026-11-05 §1J, recorded 2026-11-05)

- **What already was the door.** The merchant-initiated charge with the mandate's reference held by the consumer and the
  token held by Core alone is `ChargeSaved` (v0.30.0): Core checks the mandate, the charge carries the consumer's
  idempotency key, `authentication_required` and a decline are results, and a lost answer is held `pending` and asked
  again under the same session. D5 added only what was missing.
- **The charge says its kind, and Core decides it.** Every processor asks whether a merchant-initiated charge is
  recurring or unscheduled — Stripe's `off_session` `recurring` / `one_off` (docs.stripe.com/api/payment_intents/create,
  read 2026-11-05), PayPal's card `stored_credential.payment_type` (Orders v2 schema 2.36, read 2026-11-05: only a card's
  stored credential has it; a PayPal wallet's has `payment_initiator`, `usage` and `usage_pattern`, the last matching its
  setup token, so a wallet's kind is not sent yet — plugin-paypal's open item P-13), Adyen's `recurringProcessingModel` — and it is what flags
  the charge out of SCA's scope (PSD2; EBA Q&A 2018_4031). Core derives it from the mandate it keeps (an interval:
  recurring; none: unscheduled), so no consumer chooses which exemption its charge claims. Additive to `payments/v1`: a
  gateway built before it ignores the field and charges as before (it already knew a `Saved` start is off-session).
- **The kind is the agreement's purpose, not its interval** (review 2026-11-05). The first cut read the interval, which
  the consumer writes: a pre-order naming one claimed "recurring". Now a subscription is recurring, a pre-order
  unscheduled, an update the kind of the agreement it replaces (`Replaces`, a kept method of the same consumer, followed
  back through earlier updates). A new mandate cannot say otherwise (`Validate`); a pre-order Core kept with an interval
  before this reads as kept and charges unscheduled, and an update kept before `Replaces` existed is read by its interval,
  the rhythm Core holds its charges to.
- **A reader's payment is never pending for ever** (review 2026-11-05). The expiry closes only created and redirected
  sessions, and a reader can stay "in_progress" with no webhook (Stripe, "Missing webhooks", read 2026-11-05). So a
  reader's payment has a deadline (five minutes, or the gateway's up to thirty: past the seconds a payer takes and the two
  minutes after which Stripe calls a silent reader offline), past which Core looks it up and calls it off; a void nobody
  answers is left for a person after eight asks, as a kept method's unanswered charge is after 20 hours. The till's Cancel
  before the start's answer is kept, not refused; a report that ends a payment drops a void asked of it; a person's
  reject asks the reader to stop first.
- **A card in person is a session sent to a named reader.** The market's server-side shape: Stripe Terminal's
  server-driven `process_payment_intent` on a reader id (docs.stripe.com/terminal/payments/collect-card-payment, read
  2026-11-05), Adyen's cloud Terminal API on a POIID, Square's Terminal API on a `device_id`. The gateway lists its
  readers on the method, answers `pending`, and the outcome arrives as for any session; the till's Cancel is the
  existing void, so no new hook and no new state. A reader driven by the processor's SDK on the till device (Tap to Pay)
  stays between the gateway and the till app.
- **Why inside `payments/v1` and still experimental.** `payments/v1` carries every live sale and cannot be marked
  experimental as a whole (`WRITE_COMMANDS.md` §5 marks a contract file); the in-person fields are optional and only a
  gateway that lists `card_present` ever meets them, so they extend it compatibly — and are named EXPERIMENTAL here and in
  their Go doc until the till (plugin P17) ships green on them.

### Risk's decisions (commerce plan 10.3, step F1, recorded 2026-11-05)

- **Stripe's five words, not three.** The plan's row named `normal / elevated / highest`; Stripe returns two more
  (`not_assessed`, `unknown`, docs.stripe.com/api/charges/object, `outcome.risk_level`, read 2026-11-05), and each says
  something a shop must not read as `normal`. Nil stays distinct from both: the gateway did not say.
- **The score is 0 to 100 and optional** — Stripe's page says "between 0 and 100", returned only with Radar for Fraud
  Teams and absent for a payment it did not score; so a pointer, where 0 is a score, and none beside `not_assessed` or
  `unknown`. The plan's "0-99" was a misreading.
- **On the hold and on the resolution, kept once.** A card is scored when it is authorised, so the hold carries it for a
  payment taken later; a payment taken at once carries it on the resolution. Core keeps the first, never moves it, and
  keeps a late one (as a kept method arriving after the money is kept): one verdict per payment, the processor's.
- **Reported, never decided on by Core.** Core keeps and passes the verdict; holding an order, telling the owner, a
  refund marked fraudulent (Stripe's `reason=fraudulent`, which feeds the merchant's Radar block lists) are the shop's
  and the gateway's (plan 10.3, steps F2-F3). A consumer marks such a refund with the reason `RefundReasonFraudulent`
  (`"fraudulent"`, exactly); plugin-stripe 0.8.1 sends Stripe `reason=fraudulent` for it and nothing for any other reason.
- **Inside `payments/v1`, experimental**: every field optional, so a gateway or a consumer built before ignores it. The
  Core landed step F1 in core #181, so the truth tests hold the session's `risk` too.

### Express buttons' decisions (commerce plan 10.2, step X1, recorded 2026-11-05)

- **The method describes its buttons; nothing new describes the gateway.** `PaymentMethod.Express` rides the describe
  answer Core already asks for, and reaches consumers on `PaymentOption` — the kind, the script, the origins and the
  public configuration are facts of one way to pay, as `credential_types` and `readers` are.
- **Two kinds, because the processors differ** (each page read 2026-11-05): Stripe makes nothing before the payer confirms
  and hands a ConfirmationToken; PayPal's order exists from the press and is PATCHed while the payer chooses. One contract
  fits both by reusing what was there: a token is a `credential` (v0.20.0's path, a new type), a session waiting on the
  payer's page is `redirected` — with a `client_action` instead of, or beside, a page — and the PayPal order's update is
  the one new hook, `payment.reprice`.
- **Re-priced only before the payer approves**, and Core decides "before" from its own facts: the status (created or
  redirected) and whether the gateway has asked `Confirm` — the moment the payer's approval reaches Core. PayPal would
  still PATCH an `APPROVED` order; the contract does not, because the payer approved an amount.
- **The credential type is `stripe.confirmation_token`**, named like `stripe.shared_payment_token` (the plan wrote
  `stripe_confirmation_token`; one spelling per vocabulary).
- **A public configuration refuses a secret by shape AND by name.** Stripe's secrets have prefixes; PayPal's client secret
  has none, so a key named for a secret is refused whatever it holds. A value that slips both (a secret under an innocent
  name, with no known prefix) is not caught — the gateway's own test is the last guard, and the plugin's review.
- **The shop's side is its own endpoint, not `shop.checkout/v1`.** The 2026-10-18 plan named that contract; it was retired
  in v0.47.0 (commerce DR-19), and the basket lives under a cookie only the shop's route receives. The endpoint is the
  express channel, so no `channel` field (`docs/COMMERCE.md` §6).
- **Why inside `payments/v1` and experimental**: every field is optional, so a gateway or consumer built before ignores
  them; they are named EXPERIMENTAL here and in their Go doc until the shop and a gateway built by Nilda ship green on them.
  The truth tests that read Core skip exactly what Core has not landed yet (`coreahead_test.go`) and fail once it has:
  the hook, the route and the session's `client_action` landed in Core's X2b and the bridge's `shown_total` with them (core #178): the list is empty.
- **The review of X1 (2026-11-05): the payer pays what the sheet showed, or nothing.** `complete` carries `shown_total`
  (`ExpressTotal`, `{"amount_minor","currency"}` — nested, so the amount never travels without its currency), required
  with a token kind's credential, optional with an order kind's session, whose re-priced amount already binds it; the shop
  re-quotes at the final address and answers `code: "total_changed"`, charging nothing, on any difference. `complete`
  carries the `shipping_option` the payer chose, and every quote naming a session carries the start's `press`, which the
  shop requires — the session is found by the key the shop made, never by an id a page could learn.

