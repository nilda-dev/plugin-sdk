package nilda

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/jsonschema-go/jsonschema"
)

// THE CONTRACT FILES ARE GENERATED FROM THIS MODULE'S GO TYPES, and checked against them.
//
// A schema written by hand beside a Go type is a second copy that drifts: a field added to PaymentSession would
// reach Go plugins and be refused by Core's check against the file. So each contract is defined once below — its
// method table, and the Go type of each method's request and answer — and the file is what that definition says.
// After changing a type or a row: UPDATE_CONTRACTS=1 go test -run TestEveryContractFileIsItsGoDefinition .

// methodDef is one method: its row, and the Go types its schemas come from.
type methodDef struct {
	row     ContractMethod
	in, out any
}

type contractDef struct {
	name, description string
	methods           []methodDef
}

// ordinary is a request or an answer that carries nothing.
type ordinary struct{}

// paymentsV1 describes the payment hooks Core already sends, unchanged on the wire (owner decision D11: payments
// keeps its own module and state machine; only its timing and its "ask again" rule join the shared call table).
// Its methods are not tools: the owner's doors onto payments are Core's own payment tools.
var paymentsV1 = contractDef{
	name: "payments/v1",
	description: "Taking money: a gateway plugin describes its payment methods, starts and refunds payments and " +
		"reads a payment's state; the plugin that asked for the payment — the shop — is told what happened. Core " +
		"holds every session's state (PAYMENTS.md).",
	methods: []methodDef{
		{ContractMethod{Name: "describe", Hook: HookPaymentDescribe, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "failure", Idempotent: true,
			Description: "The payment methods this gateway offers now, for checkout to show."},
			ordinary{}, PaymentDescribeResponse{}},
		{ContractMethod{Name: "start", Hook: HookPaymentStart, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "failure", Idempotent: true,
			Description: "Open a payment at the processor for a session Core created; the session id is the idempotency key at the processor."},
			PaymentStartRequest{}, PaymentStartResult{}},
		{ContractMethod{Name: "refund", Hook: HookPaymentRefund, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "answer", Idempotent: true,
			Description: "Give money back for a refund Core recorded; the refund id is the idempotency key, and an error means its fate is unknown, so Core asks again."},
			PaymentRefundRequest{}, PaymentRefundResult{}},
		{ContractMethod{Name: "sync", Hook: HookPaymentSync, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "answer", Idempotent: true,
			Description: "Read a payment's state at the processor and report it, for a session whose news did not arrive."},
			PaymentSyncRequest{}, ordinary{}},
		{ContractMethod{Name: "session.confirm", Hook: HookPaymentConfirm, AnsweredBy: "consumer",
			RetryClass: "answer", Idempotent: true,
			Description: "Ask the plugin that wanted the payment whether it still wants it, before Core resolves it."},
			PaymentConfirmRequest{}, PaymentConfirmResult{}},
		{ContractMethod{Name: "session.updated", Hook: HookPaymentSessionUpdated, AnsweredBy: "consumer",
			RetryClass: "answer", Idempotent: true,
			Description: "Tell the plugin that wanted the payment its latest state; repeated until it returns nil."},
			PaymentSessionUpdate{}, ordinary{}},
		{ContractMethod{Name: "refund.updated", Hook: HookPaymentRefundUpdated, AnsweredBy: "consumer",
			RetryClass: "answer", Idempotent: true,
			Description: "Tell the plugin that asked for a refund its latest state; repeated until it returns nil."},
			PaymentRefundUpdate{}, ordinary{}},
		{ContractMethod{Name: "dispute.updated", Hook: HookPaymentDisputeUpdated, AnsweredBy: "consumer",
			RetryClass: "answer", Idempotent: true,
			Description: "Tell the plugin that took the payment about a dispute or chargeback on it; repeated until it returns nil."},
			PaymentDisputeUpdate{}, ordinary{}},
	},
}

