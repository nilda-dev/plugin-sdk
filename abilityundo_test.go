package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// B4a (Core's agent-control plan, plugin undo): an ability may declare its way back, so its change can be undone on
// Core's AI changes ledger the way a built-in tool's is — or say why it cannot. EXPERIMENTAL until Core reads it (B4b).

// undoPlugin is a small shop whose abilities cover each shape: a creation undone by a deletion that needs the created
// id (only the result knows it), a change that is its own inverse and is checked by a read, a money pair, mail that
// cannot be called back, and the doors an inverse may not be.
func undoPlugin() []Ability {
	obj := ObjectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")
	return []Ability{
		{Name: "create_product", Class: ClassWrite, InputSchema: obj, Undo: UndoBy("delete_product")},
		{Name: "delete_product", Class: ClassDestructive, InputSchema: obj},
		{Name: "set_price", Class: ClassWrite, InputSchema: obj,
			Undo: AbilityUndo{By: "set_price", Check: "product_version"}},
		{Name: "product_version", Class: ClassRead, InputSchema: obj},
		{Name: "capture", Class: ClassWrite, Effect: EffectMoney, InputSchema: obj, Undo: UndoBy("refund")},
		{Name: "refund", Class: ClassWrite, Effect: EffectMoney, InputSchema: obj},
		{Name: "mail_customers", Class: ClassWrite, Effect: EffectOutboundMail, InputSchema: obj,
			Undo: NoUndo(UndoReasonSent, "mail_correction")},
		{Name: "mail_correction", Class: ClassWrite, Effect: EffectOutboundMail, InputSchema: obj},
		{Name: "import_rows", Class: ClassWrite, InputSchema: obj, Callers: []string{CallerKindImporter}},
		{Name: "set_api_key", Class: ClassAccess, Effect: EffectCredential, InputSchema: obj},
		{Name: "list_products", Class: ClassRead, InputSchema: obj},
	}
}

func TestAWellDeclaredWayBackPassesInit(t *testing.T) {
	s := &pluginServer{handler: &recordingHandler{}}
	if _, err := s.wire(InitResult{Abilities: undoPlugin()}); err != nil {
		t.Fatalf("Init refused a plugin whose ways back are all sound: %v", err)
	}
}

// Every rule names the ability and says what to do; none is a silent drop.
func TestInitRefusesAWayBackCoreCouldNotTake(t *testing.T) {
	with := func(name string, u AbilityUndo, more ...Ability) []Ability {
		list := undoPlugin()
		for i := range list {
			if list[i].Name == name {
				list[i].Undo = u
			}
		}
		return append(list, more...)
	}
	for _, c := range []struct {
		why   string
		list  []Ability
		words string // a fragment the refusal must say
	}{
		{"an inverse this plugin does not declare", with("create_product", UndoBy("remove_product")), "remove_product"},
		{"a money change undone by something that is not money", with("capture", UndoBy("delete_product")), "money"},
		{"an inverse a person must perform (a credential)", with("create_product", UndoBy("set_api_key")), "set_api_key"},
		{"an inverse no undo door reaches (importer only)", with("create_product", UndoBy("import_rows")), "import_rows"},
		{"an inverse only one undo door reaches (mcp only)", with("create_product", UndoBy("mcp_only")),
			"mcp_only"},
		{"a check this plugin does not declare", with("set_price", AbilityUndo{By: "set_price", Check: "price_ver"}), "price_ver"},
		{"a check that is not a read", with("set_price", AbilityUndo{By: "set_price", Check: "delete_product"}), "read"},
		{"a check without an inverse", with("set_price", AbilityUndo{Check: "product_version"}), "check"},
		{"a way back and no way back at once", with("set_price", AbilityUndo{By: "set_price", NoUndo: UndoReasonSent}), "both"},
		{"a reason Core does not translate", with("mail_customers", NoUndo("because", "")), "because"},
		{"a compensation this plugin does not declare", with("mail_customers", NoUndo(UndoReasonSent, "apologise")), "apologise"},
		{"a compensation a person must perform", with("mail_customers", NoUndo(UndoReasonSent, "set_api_key")), "set_api_key"},
		{"a compensation alone, with no reason", with("mail_customers", AbilityUndo{Compensation: "mail_correction"}), "reason"},
		{"a way back from a read, which changes nothing", with("list_products", UndoBy("delete_product")), "changes nothing"},
		{"no way back from a read, which changes nothing", with("list_products", NoUndo(UndoReasonSent, "")), "changes nothing"},
	} {
		list := c.list
		if c.words == "mcp_only" {
			list = append(list, Ability{Name: "mcp_only", Class: ClassDestructive, Callers: []string{CallerKindMCP}})
		}
		err := checkAbilityUndo(list)
		if err == nil {
			t.Errorf("%s: Init went on", c.why)
			continue
		}
		if !strings.Contains(err.Error(), c.words) {
			t.Errorf("%s: the refusal does not say %q: %v", c.why, c.words, err)
		}
	}
}

