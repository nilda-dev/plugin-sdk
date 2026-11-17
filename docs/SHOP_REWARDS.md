# shop.rewards/v1 — what a rewards plugin gives a buyer (EXPERIMENTAL)

The shop's plan 2026-11-05 §1J ("points / store credit spend", behind door D0); P08 loyalty and its referral reward.
From plugin-sdk v0.68.0. The shop provides it; a rewards plugin requires it.

A loyalty plugin never prices a basket and never keeps money: a buyer's points are spent as something the shop already
honours.

| Method | Kind | What |
|---|---|---|
| `credit.issue` | write, record `store_credit`, undo `credit.undo`, emits `ecommerce.store_credit.issued` | store credit on one buyer's account (Core's account id), in the shop's currency, with a reason |
| `credit.undo` | undo, emits `ecommerce.store_credit.undone` | takes the credit back; refused `spent` once the balance no longer holds it |
| `discount.issue` | write, record `reward_discount`, undo `discount.undo`, emits `ecommerce.reward_discount.issued` | one single-use code through the shop's discount engine, usable once and only by that signed-in buyer |
| `discount.undo` | undo, emits `ecommerce.reward_discount.undone` | the code goes to the site's recycle bin; refused `used` once an order used it |
| `issued.get` | read | what one record the caller issued is now: `open`, `used` (with the order), `expired`, `undone` |

Every write follows `WRITE_COMMANDS.md`: the idempotency key is derived from what the reward is for (a redemption's id,
a referral's id) and answers the first record with `already: true` for as long as the shop keeps it; the shop keeps the
calling plugin with the record, so the owner reads "issued by loyalty" on the customer's store credit and on the discount;
a refusal is the answer's `refused` code (`RewardRefusals()`), never an error.

Points come back with a refund, never as a second credit: the plugin hears `ecommerce.order.refunded` (`SHOP_EVENTS.md`)
and adjusts its own balance. Whatever the plugin gave that an order must show — the code and who issued it — is copied
into the order when it is placed (door D11's snapshot rule).