// The owner's doors onto payments: Core's tools payments_list, payment_detail, payment_decide, payment_methods
// and payment_method_set. A gateway's hooks are Core-to-plugin plumbing beneath them.
const paymentsAgentReason = "the owner reaches payments through Core's own payment tools (payments_list, payment_detail, " +
	"payment_decide, payment_methods, payment_method_set); this hook is Core-to-plugin plumbing beneath them"

// shippingCarrierV1 is a carrier plugin's contract with the shop (SHIPPING.md): relayed by Core's service desk, every
// carrier asked at once. A carrier's "no" is a result, so every method's errors are failures and none is an answer.
var shippingCarrierV1 = contractDef{
	name: ContractShippingCarrier,
	description: "Shipping a parcel: a carrier plugin — an aggregator such as Sendcloud or Shippo, or one carrier's own " +
		"API, on the merchant's own account — describes its services, quotes rates, buys and voids labels, tracks " +
		"parcels and fetches a label's documents again. The shop keeps every record (SHIPPING.md).",
	methods: []methodDef{
		{ContractMethod{Name: CarrierDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "read",
			Description: "The carrier's services: where each delivers, what it can do, and whether the plugin holds test credentials."},
			ordinary{}, CarrierDescription{}},
		{ContractMethod{Name: CarrierRates, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "read",
			Description: "What sending these parcels from the origin to the destination costs with each service, and how long it takes."},
			RatesRequest{}, RatesResult{}},
		{ContractMethod{Name: CarrierLabelCreate, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopKeepsTheRecord,
			Description: "Buy a shipping label for one service on the merchant's carrier account; the request's key makes a repeat buy one label."},
			LabelRequest{}, LabelResult{}},
		{ContractMethod{Name: CarrierLabelVoid, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopKeepsTheRecord,
			Description: "Cancel a label that has not shipped, so the carrier does not charge for it."},
			VoidRequest{}, VoidResult{}},
		{ContractMethod{Name: CarrierTrack, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "skip", Idempotent: true, Agent: "read",
			Description: "Where a parcel is now, and every scan the carrier reported."},
			TrackRequest{}, TrackResult{}},
		{ContractMethod{Name: CarrierDocumentsGet, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none",
			AgentReason: "a label's documents are files for the merchant's printer; the shop's order screen and its " +
				"buy_shipping_label ability hand them over",
			Description: "A label's documents — the label, a customs invoice — fetched again from the carrier."},
			DocumentsRequest{}, DocumentsResult{}},
	},
}

// shopKeepsTheRecord is why a label is bought and voided only through the shop: a label the assistant bought straight
// from the carrier would be one no order knows about, billed to the merchant and shipped by nobody.
const shopKeepsTheRecord = "the shop keeps every label on its order, so its own abilities buy_shipping_label and " +
	"void_label are the doors; a label bought straight from the carrier would belong to no order"

// shopCatalogV1 is the shop's catalogue, read by channel plugins (CATALOG.md). One shop answers, so a failure blocks
// the call: a feed built without the catalogue would empty a channel.
var shopCatalogV1 = contractDef{
	name: ContractShopCatalog,
	description: "The shop's catalogue for a channel: a feed plugin, a marketplace, an AI agent's catalogue reads the " +
		"published products in a feed-grade shape — prices as numbers, availability per variant, brand, GTIN and MPN, " +
		"the page and the pictures — and what changed since it last asked (CATALOG.md).",
	methods: []methodDef{
		{ContractMethod{Name: CatalogDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: catalogAgentReason,
			Description: "The shop as a channel needs it: name, address, currency, whether prices include tax, where it ships, the return window."},
			ordinary{}, CatalogDescription{}},
		{ContractMethod{Name: CatalogProductsPage, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: catalogAgentReason,
			Description: "One page of the published products, oldest change first, each with its variants; with updated_since, only what changed — a product taken off sale is listed as removed."},
			CatalogPageRequest{}, CatalogPage{}},
		{ContractMethod{Name: CatalogProductGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: catalogAgentReason,
			Description: "One product by its SKU or one of its variants'."},
			CatalogProductRequest{}, CatalogProductResult{}},
	},
}

