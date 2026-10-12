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

// contractDefs is every contract this module defines.
var contractDefs = []contractDef{paymentsV1}

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
