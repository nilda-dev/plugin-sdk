# Shipping carrier contract — plan (2026-10-12)

Owner decision 2026-10-12: carriers are separate plugins behind a contract defined here, the way payment gateways are
(`PAYMENTS.md`). Merchants connect their OWN carrier or aggregator account; Nilda takes no fee and does not resell.
First plugins: `plugin-sendcloud` (Europe) then `plugin-shippo` (US); direct carriers (DHL Express and others) later.

**Superseded in one part (2026-10-13):** the carrier contract does not get capabilities and hooks of its own
(`shipping_carrier`, `shipping_request`, `shipping.go`). It is a contract FILE, `contracts/shipping.carrier/v1/`,
on Core's service desk — `service_provider` / `service_consumer`, `provides` / `uses` (`SERVICES.md`); Core's
units PC5–PC8 replaced report 03's shipping-only relay (U3). The rest of the design below still stands.

Nothing of the carrier itself is built yet. The full design is in the commerce repo:
- `commerce/docs/plans-2026-10-12/03-carrier-contract-design.md` — proposed contract (`shipping.go` in this repo, two
  capabilities `shipping_carrier` and `shipping_request`, Core is a stateless relay, commerce keeps all records), hooks
  `describe`, `rates`, `label.create`, `label.void`, optional `track`, `pickup_points`, `documents`; a carrier's
  "no" must be a result, never an error (five errors switch a plugin off); rates use a 4 s budget inside Core's 5 s cap
  with a mandatory backup rate; labels are asynchronous; secrets use the existing `secrets` capability; test/live via
  `Test` and `TestHint`; a mock carrier plugin is on by a setting and says "not live" when off. Units U0-U7, 103 h.
- `commerce/docs/plans-2026-10-12/02-shipping-carriers.md` — carriers, aggregators, customs (the EU 150-euro duty
  exemption ended 2026-07-01), what is not verified.
- `commerce/docs/plans-2026-10-12/00-master-plan.md` — order of work and hours.

First unit, U0 (9 h, inside commerce, no SDK change): checkout honours the buyer's delivery choice, mixed-cart weight agrees
between quote and final price, `hookBudget` becomes a table.
