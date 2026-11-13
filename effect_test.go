package nilda

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// sdkEffects is every constant of type Effect this package declares, read from its source — a hand-written list
// would agree with itself.
func sdkEffects(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\t(Effect\w+)\s+Effect = "([a-z_]+)"`).FindAllStringSubmatch(readText(t, "effect.go"), -1) {
		out[m[2]] = m[1]
	}
	if len(out) == 0 {
		t.Fatal("effect.go declares no Effect constants this test can read")
	}
	return out
}

// A0b (Core's agent-control plan, unit A0): the SDK's effect vocabulary is Core's, word for word — core's
// internal/module/effect.go ToolEffect — and each effect asks the person exactly when Core's one rule table
// (effectRules, read through RuleOf) says AlwaysAsk. Both sides are read, never typed here.
func TestTheEffectVocabularyIsCores(t *testing.T) {
	src := coreSource(t, "internal", "module", "effect.go")
	core := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\t(Effect\w+)\s+ToolEffect = "([a-z_]+)"`).FindAllStringSubmatch(src, -1) {
		core[m[2]] = m[1]
	}
	if len(core) == 0 {
		t.Fatal("core's effect.go declares no ToolEffect this guard can read — repoint it")
	}
	sdk := sdkEffects(t)
	for word, name := range core {
		if sdk[word] != name {
			t.Errorf("core's effect %q (%s) is %q in the SDK", word, name, sdk[word])
		}
	}
	for word := range sdk {
		if _, ok := core[word]; !ok {
			t.Errorf("the SDK's effect %q is not Core's", word)
		}
	}

	// The rule table: which effects ask before every call.
	table := src[strings.Index(src, "var effectRules = map[ToolEffect]Rule{"):]
	table = table[:strings.Index(table, "\n}\n")]
	asks := map[string]bool{}
	rows := regexp.MustCompile(`(?m)^\s*(Effect\w+):\s*\{([^}]*)\},`).FindAllStringSubmatch(table, -1)
	for _, m := range rows {
		asks[m[1]] = strings.Contains(m[2], "AlwaysAsk: true")
	}
	if len(rows) != len(core) {
		t.Fatalf("core's effectRules has %d rows this guard read, and %d effects — teach it the table's new shape", len(rows), len(core))
	}
	for word, name := range core {
		if got := Effect(word).AlwaysAsks(); got != asks[name] {
			t.Errorf("Effect(%q).AlwaysAsks() is %v, core's effectRules says AlwaysAsk %v", word, got, asks[name])
		}
	}
	// An effect outside the vocabulary fails closed, as Core's failClosed does; no effect obliges nothing.
	if !strings.Contains(src, "var failClosed = Rule{AlwaysAsk: true") {
		t.Error("core's failClosed no longer asks — re-read Effect.AlwaysAsks")
	}
	if !Effect("refundd").AlwaysAsks() || Effect("").AlwaysAsks() {
		t.Error("an unknown effect must ask (fail closed), an undeclared one must not")
	}
	if !strings.Contains(src, "if e == \"\" {\n\t\treturn Rule{}\n\t}") {
		t.Error("core's RuleOf no longer answers nothing for an undeclared effect — re-read Effect.AlwaysAsks")
	}
}

// An ability's effect reaches Core on the wire, and always_ask goes with it as the effect's rule says, so a Core that
// reads only always_ask (before A0b) still asks the person for money moving, mail going out, a bulk change.
func TestAnAbilitysEffectGoesToCore(t *testing.T) {
	got := abilitiesToContract([]Ability{
		{Name: "refund", Class: ClassWrite, Effect: EffectMoney},
		{Name: "publish_page", Class: ClassPublish, Effect: EffectPublish},
		{Name: "old", Class: ClassWrite, AlwaysAsk: true},
		{Name: "plain", Class: ClassRead},
	})
	want := []struct {
		effect string
		ask    bool
	}{{"money", true}, {"publish", false}, {"", true}, {"", false}}
	for i, w := range want {
		if got[i].GetEffect() != w.effect || got[i].GetAlwaysAsk() != w.ask {
			t.Errorf("%s: effect %q always_ask %v, want %q %v", got[i].GetName(), got[i].GetEffect(), got[i].GetAlwaysAsk(), w.effect, w.ask)
		}
	}
}

// A typo'd effect would be one Core asks for on every call (fail closed) with nobody knowing why; Init names it.
func TestInitRefusesAnEffectCoreDoesNotKnow(t *testing.T) {
	if err := checkAbilityEffects([]Ability{{Name: "refund", Effect: "refnd"}}); err == nil ||
		!strings.Contains(err.Error(), "refund") || !strings.Contains(err.Error(), "refnd") {
		t.Errorf("an unknown effect passed or was not named: %v", err)
	}
	if err := checkAbilityEffects([]Ability{{Name: "refund", Effect: EffectMoney}, {Name: "list"}}); err != nil {
		t.Errorf("known and empty effects refused: %v", err)
	}
	// A setting has no Init: its plugin.json is written from the struct, and an unknown effect does not marshal.
	if _, err := json.Marshal(AdminPage{Key: "s", Fields: []SettingField{{Key: "to", Effect: "redirectt"}}}); err == nil ||
		!strings.Contains(err.Error(), `"to"`) || !strings.Contains(err.Error(), "redirectt") {
		t.Errorf("an unknown setting effect marshalled or was not named: %v", err)
	}
}

// Effects lists the vocabulary, sorted, for a refusal that names the choices.
func TestEffectsListsTheVocabulary(t *testing.T) {
	var words []string
	for w := range sdkEffects(t) {
		words = append(words, w)
	}
	sort.Strings(words)
	var got []string
	for _, e := range Effects() {
		got = append(got, string(e))
	}
	if strings.Join(got, ",") != strings.Join(words, ",") {
		t.Errorf("Effects() is %v, the constants are %v", got, words)
	}
}

// A setting declares its effect in plugin.json, and always_ask is written beside it as the effect's rule says, so a
// Core before A0b — which reads only always_ask — still asks before the change.
func TestASettingsEffectIsWrittenWithItsAlwaysAsk(t *testing.T) {
	for _, c := range []struct {
		f    SettingField
		want []string
		not  []string
	}{
		{SettingField{Key: "alerts_to", Type: SettingText, Effect: EffectRedirect}, []string{`"effect":"redirect"`, `"always_ask":true`}, nil},
		{SettingField{Key: "title", Type: SettingText, Effect: EffectNone}, []string{`"effect":"none"`}, []string{"always_ask"}},
		{SettingField{Key: "old", Type: SettingText, AlwaysAsk: true}, []string{`"always_ask":true`}, []string{"effect"}},
		{SettingField{Key: "plain", Type: SettingText}, nil, []string{"effect", "always_ask"}},
	} {
		raw, err := json.Marshal(c.f)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range c.want {
			if !strings.Contains(string(raw), w) {
				t.Errorf("%s: %s has no %s", c.f.Key, raw, w)
			}
		}
		for _, n := range c.not {
			if strings.Contains(string(raw), n) {
				t.Errorf("%s: %s says %s", c.f.Key, raw, n)
			}
		}
		var back SettingField
		if err := json.Unmarshal(raw, &back); err != nil || back.Effect != c.f.Effect || back.Key != c.f.Key {
			t.Errorf("%s: read back %+v (%v)", c.f.Key, back, err)
		}
	}
}
