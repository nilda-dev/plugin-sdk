package nilda

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestObjectSchemaBuildsAValidJSONSchemaObject(t *testing.T) {
	raw := ObjectSchema(map[string]any{
		"title": map[string]any{"type": "string"},
		"price": map[string]any{"type": "number"},
	}, "title")

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	// Core REFUSES an ability whose schema is not an object, so this is the shape that decides whether a
	// plugin's ability appears at all.
	if got["type"] != "object" {
		t.Fatalf(`type = %v, want "object"`, got["type"])
	}
	props, ok := got["properties"].(map[string]any)
	if !ok || len(props) != 2 {
		t.Fatalf("properties lost: %v", got["properties"])
	}
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "title" {
		t.Fatalf("required lost: %v", got["required"])
	}
}

func TestObjectSchemaWithNoPropertiesIsStillAnObject(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(ObjectSchema(nil), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["type"] != "object" {
		t.Fatal("an argument-less ability must still declare an object schema")
	}
	if _, ok := got["properties"]; !ok {
		t.Fatal("properties must be present, even when empty")
	}
}

// The point of Ability.Run: declaring an ability and then forgetting to route its hook would be a plugin
// that advertises an action and fails every time an agent tries it.
func TestDeclaredRunIsDispatchedWithoutTheAuthorRoutingIt(t *testing.T) {
	var gotArgs string
	runners := abilityRunners([]Ability{{
		Name: "create_product",
		Run: func(_ context.Context, args json.RawMessage) (any, error) {
			gotArgs = string(args)
			return map[string]any{"id": "p1"}, nil
		},
	}})

	out, handled, err := dispatchAbility(context.Background(), runners,
		AbilityHook("create_product"), []byte(`{"title":"Mug"}`))
	if !handled {
		t.Fatal("an ability hook must be handled by the SDK, not passed through")
	}
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if gotArgs != `{"title":"Mug"}` {
		t.Fatalf("arguments were not passed through: %q", gotArgs)
	}
	if !strings.Contains(string(out), `"id":"p1"`) {
		t.Fatalf("result was not returned: %s", out)
	}
}

// Everything that is NOT an ability must still reach the author's own HandleHook.
func TestOrdinaryHooksArePassedThrough(t *testing.T) {
	_, handled, err := dispatchAbility(context.Background(), abilityRunners(nil), "content.saved", []byte(`{}`))
	if handled || err != nil {
		t.Fatalf("an ordinary hook must pass through untouched (handled=%v err=%v)", handled, err)
	}
}

// An `ability:` hook the author did not give a Run to falls THROUGH to their HandleHook. That is the
// documented way to handle abilities yourself, it is the only reason `AbilityHook` is exported, and it is
// what makes `Ability.Run` genuinely optional.
//
// This asserted the opposite until 2026-08-06 — handled=true with "no runner for ability" — which made the
// documented pattern fail at runtime with an error naming the author's own ability. The reserved-namespace
// worry it was written for cannot happen: Core dispatches `ability:<name>` only for abilities this plugin
// declared at Init, so the only names that arrive are the author's.
func TestAnAbilityWithNoRunReachesTheAuthorsHandleHook(t *testing.T) {
	out, handled, err := dispatchAbility(context.Background(), abilityRunners(nil), AbilityHook("manual"), nil)
	if handled {
		t.Fatal("an ability declared without a Run must reach the author's HandleHook")
	}
	if err != nil || out != nil {
		t.Fatalf("a fall-through answers nothing: out=%s err=%v", out, err)
	}
}

// An ability that returns nothing is a success, not an empty response the agent has to guess about.
func TestNilResultBecomesAnExplicitOK(t *testing.T) {
	runners := abilityRunners([]Ability{{
		Name: "reindex",
		Run:  func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	}})
	out, handled, err := dispatchAbility(context.Background(), runners, AbilityHook("reindex"), nil)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("got %s, want an explicit ok", out)
	}
}

