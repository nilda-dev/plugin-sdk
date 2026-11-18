package nilda

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
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
	status            string
	methods           []methodDef
	// refineIn finishes a method's input schema, by the method's name, with what no one Go type says (shop.import's
	// records.put: each kind's data checked by its kind).
	refineIn map[string]func(t *testing.T, s *jsonschema.Schema)
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
		{ContractMethod{Name: "saved.forget", Hook: HookPaymentForget, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "answer", Idempotent: true,
			Description: "Let go of a kept payment method at the processor; an error means it may still be kept, so Core asks again."},
			PaymentForgetRequest{}, ordinary{}},
		{ContractMethod{Name: "capture", Hook: HookPaymentCapture, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "answer", Idempotent: true,
			Description: "Take money the processor holds for a session, once, under the key Core gives; an error means its fate is unknown, so Core asks again with the same key."},
			PaymentCaptureRequest{}, PaymentCaptureResult{}},
		{ContractMethod{Name: "void", Hook: HookPaymentVoid, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "answer", Idempotent: true,
			Description: "Let go of money the processor holds for a session, under the key Core gives; an error means it may still be held, so Core asks again."},
			PaymentVoidRequest{}, PaymentVoidResult{}},
		{ContractMethod{Name: "reprice", Hook: HookPaymentReprice, AnsweredBy: "provider", BudgetMS: 15000,
			RetryClass: "failure", Idempotent: true,
			Description: "EXPERIMENTAL. Change the amount of a session's processor order before its payer approves (express buttons: the wallet's address or delivery changed the total); a refusal is an answer, an error changed nothing."},
			PaymentRepriceRequest{}, PaymentRepriceResult{}},
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
		{ContractMethod{Name: "saved.updated", Hook: HookPaymentSavedUpdated, AnsweredBy: "consumer",
			RetryClass: "answer", Idempotent: true,
			Description: "EXPERIMENTAL (v0.69.0). Tell the plugin that kept a payment method that its processor changed the method's details by itself — a card network's account updater gave the card a new expiry or number; repeated until it returns nil. Only a consumer implementing SavedMethodWatcher is sent it."},
			PaymentSavedUpdate{}, ordinary{}},
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
		"parcels, fetches a label's documents again and lists the service points it delivers to. The shop keeps " +
		"every record (SHIPPING.md).",
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
		{ContractMethod{Name: CarrierServicePointsGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "read",
			Description: "EXPERIMENTAL. The service points — parcel shops, lockers, post offices — one point service delivers to near a place, nearest first and bounded, for the buyer to choose one."},
			ServicePointsRequest{}, ServicePointsResult{}},
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

// contractDefs is every contract this module defines.
var contractDefs = []contractDef{paymentsV1, shippingCarrierV1, shopCatalogV1, shopOrdersV1,
	shopDeliverablesV1, siteReaderV1, shopAttributesV1, shopAttributeValuesV1, shopEventsV1, shopRewardsV1, shopSubscriptionsV1,
	shopAccountSectionsV1, shopImportV1}

