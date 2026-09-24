# Changelog

What changed for a plugin author, per release. The Go API a release freezes is `surface.txt` (and
`nildatest/surface.txt`); the git log has the reasons in full. Versions before v0.10.0 are not written up
here.

## Unreleased

Documentation, the example programs and the tests that hold them to Core — no change to the Go API or to
what any exported function does.

- The guides now say what Core does where they said otherwise: which hooks need a subscription (every one —
  `Serve` lists a provider's for you, and a row action or a report page is listed by hand), what
  `nilda plugin build` does with `os`/`arch` and the signing key, where a plugin's log lines go and at which
  level, that emitted events arrive in no fixed order, how the payment confirm step's errors divide into
  "ask again" and "final", and the broker and TLS details a plugin in another language needs.
- `_examples/shop` reads `content.saved` in the shape Core sends (`content_id`, `type`) and listens for
  `ecommerce.order_paid`, an event the site's shop really emits, instead of `order.paid`, which nothing
  sends.
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
