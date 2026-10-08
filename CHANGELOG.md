# Changelog

What changed for a plugin author, per release. The Go API a release freezes is `surface.txt` (and
`nildatest/surface.txt`); the git log has the reasons in full. Versions before v0.10.0 are not written up
here.

## v0.10.3 — 2026-10-08

No change to the Go API (`surface.txt` is untouched) and none to the wire contract. The module path is now
`github.com/nilda-dev/plugin-sdk`: the GitLab group is retired, and `v0.10.3` is the first tag that declares it.
Every earlier tag declares a GitLab path, so a plugin or Core must pin `v0.10.3` or later once it imports the new path.

## v0.10.2 — 2026-10-04

One addition to the Go API: `CommerceEndpoints.Reviews` (`surface.txt`).

### Added — commerce

- **`CommerceEndpoints.Reviews`**: a GET answering one product's reviews and review form as a fragment. Core's
  new Product Reviews widget asks it with `?sku=<product id>` and draws it inside the site's page; empty, the
  widget draws nothing.

### Documented — Core's storefront shell

- **Forms in your commerce fragments** (`commerce.go`): Core's shell sends a POST form found in its mount
  itself and draws your answer back in place (or into the `data-pb-fragment` around the form); send the shopper
  to a processor's page with the `Nilda-Redirect` header on a 200, not a 3xx. Core's CSRF check now accepts the
  browser's same-origin stamp, so these forms — and a plain form posted to your route — are no longer refused.

## v0.10.1 — 2026-09-25

One addition to the Go API: `nildatest.Payments.Decide` (`nildatest/surface.txt`). `surface.txt` is as in
v0.10.0. What a plugin can notice:

### Changed — the SDK

- **An error your handler returns reaches Core as your ANSWER, whatever it wraps.** `Serve` sends it as gRPC
  `Unknown`, the one code Core reads as the plugin answering — a row action's or an ability's refusal, a payment
  consumer's "tell me again". grpc-go sends the code of any gRPC status an error wraps, so a refusal wrapping a
  Core call's error (kv's `ResourceExhausted`, say) arrived as that code and counted toward switching the plugin
  off.
- **A panic in your code now counts as a failure.** It reaches Core as `Internal`; it used to arrive as a plain
  error, which Core read as your answer — a row action that panicked on every press was shown to the owner as
  your words and never counted.
- **`API.UploadMedia` with an Idempotency-Key sends the same bytes on every run** — its multipart boundary is
  derived from the key and everything the body carries, the file included. A keyed upload re-run after a crash
  was refused by Core as "a different request". (Derived from the key alone, as first shipped here, a file
  could carry the boundary and add form fields of its own; a file that contains its boundary is refused.)
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
- **`Decide`** — a person settling a pending payment, as the owner does on Settings → Payments: a held mismatch
  resolved at the processor's amount (one in another currency refused), a rejection keeping `amount_mismatch`
  as its reason with the note as its message. A consumer can test the order it books at an amount it did not ask.
- As Core, now: the same key with another body while the first runs is **409** (it was 422); a panic under a
  key records a **500** instead of leaving the key "still being processed" for good; an over-refund while the
  gateway cannot be asked is **503** (it was 422); a start the gateway could not answer carries no failure
  message; a consumer's refusal in the confirm step is not written onto a payment settled meanwhile, and its
  reason is cut to 500 characters; a report's body is checked before its session is looked up (a malformed one
  is 422, never 404); a currency is compared as Core normalises it (`" eur"` is EUR).
