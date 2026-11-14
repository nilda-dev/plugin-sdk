package nildatest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// fakeT records a failure instead of stopping the real test.
type fakeT struct {
	testing.TB
	failed string
}

func (f *fakeT) Helper()                   {}
func (f *fakeT) Logf(string, ...any)       {}
func (f *fakeT) Fatalf(s string, a ...any) { f.failed = fmt.Sprintf(s, a...); panic(f) }

func run(tb testing.TB, path string, list []nilda.Ability) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	ManifestAbilities(f, path, list)
	return ""
}

var shopAbilities = []nilda.Ability{
	{Name: "create_product", Label: "Create a product", Description: "Add a product.", Class: nilda.ClassWrite},
	{Name: "list_orders", Label: "List orders", Description: "List the orders.", Class: nilda.ClassRead},
}

// WP15: the manifest's abilities are written by the SDK, not typed — and a manifest that disagrees with Init fails.
func TestManifestAbilitiesWritesThenHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	orig := "{\n  \"key\": \"shop\",\n  \"capabilities\": [\"abilities\"]\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if failed := run(t, path, shopAbilities); !strings.Contains(failed, WriteManifestEnv) {
		t.Fatalf("a manifest with no abilities passed: %q", failed)
	}
	t.Setenv(WriteManifestEnv, "1")
	if failed := run(t, path, shopAbilities); failed != "" {
		t.Fatalf("writing failed: %s", failed)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "{\n  \"key\": \"shop\",\n  \"capabilities\": [\"abilities\"],\n  \"abilities\": [") {
		t.Fatalf("the rest of the file was not kept as it was:\n%s", got)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := run(t, path, shopAbilities); failed != "" {
		t.Fatalf("the written manifest does not hold: %s", failed)
	}
	// A changed description fails until the manifest is written again, and the rewrite replaces in place.
	changed := append([]nilda.Ability{}, shopAbilities...)
	changed[1].Description = "List the orders, newest first."
	if failed := run(t, path, changed); !strings.Contains(failed, "list_orders differs") {
		t.Fatalf("a changed description passed: %q", failed)
	}
	t.Setenv(WriteManifestEnv, "1")
	_ = run(t, path, changed[:1])
	got, _ = os.ReadFile(path)
	if strings.Contains(string(got), "list_orders") || strings.Count(string(got), "\"abilities\"") != 2 {
		t.Fatalf("the rewrite did not replace the old list in place:\n%s", got)
	}
}

func runVisitor(tb testing.TB, path string, list []nilda.VisitorTool) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	ManifestVisitorTools(f, path, list)
	return ""
}

