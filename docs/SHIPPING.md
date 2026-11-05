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
| `service_points` | a buyer chose a rate that delivers to a service point (v0.58.0, EXPERIMENTAL) | 5 s; the SDK gives your `ServicePoints` 4 s | read |

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
// optional: Track (CarrierTracker), Documents (CarrierDocuments), ServicePoints (CarrierServicePoints) — without them
// the answer is "unsupported"
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

**Return labels, and paperless ones** (v0.58.0, EXPERIMENTAL). `LabelRequest.Return` asks for a label the buyer sends
back to the shop (the destination sends to the origin); its document is a `return_label`. Add `Paperless` to ask for one
the buyer needs no printer for, on a service whose `Supports` lists `paperless_returns` (`CarrierSupportPaperlessReturns`):
answer it with what the carrier gives —

- a **QR to scan** at the drop-off point: a document of kind `return_qr`, `png` or `pdf` (never `zpl`: the buyer shows it
  from a phone or a page). Shippo returns one as `qr_code_url` when the shipment asked `extra.qr_code_requested` (USPS
  domestic and Evri UK only); Sendcloud's labelless return (`delivery_option: drop_off_labelless`, an option whose
  `functionalities.labelless` is true) brings its QR as the parcel's label document. A carrier's link may be a signed
  one that lapses: send the bytes (`DocumentData`) when you can, and the shop keeps whatever it receives.
- a **code to show or read out**: `LabelResult.DropOffCode`, one plain line of at most 64 characters — the counter
  prints the label from it (ShipEngine calls it the `handoff_code`).

Either, both, or — when the carrier gives neither for this parcel — a `return_label` to print as without `Paperless`:
never a refusal. The tracking number and `provider_ref` are the same as any label's. A drop-off code or a `return_qr` on
a label not asked as a return, or a code on one not `purchased`, never leaves the carrier: `LabelResult.ValidateFor`
(run by `CarrierMethods` and by the shop's `CreateShippingLabel`) refuses it. The shop puts the code and the QR in the
return-label mail.

**Test or live.** Set `CarrierService.Test` while the plugin holds the carrier's test credentials; the shop stamps it
on every label and never ships one or tells a customer about it. `TestHint` says what test mode means, by language.

**Service points** (v0.58.0, EXPERIMENTAL; `shippingpoints.go`). A service that delivers to a parcel shop, a locker or
a post office the buyer chooses lists `service_points` (`CarrierSupportServicePoints`) — its own key beside the door
service, `standard_point` beside `standard` — and the carrier implements `CarrierServicePoints`:

```go
func (p *MyCarrier) ServicePoints(ctx context.Context, req nilda.ServicePointsRequest) (nilda.ServicePointsResult, error) { … }
```

1. **Rates.** The shop sets `RatesRequest.ServicePoints` when it can let the buyer choose a point; only then offer a point
   service's rate, marked `NeedsServicePoint`. A shop that does not set it is one built before points, and never sees
   one. `RatesRequest.ServicePoint` (a `ChosenServicePoint`) asks the price to a point already chosen.
2. **The search.** `ServicePointsRequest` names the point `Service`, the delivery address's `Country` and at least its
   `PostalCode` or `City` (`Line1` places it better), or a `Latitude`/`Longitude` — the place, never the buyer — and a
   `Limit` of 1 to 50 (`MaxServicePoints`; 0 is 10, `DefaultServicePoints`). Answer the nearest first, at most `Limit`:
   each `ServicePoint` with the `ID` you find it by again, its `Name`, `Carrier` (who runs it), `Kind` (`parcel_shop`,
   `locker`, `post_office`, `depot`, or none), `Address`, position, `DistanceMeters`, this week's `Hours`
   (`{"day","opens","closes"}`, `HH:MM`) and `RequiresPostNumber` (DHL's Postnummer for a Packstation). No points and no
   failure code is "nothing near" — an answer; `unavailable` is "could not look". Sendcloud: Service Points API v3 —
   `GET /service-points` with `country_code`, `carrier_code`, `address_postal_code`/`address_city` or
   `latitude`/`longitude`, `limit`; map `general_shop_type` `servicepoint` → `parcel_shop`, `carrier_depot` → `depot`,
   `requirements.post_number` `required` → `RequiresPostNumber` (sendcloud.dev/.openapi/v3/service-points/openapi.yaml,
   read 2026-11-05). Shippo's API has no point search (docs.goshippo.com/spec/shippoapi/public-api.yaml, read
   2026-11-05): plugin-shippo declares no point service.
3. **The label.** `LabelRequest.ServicePoint` carries the chosen point's `ID` and the buyer's `PostNumber`; `Destination`
   stays the buyer's own address. Sendcloud: `to_service_point.id` on the shipment and the post number in
   `to_address.po_box` (sendcloud.dev/.openapi/v3/shipments/openapi.yaml, `service-point-request`). A point you no longer
   know, or one that stopped taking parcels, is `rejected` with `invalid_service_point` (`CarrierInvalidServicePoint`):
   the shop asks the buyer to choose again.

`CarrierMethods` holds the rules: a describe listing a point service from a carrier that is not a
`CarrierServicePoints`, a point rate to a shop that did not ask for points or from a carrier that cannot list them, and a
points answer past its `Limit`, naming a point twice or beside a failure code, never leave the carrier; a label for a
point service with no point, or with a point on a door service, is answered `rejected`, `invalid_service_point`, without
your `CreateLabel` being asked (it reads your `DescribeCarrier` — settings, never a live call). A return label names no
point. A `ServicePoints` past its 4 seconds answers `unavailable`.

**Customs.** A route that needs a declaration is refused with `customs_missing` until the shop sends `Customs`
(an HS code and origin country per item). The incoterm is `DAP`: the receiver pays any duties.

## 4. Calling carriers — the shop

```go
answers, missed, err := core.API().Services().ShippingRates(ctx, req, nilda.ServiceCallOptions{})
label, err := core.API().Services().CreateShippingLabel(ctx, answers[0].Provider, labelReq) // labelReq.Key: the order line's
void, err := core.API().Services().VoidShippingLabel(ctx, provider, nilda.VoidRequest{ProviderRef: label.ProviderRef})
track, err := core.API().Services().TrackShipment(ctx, provider, nilda.TrackRequest{TrackingNumber: label.TrackingNumber})
docs, err := core.API().Services().ShippingDocuments(ctx, provider, nilda.DocumentsRequest{ProviderRef: label.ProviderRef})
points, err := core.API().Services().ShippingServicePoints(ctx, provider, nilda.ServicePointsRequest{Service: rate.Service,
	Country: "AT", PostalCode: "1010"}) // the carrier of the point rate the buyer chose — never every carrier
```

A carrier that failed or ran out of time is in `missed`, never `err`; the shop shows the rates it has and its backup
rate when it has none. Never ask a carrier inside a database transaction.

## 5. Testing

`nildatest.NewStubCarrier(name, rates...)` is a carrier in memory: rates for its services, a label once per key,
void, tracking through `Events`, `Latency` and `Down` to make it slow or broken, and `Paperless` to make its return labels
paperless when asked (a `DROP-…` code and a `return_qr`), and `Points` for its point services (a rate in its quotes with
`NeedsServicePoint`); `LabelsAsked` is every label request it saw. Install it on
`nildatest.NewServices()` beside the plugin under test (`SERVICES.md` §6).