- **The kv budget**: a `Host` refuses a write past 10,000 keys or 16 MiB — counted as Core counts them, the
  key under Core's namespace prefix plus the value — with `ResourceExhausted` and Core's words, after sweeping
  expired keys. It said it did not model this, and a plugin that filled its namespace passed its tests.

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
- `CHANGELOG.md` (this file).
- After an independent review of the above, the guides say, where they said otherwise or nothing:
  - which code Core reads as your answer (`UNKNOWN`; ANY_LANGUAGE), and that a panic counts;
  - that a gateway's budget is AT MOST 15 seconds — `payment.start` and `payment.describe` keep the asking
    request's deadline;
  - that a lost `payment.start` answer usually means no charge, and what to do with one that happened;
  - that a second `Resolve` with another amount is a 409, not a no-op;
  - that the assistant reads every non-secret setting — `secret` is the line the model does not cross;
  - that a free install's assistant runs only `read` abilities;
  - that a row action is not told who pressed it;
  - the `content.*`, `schedule:` and `ability:` payloads for a plugin in another language;
  - never to put the plugin key on an https request's own headers;
  - that `Health` is never called; that a 503 gives its Idempotency-Key back; that `-ldflags='-s -w'` keeps a
    binary's build information; that a field type's `base` is a content field type, not a widget one;
  - how to read the Core-reading tests' skips, and that they fail when a file they read has moved.
- `_examples/shop` has a `plugin.json` declaring the settings page it reads and the capabilities it uses, held
  by its tests.

### Changed — what Core does, and the guides say so

- The `kv` row: one plugin's 10,000 keys and 16 MiB now hold on every install. Core used to keep them on a
  Lite install only, and keeps them on Dragonfly too now — a plugin there past either answers
  `ResourceExhausted`, where it used to be let through. (On Dragonfly, writes racing each other at the very edge
  can pass it together — by what one plugin writes at that instant, never more.)
- A call past Core's rate limit (email, emit, kv) answers `ResourceExhausted` **with a `google.rpc.RetryInfo`**
  saying when there is room again; a write to a full kv namespace answers the same code with none — retrying
  does not help there. The two could not be told apart before.
- `route_prefix`: Core now refuses `/category`, `/tag`, `/search` and any language's code (`/fa`, `/en`,
  `/pt`) — Core serves pages under each, after a plugin's route has had its turn, so a plugin holding one
  answered every category page or every Persian one. A plugin already installed under one of them keeps it
  until its next update. The code is the whole prefix: `/my-account` is not Burmese (Core refused it as such at
  first, reading only up to the hyphen).
- Tables: the 16-index cap counts every index Core builds (unique columns and a list page's `order_by`
  included); a table may not be named like another's primary key (`orders_pkey`); a plain-number default must
  fit its column's range; and switching on a reinstall over the tables an earlier install left cannot move a
  primary key, as an update already could not.

- **The site's assistant manages plugins now — never removes one** (Core's D-87): it can turn a plugin on,
  update it to the version the install ships, and read and change its settings (`plugin_settings_set`, for a
  person who may configure your plugin, after they approved it). The settings go through the same save as
  your admin section — declared fields only, a required field never emptied, a secret written and never read
  back — and your plugin restarts with them, as after any save.
- **A row action's error reaches the owner in your words** — its first 300 characters, through your
  `translations` like a success `Message`. It used to arrive as "internal error". An error you give a person
  (a row action, a report, an ability) no longer counts toward `PLUGIN_MAX_FAILURES`; a timeout or a crash on
  those calls still does, and a row action whose process stopped mid-call is "may have run", like a timeout.
- **An Idempotency-Key replays only under the same scopes.** A repeat made after an update changed your
  plugin's scopes is a different request (422), never the first answer.
- **The egress proxy challenges a request that names no plugin** (`407` with a Basic challenge) instead of
  refusing it as undeclared, and keeps no connection to the provider open after a plain (http) request.
- A hook, schedule, ability or event subscription your capabilities do not admit is logged at start — a hook by
  its name; schedules, abilities and events by the capability they need (they were dropped in silence).
- `admin_pages` and `editor_commands`: help past 500 characters, a placeholder, a choice, a page icon, a
  command's group, icon or keyword past 120, and more than 16 keywords are refused at install.
- `nilda plugin check` refuses a field Nilda does not know, as the guide said it did — only `build` did.
- `backup.*` is Core's event namespace too: a plugin emitting under it is refused.
- `POST /api/rest/v1/media` takes the media upload limit, not the general body limit.
- The guides say an ability is not told which person ran it (Core records that as a known limit).

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