// An ability with no Run is a legitimate choice — the author handles it in HandleHook — so it must not be
// indexed as a runner.
func TestAbilityWithoutRunIsNotIndexed(t *testing.T) {
	runners := abilityRunners([]Ability{{Name: "manual"}})
	if _, ok := runners["manual"]; ok {
		t.Fatal("an ability with no Run must not be registered as a runner")
	}
}

// M5 (2026-09-23 plugin hunt): two abilities with one name. Core offered the agent the FIRST declaration while
// the runner map kept the LAST one's Run, so the model read one action and a different one ran. Serve now
// refuses the duplicate at Init; the runner map agrees with Core in case anything ever bypasses that.
func TestTwoAbilitiesWithOneNameAreRefused(t *testing.T) {
	first := func(context.Context, json.RawMessage) (any, error) { return "first", nil }
	second := func(context.Context, json.RawMessage) (any, error) { return "second", nil }
	list := []Ability{{Name: "refund", Run: first}, {Name: "report"}, {Name: "refund", Run: second}}
	if err := duplicateAbility(list); err == nil || !strings.Contains(err.Error(), `"refund"`) {
		t.Fatalf("a duplicated ability name was not refused by name: %v", err)
	}
	if err := duplicateAbility(list[:2]); err != nil {
		t.Fatalf("distinct names were refused: %v", err)
	}
	got, err := abilityRunners(list)["refund"](context.Background(), nil)
	if err != nil || got != "first" {
		t.Fatalf("the runner for a duplicated name is %v — Core offers the FIRST declaration, so it must run", got)
	}
	// And the Init that Core calls is the one that refuses it. pluginServer.Init needs a live broker, so the
	// call site is read rather than driven.
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "duplicateAbility(res.Abilities)") {
		t.Fatal("serve.go's Init no longer refuses duplicated ability names")
	}
}

// M3 (2026-09-23 plugin hunt): the SDK's Class vocabulary is Core's, name for name. `analyze` was added to
// Core's risk classes and never to this list, so an author had no constant for it and a typo'd string became
// the most restricted class without a word.
//
// Both sides are READ, never typed here: Core's from its catalog.go, the SDK's from every constant of type
// Class this package declares. A hand-written SDK list would agree with itself — a class added to the SDK
// alone, or to Core alone and to this test, would pass.
// "On a hosted site without the AI-execution plan, the site's assistant runs only `read` abilities", and every class on a
// self-hosted install — held to Core's gate: every tool enter()s through policyGate, which refuses a class that
// mutates() unless AIExecuteOpen (self-hosted, or the plan on a hosted site; D-96), and
// mutates() is every class ranked above read; a plugin's ability is run through enter() like a Core tool.
func TestTheGuideSaysAHostedSiteWithoutThePlanRunsOnlyReadAbilities(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	guard := flat(coreSource(t, "internal", "agent", "guardrails.go"))
	for _, want := range []string{
		"if r, blocked := s.policyGate(ctx, pol, spec); blocked { return owner, r }",
		"if spec.Class.mutates() && !license.AIExecuteOpen() {",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("core's guardrails.go no longer has %q — re-read the guide's hosted-site sentence", want)
		}
	}
	if lic := flat(coreSource(t, "internal", "license", "license.go")); !strings.Contains(lic,
		"func AIExecuteOpen() bool { return !authz.Hosted() || IsProUnlocked(FeatureAIExecute) }") {
		t.Error("core's AIExecuteOpen is no longer \"self-hosted, or the plan on a hosted site\" — re-read the guide's sentence")
	}
	if !strings.Contains(flat(coreSource(t, "internal", "agent", "catalog.go")), "return ok && r > classRank[ClassRead]") {
		t.Error("core's mutates() is no longer every class above read — re-read the guide's free-install sentence")
	}
	if !strings.Contains(flat(coreSource(t, "internal", "agent", "abilities.go")), "owner, ref := svc.enter(ctx, name)") {
		t.Error("core no longer runs a plugin's ability through enter() — re-read the guide's free-install sentence")
	}
	guide := flat(readText(t, "docs/PLUGIN_SDK.md"))
	for _, want := range []string{
		"**On a hosted site without the AI-execution plan, the site's assistant runs only `read` abilities.**",
		"A self-hosted install brings its own AI key and client, so there every class runs (Core's D-96).",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("PLUGIN_SDK.md no longer says %q, which core's guardrails make true", want)
		}
	}
}

