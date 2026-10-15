# The shop's checkout — `shop.checkout/v1`

An agentic-commerce protocol — ACP (OpenAI and Stripe), UCP (Google, Shopify and others) — lets an AI agent build a
basket on a merchant's site, have it priced, and either pay for it with a credential the buyer's wallet delegated to the
agent or hand it to the buyer to finish. A **protocol adapter** plugin speaks the protocol to the agent's platform and
drives the shop's own checkout through `shop.checkout/v1`: the same basket, the same delivery options, tax and
discounts as the shop's checkout page, the same order. The shop **provides** the contract; an adapter **requires** it.
A protocol's new version is the adapter's release, never the shop's.

## The methods

| Method | Asks | Answers |
|---|---|---|
| `describe` | nothing | `CheckoutDescription`: the shop's name and address, currency, country, language, its terms and privacy pages, and the delegated credential types its payment plugins can charge now |
| `session.create` | `CheckoutCreateRequest`: lines (SKU, quantity; 1–50), the buyer, the address, a discount code, the agent | `CheckoutResult` |
| `session.update` | `CheckoutUpdateRequest`: the token, and what changes — the whole basket, the buyer, the address, the delivery option, the discount code | `CheckoutResult` |
| `session.get` | `CheckoutRef`: the token | `CheckoutResult` |
| `session.complete` | `CheckoutCompleteRequest`: the token, the buyer, `terms_accepted`, `digital_consent`, the credential, the agent | `CheckoutResult` |
| `session.cancel` | `CheckoutRef` | `CheckoutResult` |

`CheckoutResult` is the checkout as it now stands (`state`) or why the shop refused (`refused`: `not_found`, `invalid`,
`not_allowed`, `closed`, `not_completed`, with a message the agent can read) — exactly one. A refusal is an answer, not
an error, so the adapter can tell its agent what was wrong.

Every method is relayed by Core's service desk, checked against the contract's schema both ways, never cached, and
fails the call when the shop does not answer. Create, update, complete and cancel carry the adapter's idempotency key
(`Services.CreateCheckout(ctx, req, key)`): Core answers a repeat within a day from the first, and the shop's own
complete places one order per checkout however often it is asked. None is a tool of the site's assistant: a visitor's
own agent has the shop's visitor tools, the owner the shop's abilities.

## A checkout

`CheckoutState` carries:

- `token` — the checkout's handle, the protocols' checkout session id. A bearer value: whoever holds it reaches the
  basket, so an adapter gives it only to the agent that made it.
- `status` — `incomplete` (a message of severity `error` says what is missing), `ready_for_payment` (this caller may
  complete it with a credential of one of `credential_types`), `requires_escalation` (a person finishes it at
  `continue_url`), `completed` (`order` names the order), `canceled`.
- `lines` as the shop priced them, `totals` in minor units (`tax_included` when the tax is inside the total),
  `requires_shipping`, `delivery_options` and the chosen `delivery_option_id`, the `buyer`, the `address`,
  `messages`, `continue_url`, `credential_types`, `terms_url`, `expires_at`.

## What the buyer is told first

Every open state carries `pre_contract`: who sells (name, address, email, phone, VAT number), the withdrawal period and
its text and form, the terms, privacy and returns pages, and the words the final step must carry. An agent buying for a
person shows it before it completes — a consumer the trader did not inform is not bound, and a withdrawal period
without its information runs twelve months longer (Directive 2011/83/EU Art. 6(1), 8(2), 10(1)). A basket holding a
download carries `digital_consent_text`, and each such line `digital: true`; `session.complete` sends the sentence back
word for word with `digital_consent`, and the shop keeps the sentence and the agent with the order as proof (Art. 16(m)).

## Who may pay

`agent` names the AI agent the adapter serves — the platform its API key belongs to, or the agent Core identified by
its Web Bot Auth signature (`AgentOf`). The shop does not take the tier the adapter claims: it asks Core what the owner
set for that operator on SEO → Shopping agents (`API().AgentTier`), refuses a blocked one, and lets only `buy` complete
with a credential — every other checkout is finished by a person at `continue_url`. The operator, and the door it came
through (the adapter's plugin key), are recorded on the order (the shop's "sold by agents" report).

The credential (`PaymentCredential`: `stripe.shared_payment_token`, …) passes from the adapter through Core's desk to
the shop, the payment plugin and Core's payments, and none of them logs or keeps it. Core's desk logs who asked whom,
never what; its idempotency replay keeps the answer, which holds no credential.

## In Go

```go
// The shop: Serve finds a CheckoutProvider by itself.
func (s *Shop) DescribeCheckout(ctx context.Context) (nilda.CheckoutDescription, error) { … }
func (s *Shop) CreateCheckout(ctx context.Context, req nilda.CheckoutCreateRequest) (nilda.CheckoutResult, error) { … }
// … UpdateCheckout, GetCheckout, CompleteCheckout, CancelCheckout

// An adapter: "requires": ["shop.checkout/v1"], with service_consumer.
res, err := core.API().Services().CreateCheckout(ctx, nilda.CheckoutCreateRequest{
	Lines: []nilda.CheckoutLineRequest{{SKU: "mug-blue", Quantity: 1}},
	Agent: &nilda.Agent{Operator: "chatgpt.com", Tier: nilda.AgentBrowse},
}, idempotencyKey)
```
