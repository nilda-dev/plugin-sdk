# Changelog

What changed for a plugin author, per release. The Go API a release freezes is `surface.txt` (and
`nildatest/surface.txt`); the git log has the reasons in full. Versions before v0.10.0 are not written up
here.

## Unreleased

No change to the Go API (`surface.txt` and `nildatest/surface.txt` are as in v0.10.0). What a plugin can notice:

### Changed — the SDK

- **`API.UploadMedia` with an Idempotency-Key sends the same bytes on every run** — its multipart boundary is
  derived from the key. A keyed upload re-run after a crash was refused by Core as "a different request".
- **The plugin's key no longer rides an https request's own headers.** `NewTransport` / `HTTPClient` put it on
  the CONNECT (which the egress proxy reads) and, for plain http, on the request; on https the request headers
  travel inside TLS to the provider, which learned the key.

### Changed — `nildatest.Payments`, following Core

- **`CreateRefund` answers `requested`, and `Deliver` asks the gateway.** Core asks a gateway about a refund from
  its delivery job, never inside the consumer's call; a test that read the gateway's answer straight from
  `CreateRefund` calls `Deliver` first.
- A refund whose gateway cannot be asked answers **503**, and a 503 gives its Idempotency-Key back (the same
  key runs again); the method's currencies are not checked again for a refund.
- A keyed request sent again while the first is still running answers **409**.
- A mismatched resolve writes no failure message (Core keeps the processor's amount for the owner), and the
  same mismatch reported twice changes nothing; a pending report on a pending session changes nothing; a
  processor's reference past 255 bytes is refused on a pending report and a refund resolve.

### Documentation

- `docs/PAYMENTS.md` says what Core does now: refunds are asked from a job; a consumer's "tell me again" error
  is not counted against the plugin; money reported on a refused payment is recorded for the owner (still
  409); a person accepting a mismatch resolves at the processor's amount; no readable site address refuses a
  payment; the 503 and in-flight 409 refusals.
- A shop that must emit its catalogue event is a Handler served with `Serve` (it keeps the `*Core` from Init);
  `ServeCommerce` never hands its `Commerce` one. The guide's walkthrough and `commerce.go` show that shape.
- The API token's rate limit (`API_RATE_LIMIT_PER_TOKEN`, 1,000 an hour by default) and what the client does
  when it runs out.
- The guides now say what Core does where they said otherwise: which hooks need a subscription (every one —
  `Serve` lists a provider's for you, and a row action or a report page is listed by hand), what
  `nilda plugin build` does with `os`/`arch` and the signing key, where a plugin's log lines go and at which
  level, that emitted events arrive in no fixed order, how the payment confirm step's errors divide into
  "ask again" and "final", and the broker and TLS details a plugin in another language needs.
- `_examples/shop` reads `content.saved` in the shape Core sends (`content_id`, `type`) and listens for
  `ecommerce.order_paid`, an event the site's shop really emits, instead of `order.paid`, which nothing
  sends.
- The `kv` row: one plugin's 10,000 keys and 16 MiB now hold on every install. Core used to keep them on a
  Lite install only, and keeps them on Dragonfly too now — a plugin there past either answers
  `ResourceExhausted`, where it used to be let through.
- `CHANGELOG.md` (this file).

## v0.10.0 — 2026-09-24

### Added

- **The payment contract (Nilda D-84).** Two capabilities: `payment_gateway` for a plugin that talks to a
  processor, `payment_session` for one that takes money. `PaymentGateway`, `PaymentConsumer`,
  `ServePaymentGateway`, the wire types, one state machine (`PaymentCanMove`, `RefundCanMove`), ISO 4217
  minor units (`MinorUnits`, `ParseMinor`, `FormatMinor`), and `core.API().Payments()` for the calls into
  Core. `Serve` subscribes and answers both interfaces' hooks. `nildatest.Payments`, `StubGateway` and
  `Webhook` test either side with nothing running. `docs/PAYMENTS.md` is the contract; `_examples/gateway`
  is a whole gateway. Needs a Core built on this release — an older Core refuses a manifest declaring
  either capability.
- `ClassAnalyze`, the risk class Core had and this module did not.
- `FieldChoicesRequest.Locale` and `FieldValidateRequest.Locale`: the language to answer a field question in.
- `API.UploadMedia`: `POST /media` takes a multipart upload, which `api.Post` cannot send.
- `nildatest.Host.Advance` (expire KV entries without sleeping) and `Host.SetSessionsPerIdentity`.
- `nildatest/surface.txt`: the test kit's exported names are frozen by a ledger, like the SDK's.

### Changed — behaviour a plugin can notice

- **OIDC extra scopes add to the defaults.** `NewOIDCClient`'s extra scopes are now requested alongside
  `openid email profile` rather than instead of them.
- **Two abilities with one name fail `Init`.** Core offered the agent the first declaration while the SDK
  ran the last one's `Run`; a duplicate name is now an error at startup.
- **A sub-second KV TTL expires.** `KVSet` with a TTL under one second used to be truncated to zero —
  "never expire"; it is now rounded up to one second.
- **`nildatest` refuses more of what Core refuses**: an event name that is not the plugin's (Core's C11
  rule, with Core's codes), a KV value past Core's 64 KiB, `SendEmail` and `RevokeIdentity` with empty
  required fields, and `KVIncr` on text with Core's code. A plugin that relied on the fake being lax fails
  its own tests now — as it would on a real install.
- `ServeCommerce` serves the storefront's cart routes when the `Commerce` is an `http.Handler`.
- The slog handler behind `Log()` follows slog's grouping rules.

### Deprecated

- `nildatest.SessionsPerIdentity`, a package variable shared by every test in a binary. Use
  `Host.SetSessionsPerIdentity`.
