# Write commands — how a plugin changes what another plugin keeps (ADR, door D0)

Status: accepted 2026-11-05 (the shop's DR-24, its door D0). From plugin-sdk v0.58.0.

Until now every contract a plugin could call through Core's service desk (`SERVICES.md`) was a READ, or a question the
provider answered without keeping anything (`shipping.carrier/v1` rates, `shop.catalog/v1`). The shop's plugins of
2026-11 need to WRITE into the shop: a field on an order (`shop.attributes/v1`), a tax answer kept on the order, an
import, a renewal order. A write has failure modes a read does not: it can happen twice, it can be left half done, it can
be made by a plugin that is later removed, and the owner may want it gone. This record fixes ONE pattern for every write
any contract carries, so each new door is built on it rather than inventing its own.

## 1. The decision

A method that changes a record its provider keeps is a **write command**. Its contract file says so with a `write`
part, and `ParseContract` refuses the file unless the method keeps every rule of §2:

```json
{
  "name": "note.set", "answered_by": "provider", "retry_class": "answer",
  "fallback": "block", "idempotent": true, "agent": "write",
  "input":  { "type": "object", "properties": { "order_id": {"type": "string"}, "text": {"type": "string"} } },
  "output": { "type": "object", "properties": { "record_id": {"type": "string"}, "already": {"type": "boolean"} },
              "required": ["record_id", "already"] },
  "write":  { "record": "order.note", "undo": "note.undo", "emits": "shop.note.set" }
}
```

## 2. The rules (each is checked by `ParseContract`, or by the provider's own test where noted)

1. **One write per idempotency key, for as long as the record lives.** The caller sends a key
   (`ServiceCallOptions.IdempotencyKey`); Core answers a repeat within 24 hours from its cache and hands the key to the
   provider (`CallOf(ctx).IdempotencyKey`). The provider does not rely on Core's window: it stores the key with the
   record under a **unique index** on (calling plugin, method, key), so a retry a week later — a plugin that crashed
   before it saw the answer — gets the first answer with `already: true`, and nothing is written twice. A write without
   a key is refused by the provider (its test shows it). Contract rule: `idempotent: true`.
2. **A write is never skipped and never answered from a cache.** Contract rule: `fallback: "block"`. A consumer whose
   write could not be made is told so and decides; Core never pretends it happened.
3. **The answer names the record and says whether this call made it.** Contract rule: the output requires `record_id`
   and `already` (`nilda.WriteAnswer`).
4. **Every write can be taken back.** Contract rule: `write.undo` names another write command of the same record kind,
   whose request requires `record_id` (`nilda.UndoInput`). An undo is itself idempotent and keyed. An undo of a record
   that something later depends on (an order already paid with the tax answer on it) is refused in words by the
   provider — it is a compensation the provider understands, never a blind delete. What an undo removes goes to Core's
   recycle bin where the provider bins its own deletes (the shop: `binDelete`, DR-22).
5. **The provider keeps who wrote it.** Every record a write command makes carries the calling plugin's key
   (`CallOf(ctx).Caller`, set by Core, never by the caller's arguments), so the owner sees "added by <plugin>" and an
   uninstall can name what the plugin left (door D11). The provider's test shows the attribution.
6. **The provider announces it.** `write.emits` names the event the provider emits once the write is COMMITTED (never
   before, never from inside the transaction): other plugins and the owner's automations learn of it the way they learn
   of everything else (`EVENTS.md`), and the writing plugin can confirm it.
7. **The assistant's class is write, destructive or none, never read.** A model reaching a write command is told it
   changes something; money and deletions are `destructive` (the shop's DR-15).
8. **A write command is relayed by the service desk** — never one of Core's own hooks (`hook` empty), so Core checks the
   caller declared the contract and checks the request and the answer against their schemas.

## 3. What the provider keeps (the shop's base, `plugin-commerce/pluginwrites.go`)

One table, `plugin_writes`: `(caller, method, idempotency_key)` unique, the record kind and id, the answer given, when,
and when it was undone. A write command's handler runs inside one transaction that (a) looks the key up — found: return
the stored answer with `already: true`; (b) makes the change; (c) inserts the `plugin_writes` row; commits; then emits.
Two concurrent calls with one key: the unique index lets one commit and the other reads the first's row. The shop's
helper `pluginWrite` does exactly this so no door writes it again.

## 4. Compensation, not distributed transactions

A plugin that makes several writes (an importer, a renewal: an order, then a charge) does not get a transaction across
plugins. It gets: idempotent steps (a retry finishes the job without doubling it), an undo per step, and the events to
see where it stopped. A step that fails after an earlier one succeeded is followed by the caller's undo of the earlier
one, or a retry of the failed one with the same key — the caller chooses, and both are safe.

## 5. Experimental contracts (`"status": "experimental"`)

A contract file may say `"status": "experimental"`. It is published, Core relays it like any other, and an outside
developer may build on it — but it is OUTSIDE the v1 no-breaking promise: its shape may still change in a minor release,
said in `CHANGELOG.md`. Every new contract of the shop's doors (D1-D11) ships experimental — the first two are D2's (`ATTRIBUTES.md`). A contract leaves
experimental — gains `"status": "stable"` or loses the field, and from then on only grows (`SERVICES.md`) — once its
first plugin built by Nilda has shipped green on it. `ServiceContract.Experimental()` reads it; a tool that lists
contracts (Core's plugin detail, Central's review) shows it.

## 6. What a plugin author does

- Consumer: always send `IdempotencyKey`, derived from what the write is FOR (the order id and the field name, the
  import's row id), never a fresh random value per attempt — a random key makes every retry a new write.
- Consumer: read `already`; keep `record_id` if you will ever undo.
- Provider: answer every write command through one helper that keeps §3, and test the key, the attribution and the undo.
- Both: treat an experimental contract as something that may change; pin the SDK version you built against.

## 7. A refusal is an answer, not an error (amended 2026-11-05, door D2)

Core's service desk relays a provider's ERROR as the provider failing (a miss): with `fallback: "block"` the call fails
with 503 and the caller's client retries it. So a provider that says "no" by returning an error — a field that is not the
caller's, an undo past a later write — makes the caller wait out the retries and read "unavailable". A write command's
refusal is therefore a RESULT in its answer: `record_id` empty, `already` false, and a code saying why (D2's
`AttributeWriteAnswer.Refused`). A refusal wrote nothing and is not kept under its key, so the same key may succeed once
the record is there. Errors stay for what a well-built caller never sends (no key, no caller) and for the provider
failing. The event a provider emits is in a namespace Core lets it emit: the shop's own, `ecommerce.*` (the `shop.*`
names above are illustrations of the file's shape).

## 8. Alternatives considered

- **Core's 24-hour idempotency cache alone.** Rejected: a plugin retrying after a day doubles the write, and the
  record of who wrote what would live in Core's cache, not on the record.
- **A distributed transaction (two-phase commit) between plugins.** Rejected: plugins are separate processes that can be
  switched off at any moment; the market's platforms (Shopify's app mutations with idempotency keys, Stripe's
  `Idempotency-Key`, WooCommerce's REST writes) all use idempotent steps plus compensation instead.
- **Each door designing its own.** Rejected by DR-24: eleven doors, eleven patterns, and a money path changed twice.
