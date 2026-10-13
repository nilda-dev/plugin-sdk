# Shipping carriers — the contract

A carrier plugin connects the merchant's OWN carrier or aggregator account — Sendcloud, Shippo, a carrier's own API
— and the shop asks it what a parcel costs, buys labels from it and follows the parcel. Nilda takes no fee and
resells nothing. The contract is `shipping.carrier/v1` on Core's service desk (`SERVICES.md`): the carrier
`provides` it, the shop `uses` it, Core checks every request and every answer against
`contracts/shipping.carrier/v1/contract.json`, and **the shop keeps every record** — quotes, labels, tracking — so a
carrier plugin keeps nothing it needs to answer again. The Go surface is in this module from **plugin-sdk v0.15.0**
(`shipping.go`).

## 1. The methods

| method | asked when | time | the assistant |
|---|---|---|---|
| `describe` | the merchant sets up shipping, or asks what is installed | Core's 5 s | read |
| `rates` | a buyer reaches the delivery step — every carrier at once | 5 s; the SDK gives your `Rates` 4 s | read |
| `label.create` | the merchant (or the assistant, through the shop) buys a label | 15 s | none — the shop's `buy_shipping_label` records it on the order |
| `label.void` | a label is cancelled before it ships | 15 s | none — the shop's `void_label` records it |
| `track` | the shop's tracking job, or the merchant asks where a parcel is | 15 s | read |
| `documents` | a label's PDF is needed again | 15 s | none — the order screen hands it over |

Money is `AmountMinor` in the currency's minor unit (ISO 4217, `MinorUnits`), countries ISO 3166-1 alpha-2, weights
grams, lengths millimetres — all whole numbers.

## 2. The rule: a carrier's "no" is a result

"No service to Oslo", "that address does not exist", "the carrier's API is down" are answers: a status and one of
the failure codes — `unavailable`, `invalid_address`, `invalid_parcel`, `no_service`, `customs_missing`,
`credentials_refused`, `not_voidable`, `declined`, `unsupported` — that the shop acts on: it shows its backup rate,
asks the buyer for a phone number, tells the merchant the HS code is missing. **Return an error only when your plugin
itself failed**: Core counts errors against your circuit for this contract and stops asking you for a while.

`CarrierMethods` keeps the rule for you where it is easiest to break: a `Rates` that runs out of its 4 seconds
answers `unavailable`, not a timeout.

## 3. Writing a carrier

```go
func (p *MyCarrier) ServiceMethods() []nilda.ServiceMethod { return nilda.CarrierMethods(p) }

func (p *MyCarrier) DescribeCarrier(ctx context.Context) (nilda.CarrierDescription, error) { … } // settings only
func (p *MyCarrier) Rates(ctx context.Context, req nilda.RatesRequest) (nilda.RatesResult, error) { … }
func (p *MyCarrier) CreateLabel(ctx context.Context, req nilda.LabelRequest) (nilda.LabelResult, error) { … }
func (p *MyCarrier) VoidLabel(ctx context.Context, req nilda.VoidRequest) (nilda.VoidResult, error) { … }
// optional: Track (CarrierTracker), Documents (CarrierDocuments) — without them the answer is "unsupported"
```

```json
{ "capabilities": ["service_provider", "admin_page"], "provides": ["shipping.carrier/v1"] }
```

`_examples/carrier` is a whole one with a price table where the carrier would be — copy it and replace the four
places marked CARRIER. `CarrierMethods` checks every request before your method sees it and every answer before it
leaves (`Validate` on each type), so an answer the shop could not use — a rate of nothing, a currency ISO 4217 does
not price, two rates for one service, a purchased label with no tracking number — fails your own test, not a buyer's
checkout.

**Labels are bought once.** `LabelRequest.Key` is the idempotency key and the key you look a label up by at the
carrier: look it up FIRST and buy only when there is none, so a call whose answer was lost — Core repeats it, the
shop repeats it — never buys a second label. `pending` means the carrier is still making it; the shop asks again
with the same key.

**Documents** travel by `https` URL or as their bytes in base64 (`DocumentData`, `Bytes`), at most 1 MiB.

**Test or live.** Set `CarrierService.Test` while the plugin holds the carrier's test credentials; the shop stamps it
on every label and never ships one or tells a customer about it. `TestHint` says what test mode means, by language.

**Customs.** A route that needs a declaration is refused with `customs_missing` until the shop sends `Customs`
(an HS code and origin country per item). The incoterm is `DAP`: the receiver pays any duties.

## 4. Calling carriers — the shop

```go
answers, missed, err := core.API().Services().ShippingRates(ctx, req, nilda.ServiceCallOptions{})
label, err := core.API().Services().CreateShippingLabel(ctx, answers[0].Provider, labelReq) // labelReq.Key: the order line's
void, err := core.API().Services().VoidShippingLabel(ctx, provider, nilda.VoidRequest{ProviderRef: label.ProviderRef})
track, err := core.API().Services().TrackShipment(ctx, provider, nilda.TrackRequest{TrackingNumber: label.TrackingNumber})
docs, err := core.API().Services().ShippingDocuments(ctx, provider, nilda.DocumentsRequest{ProviderRef: label.ProviderRef})
```

A carrier that failed or ran out of time is in `missed`, never `err`; the shop shows the rates it has and its backup
rate when it has none. Never ask a carrier inside a database transaction.

## 5. Testing

`nildatest.NewStubCarrier(name, rates...)` is a carrier in memory: rates for its services, a label once per key,
void, tracking through `Events`, `Latency` and `Down` to make it slow or broken. Install it on
`nildatest.NewServices()` beside the plugin under test (`SERVICES.md` §6).