// shopImportV1 is door D10 (SHOP_IMPORT.md): an import plugin writes an old shop into this one, in the shop's neutral
// records. One shop answers, so a failure blocks; records.put is an import call. Experimental until its first plugin ships.
var shopImportV1 = contractDef{
	name:   ContractShopImport,
	status: ContractExperimental,
	description: "An old shop written into this one by an import plugin (SHOP_IMPORT.md): the plugin reads WooCommerce, later " +
		"Shopify or Wix, and sends the shop's own NEUTRAL records one kind a batch — categories, tags, attributes, products, " +
		"variants, links, reviews, coupons; the shop writes drafts only, quietly, keyed by where each came from so a batch sent " +
		"again changes nothing, converts money only at the owner's rate, holds what cannot be published yet, and later publishes " +
		"the import on the owner's act and takes that act back.",
	methods: []methodDef{
		{ContractMethod{Name: ShopImportSettingsAnswer, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason,
			Description: "EXPERIMENTAL. What the shop would do with an old shop's settings: the money decision (same, adopt, convert, no_prices, or ask when the currencies differ and the owner has not answered), the ECB's rate for the pair, whether the two shops' prices_include_tax differ, this shop's currency and whether it is empty."},
			ShopImportSettingsRequest{}, ShopImportSettingsResult{}},
		{ContractMethod{Name: ShopImportRecordsPut, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason, Import: true,
			Description: "EXPERIMENTAL. One batch of ONE kind of an old shop's records, an import call: drafts only, no mail, webhook, notice or catalogue event; keyed by source, kind and source id, so a batch sent again answers already (the same record and money), kept (changed here since: never overwritten) or updated. apply false is the preview: nothing is written. Each record's answer names what was made, why it is held, and what it lost."},
			ShopImportBatch{}, ShopImportAnswer{}},
		{ContractMethod{Name: ShopImportRecordsList, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "read",
			Description: "EXPERIMENTAL. What imports brought into the shop, held, paired with the owner's products and converted, with the old prices as they were read, a page at a time."},
			ShopImportListRequest{}, ShopImportListAnswer{}},
		{ContractMethod{Name: ShopImportPublishPlan, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason,
			Description: "EXPERIMENTAL. What publishing an old shop's import would do now: its counts, its groups (converted products, used once-per-customer codes, SKU pairs, sales starting within 30 days) and the shop's settings it would replace."},
			ShopImportActRequest{}, ShopImportPlan{}},
		{ContractMethod{Name: ShopImportPublishPrices, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason,
			Description: "EXPERIMENTAL. A page of the converted products' old and new prices, for the owner to check before the act."},
			ShopImportActRequest{}, ShopImportPrices{}},
		{ContractMethod{Name: ShopImportPublishApply, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason,
			Description: "EXPERIMENTAL. The owner's act, one page at a time under the act's marker: publishes what was public at the old shop and is not held, with the owner's choices; a page asked again changes nothing."},
			ShopImportApplyRequest{}, ShopImportActPage{}},
		{ContractMethod{Name: ShopImportPublishUndo, AnsweredBy: "provider", BudgetMS: 15000, RetryClass: "failure",
			Fallback: "block", Idempotent: true, Agent: "none", AgentReason: shopImportAgentReason,
			Description: "EXPERIMENTAL. Takes an act back, one page at a time, from what each record held before it."},
			ShopImportUndoRequest{}, ShopImportActPage{}},
	},
	refineIn: map[string]func(*testing.T, *jsonschema.Schema){ShopImportRecordsPut: shopImportDataByKind},
}

const shopImportAgentReason = "the import plugin's own abilities are the door: they keep the job, preview first and ask the " +
	"owner before the act"

// shopImportData is each kind's data type.
var shopImportData = map[string]any{
	ShopImportCategory: ShopImportCategoryData{}, ShopImportTag: ShopImportTagData{}, ShopImportAttribute: ShopImportAttributeData{},
	ShopImportProduct: ShopImportProductData{}, ShopImportVariant: ShopImportVariantData{}, ShopImportLinks: ShopImportLinksData{},
	ShopImportReview: ShopImportReviewData{}, ShopImportCoupon: ShopImportCouponData{},
}

// shopImportDataByKind holds each record's data to its batch's kind: if kind is K, every record's data is K's.
func shopImportDataByKind(t *testing.T, s *jsonschema.Schema) {
	t.Helper()
	for _, kind := range ShopImportKinds() {
		data, ok := shopImportData[kind]
		if !ok {
			t.Fatalf("the kind %s has no data type", kind)
		}
		var k any = kind
		s.AllOf = append(s.AllOf, &jsonschema.Schema{
			If: &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"kind": {Const: &k}}, Required: []string{"kind"}},
			Then: &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"records": {Items: &jsonschema.Schema{
				Properties: map[string]*jsonschema.Schema{"data": schemaTree(t, data)}}}}},
		})
	}
}

// shopAccountSectionsV1 is another plugin's section on the buyer's account page (SHOP_ACCOUNT_SECTIONS.md): the plugin
// describes it, the shop draws it in its menu and mounts the plugin's own route, which Core's proxy tells who is signed in.
var shopAccountSectionsV1 = contractDef{
	name:   ContractShopAccountSections,
	status: ContractExperimental,
	description: "A plugin's sections on the buyer's account page (SHOP_ACCOUNT_SECTIONS.md): the plugin describes each — a key, " +
		"the menu's words per language and its own route under its route_prefix; the shop puts it in its account menu where the " +
		"owner chose and mounts that route, which the buyer's browser asks through Core's proxy, so the section answers only " +
		"for the account Core's session names. No request carries a user id.",
	methods: []methodDef{
		{ContractMethod{Name: AccountSectionsDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "cached",
			Idempotent: true, Agent: "none",
			AgentReason: "the owner turns a section on or off and orders it through the shop's own abilities (list_account_sections, " +
				"set_account_section) and reads what a buyer's section shows through the providing plugin's own ability; this is " +
				"how the shop asks a plugin which sections it has",
			Description: "EXPERIMENTAL. This plugin's sections on the buyer's account page: each one's key, the menu's words per " +
				"language (English required), the plugin's own route that draws it for the signed-in buyer (under its route_prefix, " +
				"no query) and a position hint. At most 4. The request names the site's published account page, so the plugin's mailed links open it at the section."},
			AccountSectionsRequest{}, AccountSectionsAnswer{}},
	},
}