// The visitor tools the owner reads before granting `visitor_tools` are written by the SDK, beside the abilities,
// and a changed one fails until written again.
func TestManifestVisitorToolsWritesThenHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte("{\n  \"key\": \"shop\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tools := []nilda.VisitorTool{{Name: "search_products", Description: "Search the shop.", Effect: nilda.VisitorRead, Paths: []string{"/shop"}}}
	if failed := runVisitor(t, path, tools); !strings.Contains(failed, WriteManifestEnv) {
		t.Fatalf("a manifest with no visitor tools passed: %q", failed)
	}
	t.Setenv(WriteManifestEnv, "1")
	if failed := runVisitor(t, path, tools); failed != "" {
		t.Fatalf("writing failed: %s", failed)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"effect": "read"`) || !strings.Contains(string(got), `"/shop"`) {
		t.Fatalf("written:\n%s", got)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := runVisitor(t, path, tools); failed != "" {
		t.Fatalf("the written manifest does not hold: %s", failed)
	}
	tools[0].Effect = nilda.VisitorSubmit
	if failed := runVisitor(t, path, tools); failed == "" {
		t.Fatal("a changed effect passed")
	}
}

// An ALWAYS ASK ability is declared so in plugin.json too — Core offers only an ability declared exactly as Init returns
// it, and the marketplace review reads which ones ask.
func TestManifestAbilitiesWritesAlwaysAsk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(path, []byte(`{"key":"shop"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list := []nilda.Ability{
		{Name: "refund_order", Label: "Refund", Description: "Give the money back.", Class: nilda.ClassDestructive, AlwaysAsk: true},
		{Name: "list_orders", Label: "Orders", Description: "List them.", Class: nilda.ClassRead},
	}
	t.Setenv(WriteManifestEnv, "1")
	ManifestAbilities(t, path, list)
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"always_ask": true`) || strings.Count(string(raw), "always_ask") != 1 {
		t.Fatalf("the written manifest:\n%s", raw)
	}
	t.Setenv(WriteManifestEnv, "")
	list[0].AlwaysAsk = false
	if failed := run(t, path, list); failed == "" {
		t.Error("a manifest that says always_ask while Init does not was accepted")
	}
}

// An ability's callers (ADR-7(a), card T56b) are declared in plugin.json too: Core offers only an ability declared
// exactly as Init returns it, and the review reads which ones no model may call.
func TestTheManifestDeclaresAnAbilitysCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(path, []byte(`{"key":"booking"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	list := []nilda.Ability{
		{Name: "import_apply", Label: "Import", Description: "Apply one chunk.", Class: nilda.ClassDestructive,
			Callers: []string{nilda.CallerKindImporter}},
		{Name: "list_orders", Label: "Orders", Description: "List them.", Class: nilda.ClassRead},
	}
	t.Setenv(WriteManifestEnv, "1")
	ManifestAbilities(t, path, list)
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"callers": [`) || !strings.Contains(string(raw), `"importer"`) ||
		strings.Count(string(raw), "callers") != 1 {
		t.Fatalf("the written manifest:\n%s", raw)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("the manifest just written does not hold: %s", failed)
	}
	list[0].Callers = nil
	if failed := run(t, path, list); failed == "" {
		t.Error("a manifest that names callers while Init does not was accepted")
	}
	list[0].Callers = []string{nilda.CallerKindOwnerUI}
	if failed := run(t, path, list); failed == "" {
		t.Error("a manifest whose callers differ from Init's was accepted")
	}
}

// D4 (Core's agent-control C4, 2026-11-13): Core no longer counts abilities, it bounds what one plugin's abilities put
// in front of a model — labels, descriptions and input schemas together, nilda.MaxManifestAbilityDefinitionBytes.
// ManifestAbilities holds a plugin to it at the author's keyboard, schemas included, and says the size — even with
// NILDA_WRITE_MANIFEST=1, since writing an over-budget list only moves the refusal to the install.
func TestManifestAbilitiesHoldsTheDefinitionBudget(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte("{\n  \"key\": \"shop\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	schema := nilda.ObjectSchema(map[string]any{"note": map[string]any{"type": "string",
		"description": strings.Repeat("s", 1000)}})
	var list []nilda.Ability
	total := 0
	for i := 0; total <= nilda.MaxManifestAbilityDefinitionBytes; i++ {
		a := nilda.Ability{Name: fmt.Sprintf("act_%d", i), Label: "Act", Description: strings.Repeat("d", 1500),
			Class: nilda.ClassRead, InputSchema: schema}
		total += len(a.Label) + len(a.Description) + len(a.InputSchema)
		list = append(list, a)
	}
	// Far more than the 200 Core once counted: the count is gone, the size is what stops it.
	if len(list) <= 200 {
		t.Fatalf("the fixture has only %d abilities; it must pass the old count to show the count is gone", len(list))
	}
	t.Setenv(WriteManifestEnv, "1")
	failed := run(t, path, list)
	if failed == "" {
		t.Fatalf("%d bytes of abilities passed a %d-byte budget", total, nilda.MaxManifestAbilityDefinitionBytes)
	}
	for _, want := range []string{fmt.Sprint(total), fmt.Sprint(nilda.MaxManifestAbilityDefinitionBytes), "input schemas"} {
		if !strings.Contains(failed, want) {
			t.Errorf("the failure does not say %q: %s", want, failed)
		}
	}
	if got, _ := os.ReadFile(path); strings.Contains(string(got), "abilities") {
		t.Errorf("an over-budget list was written into plugin.json:\n%.200s", got)
	}

	// One ability fewer than the budget allows writes, however many there are.
	list = list[:len(list)-1]
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("%d abilities inside the budget failed: %s", len(list), failed)
	}
}

// A0b: an ability's effect is declared in plugin.json beside always_ask, which follows the effect's rule — the
// manifest Core reviews is the one Init returns, and a Core before A0b still asks.
func TestManifestAbilitiesWritesTheEffect(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte("{\n  \"key\": \"shop\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := []nilda.Ability{
		{Name: "refund", Label: "Refund", Description: "Refund an order.", Class: nilda.ClassWrite, Effect: nilda.EffectMoney},
		{Name: "list_orders", Label: "List orders", Description: "List the orders.", Class: nilda.ClassRead, Effect: nilda.EffectNone},
	}
	t.Setenv(WriteManifestEnv, "1")
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("writing failed: %s", failed)
	}
	got, _ := os.ReadFile(path)
	s := strings.Join(strings.Fields(string(got)), " ")
	for _, want := range []string{`"name": "refund"`, `"effect": "money"`, `"always_ask": true`, `"effect": "none"`} {
		if !strings.Contains(s, want) {
			t.Errorf("plugin.json has no %s:\n%s", want, got)
		}
	}
	if strings.Count(s, `"always_ask": true`) != 1 {
		t.Errorf("want always_ask on the money ability alone:\n%s", got)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("the written manifest does not hold: %s", failed)
	}
	// A changed effect is a changed declaration.
	list[1].Effect = nilda.EffectBulk
	if failed := run(t, path, list); !strings.Contains(failed, "list_orders differs") {
		t.Errorf("a changed effect passed: %q", failed)
	}
}

// B4a: an ability's declared way back (nilda.Ability.Undo, EXPERIMENTAL) is written into plugin.json as "undo" — the
// inverse and its check, or the reason there is none and what makes up for it — and held like every other field. An
// ability that declares none writes no key, as a v0.65 manifest has none.
func TestManifestAbilitiesWritesTheWayBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	if err := os.WriteFile(path, []byte("{\n  \"key\": \"shop\"\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	list := []nilda.Ability{
		{Name: "set_price", Label: "Set a price", Description: "Set a price.", Class: nilda.ClassWrite,
			Undo: nilda.AbilityUndo{By: "set_price", Check: "price_version"}},
		{Name: "price_version", Label: "Price version", Description: "Read a price's version.", Class: nilda.ClassRead},
		{Name: "mail", Label: "Mail", Description: "Mail the customers.", Class: nilda.ClassWrite,
			Effect: nilda.EffectOutboundMail, Undo: nilda.NoUndo(nilda.UndoReasonSent, "")},
	}
	t.Setenv(WriteManifestEnv, "1")
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("writing failed: %s", failed)
	}
	got, _ := os.ReadFile(path)
	s := strings.Join(strings.Fields(string(got)), " ")
	for _, want := range []string{`"undo": { "by": "set_price", "check": "price_version" }`, `"undo": { "no_undo": "sent" }`} {
		if !strings.Contains(s, want) {
			t.Errorf("plugin.json has no %s:\n%s", want, got)
		}
	}
	if strings.Count(s, `"undo"`) != 2 {
		t.Errorf("want undo on two abilities, none on the read:\n%s", got)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := run(t, path, list); failed != "" {
		t.Fatalf("the written manifest does not hold: %s", failed)
	}
	list[2].Undo = nilda.NoUndo(nilda.UndoReasonOutside, "")
	if failed := run(t, path, list); !strings.Contains(failed, "mail differs") {
		t.Errorf("a changed way back passed: %q", failed)
	}
}
