# shop.subscriptions/v1 — subscriptions sold and renewed by the shop (EXPERIMENTAL)

Doors D3 (the selling-plan discount as a pricing input, the subscription line at checkout) and D9 (the recurring billing
attempt) of the shop's plan 2026-11-05 §1J; P02 subscriptions. From plugin-sdk v0.68.0. The shop provides it; a
subscriptions plugin requires it.

## One selling-plan concept

A subscription plan IS one of the shop's selling plans — the mechanism its pre-orders use — written by the plugin
(`plan.set`). A plan is never edited: a change is a new plan and `plan.undo` of the old one, so every agreement keeps the
terms it was sold on. A plan's terms in deliveries (`SubscriptionCycles`): `discount_cycles` — the discount applies to
the first N deliveries, later renewals are charged at the price agreed; `min_cycles` — the minimum term, shown above the
order button and in the mandate (a stop before it is the plugin's call, the shop records it); `max_cycles` — the
subscription ends by itself after N deliveries, a renewal past it refused `past_max_cycles`. The shop sells it: the basket line on the plan, the plan's discount priced as a line deal beside a
member's price and an offer (tax is worked on what is left), the stock, the checkout. The product page's choice is Core's add-to-cart, drawn
from the plans the shop answers on the product (`CommerceProduct.Plans`, v0.69.0 — COMMERCE.md §8, "Purchase options"),
priced by the shop and posted back as `selling_plan`.

## The agreement

At checkout a basket holding a subscription line pays through a method that can keep the buyer's method
(`SupportSaveForLater`); the session carries `Save` — a `PaymentMandate` of purpose `MandateSubscription` with the plan's
rhythm, the rule that sets each charge and the most one may be. Core keeps the method; the shop keeps its id; the plugin
never sees either. The order makes an agreement the shop keeps (`SubscriptionAgreement`): the lines at the unit price
agreed — LOCKED: a renewal never charges today's catalogue price; a price change for running agreements is an owner's act
with notice to the buyer, not this door — the rhythm, the buyer, the mandate's words and limit. It is `pending` until the
first order is paid with a method kept, then `active` (`ecommerce.subscription.started`).

## Renewals

`renewal.place {agreement_id, cycle}` places the cycle's order through the shop's own checkout — the agreement's lines (or
the buyer's change of quantities, its own SKUs only) at the locked price less the plan's discount, today's tax, the
delivery — and charges the kept method off-session (`Payments.ChargeSaved`, consumer the shop), never above the
mandate's limit (`over_mandate`). One order per (agreement, cycle), held by a unique index: asked again with a new key
for a cycle whose order is unpaid, the same order is charged again (a dunning retry); once it is paid, `cycle_paid`.
Cycles may be skipped, never gone back to (`cycle_out_of_order`), and not placed more than a day before they are due
(`too_early`). `renewal.quote` answers what a cycle would cost if it were placed now (the reminder before a charge), placing nothing.
`renewal.undo` cancels an unpaid cycle's order — refused `charging` while its charge is with the processor, undecided; a paid one is refunded only by the owner, through the shop's
`refund_lines`. Events: `ecommerce.renewal.placed`, `.paid`, `.failed` (with `fail_code`), `.undone`.

## Stopping

`agreement.stop {agreement_id, reason, ends_at}` stops renewals at once and cancels a cycle's order still unpaid; the method is forgotten at `ends_at` (now when
absent) and until then `agreement.resume` takes the stop back. After it, a new agreement needs the buyer present.

A renewal's lines never ask more of an item than the agreement has (`over_agreement`): a bigger order needs a new
agreement, made with the buyer present. A delivery no longer offered is `delivery_unavailable`.

**Cycles and Core.** The shop numbers an agreement's cycles from 1, the order that started it, paid on the buyer's own
checkout session — the cycles of this contract. Core's (door D5) number the charges after sign-up from 1: each
renewal's off-session charge carries `PaymentChargeParams.Agreement` = the agreement's id and `Cycle` = the
contract's cycle less one (cycle 2, the first renewal, is Core's 1), so Core's one paid-or-deciding charge per
(consumer, agreement, cycle) is the shop's one order per cycle. A charge's money that arrives after its cycle was paid
by a retry (`PaymentSession.DuplicateOf`) is refunded by the shop in full, once, and the owner told; it never pays the
cycle again.

## The buyer's way back from a failed renewal (v0.69.0, EXPERIMENTAL)

A shop that implements `SubscriptionLinksProvider` answers two reads, each a signed link that expires (`ExpiresAt`) and
opens only for the buyer it is for — or a refusal. Asking writes nothing and charges nothing.

- `renewal.pay_link {agreement_id, cycle}` — the cycle's order on the shop's own pay page (the order's page, the same
  door "Pay the difference" uses after an order edit — never a second checkout). The buyer pays it present, through the
  agreement's method, in a session that names the agreement and Core's cycle (`PaymentSessionParams.Agreement`), so
  Core's one payment per cycle holds between the buyer and a dunning retry: whichever is second is refused. Refused
  `no_renewal` (no order for the cycle), `cycle_paid`, `renewal_closed` (cancelled or refunded) and `charging` (a
  payment of it is with the processor). Paid, it is `ecommerce.renewal.paid` as any; a buyer's payment that ends
  without money is `ecommerce.renewal.failed` with the cycle's earlier `fail_code` and `by_buyer: true` — the dunning
  goes on, and is not told the buyer was turned away as a new failure.
- `method.update_link {agreement_id}` — a page where the SIGNED-IN buyer of the agreement gives another method: a
  session that only keeps one (`Save` of purpose `update`, `Replaces` the agreement's method, the agreement's own
  words and limit), on the processor's own page — nothing card-like touches Nilda. Resolved, the agreement renews on the
  new method (`ecommerce.subscription.method_updated`, `source: "buyer"`), the link stops working, and the old method
  is forgotten once nothing is charging it. Refused `agreement_stopped` and `no_account` (a guest's agreement has
  nobody to sign in as).

The card network updating the kept card by itself (Core's card updater, `PAYMENTS.md`) is
`ecommerce.subscription.method_updated` with `source: "network"`; the agreement's `method` carries the new label and
expiry.

Every write follows `WRITE_COMMANDS.md`; refusals are `SubscriptionRefusals()`; every event `SubscriptionEvents()`.