// shopRewardsV1 is the shop's door for what a rewards plugin gives a buyer (SHOP_REWARDS.md): store credit and single-use
// codes, as write commands. One shop answers, so a failure blocks. Experimental until its first plugin ships on it.
var shopRewardsV1 = contractDef{
	name:   ContractShopRewards,
	status: ContractExperimental,
	description: "What a rewards plugin gives a buyer, honoured by the shop (SHOP_REWARDS.md): store credit on their account and " +
		"single-use discount codes only they can use — write commands, keyed, kept with the plugin that issued them, announced " +
		"once committed, and taken back while nothing depends on them. The plugin never prices a basket or keeps money.",
	methods: []methodDef{
		{ContractMethod{Name: RewardsCreditIssue, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: rewardsAgentReason,
			Write:       &ContractWrite{Record: RewardCreditRecord, Undo: RewardsCreditUndo, Emits: EventStoreCreditIssued},
			Description: "EXPERIMENTAL. Put store credit on one buyer's account (Core's account id), in the shop's currency, with the reason the owner and the buyer read; the same idempotency key answers the first credit with already: true. A refusal is the answer's refused code."},
			RewardCreditRequest{}, RewardWriteAnswer{}},
		{ContractMethod{Name: RewardsCreditUndo, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: rewardsAgentReason,
			Write:       &ContractWrite{Record: RewardCreditRecord, Emits: EventStoreCreditUndone},
			Description: "EXPERIMENTAL. Take back a credit this plugin issued. Refused spent when the buyer's balance no longer holds it."},
			UndoInput{}, RewardWriteAnswer{}},
		{ContractMethod{Name: RewardsDiscountIssue, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: rewardsAgentReason,
			Write:       &ContractWrite{Record: RewardDiscountRecord, Undo: RewardsDiscountUndo, Emits: EventRewardDiscountIssued},
			Description: "EXPERIMENTAL. Make one single-use discount code through the shop's discount engine, usable once and only by the named signed-in buyer: a fixed amount or a percentage of the goods, with its title, an optional expiry and minimum. The answer carries the code."},
			RewardDiscountRequest{}, RewardWriteAnswer{}},
		{ContractMethod{Name: RewardsDiscountUndo, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: rewardsAgentReason,
			Write:       &ContractWrite{Record: RewardDiscountRecord, Emits: EventRewardDiscountUndone},
			Description: "EXPERIMENTAL. Take back a code this plugin issued: it goes to the site's recycle bin. Refused used once an order used it."},
			UndoInput{}, RewardWriteAnswer{}},
		{ContractMethod{Name: RewardsIssuedGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: rewardsAgentReason,
			Description: "EXPERIMENTAL. What one record this plugin issued is now: open, used (and by which order), expired, binned (in the site's recycle bin until purges_at: still the buyer's) or undone."},
			RewardIssuedRequest{}, RewardIssuedAnswer{}},
	},
}

const rewardsAgentReason = "the owner reads and takes back store credit and discount codes through the shop's own abilities " +
	"(customer_store_credit, list_discounts, delete_discount), each saying which plugin issued it; this is how the plugin " +
	"that owns the points spends them"