// The declaration reaches Core in the Init answer (Ability.undo, proto field 10). An ability that declares nothing
// sends nothing — a v0.65 plugin's answer, which Core reads as today's "no recorded way back".
func TestAnAbilitysWayBackGoesToCore(t *testing.T) {
	got := map[string]*contract.AbilityUndo{}
	for _, a := range abilitiesToContract(undoPlugin()) {
		got[a.GetName()] = a.GetUndo()
	}
	if u := got["create_product"]; u.GetBy() != "delete_product" || u.GetCheck() != "" || u.GetNoUndo() != "" {
		t.Errorf("create_product: %+v", u)
	}
	if u := got["set_price"]; u.GetBy() != "set_price" || u.GetCheck() != "product_version" {
		t.Errorf("set_price: %+v", u)
	}
	if u := got["mail_customers"]; u.GetBy() != "" || u.GetNoUndo() != "sent" || u.GetCompensation() != "mail_correction" {
		t.Errorf("mail_customers: %+v", u)
	}
	if u := got["list_products"]; u != nil {
		t.Errorf("an ability that declares no way back sent one: %+v", u)
	}
}

// The way back from ONE CALL is in that call's result (`undo`, a WayBack): the arguments of the inverse — which for a
// creation is the id only the result knows — the target it changed, and the version it left. Serve holds it to the
// declaration before Core sees it; a way back that could not run is taken off the answer (the change happened, so the
// call still succeeds) and Core records the change as having none.
func TestTheResultCarriesTheWayBackAndServeHoldsIt(t *testing.T) {
	var answer any
	list := undoPlugin()
	for i := range list {
		switch list[i].Name {
		case "create_product", "set_price", "mail_customers":
			list[i].Run = func(context.Context, json.RawMessage) (any, error) { return answer, nil }
		}
	}
	s := &pluginServer{handler: &recordingHandler{}}
	if _, err := s.wire(InitResult{Abilities: list}); err != nil {
		t.Fatal(err)
	}
	call := func(ability string, result any) map[string]json.RawMessage {
		t.Helper()
		answer = result
		resp, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook(ability),
			Payload: []byte(`{"id":"p1"}`), CallerKind: CallerKindAI})
		if err != nil {
			t.Fatalf("%s: a call whose change applied answered an error: %v", ability, err)
		}
		var out map[string]json.RawMessage
		if err := json.Unmarshal(resp.GetPayload(), &out); err != nil {
			t.Fatalf("%s: %v", ability, err)
		}
		return out
	}
	type created struct {
		ID   string   `json:"id"`
		Undo *WayBack `json:"undo,omitempty"`
	}

	// Kept: the inverse's arguments match its schema; the target and the version are there when a check is declared.
	good := call("create_product", created{ID: "p9", Undo: &WayBack{Args: map[string]any{"id": "p9"},
		Target: UndoTarget{Kind: "product", ID: "p9"}}})
	var wb struct {
		Args   map[string]any `json:"args"`
		Target UndoTarget     `json:"target"`
	}
	if err := json.Unmarshal(good["undo"], &wb); err != nil || wb.Args["id"] != "p9" || wb.Target.ID != "p9" {
		t.Errorf("a sound way back did not reach Core: %s (%v)", good["undo"], err)
	}
	if string(good["id"]) != `"p9"` {
		t.Errorf("the plugin's own answer was changed: %v", good)
	}
	checked := call("set_price", created{ID: "p1", Undo: &WayBack{Args: map[string]any{"id": "p1"},
		Target: UndoTarget{Kind: "product", ID: "p1"}, Version: "v7"}})
	if !strings.Contains(string(checked["undo"]), `"version":"v7"`) {
		t.Errorf("a checked way back lost its version: %s", checked["undo"])
	}
	// No way back in the result: this call changed nothing that needs one, and Core records none.
	if out := call("create_product", created{ID: "p9"}); out["undo"] != nil {
		t.Errorf("an undo appeared: %v", out)
	}

	for why, bad := range map[string]created{
		"arguments the inverse's schema refuses": {ID: "p9", Undo: &WayBack{Args: map[string]any{"id": 9}}},
		"arguments that are not an object":       {ID: "p9", Undo: &WayBack{Args: []string{"p9"}}},
		"a target with a kind and no id":         {ID: "p9", Undo: &WayBack{Args: map[string]any{"id": "p9"}, Target: UndoTarget{Kind: "product"}}},
		"a version with no check to compare it":  {ID: "p9", Undo: &WayBack{Args: map[string]any{"id": "p9"}, Version: "v1"}},
	} {
		if out := call("create_product", bad); out["undo"] != nil || string(out["id"]) != `"p9"` {
			t.Errorf("%s: kept (%v)", why, out)
		}
	}
	// A declared check has nothing to compare without the target and the version the change left.
	if out := call("set_price", created{ID: "p1", Undo: &WayBack{Args: map[string]any{"id": "p1"}}}); out["undo"] != nil {
		t.Errorf("a checked way back with no version was kept: %v", out)
	}
	// An ability that declares no inverse carries none: an `undo` field of its own is the plugin's business, untouched.
	if out := call("mail_customers", map[string]any{"undo": "whatever"}); string(out["undo"]) != `"whatever"` {
		t.Errorf("an ability with no declared inverse had its own field rewritten: %v", out)
	}
}

// The reasons are a closed vocabulary Core translates; UndoReasons lists it, sorted, for a refusal that names them.
func TestTheReasonsAreAClosedVocabulary(t *testing.T) {
	got := UndoReasons()
	if len(got) != 5 {
		t.Fatalf("UndoReasons() = %v", got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
	for _, r := range got {
		if !r.Known() {
			t.Errorf("%q is listed and not known", r)
		}
	}
	if UndoReason("because").Known() || UndoReason("").Known() {
		t.Error("a word outside the vocabulary is known")
	}
}
