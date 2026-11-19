package nilda

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// importContract is a small contract with one import call, with edit applied to its decoded tree.
func importContract(t *testing.T, edit func(m map[string]any)) []byte {
	t.Helper()
	m := map[string]any{"name": "records.put", "answered_by": "provider", "description": "Takes a batch.",
		"retry_class": "failure", "fallback": "block", "idempotent": true, "agent": "none",
		"agent_reason": "the import plugin's abilities", "import": true,
		"input": map[string]any{"type": "object", "required": []any{"import"}, "properties": map[string]any{
			"import": map[string]any{"type": "object", "required": []any{"id", "planned"}, "properties": map[string]any{
				"id": map[string]any{"type": "string"}, "planned": map[string]any{"type": "integer"}}}}},
		"output": map[string]any{"type": "object"}}
	if edit != nil {
		edit(m)
	}
	raw, err := json.Marshal(map[string]any{"name": "shop.example/v1", "description": "An example import.",
		"caller_capability": CapImport, "methods": []any{m}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// AN IMPORT CALL KEEPS WHAT CORE'S DESK RELIES ON OR THE CONTRACT IS REFUSED (SHOP_IMPORT.md §1.1): the provider answers
// it, it blocks, it is idempotent, the assistant never calls it, and its request carries the job's id and plan.
func TestAnImportCallIsHeldToItsRules(t *testing.T) {
	c, err := ParseContract(importContract(t, nil))
	if err != nil {
		t.Fatalf("the example is refused: %v", err)
	}
	if m, _ := c.Method("records.put"); !m.Import {
		t.Fatal("import: true was not read")
	}
	input := func(m map[string]any) map[string]any { return m["input"].(map[string]any) }
	importObj := func(m map[string]any) map[string]any {
		return input(m)["properties"].(map[string]any)["import"].(map[string]any)
	}
	for name, tc := range map[string]struct {
		edit func(m map[string]any)
		want string
	}{
		"rides a core hook":       {func(m map[string]any) { m["hook"] = "x.y"; delete(m, "fallback") }, ""},
		"skipped":                 {func(m map[string]any) { m["fallback"] = "skip" }, "blocks"},
		"cached":                  {func(m map[string]any) { m["fallback"] = "cached" }, "blocks"},
		"not idempotent":          {func(m map[string]any) { m["idempotent"] = false }, "not idempotent"},
		"a write tool":            {func(m map[string]any) { m["agent"] = "write"; delete(m, "agent_reason") }, "it is none"},
		"import not required":     {func(m map[string]any) { delete(input(m), "required") }, "require an import object"},
		"no import object":        {func(m map[string]any) { delete(input(m), "properties") }, "require an import object"},
		"import not an object":    {func(m map[string]any) { importObj(m)["type"] = "string" }, "require an import object"},
		"planned not required":    {func(m map[string]any) { importObj(m)["required"] = []any{"id"} }, "requiring id and planned"},
		"id not required":         {func(m map[string]any) { importObj(m)["required"] = []any{"planned"} }, "requiring id and planned"},
		"nothing required inside": {func(m map[string]any) { delete(importObj(m), "required") }, "requiring id and planned"},
	} {
		_, err := ParseContract(importContract(t, tc.edit))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want a refusal saying %q", name, err, tc.want)
		}
	}
	plain, err := ParseContract(importContract(t, func(m map[string]any) { delete(m, "import"); delete(input(m), "required") }))
	if err != nil {
		t.Fatalf("an ordinary method without the import object was refused: %v", err)
	}
	if m, _ := plain.Method("records.put"); m.Import {
		t.Fatal("a method without import: true reads as an import call")
	}
}

// EVERY CALL OF AN IMPORT CONTRACT NEEDS THE IMPORT GRANT (the security review of D10-a, F1): a contract with an import
// call requires caller_capability import, so Core gates every method by it — the plan, the act and the undo as much as
// records.put; a capability no contract may require is refused; shop.import/v1 requires it.
func TestAnImportContractRequiresTheGrantOfEveryCaller(t *testing.T) {
	var tree map[string]any
	if err := json.Unmarshal(importContract(t, nil), &tree); err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(){
		"no caller capability":       func() { delete(tree, "caller_capability") },
		"an unknown one":             func() { tree["caller_capability"] = "commerce" },
		"an empty one beside import": func() { tree["caller_capability"] = "" },
	} {
		var saved = tree["caller_capability"]
		edit()
		raw, _ := json.Marshal(tree)
		if _, err := ParseContract(raw); err == nil {
			t.Errorf("%s: the contract was taken", name)
		}
		tree["caller_capability"] = saved
	}
	c, ok := ContractNamed(ContractShopImport)
	if !ok || c.CallerCapability != CapImport {
		t.Fatalf("shop.import/v1 requires %q of its callers, want %q", c.CallerCapability, CapImport)
	}
	// A contract with no import call may require the grant too; one requiring nothing stays open to its consumers.
	for _, other := range contractDefs {
		if other.name != ContractShopImport && other.callerCapability != "" {
			t.Errorf("%s requires %q of every caller; only the import contract does", other.name, other.callerCapability)
		}
	}
}

// A CLOSED CONTRACT REFUSES WHAT IT DOES NOT DECLARE (the security review of D10-a, F2): encoding/json matches a key without
// regard to case, so in an open schema "KIND" beside "kind", or a "NAME" of any length and any characters, passed the check
// and reached the provider decoded. shop.import/v1 is closed: every object of its schemas refuses an undeclared member,
// on Core's side, on the client's and in Serves before it decodes; a closed contract with an open object is refused.
func TestTheImportContractRefusesWhatItDoesNotDeclare(t *testing.T) {
	c, ok := ContractNamed(ContractShopImport)
	if !ok || !c.Closed {
		t.Fatal("shop.import/v1 is not a closed contract")
	}
	put, _ := c.Method(ShopImportRecordsPut)
	ok1 := `{"import":{"id":"j","planned":1},"source":"woocommerce:a.example","platform":"woocommerce","settings":` +
		`{"currency":"EUR","prices_include_tax":true,"weight_unit":"kg","dimension_unit":"cm","from":"site","money":"same"},` +
		`"kind":"tag","records":[{"source_id":"a#1","data":{"name":"Linen","slug":"linen"}}]}`
	if err := put.ValidateInput([]byte(ok1)); err != nil {
		t.Fatalf("a well-formed batch was refused: %v", err)
	}
	for name, bad := range map[string]string{
		"a case-folded kind":         strings.Replace(ok1, `"kind":"tag"`, `"kind":"tag","KIND":"product"`, 1),
		"a case-folded name in data": strings.Replace(ok1, `"name":"Linen"`, `"name":"Linen","NAME":"`+strings.Repeat("", 70)+`"`, 1),
		"an undeclared member":       strings.Replace(ok1, `"slug":"linen"`, `"slug":"linen","email":"a@b.example"`, 1),
		"one in the import object":   strings.Replace(ok1, `"planned":1`, `"planned":1,"Planned":9`, 1),
		"one in the settings":        strings.Replace(ok1, `"money":"same"`, `"money":"same","Money":"convert"`, 1),
	} {
		if err := put.ValidateInput([]byte(bad)); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
	// Serves checks the request itself, before it decodes it, whoever calls its Handle.
	var decoded bool
	sm := Serves(ContractShopImport, ShopImportRecordsPut, func(_ context.Context, b ShopImportBatch) (ShopImportAnswer, error) {
		decoded = true
		return ShopImportAnswer{Kind: b.Kind, Results: []ShopImportResult{}}, nil
	})
	if _, err := sm.Handle(context.Background(), json.RawMessage(strings.Replace(ok1, `"kind":"tag"`, `"KIND":"product","kind":"tag"`, 1))); err == nil || decoded {
		t.Errorf("Serves decoded a request its schema refuses: %v (decoded %v)", err, decoded)
	}
	// A closed contract whose object is open is refused when it loads.
	var tree map[string]any
	_ = json.Unmarshal(importContract(t, nil), &tree)
	tree["closed"] = true
	raw, _ := json.Marshal(tree)
	if _, err := ParseContract(raw); err == nil || !strings.Contains(err.Error(), "is open") {
		t.Errorf("a closed contract with an open object: %v", err)
	}
}

// records.put and the owner's act — publish.apply and publish.undo — are import calls (D10 §1.4 amendment 17): what the shop
// writes back through Core while it serves them is quiet, as Core's own import publish is (core internal/importer/publish.go,
// events.WithImport "publish:<job>"); the act's page edits fired content.published webhooks, automations and author notices for
// every product while only records.put was one. The settings answer, the reads and the plan are ordinary calls.
func TestTheBatchAndTheActAreImportCalls(t *testing.T) {
	c, ok := ContractNamed(ContractShopImport)
	if !ok {
		t.Fatal("shop.import/v1 is not a contract of this SDK")
	}
	if !c.Experimental() {
		t.Fatal("shop.import/v1 ships experimental")
	}
	for _, m := range c.Methods {
		if want := m.Name == ShopImportRecordsPut || m.Name == ShopImportPublishApply || m.Name == ShopImportPublishUndo; m.Import != want {
			t.Errorf("%s: import %v", m.Name, m.Import)
		}
		if want := m.Name == ShopImportRecordsList; (m.Agent == "read") != want {
			t.Errorf("%s: agent %s", m.Name, m.Agent)
		}
		if m.Fallback != "block" {
			t.Errorf("%s: fallback %s, want block: one shop answers", m.Name, m.Fallback)
		}
	}
	if len(c.Methods) != 7 {
		t.Fatalf("%d methods, want the seven of phase A", len(c.Methods))
	}
}

func goodBatch() ShopImportBatch {
	return ShopImportBatch{Import: ImportCall{ID: "job-7", Planned: 5200}, Source: "woocommerce:shop.example.com",
		Platform: ShopImportWooCommerce, Locale: "fa", Kind: ShopImportProduct,
		Settings: ShopImportSettings{Currency: "EUR", WeightUnit: "kg", DimensionUnit: "cm", From: "site", Money: ShopImportMoneySame}}
}

func productRecord(t *testing.T, id string, p ShopImportProductData) ShopImportRecord {
	t.Helper()
	r, err := ShopImportRecordOf(id, ShopImportPublished, p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func recordsPut(t *testing.T) ContractMethod {
	t.Helper()
	c, _ := ContractNamed(ContractShopImport)
	m, ok := c.Method(ShopImportRecordsPut)
	if !ok {
		t.Fatal("no records.put")
	}
	return m
}

// THE CONTRACT CHECKS EVERY RECORD'S DATA BY ITS BATCH'S KIND, and every bound the envelope has: what Core checks before
// the shop is asked.
func TestABatchIsCheckedAgainstItsKindAndItsBounds(t *testing.T) {
	m := recordsPut(t)
	apron := ShopImportProductData{Name: "Linen apron", Slug: "linen-apron", Kind: "simple", Visibility: "everywhere",
		SKU: "AP-1", ShopImportPricing: ShopImportPricing{RegularPrice: "19.99", SaleFrom: "2026-11-01T00:00:00Z"},
		Categories: []string{"shop.example.com#cat:9"}, Images: []ShopImportImage{{MediaID: "0d7c2a1e-5f64-4b8a-9b2e-1f5c3e9d7a10"}},
		Body: json.RawMessage(`{"type":"doc","content":[]}`)}
	good := goodBatch()
	good.Records = []ShopImportRecord{productRecord(t, "shop.example.com#123", apron)}
	good.Records[0].Unmapped = []string{"meta:_wc_pre_orders_enabled"}
	raw, _ := json.Marshal(good)
	if err := m.ValidateInput(raw); err != nil {
		t.Fatalf("a good batch was refused: %v", err)
	}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good batch failed its own rules: %v", err)
	}

	bad := map[string]func(b *ShopImportBatch){
		"no import object":     func(b *ShopImportBatch) {},
		"an import id with /":  func(b *ShopImportBatch) { b.Import.ID = "job/7" },
		"planned past a mill.": func(b *ShopImportBatch) { b.Import.Planned = MaxImportPlanned + 1 },
		"a source with |":      func(b *ShopImportBatch) { b.Source = "woocommerce:shop|x" },
		"a source of no kind":  func(b *ShopImportBatch) { b.Source = "magento:shop.example.com" },
		"an unknown kind":      func(b *ShopImportBatch) { b.Kind = "customer" },
		"an unknown platform":  func(b *ShopImportBatch) { b.Platform = "magento" },
		"a weight unit":        func(b *ShopImportBatch) { b.Settings.WeightUnit = "stone" },
		"a money answer":       func(b *ShopImportBatch) { b.Settings.Money = "guess" },
		"too many records": func(b *ShopImportBatch) {
			for len(b.Records) <= MaxShopImportRecords {
				b.Records = append(b.Records, b.Records[0])
			}
		},
		"a price that is not money": func(b *ShopImportBatch) { b.Records[0] = productRecord(t, "s#1", withPrice(apron, "12,50")) },
		"a price of 16 digits":      func(b *ShopImportBatch) { b.Records[0] = productRecord(t, "s#1", withPrice(apron, "1234567890123456")) },
		"a time that is not UTC": func(b *ShopImportBatch) {
			p := apron
			p.SaleFrom = "2026-11-01T00:00:00+01:00"
			b.Records[0] = productRecord(t, "s#1", p)
		},
		"a product kind": func(b *ShopImportBatch) {
			p := apron
			p.Kind = "bundle"
			b.Records[0] = productRecord(t, "s#1", p)
		},
		"a name of two lines": func(b *ShopImportBatch) {
			p := apron
			p.Name = "Linen\napron"
			b.Records[0] = productRecord(t, "s#1", p)
		},
		"a media id that is an address": func(b *ShopImportBatch) {
			p := apron
			p.Images = []ShopImportImage{{MediaID: "https://old.example.com/a.jpg"}}
			b.Records[0] = productRecord(t, "s#1", p)
		},
		"a coupon's data in a product batch": func(b *ShopImportBatch) {
			r, _ := ShopImportRecordOf("s#1", "", ShopImportCouponData{Code: "SAVE", Kind: "percent", Amount: "10"})
			b.Records[0] = r
		},
		"a status": func(b *ShopImportBatch) { b.Records[0].Status = "publish" },
	}
	for name, edit := range bad {
		b := goodBatch()
		b.Records = []ShopImportRecord{good.Records[0]}
		edit(&b)
		var raw []byte
		if name == "no import object" {
			var tree map[string]any
			r, _ := json.Marshal(b)
			_ = json.Unmarshal(r, &tree)
			delete(tree, "import")
			raw, _ = json.Marshal(tree)
		} else {
			raw, _ = json.Marshal(b)
		}
		if err := m.ValidateInput(raw); err == nil {
			t.Errorf("%s: the contract let it through", name)
		}
	}

	// The data of each kind is held to that kind: a coupon batch takes a coupon, and refuses a coupon of no amount.
	coupons := goodBatch()
	coupons.Kind = ShopImportCoupon
	cp, _ := ShopImportRecordOf("shop.example.com#coupon:5", ShopImportPublished,
		ShopImportCouponData{Code: "SAVE10", Enabled: true, Kind: "percent", Amount: "10", UsageLimitPerCustomer: 1})
	coupons.Records = []ShopImportRecord{cp}
	raw, _ = json.Marshal(coupons)
	if err := m.ValidateInput(raw); err != nil {
		t.Fatalf("a coupon batch was refused: %v", err)
	}
	cp.Data = json.RawMessage(`{"code":"SAVE10","enabled":true,"kind":"percent"}`)
	coupons.Records = []ShopImportRecord{cp}
	raw, _ = json.Marshal(coupons)
	if err := m.ValidateInput(raw); err == nil || !strings.Contains(err.Error(), "amount") {
		t.Fatalf("a coupon with no amount: %v", err)
	}
}

func withPrice(p ShopImportProductData, price string) ShopImportProductData {
	p.RegularPrice = price
	return p
}

// What the schema cannot say, the batch's own Validate does.
func TestABatchKeepsTheRulesItsSchemaCannotSay(t *testing.T) {
	r := ShopImportRecord{SourceID: "s#1", Data: json.RawMessage(`{}`)}
	for name, edit := range map[string]func(b *ShopImportBatch){
		"another platform's source": func(b *ShopImportBatch) { b.Platform = ShopImportShopify },
		"a record twice":            func(b *ShopImportBatch) { b.Records = []ShopImportRecord{r, r} },
		"a variant with no product": func(b *ShopImportBatch) { b.Kind = ShopImportVariant; b.Records = []ShopImportRecord{r} },
		"a product with a parent": func(b *ShopImportBatch) {
			p := r
			p.ParentSourceID = "s#0"
			b.Records = []ShopImportRecord{p}
		},
		"convert without its rate": func(b *ShopImportBatch) { b.Settings.Money = ShopImportMoneyConvert },
		"a rate without convert": func(b *ShopImportBatch) {
			b.Settings.Convert = &ShopImportConvert{To: "USD", Rate: "1.08", RateDay: "2026-11-03", RateSource: "ecb", Ending: "exact"}
		},
		"a rate of zero": func(b *ShopImportBatch) {
			b.Settings.Money = ShopImportMoneyConvert
			b.Settings.Convert = &ShopImportConvert{To: "USD", Rate: "0.000", RateDay: "2026-11-03", RateSource: "ecb", Ending: "exact"}
		},
		"an import id of nothing": func(b *ShopImportBatch) { b.Import.ID = "" },
	} {
		b := goodBatch()
		b.Records = []ShopImportRecord{r}
		edit(&b)
		if b.Validate() == nil {
			t.Errorf("%s: let through", name)
		}
	}
	v := goodBatch()
	v.Kind = ShopImportVariant
	v.Records = []ShopImportRecord{{SourceID: "s#2", ParentSourceID: "s#1", Data: json.RawMessage(`{}`)}}
	if err := v.Validate(); err != nil {
		t.Fatalf("a variant naming its product: %v", err)
	}
}

func TestReadImportCallReadsTheJobOrSaysWhy(t *testing.T) {
	ic, err := ReadImportCall([]byte(`{"import":{"id":"job-7","planned":40},"kind":"tag"}`))
	if err != nil || ic.ID != "job-7" || ic.Planned != 40 {
		t.Fatalf("got %+v, %v", ic, err)
	}
	for _, in := range []string{`{}`, `{"import":null}`, `{"import":{"id":"","planned":1}}`, `{"import":{"id":"a b","planned":1}}`,
		`{"import":{"id":"x","planned":-1}}`, `{"import":{"id":"x","planned":1000001}}`, `[`,
		`{"import":{"id":"` + strings.Repeat("x", MaxImportCallID+1) + `","planned":1}}`} {
		if _, err := ReadImportCall([]byte(in)); err == nil {
			t.Errorf("%s was read as an import call", in)
		}
	}
	if _, err := ReadImportCall([]byte(`{"import":{"id":"` + strings.Repeat("x", MaxImportCallID) + `","planned":1000000}}`)); err != nil {
		t.Errorf("the bounds themselves were refused: %v", err)
	}
}

// The answer is about the batch sent, every record once, or the plugin cannot report it.
func TestAnAnswerIsAboutTheBatchSent(t *testing.T) {
	b := goodBatch()
	b.Records = []ShopImportRecord{{SourceID: "a"}, {SourceID: "b"}}
	res := func(ids ...string) []ShopImportResult {
		var out []ShopImportResult
		for _, id := range ids {
			out = append(out, ShopImportResult{SourceID: id, Outcome: ShopImportCreated, Losses: []ShopImportLoss{}})
		}
		return out
	}
	if err := (ShopImportAnswer{Kind: ShopImportProduct, Results: res("b", "a")}).ValidateFor(b); err != nil {
		t.Fatalf("a whole answer: %v", err)
	}
	for name, a := range map[string]ShopImportAnswer{
		"another kind":   {Kind: ShopImportTag, Results: res("a", "b")},
		"a record short": {Kind: ShopImportProduct, Results: res("a")},
		"a stranger":     {Kind: ShopImportProduct, Results: res("a", "b", "c")},
		"one twice":      {Kind: ShopImportProduct, Results: res("a", "a")},
	} {
		if a.ValidateFor(b) == nil {
			t.Errorf("%s: let through", name)
		}
	}
	if (ShopImportActPage{Done: true, Cursor: "x"}).Validate() == nil || (ShopImportActPage{}).Validate() == nil {
		t.Error("an act's page that is done with a cursor, or not done without one, was let through")
	}
}

// importShop answers every method from fixed answers, as the shop would shape them.
type importShop struct{ got []ShopImportBatch }

func (s *importShop) ShopImportSettings(context.Context, ShopImportSettingsRequest) (ShopImportSettingsResult, error) {
	return ShopImportSettingsResult{Decision: ShopImportMoneyAsk, Currency: "USD", Losses: []ShopImportLoss{}}, nil
}
func (s *importShop) PutShopImportRecords(_ context.Context, b ShopImportBatch) (ShopImportAnswer, error) {
	s.got = append(s.got, b)
	a := ShopImportAnswer{Kind: b.Kind, Results: []ShopImportResult{}}
	for _, r := range b.Records {
		a.Results = append(a.Results, ShopImportResult{SourceID: r.SourceID, Outcome: ShopImportCreated, Losses: []ShopImportLoss{}})
	}
	return a, nil
}
func (s *importShop) ShopImportRecords(context.Context, ShopImportListRequest) (ShopImportListAnswer, error) {
	return ShopImportListAnswer{Rows: []ShopImportRow{}}, nil
}
func (s *importShop) PlanShopImport(context.Context, ShopImportActRequest) (ShopImportPlan, error) {
	return ShopImportPlan{Groups: ShopImportGroups{SKUPairs: []ShopImportSKUPair{}, SaleSoon: []ShopImportSaleSoon{}}, Losses: []ShopImportLoss{}}, nil
}
func (s *importShop) ShopImportPrices(context.Context, ShopImportActRequest) (ShopImportPrices, error) {
	return ShopImportPrices{Rows: []ShopImportPriceRow{}}, nil
}
func (s *importShop) ApplyShopImport(context.Context, ShopImportApplyRequest) (ShopImportActPage, error) {
	return ShopImportActPage{Cursor: "", Losses: []ShopImportLoss{}}, nil // not done and no cursor: Validate refuses it
}
func (s *importShop) UndoShopImport(context.Context, ShopImportUndoRequest) (ShopImportActPage, error) {
	return ShopImportActPage{Done: true, Losses: []ShopImportLoss{}}, nil
}

// Serve finds the shop's import door by itself, checks the batch's own rules before the shop sees it, and refuses an answer
// the contract or its own rules refuse.
func TestServeFindsTheShopImportDoor(t *testing.T) {
	shop := &importShop{}
	p := serviceProviderOf(struct {
		plainOnly
		*importShop
	}{importShop: shop})
	if p == nil || len(p.ServiceMethods()) != 7 {
		t.Fatalf("Serve did not find the seven methods: %v", p)
	}
	call := func(method string, in any) error {
		raw, _ := json.Marshal(in)
		_, _, err := DispatchServiceHook(context.Background(), p, ServiceHook(ContractShopImport, method), raw)
		return err
	}
	b := goodBatch()
	b.Kind = ShopImportTag
	tag, _ := ShopImportRecordOf("shop.example.com#tag:1", "", ShopImportTagData{Name: "Linen", Slug: "linen"})
	b.Records = []ShopImportRecord{tag}
	if err := call(ShopImportRecordsPut, b); err != nil || len(shop.got) != 1 {
		t.Fatalf("a good batch: %v (%d asked)", err, len(shop.got))
	}
	b.Platform = ShopImportWix
	if err := call(ShopImportRecordsPut, b); err == nil || len(shop.got) != 1 {
		t.Fatalf("a batch whose source is another platform's reached the shop: %v", err)
	}
	if err := call(ShopImportPublishApply, ShopImportApplyRequest{Import: ImportCall{ID: "job-7", Planned: 3}, Source: "woocommerce:shop.example.com", Marker: "publish:job-7"}); err == nil {
		t.Fatal("an act's page that is neither done nor names the next was let out")
	}
	if err := call(ShopImportPublishUndo, ShopImportUndoRequest{Import: ImportCall{ID: "job-7", Planned: 3}, Source: "woocommerce:shop.example.com", Marker: "publish:job-7"}); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if err := call(ShopImportSettingsAnswer, ShopImportSettingsRequest{Source: "woocommerce:shop.example.com", Platform: ShopImportWooCommerce,
		Settings: ShopImportSettings{Currency: "EUR"}}); err != nil {
		t.Fatalf("settings: %v", err)
	}
}

// The bounds the constants name are the schema's: a tag and a constant are two spellings of one number, held together here.
func TestTheShopImportConstantsAreTheSchemasBounds(t *testing.T) {
	c, _ := ContractNamed(ContractShopImport)
	at := func(method, side string, path ...string) map[string]any {
		t.Helper()
		m, _ := c.Method(method)
		raw := m.Input
		if side == "out" {
			raw = m.Output
		}
		var node map[string]any
		if err := json.Unmarshal(raw, &node); err != nil {
			t.Fatal(err)
		}
		for _, p := range path {
			next, ok := node[p].(map[string]any)
			if !ok {
				if list, isList := node[p].([]any); isList && len(list) > 0 {
					next, ok = list[0].(map[string]any)
				}
			}
			if !ok {
				t.Fatalf("%s %s: no %v in the schema", method, side, path)
			}
			node = next
		}
		return node
	}
	num := func(n map[string]any, key string) int { f, _ := n[key].(float64); return int(f) }
	put := ShopImportRecordsPut
	for name, got := range map[string][2]int{
		"records a batch":       {num(at(put, "in", "properties", "records"), "maxItems"), MaxShopImportRecords},
		"unmapped fields":       {num(at(put, "in", "properties", "records", "items", "properties", "unmapped"), "maxItems"), MaxShopImportUnmapped},
		"planned records":       {num(at(put, "in", "properties", "import", "properties", "planned"), "maximum"), MaxImportPlanned},
		"answers a batch":       {num(at(put, "out", "properties", "results"), "maxItems"), MaxShopImportRecords},
		"a list's page":         {num(at(ShopImportRecordsList, "out", "properties", "rows"), "maxItems"), MaxShopImportListPage},
		"a price page":          {num(at(ShopImportPublishPrices, "out", "properties", "rows"), "maxItems"), MaxShopImportListPage},
		"the plan's pairs":      {num(at(ShopImportPublishPlan, "out", "properties", "groups", "properties", "sku_pairs"), "maxItems"), MaxShopImportPairs},
		"a category's name":     {num(at(put, "in", "allOf", "then", "properties", "records", "items", "properties", "data", "properties", "name"), "maxLength"), MaxShopImportName},
		"a category's text":     {num(at(put, "in", "allOf", "then", "properties", "records", "items", "properties", "data", "properties", "description"), "maxLength"), MaxShopImportText},
		"the list's page asked": {num(at(ShopImportRecordsList, "in", "properties", "limit"), "maximum"), MaxShopImportListPage},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: the schema says %d, the constant %d", name, got[0], got[1])
		}
	}
	if want := "{1," + strconv.Itoa(MaxImportCallID) + "}"; !strings.Contains(contractPatterns["importid"], want) {
		t.Errorf("the import id's pattern %q does not bound it to MaxImportCallID", contractPatterns["importid"])
	}
	// A coupon's product list is bounded by MaxShopImportRefs: the coupon is the last kind.
	m, _ := c.Method(put)
	var in struct {
		AllOf []struct {
			Then struct {
				Properties struct {
					Records struct {
						Items struct {
							Properties struct {
								Data struct {
									Properties map[string]json.RawMessage `json:"properties"`
								} `json:"data"`
							} `json:"properties"`
						} `json:"items"`
					} `json:"records"`
				} `json:"properties"`
			} `json:"then"`
		} `json:"allOf"`
	}
	if err := json.Unmarshal(m.Input, &in); err != nil || len(in.AllOf) != len(shopImportKinds) {
		t.Fatalf("records.put's input has %d kinds' data, want %d (%v)", len(in.AllOf), len(shopImportKinds), err)
	}
	var products struct {
		MaxItems int `json:"maxItems"`
	}
	_ = json.Unmarshal(in.AllOf[len(in.AllOf)-1].Then.Properties.Records.Items.Properties.Data.Properties["products"], &products)
	if got := products.MaxItems; got != MaxShopImportRefs {
		t.Errorf("a coupon's products: the schema says %d, MaxShopImportRefs %d", got, MaxShopImportRefs)
	}
}