// shopSubscriptionsV1 is doors D3 and D9 (SHOP_SUBSCRIPTIONS.md): plans as the shop's selling plans, the agreements its
// checkout keeps, and one renewal order per cycle through the shop's own checkout. Experimental until its first plugin ships.
var shopSubscriptionsV1 = contractDef{
	name:   ContractShopSubscriptions,
	status: ContractExperimental,
	description: "Subscriptions sold and renewed by the shop (SHOP_SUBSCRIPTIONS.md): a plugin writes its plans as the shop's " +
		"selling plans; the shop sells them, prices the plan's discount, keeps the buyer's method under a subscription mandate " +
		"with Core and keeps the agreement at the price agreed; the plugin asks for each renewal, which the shop places through " +
		"its own checkout and charges off-session — one order per agreement and cycle. Refunds only through the shop's refund_lines.",
	methods: []methodDef{
		{ContractMethod{Name: SubscriptionPlanSet, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionPlanRecord, Undo: SubscriptionPlanUndo, Emits: EventSellingPlanSet},
			Description: "EXPERIMENTAL. A new subscription plan — name, the SKUs it is offered on, its rhythm and its pricing — as one of the shop's selling plans. A plan is never edited: a change is a new plan and plan.undo of the old, so every agreement keeps the terms it was sold on."},
			SubscriptionPlanRequest{}, SubscriptionWriteAnswer{}},
		{ContractMethod{Name: SubscriptionPlanUndo, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionPlanRecord, Emits: EventSellingPlanUndone},
			Description: "EXPERIMENTAL. Stop selling a plan this plugin wrote: it leaves the product pages and baskets; agreements on it keep their terms."},
			UndoInput{}, SubscriptionWriteAnswer{}},
		{ContractMethod{Name: SubscriptionAgreementGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Description: "EXPERIMENTAL. One of this plugin's agreements: its lines at the locked price, rhythm, buyer, the mandate's words and limit, the kept method's label (never its id), and its cycles' orders."},
			SubscriptionAgreementRequest{}, SubscriptionAgreementAnswer{}},
		{ContractMethod{Name: SubscriptionAgreementList, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Description: "EXPERIMENTAL. This plugin's agreements, oldest first, after an id, at most 100 a page, optionally of one status."},
			SubscriptionAgreementsRequest{}, SubscriptionAgreementsAnswer{}},
		{ContractMethod{Name: SubscriptionRenewalPlace, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionRenewalRecord, Undo: SubscriptionRenewalUndo, Emits: EventRenewalPlaced},
			Description: "EXPERIMENTAL. Place one cycle's order through the shop's checkout at the agreement's locked price and the plan's discount, with today's tax, and charge the kept method off-session, never above the mandate's limit. One order per agreement and cycle: asked again for an unpaid cycle, the same order is charged again; a paid one is refused cycle_paid."},
			SubscriptionRenewalRequest{}, SubscriptionRenewalAnswer{}},
		{ContractMethod{Name: SubscriptionRenewalQuote, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Description: "EXPERIMENTAL. What a cycle would cost if renewal.place placed it now — goods at the locked price less the plan's discount, today's tax, the delivery — for the reminder before a renewal charge; or the refusal renewal.place would answer. Nothing is placed, held or charged."},
			SubscriptionQuoteRequest{}, SubscriptionQuoteAnswer{}},
		{ContractMethod{Name: SubscriptionRenewalUndo, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionRenewalRecord, Emits: EventRenewalUndone},
			Description: "EXPERIMENTAL. Cancel an unpaid cycle's order (the retries gave up): its goods go back to stock. Refused paid — only the owner refunds, through refund_lines."},
			UndoInput{}, SubscriptionWriteAnswer{}},
		{ContractMethod{Name: SubscriptionAgreementStop, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionAgreementRecord, Undo: SubscriptionAgreementResume, Emits: EventSubscriptionStopped},
			Description: "EXPERIMENTAL. Stop an agreement: no renewal is placed from now on and an unpaid cycle's order is cancelled; the kept method is forgotten at ends_at (now when absent), and until then agreement.resume takes the stop back."},
			SubscriptionStopRequest{}, SubscriptionWriteAnswer{}},
		{ContractMethod{Name: SubscriptionAgreementResume, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Write:       &ContractWrite{Record: SubscriptionAgreementRecord, Emits: EventSubscriptionResumed},
			Description: "EXPERIMENTAL. Take back this plugin's stop of an agreement while its method is still kept; refused method_forgotten after — a new agreement needs the buyer present."},
			UndoInput{}, SubscriptionWriteAnswer{}},
		{ContractMethod{Name: SubscriptionRenewalPayLink, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Description: "EXPERIMENTAL (v0.69.0, a shop implementing SubscriptionLinksProvider). A signed, expiring link for the buyer to pay one cycle's refused renewal themselves on the shop's own pay page — the order's own page, through the agreement's method, holding the same one payment per cycle as the off-session charge. Refused no_renewal, cycle_paid, renewal_closed or charging. Nothing is charged by asking."},
			SubscriptionPayLinkRequest{}, SubscriptionLinkAnswer{}},
		{ContractMethod{Name: SubscriptionMethodLink, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: subscriptionsAgentReason,
			Description: "EXPERIMENTAL (v0.69.0, a shop implementing SubscriptionLinksProvider). A signed, expiring link where the signed-in buyer of an agreement gives another payment method — kept by Core on the processor's own page under an update mandate — and the agreement renews on it (ecommerce.subscription.method_updated, source buyer). Refused agreement_stopped or no_account (a guest's agreement has nobody to sign in as)."},
			SubscriptionMethodLinkRequest{}, SubscriptionLinkAnswer{}},
	},
}

