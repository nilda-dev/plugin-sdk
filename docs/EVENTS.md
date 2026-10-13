# Events — what happened, told to every plugin that asked

An event is a **fact**: `content.published`, `ecommerce.order.paid`, `shop.stock_low`. A plugin subscribes with the
`events` capability and the names it returns at `Init`; a plugin emits its own with `core.Emit`. A request — "quote
me a rate" — is not an event: that is the service desk (`SERVICES.md`).

## 1. Every event is kept until it is delivered

Core stores one row per subscriber the moment an event happens, and a job delivers what is due — each plugin in a lane
of its own, so a slow subscriber holds up nobody else. A delivery that fails is tried again after 5 seconds, 5
minutes, 30 minutes, 2 hours, 5 hours, then six times 10 hours apart — about three days; after that it is dead-lettered, the
site's admins are told, and the owner (or the assistant, `plugin_event_redeliver`) can send it again once your plugin
is fixed. A subscriber that is restarting gets its rows when it is back. `Emit` returns once Core has **stored** the
event, not once the others have handled it.

## 2. The envelope is CloudEvents 1.0

Inside `HandleEvent`, `nilda.EventOf(ctx)` gives the event's context:

| field | value |
|---|---|
| `ID` | unique per event, and **kept by every redelivery** — an id you have seen is a repeat to drop |
| `Source` | `/core` or `/plugins/<key>`, set by Core: an emitter cannot claim another's |
| `Time` | when it happened (RFC 3339) |
| `Subject` | the thing it is about, such as an order's id |
| `DataSchema` | where its data's shape is described, when one is |

The event's `type` is its name, unchanged, and its data is the JSON you were handed. Outgoing webhooks carry the same
shape, keyed by the same id.

## 3. What your handler must do

- **Drop repeats.** Delivery is at least once. Keep the ids you handled (a `kv` key, a table row written in the same
  transaction as the effect) and skip one you have seen.
- **Do not rely on order.** Two events about one order can arrive either way round; compare the subject's version
  (its `updated_at`) and ignore the older.
- **Return an error to be asked again.** A nil return is "handled"; anything else is retried on the schedule above.
- **Be quick.** Each delivery is one call within Core's per-call budget (`PLUGIN_CALL_TIMEOUT`, 5 seconds by
  default); a delivery that runs out of it is tried again. Do long work on a schedule or a queue of your own.

## 4. Versions

A compatible change keeps the event's name. An incompatible one is a new name (`…/v2`), emitted beside the old one for
a time so subscribers can move.