func TestTheClassesAreCores(t *testing.T) {
	raw := coreSource(t, "internal", "agent", "catalog.go")
	decl := regexp.MustCompile(`(?m)^\s*Class\w+\s+Class\s*=\s*"([a-z]+)"`)
	var core []string
	for _, m := range decl.FindAllStringSubmatch(raw, -1) {
		core = append(core, m[1])
	}
	if len(core) < 5 {
		t.Fatalf("found %d classes in core's catalog.go — the parse is broken, repoint this guard", len(core))
	}
	sdk := declaredClasses(t)
	if len(sdk) < 5 {
		t.Fatalf("found %d Class constants in this package — the parse is broken, repoint this guard", len(sdk))
	}
	if strings.Join(core, ",") != strings.Join(sdk, ",") {
		t.Fatalf("core's risk classes are %v; the SDK declares %v", core, sdk)
	}
	// And the guide's class table lists exactly them, in the same order — it is where an author picks one.
	var table []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z]+)` \\| ").FindAllStringSubmatch(
		mdSection(t, "docs/PLUGIN_SDK.md", "### Telling the AI agent what you can do"), -1) {
		table = append(table, m[1])
	}
	if strings.Join(table, ",") != strings.Join(sdk, ",") {
		t.Errorf("PLUGIN_SDK.md's class table lists %v; the classes are %v", table, sdk)
	}
}

// declaredClasses is the value of every constant of type Class in this package's non-test source, in
// declaration order — the vocabulary as an author can import it.
func declaredClasses(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var out []string
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", f, err)
		}
		// A class is declared either typed (`ClassRead Class = "read"`) or by conversion (`ClassRead = Class("read")`,
		// no type on the spec) — the second form used to be skipped in silence, so an SDK-only class declared that
		// way passed "the SDK's classes are Core's".
		isClassConversion := func(v ast.Expr) bool {
			call, ok := v.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return false
			}
			fn, ok := call.Fun.(*ast.Ident)
			return ok && fn.Name == "Class"
		}
		for _, d := range file.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || (gen.Tok != token.CONST && gen.Tok != token.VAR) {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				typed := false
				if id, ok := vs.Type.(*ast.Ident); ok && id.Name == "Class" {
					typed = true
				}
				converted := vs.Type == nil && len(vs.Values) > 0
				for _, v := range vs.Values {
					converted = converted && isClassConversion(v)
				}
				if !typed && !converted {
					continue
				}
				for _, v := range vs.Values {
					if isClassConversion(v) {
						v = v.(*ast.CallExpr).Args[0]
					}
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						t.Fatalf("%s declares a Class that is not a string literal — teach this guard", f)
					}
					s, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// ALWAYS ASK (v0.40.0, commerce DR-15): an ability that moves money or lets data out of the site is declared so, and the
// flag reaches Core in the Init answer — Core then asks the person before every call, whatever a connector was granted.
func TestAnAlwaysAskAbilityTravelsToCore(t *testing.T) {
	out := abilitiesToContract([]Ability{
		{Name: "refund_order", Label: "Refund", Description: "Give the money back.", Class: ClassDestructive, AlwaysAsk: true},
		{Name: "list_orders", Label: "Orders", Description: "List them.", Class: ClassRead},
	})
	if len(out) != 2 || !out[0].GetAlwaysAsk() || out[1].GetAlwaysAsk() {
		t.Fatalf("the Init answer's abilities: %+v", out)
	}
	if out[0].GetClass() != "destructive" || out[0].GetName() != "refund_order" {
		t.Errorf("the rest of the declaration did not travel: %+v", out[0])
	}
	src, err := os.ReadFile("serve.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "abilitiesToContract(res.Abilities)") {
		t.Fatal("serve.go's Init no longer sends the abilities through abilitiesToContract")
	}
}
