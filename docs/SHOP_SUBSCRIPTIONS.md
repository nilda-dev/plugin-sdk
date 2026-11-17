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
member's price and an offer (tax is worked on what is left), the stock, the checkout.

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
`renewal.undo` cancels an unpaid cycle's order; a paid one is refunded only by the owner, through the shop's
`refund_lines`. Events: `ecommerce.renewal.placed`, `.paid`, `.failed` (with `fail_code`), `.undone`.

## Stopping

`agreement.stop {agreement_id, reason, ends_at}` stops renewals at once and cancels a cycle's order still unpaid; the method is forgotten at `ends_at` (now when
absent) and until then `agreement.resume` takes the stop back. After it, a new agreement needs the buyer present.

Every write follows `WRITE_COMMANDS.md`; refusals are `SubscriptionRefusals()`; every event `SubscriptionEvents()`.
