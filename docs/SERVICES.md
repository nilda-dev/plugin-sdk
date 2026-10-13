# The service desk — one plugin asking another through Core

A carrier answers the shop's question "what does shipping this basket to Oslo cost?". A feed asks the shop for its
catalogue. Neither knows the other is there: each speaks a **contract** this SDK ships as data, and Core sits between
them. That is the service desk (Core's owner decisions D5 and D9, 2026-10-12). The Go surface is in this module from
**plugin-sdk v0.14.0** (`services.go`); Core accepts `service_provider` and `service_consumer` from the release built
on it.

## 1. The shape

```
 consumer (service_consumer)          Core                          provider (service_provider)
 ───────────────────────────          ────                          ───────────────────────────
 Services.Call ──────────────────►  checks the caller declared it
   POST /services/<name>/<vN>/<m>    checks the request (input schema)
                                     asks every active provider ───► svc:<contract>.<method>
                                     at once, within the budget        (ServiceProvider; Serve checks
                                     checks each answer (output)        the request and the answer too)
                                     drops one that does not match
 ◄─────────── answers + missed ───   applies the fallback if none
```

- **Contracts are files**: `contracts/<name>/v<major>/contract.json` — each method's answerer, budget, retry class,
  fallback, idempotency, the assistant's class, and the JSON Schema (2020-12) of its request and answer. A plugin in
  another language reads the same file (`ANY_LANGUAGE.md`). `ContractNamed`, `Contracts` and `ParseContract` read them.
- **Only the contracts this SDK ships exist.** Core reads the files of the SDK version it is built with; a manifest
  naming any other contract is refused at install. A new contract is a change to this module, reviewed like a
  protocol change, never something a plugin declares into being.
- **Within a major, a contract only grows.** A schema never closes an object, so a field one side adds is never refused
  by the other. A breaking change is a new major (`…/v2`), served beside the old one for a while.

## 2. The manifest

```json
{
  "capabilities": ["service_provider", "service_consumer"],
  "provides": ["shipping.carrier/v1"],
  "requires": ["catalogue.read/v1"],
  "uses":     ["tax.rates/v1"]
}
```

| list | needs | means |
|---|---|---|
| `provides` | `service_provider` | Core asks you each relayed method of the contract. You list no hook: the manifest is the subscription. |
| `requires` | `service_consumer` | You cannot work without it. Core will not switch you on until an active plugin provides it, will not switch off (or remove, or update away) the last provider while you are on, and says why in each case. |
| `uses` | `service_consumer` | You call it when a provider is there. Until then the method's `fallback` answers. Nothing waits for it. |

The rule is the one the platforms use — WordPress's plugin dependencies, Drupal's module dependencies, Debian's
Depends and Recommends, OSGi's mandatory and optional requirements: a required dependency holds the switch, an
optional one does not, and installing is never refused for either. No version ranges: the name carries the major.

Your token carries `use:<contract>` for each contract you require or use. Core refuses a call for any other with a 403
whose `details.reason` is `contract_not_declared`. An update of your plugin that adds a contract to any of the three
lists waits for the owner's yes, as a new capability does.

## 3. The consumer

```go
res, err := core.API().Services().Call(ctx, "shipping.carrier/v1", "rates", req,
    nilda.ServiceCallOptions{IdempotencyKey: "order-1042-rates"})
if err != nil {
    return err // the request broke the contract, you did not declare it, or a "block" method had no answer
}
for _, a := range res.Answers {
    var rates RatesAnswer
    if err := a.Decode(&rates); err == nil { … } // a.Provider says whose; a.Cached marks a last good answer
}
for _, m := range res.Missed { … } // "timeout", "error", "invalid_answer", "circuit_open", "busy", "unavailable"
```

- **A provider that fails is a Miss, not an error.** The others' answers stand, and a deadline is a result: the shop
  shows the rates it has.
- **The fallback decides when nothing answered.** `skip`: an empty answer and `Fallback: "skip"`. `block`: an error
  (Core's 503). `cached`: each provider's last good answer to the *same* request (kept a day, in memory), marked
  `Cached`. When anything answered, the fallback is not used.
- **`IdempotencyKey`** makes a repeated call one call: Core replays the first answer for 24 hours, and hands the key to
  each provider for an effect outside Core — a label bought — to happen once.
- **`Provider`** asks one provider by its plugin key instead of all of them.
- The client checks your request before it leaves and each answer before it returns; Core checks both as well.

## 4. The provider

```go
func (c *Carrier) ServiceMethods() []nilda.ServiceMethod {
    return []nilda.ServiceMethod{
        nilda.Serves("shipping.carrier/v1", "rates", c.rates),
    }
}

func (c *Carrier) rates(ctx context.Context, req RatesRequest) (RatesAnswer, error) {
    call := nilda.CallOf(ctx) // call.Caller is the asking plugin, call.IdempotencyKey its key
    …
}
```

Implement `ServiceProvider` and `Serve` answers each call for you: the request is checked against the contract before
your method runs, and your answer after — an answer the contract refuses is returned as an error, so your own test
fails on it before Core drops it in production. A hook for a method you do not list reaches your `HandleHook`, for an
author who routes by hand (`SplitServiceHook`, `DispatchServiceHook`).

An error from your method is the method's answer only where its `retry_class` is `answer`; otherwise it is you
failing. Core keeps a circuit for you **per contract**: a provider that keeps failing `rates` is left alone for
`rates` for a cooldown, while your other contracts and Core's own hooks still reach you, and none of it counts
toward switching your plugin off.

## 5. The owner's doors

`services_status` lists every contract, who provides, requires and uses it, each provider's circuit and last failure.
Every relayed method a contract marks for the assistant (`agent: read | write | destructive`) is a tool named
`<name>.v<major>.<method>` (`shipping.carrier.v1.rates`), with the contract's schema and class — a changing one shows
who will be asked to do what and waits for a yes. A method marked `none` names its other door in `agent_reason`.
Each relayed call is one line in Core's log — contract, method, caller, who answered, who missed and why, how long —
and never the request or the answer.

## 6. Testing

```go
desk := nildatest.NewServices()
desk.Provider("dhl", nildatest.StubService("shipping.carrier/v1", "rates", func(in json.RawMessage) (any, error) {
    return RatesAnswer{…}, nil
}))
core, _, srv := desk.Core("shop", "shipping.carrier/v1")
defer srv.Close()
res, err := core.API().Services().Call(ctx, "shipping.carrier/v1", "rates", req, nilda.ServiceCallOptions{})
```

A provider's test swaps the sides: `desk.Provider("dhl", myCarrier)` and `desk.Ask(...)`. For a contract this SDK
does not ship yet — one being written — `nilda.RegisterContractForTest(c)` (from `ParseContract`) makes it known to
the process until the returned function is called. Core never sees it.