// catalogAgentReason: the owner reads and changes the catalogue through the shop's own abilities, which a feed's read
// would only repeat.
const catalogAgentReason = "the owner reads the catalogue through the shop's own abilities (list_products, " +
	"get_product, list_variants); this is how a channel plugin reads it"

// shopCheckoutV1 is the shop's checkout, driven by a protocol adapter (CHECKOUT.md). One shop answers, so a failure
// blocks: a checkout nobody answered cannot be reported to an agent as anything but a failure. Nothing is cached — a
// price or a stock level from earlier is not a checkout. Create, update and complete carry the adapter's idempotency
// key, so a retried request is one checkout and one order.
var shopCheckoutV1 = contractDef{
	name: ContractShopCheckout,
	description: "The shop's checkout for an agentic-commerce adapter (ACP, UCP): open a checkout for a basket, price " +
		"it for an address with the shop's own delivery, tax and discounts, then place the order with a delegated " +
		"payment credential where the merchant lets that agent pay, or hand it to a person by continue_url " +
		"(CHECKOUT.md).",
	methods: []methodDef{
		{ContractMethod{Name: CheckoutDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "The shop as an adapter announces it: name, address, currency, its legal pages, and the delegated payment credentials it can charge."},
			ordinary{}, CheckoutDescription{}},
		{ContractMethod{Name: CheckoutSessionCreate, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "Open a checkout for a new basket, with the buyer and the address when known; answers the checkout as the shop priced it, or why it refused."},
			CheckoutCreateRequest{}, CheckoutResult{}},
		{ContractMethod{Name: CheckoutSessionUpdate, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "Change a checkout — the basket, the buyer, the address, the delivery option, the discount code — and price it again."},
			CheckoutUpdateRequest{}, CheckoutResult{}},
		{ContractMethod{Name: CheckoutSessionGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "A checkout as it stands, or the order it became."},
			CheckoutRef{}, CheckoutResult{}},
		{ContractMethod{Name: CheckoutSessionComplete, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "Place the order, paid with the delegated credential when the merchant lets this agent pay; asked again, the same order."},
			CheckoutCompleteRequest{}, CheckoutResult{}},
		{ContractMethod{Name: CheckoutSessionCancel, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: checkoutAgentReason,
			Description: "End a checkout without an order."},
			CheckoutRef{}, CheckoutResult{}},
	},
}

// checkoutAgentReason: a checkout is a visitor's, never the owner's; a visitor's own agent has the shop's visitor
// tools, and the owner reads checkouts through the shop's abilities.
const checkoutAgentReason = "a checkout is a buyer's: a visitor's agent reaches it through the shop's visitor tools " +
	"(create_cart, quote, place_order), the owner through the shop's own abilities (list_saved_checkouts); this is " +
	"how a protocol adapter drives it"

// stockAlertsV1 is back-in-stock requests, kept by a plugin of their own (STOCK_ALERTS.md). Describe skips when nothing
// answers — the shop then shows no form; a subscribe nobody answered fails, so the shopper is told it did not go.
var stockAlertsV1 = contractDef{
	name: ContractStockAlerts,
	description: "Back-in-stock requests: the shop's product page hands a shopper's \"tell me when it is back\" for a " +
		"sold-out item to the plugin that keeps the requests, confirms them and sends the news when the item can be " +
		"bought again (STOCK_ALERTS.md).",
	methods: []methodDef{
		{ContractMethod{Name: StockAlertsDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: stockAlertsAgentReason,
			Description: "Whether requests are taken now, and the sentence the form shows beside the email box."},
			ordinary{}, StockAlertsDescription{}},
		{ContractMethod{Name: StockAlertSubscribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: stockAlertsAgentReason,
			Description: "Keep one shopper's request for one sold-out item, and confirm it to them; the same request again changes nothing."},
			StockAlertRequest{}, StockAlertResult{}},
	},
}