const subscriptionsAgentReason = "the owner reads agreements and renewal orders through the shop's own abilities (get_order, " +
	"list_subscriptions) and stops or changes a subscription through the subscriptions plugin's abilities; this is how that " +
	"plugin asks the shop, under its own key"

// shopEventsV1 is door D1 (SHOP_EVENTS.md): the shop's order events and the read a subscriber catches up with. One shop
// answers, so a failure blocks. Experimental until its first plugin ships on it.
var shopEventsV1 = contractDef{
	name:   ContractShopEvents,
	status: ContractExperimental,
	description: "The shop's order events — placed, paid, cancelled, refunded line by line, fulfilled — each with the shop's " +
		"event_id and sequence, the order's money net of tax in its currency, and the visitor's consent copied when the order " +
		"was placed; and the read a subscriber catches up with, after the last sequence it handled (SHOP_EVENTS.md).",
	methods: []methodDef{
		{ContractMethod{Name: ShopEventsRead, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: shopEventsAgentReason,
			Description: "EXPERIMENTAL. The shop's order events after a sequence, oldest first, at most 100 a page; more says another page follows. Each event is the same data its delivery carried."},
			ShopEventsRequest{}, ShopEventsAnswer{}},
	},
}

const shopEventsAgentReason = "the owner reads orders, refunds and shipments through the shop's own abilities (list_orders, " +
	"get_order); this is how a measuring plugin catches up on what it was told"

// render builds a contract file from its definition.
func (d contractDef) render(t *testing.T) []byte {
	t.Helper()
	c := ServiceContract{Name: d.name, Description: d.description, Status: d.status}
	for _, md := range d.methods {
		m := md.row
		if m.Agent == "" {
			m.Agent, m.AgentReason = "none", paymentsAgentReason
		}
		in := schemaTree(t, md.in)
		if refine := d.refineIn[m.Name]; refine != nil {
			refine(t, in)
		}
		m.Input, m.Output = marshalSchema(t, in), schemaOf(t, md.out)
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
	return marshalSchema(t, schemaTree(t, v))
}

// schemaTree is the schema inferred from v's Go type, opened, with its fields' contract tags applied.
func schemaTree(t *testing.T, v any) *jsonschema.Schema {
	t.Helper()
	// json.RawMessage is any JSON value (a reader's piece holds members of the site; an input holds the owner's field values), not
	// the array of bytes the default translation would make of it.
	s, err := jsonschema.ForType(reflect.TypeOf(v), &jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeOf(json.RawMessage(nil)): {}},
	})
	if err != nil {
		t.Fatalf("inferring the schema of %T: %v", v, err)
	}
	openObjects(s)
	boundSchema(t, reflect.TypeOf(v), s)
	return s
}

func marshalSchema(t *testing.T, s *jsonschema.Schema) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// boundSchema applies the `contract` tags of typ's fields (contractPatterns says the grammar) to s, the schema inferred
// from typ, all the way down.
func boundSchema(t *testing.T, typ reflect.Type, s *jsonschema.Schema) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		if s.Items != nil {
			boundSchema(t, typ.Elem(), s.Items)
		}
	case reflect.Map:
		if s.AdditionalProperties != nil {
			boundSchema(t, typ.Elem(), s.AdditionalProperties)
		}
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.Anonymous && name == "" {
				boundSchema(t, f.Type, s) // its fields are the parent's, as encoding/json promotes them
				continue
			}
			p := s.Properties[name]
			if !f.IsExported() || name == "-" || p == nil {
				continue
			}
			if tag, ok := f.Tag.Lookup("contract"); ok {
				applyBounds(t, typ.Name()+"."+f.Name, f.Type, p, tag)
			}
			boundSchema(t, f.Type, p)
		}
	}
}