// stockAlertsAgentReason: a request is a shopper's, sent from the product page; the owner reads and removes them
// through the stock-alerts plugin's own abilities.
const stockAlertsAgentReason = "a back-in-stock request is a shopper's, sent from the shop's product page; the owner " +
	"reads and removes them through the stock-alerts plugin's own abilities"

// shopOrdersV1 is the shop's order reads, for a plugin that delivers what an order bought (ORDERS.md). One shop
// answers; a read nobody answered fails, and the plugin tries again at its next pass.
var shopOrdersV1 = contractDef{
	name: ContractShopOrders,
	description: "The shop's orders, read by a plugin that delivers what an order bought — a licence key, a code, a " +
		"ticket: the lines, the buyer's email and language, and whether it is paid (ORDERS.md).",
	methods: []methodDef{
		{ContractMethod{Name: OrderGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: shopOrdersAgentReason,
			Description: "One order by its id: reference, status, buyer email and language, currency, and each line's SKU, title, quantity and quantity refunded."},
			ShopOrderRequest{}, ShopOrderResult{}},
	},
}

// shopOrdersAgentReason: the owner reads orders through the shop's own abilities.
const shopOrdersAgentReason = "the owner reads orders through the shop's own abilities (list_orders, get_order); this " +
	"is how a delivering plugin reads the order it was told was paid"

// shopDeliverablesV1 is what plugins deliver for the shop's orders (ORDERS.md). Several plugins may answer; one that
// does not is left out of a page (skip), and the shop's checkout treats a describe nobody could answer as an error.
var shopDeliverablesV1 = contractDef{
	name: ContractShopDeliverables,
	description: "What a plugin delivers for the shop's orders — a licence key, a code, a ticket — for the shop to show " +
		"the buyer on its thank-you page and in their account, and the SKUs it delivers, which the shop sells as " +
		"digital content (ORDERS.md).",
	methods: []methodDef{
		{ContractMethod{Name: DeliverablesDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: deliverablesAgentReason,
			Description: "The SKUs this plugin delivers, which the shop sells as digital content."},
			ordinary{}, DeliverablesDescription{}},
		{ContractMethod{Name: DeliverablesOrderItems, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: deliverablesAgentReason,
			Description: "What this plugin delivered for one order, for the buyer to read."},
			DeliverablesRequest{}, DeliverablesResult{}},
	},
}

// deliverablesAgentReason: a delivered key is the buyer's; the owner reads and resends them through the delivering
// plugin's own abilities.
const deliverablesAgentReason = "what was delivered is shown to its buyer; the owner reads, resends and disables it " +
	"through the delivering plugin's own abilities"

// shopOffersV1 is the owner's offers at the cart, the checkout and after the purchase (OFFERS.md). Several plugins may
// answer; one that does not is left out (an offer is a suggestion), and an offer it does not confirm gives no discount.
var shopOffersV1 = contractDef{
	name: ContractShopOffers,
	description: "Offers at the cart, the checkout (an order bump) and after the purchase: which to show for a basket, " +
		"and which offers already on basket lines still stand, for the shop to price them through its own " +
		"discounts (OFFERS.md).",
	methods: []methodDef{
		{ContractMethod{Name: OffersFor, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: offersAgentReason,
			Description: "The offers to show at one stage for one basket or the order just paid, the first first."},
			OffersRequest{}, OffersResult{}},
		{ContractMethod{Name: OffersConfirm, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: offersAgentReason,
			Description: "Which offers on the basket's lines still stand, with their discount now."},
			OffersConfirmRequest{}, OffersConfirmResult{}},
	},
}

// offersAgentReason: an offer is shown to a shopper; the owner sets offers through the offers plugin's own abilities.
const offersAgentReason = "an offer is shown to a shopper on the shop's pages; the owner sets and reads offers " +
	"through the offers plugin's own abilities"

// contractDefs is every contract this module defines.
var contractDefs = []contractDef{paymentsV1, shippingCarrierV1, shopCatalogV1, shopCheckoutV1, stockAlertsV1, shopOrdersV1,
	shopDeliverablesV1, shopOffersV1}

// render builds a contract file from its definition.
func (d contractDef) render(t *testing.T) []byte {
	t.Helper()
	c := ServiceContract{Name: d.name, Description: d.description}
	for _, md := range d.methods {
		m := md.row
		if m.Agent == "" {
			m.Agent, m.AgentReason = "none", paymentsAgentReason
		}
		m.Input, m.Output = schemaOf(t, md.in), schemaOf(t, md.out)
		c.Methods = append(c.Methods, m)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func schemaOf(t *testing.T, v any) json.RawMessage {
	t.Helper()
	s, err := jsonschema.ForType(reflect.TypeOf(v), nil)
	if err != nil {
		t.Fatalf("inferring the schema of %T: %v", v, err)
	}
	openObjects(s)
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// openObjects undoes the one thing inference gets wrong for a contract: it closes every struct's object
// (additionalProperties: false), and a contract only grows within its major version — a field added on one side must
// not be refused by the other. A map's additionalProperties is its value schema, and stays.
func openObjects(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if a := s.AdditionalProperties; a != nil && a.Not != nil && reflect.DeepEqual(*a.Not, jsonschema.Schema{}) {
		s.AdditionalProperties = nil
	}
	openObjects(s.AdditionalProperties)
	openObjects(s.Items)
	for _, p := range s.Properties {
		openObjects(p)
	}
	for _, list := range [][]*jsonschema.Schema{s.AnyOf, s.OneOf, s.AllOf, s.PrefixItems} {
		for _, c := range list {
			openObjects(c)
		}
	}
}

func contractPath(name string) string {
	return filepath.Join(append([]string{"contracts"}, append(strings.Split(name, "/"), "contract.json")...)...)
}

// The files on disk are exactly what the Go definitions say — and no file exists that no definition makes.
func TestEveryContractFileIsItsGoDefinition(t *testing.T) {
	want := map[string]bool{}
	for _, d := range contractDefs {
		p := contractPath(d.name)
		want[filepath.ToSlash(p)] = true
		got := d.render(t)
		if os.Getenv("UPDATE_CONTRACTS") == "1" {
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("%s: %v — generate it with UPDATE_CONTRACTS=1 go test -run TestEveryContractFileIsItsGoDefinition .", p, err)
		}
		if !bytes.Equal(have, got) {
			t.Errorf("%s is not what its Go definition says — a type or a row changed. Regenerate with "+
				"UPDATE_CONTRACTS=1 go test -run TestEveryContractFileIsItsGoDefinition . and read the diff: it is what "+
				"Core will check every plugin against", p)
		}
	}
	err := filepath.WalkDir("contracts", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !want[filepath.ToSlash(p)] {
			t.Errorf("%s is in contracts/ and no Go definition makes it", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// PC3: every file this module ships loads, and every schema in it compiles.
func TestEveryContractFileLoadsAndItsSchemasCompile(t *testing.T) {
	all, err := Contracts()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != len(contractDefs) {
		t.Fatalf("Contracts() loaded %d, the module defines %d", len(all), len(contractDefs))
	}
	for _, c := range all {
		for _, m := range c.Methods {
			if m.in == nil || m.out == nil {
				t.Errorf("%s %s: a schema did not compile", c.Name, m.Name)
			}
		}
	}
	pay, ok := ContractNamed("payments/v1")
	if !ok {
		t.Fatal("ContractNamed(payments/v1) found nothing")
	}
	start, _ := pay.Method("start")
	// What Core sends: the whole session, every field the Go type does not mark omitempty.
	req, err := json.Marshal(PaymentStartRequest{Session: PaymentSession{ID: "s1", AmountMinor: 900, Currency: "EUR"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := start.ValidateInput(req); err != nil {
		t.Errorf("a start request was refused: %v", err)
	}
	// A field a newer Core adds is not refused by an older contract: within a major version it only grows.
	grown := bytes.Replace(req, []byte(`{"session":{`), []byte(`{"added_later":true,"session":{"added_later":1,`), 1)
	if err := start.ValidateInput(grown); err != nil {
		t.Errorf("a request with a field added later was refused: %v", err)
	}
	if err := start.ValidateInput([]byte(`{"session":{"amount_minor":"nine"}}`)); err == nil {
		t.Error("a start request with a text amount passed its schema")
	}
	if err := start.ValidateOutput([]byte(`[]`)); err == nil {
		t.Error("an answer that is not an object passed its schema")
	}
}

// PC3: the payments contract names exactly the hooks Core sends for payments — Core's own constants, read from its
// source — so the contract cannot describe a hook Core never sends or leave one out.
func TestThePaymentsContractNamesExactlyTheHooksCoreSends(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "payments.go")
	coreHooks := map[string]bool{}
	for _, m := range regexp.MustCompile(`HookPayment\w+\s*=\s*"([a-z.]+)"`).FindAllStringSubmatch(src, -1) {
		coreHooks[m[1]] = true
	}
	if len(coreHooks) == 0 {
		t.Fatal("found no HookPayment constants in core's payments.go — repoint this guard")
	}
	pay, ok := ContractNamed("payments/v1")
	if !ok {
		t.Fatal("no payments/v1")
	}
	inFile := map[string]bool{}
	for _, m := range pay.Methods {
		inFile[m.Hook] = true
		if !coreHooks[m.Hook] {
			t.Errorf("payments/v1 %s rides %q, which Core does not send", m.Name, m.Hook)
		}
	}
	var missing []string
	for h := range coreHooks {
		if !inFile[h] {
			missing = append(missing, h)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("Core sends %v and payments/v1 does not describe them", missing)
	}
}

// The contract's timing and answer rule for each hook are Core's call table's (calltable.go) — the two must say the
// same until Core reads them from this file.
func TestThePaymentsContractTimesEachHookAsCoreDoes(t *testing.T) {
	calls := strings.Join(strings.Fields(coreSource(t, "internal", "plugin", "calltable.go")), " ")
	rows := map[string]string{
		"HookPaymentDescribe":       "gatewayCall",
		"HookPaymentStart":          "gatewayCall",
		"HookPaymentRefund":         "{budget: PaymentHookBudget, errorIsAnswer: true}",
		"HookPaymentSync":           "{budget: PaymentHookBudget, errorIsAnswer: true}",
		"HookPaymentConfirm":        "answersAgain",
		"HookPaymentSessionUpdated": "answersAgain",
		"HookPaymentRefundUpdated":  "answersAgain",
		"HookPaymentDisputeUpdated": "answersAgain",
	}
	for name, row := range rows {
		if !strings.Contains(calls, name+": "+row+",") {
			t.Errorf("core's call table no longer has %s: %s — re-read payments/v1's budget_ms and retry_class", name, row)
		}
	}
	for _, md := range paymentsV1.methods {
		m := md.row
		budget, answer := m.BudgetMS == 15000, m.RetryClass == "answer"
		switch m.Hook {
		case HookPaymentDescribe, HookPaymentStart:
			if !budget || answer {
				t.Errorf("%s: want 15 s and errors as failures, as gatewayCall", m.Name)
			}
		case HookPaymentRefund, HookPaymentSync:
			if !budget || !answer {
				t.Errorf("%s: want 15 s and errors as answers", m.Name)
			}
		default:
			if m.BudgetMS != 0 || !answer {
				t.Errorf("%s: want the ordinary budget and errors as answers, as answersAgain", m.Name)
			}
		}
	}
}

// PC3: a method that does not say how the assistant may use it is refused — nothing without a door (D8) — and so is
// one kept from the assistant without saying which door the owner has instead.
func TestAContractWithAMethodWithoutAnAgentClassIsRefused(t *testing.T) {
	file := func(method string) fstest.MapFS {
		return fstest.MapFS{"contracts/demo/v1/contract.json": {Data: []byte(`{"name":"demo/v1","description":"d",` +
			`"methods":[` + method + `]}`)}}
	}
	const schemas = `"input":{"type":"object"},"output":{"type":"object"}`
	for _, tc := range []struct{ name, method, want string }{
		{"no agent class", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip",` + schemas + `}`,
			"every method says how the assistant may use it"},
		{"none without a reason", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"none",` + schemas + `}`,
			"which door the owner has instead"},
		{"relayed without a fallback", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","agent":"read",` + schemas + `}`,
			"needs fallback"},
		{"a hook with a fallback", `{"name":"go","hook":"x.y","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"read",` + schemas + `}`,
			"whose owner handles failure"},
		{"a budget past 15 s", `{"name":"go","answered_by":"provider","description":"d","budget_ms":20000,"retry_class":"failure","fallback":"skip","agent":"read",` + schemas + `}`,
			"budget_ms 20000"},
		{"an answer that is not an object", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"read","input":{"type":"object"},"output":{"type":"array"}}`,
			"every request and answer is a JSON object"},
		{"a closed object", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"read","input":{"type":"object","properties":{"a":{"type":"object","additionalProperties":false}}},"output":{"type":"object"}}`,
			"within a major version a contract only grows"},
		{"an unknown field", `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"read","retry":3,` + schemas + `}`,
			"unknown field"},
	} {
		_, err := loadContracts(file(tc.method))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want an error saying %q", tc.name, err, tc.want)
		}
	}
	ok := `{"name":"go","answered_by":"provider","description":"d","retry_class":"failure","fallback":"skip","agent":"read",` + schemas + `}`
	if _, err := loadContracts(file(ok)); err != nil {
		t.Errorf("a well-formed method was refused: %v", err)
	}
	if _, err := loadContracts(fstest.MapFS{"contracts/demo/v2/contract.json": {Data: []byte(`{"name":"demo/v1",` +
		`"description":"d","methods":[` + ok + `]}`)}}); err == nil || !strings.Contains(err.Error(), "its directory says") {
		t.Errorf("a file whose name and directory disagree: %v", err)
	}
}

// A []byte in a contract type is a schema that refuses its own JSON: inference writes "an array of integers 0–255",
// and encoding/json sends a base64 string. The first carrier label carried its PDF that way and every answer was
// dropped as invalid (DocumentRef.Data is a base64 string for this reason).
func TestNoContractTypeCarriesBytes(t *testing.T) {
	var walk func(path string, typ reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, typ reflect.Type, seen map[reflect.Type]bool) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			if typ.Elem().Kind() == reflect.Uint8 && typ != reflect.TypeOf(json.RawMessage{}) {
				t.Errorf("%s is %s: its schema would refuse the base64 JSON sends — carry it as a base64 string", path, typ)
				return
			}
			walk(path+"[]", typ.Elem(), seen)
		case reflect.Map:
			walk(path+"{}", typ.Elem(), seen)
		case reflect.Struct:
			if seen[typ] {
				return
			}
			seen[typ] = true
			for i := 0; i < typ.NumField(); i++ {
				if f := typ.Field(i); f.IsExported() {
					walk(path+"."+f.Name, f.Type, seen)
				}
			}
		}
	}
	for _, d := range contractDefs {
		for _, m := range d.methods {
			walk(d.name+" "+m.row.Name+" input", reflect.TypeOf(m.in), map[reflect.Type]bool{})
			walk(d.name+" "+m.row.Name+" output", reflect.TypeOf(m.out), map[reflect.Type]bool{})
		}
	}
}