// applyBounds applies one field's contract tag to its schema p.
func applyBounds(t *testing.T, field string, typ reflect.Type, p *jsonschema.Schema, tag string) {
	t.Helper()
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	each := p // what len, pattern, enum, min and max hold: the value, or each item or value of a list or a map
	switch typ.Kind() {
	case reflect.Slice:
		each = p.Items
	case reflect.Map:
		each = p.AdditionalProperties
	}
	span := func(v string) (lo, hi int) {
		a, b, ok := strings.Cut(v, "-")
		var err1, err2 error
		lo, err1 = strconv.Atoi(a)
		hi, err2 = strconv.Atoi(b)
		if !ok || err1 != nil || err2 != nil || lo < 0 || hi < lo {
			t.Fatalf("%s: %q is not <least>-<most>", field, v)
		}
		return lo, hi
	}
	for _, part := range strings.Split(tag, ",") {
		key, v, ok := strings.Cut(part, "=")
		if !ok || each == nil {
			t.Fatalf("%s: contract tag %q: %q is not key=value of a field it can bound", field, tag, part)
		}
		switch key {
		case "len":
			lo, hi := span(v)
			if lo > 0 {
				each.MinLength = &lo
			}
			each.MaxLength = &hi
		case "items":
			lo, hi := span(v)
			if lo > 0 {
				p.MinItems = &lo
			}
			p.MaxItems = &hi
		case "props":
			n, err := strconv.Atoi(v)
			if err != nil || typ.Kind() != reflect.Map {
				t.Fatalf("%s: props=%q bounds a map's keys", field, v)
			}
			p.MaxProperties = &n
		case "pattern":
			re, known := contractPatterns[v]
			if !known {
				t.Fatalf("%s: no contract pattern %q", field, v)
			}
			each.Pattern = re
		case "enum":
			for _, e := range strings.Split(v, "|") {
				each.Enum = append(each.Enum, e)
			}
		case "min", "max":
			n, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("%s: %s=%q is not a number", field, key, v)
			}
			if key == "min" {
				each.Minimum = &n
			} else {
				each.Maximum = &n
			}
		default:
			t.Fatalf("%s: contract tag %q: no bound %q", field, tag, key)
		}
	}
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
		if !coreHooks[m.Hook] && !coreLacks("hook "+m.Hook) {
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
		"HookPaymentForget":         "{budget: PaymentHookBudget, errorIsAnswer: true}",
		"HookPaymentCapture":        "{budget: PaymentHookBudget, errorIsAnswer: true}",
		"HookPaymentVoid":           "{budget: PaymentHookBudget, errorIsAnswer: true}",
		"HookPaymentConfirm":        "answersAgain",
		"HookPaymentSessionUpdated": "answersAgain",
		"HookPaymentRefundUpdated":  "answersAgain",
		"HookPaymentDisputeUpdated": "answersAgain",
		"HookPaymentSavedUpdated":   "answersAgain",
		"HookPaymentReprice":        "gatewayCall",
	}
	for name, row := range rows {
		if name == "HookPaymentReprice" && coreLacks("hook "+HookPaymentReprice) {
			continue // SDK-first: Core's X2b adds the row (coreahead_test.go)
		}
		if !strings.Contains(calls, name+": "+row+",") {
			t.Errorf("core's call table no longer has %s: %s — re-read payments/v1's budget_ms and retry_class", name, row)
		}
	}
	for _, md := range paymentsV1.methods {
		m := md.row
		budget, answer := m.BudgetMS == 15000, m.RetryClass == "answer"
		switch m.Hook {
		case HookPaymentDescribe, HookPaymentStart, HookPaymentReprice:
			if !budget || answer {
				t.Errorf("%s: want 15 s and errors as failures, as gatewayCall", m.Name)
			}
		case HookPaymentRefund, HookPaymentSync, HookPaymentForget, HookPaymentCapture, HookPaymentVoid:
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

// siteReaderV1 is a plugin that reads another platform's site and hands Core's importer pieces of it (SITE_READER.md). Only Core
// asks, one named provider, so a failure blocks: a read half-delivered is not a read. Experimental until its first plugin ships on it.
var siteReaderV1 = contractDef{
	name:   ContractSiteReader,
	status: ContractExperimental,
	description: "A plugin that READS another platform's site — a WordPress export file or a live WordPress, later Shopify, Wix, " +
		"Squarespace — and hands Core's importer a neutral description of it (package siteimport, Core's snapshot version 5) in " +
		"pieces; Core checks every piece, writes the site as drafts and keeps the report, the publish act and the undo (SITE_READER.md).",
	methods: []methodDef{
		{ContractMethod{Name: SiteReaderSources, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: siteReaderAgentReason,
			Description: "The platforms this plugin reads and, for each, the fields the owner fills in (a file, an address, an application password, a choice) as a description Core draws on its Importer screen."},
			SiteReaderSourcesRequest{}, SiteReaderSourcesAnswer{}},
		{ContractMethod{Name: SiteReaderStart, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block", BudgetMS: 5000,
			Idempotent: true, Agent: "none", AgentReason: siteReaderAgentReason,
			Description: "Begin one read of one source with the owner's input; the same idempotency key returns the same read. A read nobody asks for in 30 minutes is dropped, and everything the plugin held for it with it — the owner's password included."},
			SiteReaderStartRequest{}, SiteReaderStartAnswer{}},
		{ContractMethod{Name: SiteReaderFeed, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block", BudgetMS: 5000,
			Idempotent: true, Agent: "none", AgentReason: siteReaderAgentReason,
			Description: "One chunk, in order, of the export file the owner uploaded: Core holds the file and hands it over, the plugin has no door to Core's uploads. last closes the file; a repeated seq is accepted once."},
			SiteReaderFeedRequest{}, SiteReaderFeedAnswer{}},
		{ContractMethod{Name: SiteReaderNext, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block", BudgetMS: 15000,
			Idempotent: true, Agent: "none", AgentReason: siteReaderAgentReason,
			Description: "The piece with this sequence number: members of the neutral Site in snapshot encoding, progress, and the plugin's losses as codes with data. A repeat of the same seq returns the same piece; a plugin restarted since start answers reader_restarted, never last. A piece is at most 4 MiB, one element at most 1 MiB."},
			SiteReaderNextRequest{}, SiteReaderPiece{}},
		{ContractMethod{Name: SiteReaderCancel, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: siteReaderAgentReason,
			Description: "Stop a read and drop everything the plugin holds for it."},
			SiteReaderCancelRequest{}, SiteReaderCancelAnswer{}},
	},
}

const siteReaderAgentReason = "the owner starts a read from Core's Importer screen and the assistant's import tools drive the same job; " +
	"this is how Core asks the reader plugin, and no other plugin or tool may start a read with an owner's password"

// shopAttributesV1 is door D2's first half (ATTRIBUTES.md): a plugin that owns fields on the shop's orders, lines, products
// and customers — or suggests addresses — answers the shop. Experimental until its first plugin ships on it.
var shopAttributesV1 = contractDef{
	name:   ContractShopAttributes,
	status: ContractExperimental,
	description: "A plugin's own fields on the shop's orders, lines, products and customers, and its address suggestions: " +
		"the plugin describes its fields (type, label per language, where they are shown, required, bounds) and checks " +
		"their values before an order is placed; the shop draws, checks, keeps and copies them into the order, and " +
		"proxies a visitor's address query to the plugin, bounded (ATTRIBUTES.md).",
	methods: []methodDef{
		{ContractMethod{Name: AttributesDescribe, AnsweredBy: "provider", RetryClass: "failure", Fallback: "cached",
			Idempotent: true, Agent: "none", AgentReason: attributesAgentReason,
			Description: "EXPERIMENTAL. The fields this plugin owns — key, what they are on, type, label per language, where they are shown, required, bounds — whether it checks them when an order is placed and what the shop does when it does not answer, and whether it suggests addresses."},
			ordinary{}, AttributeDescription{}},
		{ContractMethod{Name: AttributesValidate, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: attributesAgentReason,
			Description: "EXPERIMENTAL. Whether this plugin's values let the order be placed: no errors places it; each error names its field and the sentence the buyer reads. Not answering is the plugin's declared when_unanswered: block or allow."},
			AttributeValidateRequest{}, AttributeValidateAnswer{}},
		{ContractMethod{Name: AttributesAddressSuggest, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: addressAgentReason,
			Description: "EXPERIMENTAL. Addresses matching what a visitor typed, best first, at most five; the shop proxies the query so the plugin's key never reaches the browser, and bounds each visitor to 30 calls a minute."},
			AddressSuggestRequest{}, AddressSuggestAnswer{}},
		{ContractMethod{Name: AttributesAddressResolve, AnsweredBy: "provider", RetryClass: "failure", Fallback: "skip",
			Idempotent: true, Agent: "none", AgentReason: addressAgentReason,
			Description: "EXPERIMENTAL. The whole address of the suggestion a visitor picked; the shop fills its own fields with it and checks them by its own rules."},
			AddressResolveRequest{}, AddressResolveAnswer{}},
	},
}

const attributesAgentReason = "the owner reads and edits a plugin's fields on the shop's order and product screens and through " +
	"the shop's own abilities (get_order, update_product); this is how the shop asks the plugin that owns them"

const addressAgentReason = "address suggestions answer a visitor typing at checkout, through the shop's bounded proxy; " +
	"the owner has nothing to ask of them"

// shopAttributeValuesV1 is door D2's second half: the shop keeps a plugin's values, and the plugin reads and writes its own
// through write commands (WRITE_COMMANDS.md). One shop answers, so a failure blocks.
var shopAttributeValuesV1 = contractDef{
	name:   ContractShopAttributeValues,
	status: ContractExperimental,
	description: "The shop's door for a plugin's own field values (ATTRIBUTES.md): read them on an order, a line, a product or " +
		"a customer, set one — a write command, keyed, kept with the plugin that wrote it, announced once committed — and take " +
		"one back. A plugin reaches only its own fields: Core names the caller.",
	methods: []methodDef{
		{ContractMethod{Name: AttributeValuesGet, AnsweredBy: "provider", RetryClass: "failure", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: valuesAgentReason,
			Description: "EXPERIMENTAL. The calling plugin's values on one record — on an order, its order and line values and the copies of its customer and product values taken when the order was placed."},
			AttributeValuesRequest{}, AttributeValuesAnswer{}},
		{ContractMethod{Name: AttributeValueSet, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: valuesAgentReason,
			Write:       &ContractWrite{Record: AttributeValueRecord, Undo: AttributeValueUndo, Emits: EventAttributeSet},
			Description: "EXPERIMENTAL. Set one of the calling plugin's fields on one record, checked against its declaration; the same idempotency key answers the first call's record with already: true. A null value clears it. A refusal is the answer's refused code, never an error."},
			AttributeSetRequest{}, AttributeWriteAnswer{}},
		{ContractMethod{Name: AttributeValueUndo, AnsweredBy: "provider", RetryClass: "answer", Fallback: "block",
			Idempotent: true, Agent: "none", AgentReason: valuesAgentReason,
			Write:       &ContractWrite{Record: AttributeValueRecord, Emits: EventAttributeUndone},
			Description: "EXPERIMENTAL. Take back one of the calling plugin's value writes: the value it replaced returns. Refused — the answer's refused code — for another plugin's write, one taken back already, or one a later write replaced."},
			UndoInput{}, AttributeWriteAnswer{}},
	},
}

const valuesAgentReason = "a plugin's value is set by the buyer at checkout, by the owner on the shop's screens and abilities, " +
	"or by its own plugin through this door; an assistant writing here would write under no plugin's key"

// TestASchemaRefusalNeverQuotesTheValue is the 2026-11-11 review's finding 5: the validator's error carried the
// offending value — an owner's password into Core's log.
func TestASchemaRefusalNeverQuotesTheValue(t *testing.T) {
	c, ok := ContractNamed("site.reader/v1")
	if !ok {
		t.Fatal("no site.reader/v1")
	}
	m, _ := c.Method("start")
	for _, raw := range []string{`{"input":"hunter2-SECRET","source":"wp"}`, `{"source":"hunter2-SECRET-a","input":{}, "locale": 5}`} {
		err := m.ValidateInput([]byte(raw))
		if err == nil {
			t.Fatalf("%s passed", raw)
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), " 5 ") {
			t.Fatalf("the refusal quotes the value: %v", err)
		}
		if !strings.Contains(err.Error(), "/properties/") {
			t.Fatalf("the refusal does not say where: %v", err)
		}
	}
	if err := m.ValidateInput([]byte(`{}`)); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("a missing property is not named as one: %v", err)
	}
}
