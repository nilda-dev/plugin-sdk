package nilda

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// SH-I7 (full walk 2026-11-26, v0.76.0): a plugin form had one generic "Save", and on the shop's refund form that Save
// moved money at once. A form names its own button (save_label) and asks a question before it runs (confirm); Core draws
// both (core #709). Both are words the owner reads, so the SDK holds them to Core's bounds — save_label a label, confirm
// a help text — and to Core's hidden-character rule, and a form that breaks either does not marshal into plugin.json.
func TestAMoneyFormNamesItsButtonAndAsksBeforeItSaves(t *testing.T) {
	form := func(label, confirm string) PageForm {
		return PageForm{Edit: true, AlwaysAsk: true, SaveLabel: label, Confirm: confirm,
			Fields: []FormField{{Key: "amount", Label: "Amount", Type: FormMoney}}}
	}

	ok := form("Refund 3 items", "The money goes back to the customer's card. This cannot be undone.")
	if err := ok.Validate(); err != nil {
		t.Fatalf("a plain label and question were refused: %v", err)
	}
	raw, err := json.Marshal(ok)
	if err != nil {
		t.Fatalf("a plain label and question did not marshal: %v", err)
	}
	for _, want := range []string{`"save_label":"Refund 3 items"`,
		`"confirm":"The money goes back to the customer's card. This cannot be undone."`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("plugin.json must carry %s; it reads %s", want, raw)
		}
	}
	var back PageForm
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.SaveLabel != ok.SaveLabel || back.Confirm != ok.Confirm {
		t.Errorf("read back: %+v", back)
	}

	// Neither set: the form is the one it always was, and plugin.json does not mention either key.
	raw, err = json.Marshal(form("", ""))
	if err != nil || strings.Contains(string(raw), "save_label") || strings.Contains(string(raw), "confirm") {
		t.Errorf("an unset label and question are written: %s, %v", raw, err)
	}

	// The bounds are Core's, counted in characters: a Persian label as long as an English one passes, the half-space
	// (zero-width non-joiner) Persian spells with included.
	for _, good := range []PageForm{
		form(strings.Repeat("x", MaxManifestLabelLen), strings.Repeat("y", MaxManifestHelpLen)),
		form("بازپرداخت سه کالا", "پول به کارت مشتری برمی\u200Cگردد."),
	} {
		if err := good.Validate(); err != nil {
			t.Errorf("a value at Core's bound was refused: %v", err)
		}
	}

	for name, bad := range map[string]PageForm{
		"a button label that is a paragraph":       form(strings.Repeat("x", MaxManifestLabelLen+1), ""),
		"a question past help's bound":             form("", strings.Repeat("y", MaxManifestHelpLen+1)),
		"a label holding a direction override":     form("Refund \u202E3 items", ""),
		"a question holding a direction isolate":   form("", "Refund \u2066all\u2069?"),
		"a label holding a zero-width space":       form("Ref\u200Bund", ""),
		"a question holding a control character":   form("", "Refund\x07?"),
		"a joiner splitting a Latin word":          form("Ref\u200Dund", ""),
		"a label holding an invisible filler":      form("Refund\u3164", ""),
		"a question that is not UTF-8":             form("", "Refund \xff"),
		"a question holding a line separator mark": form("", "Refund\u2028now"),
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: Validate passed it", name)
		}
		if _, err := json.Marshal(bad); err == nil {
			t.Errorf("%s: it marshalled into plugin.json", name)
		}
		if _, err := json.Marshal(AdminPage{Key: "refunds", Label: "Refunds", Kind: "list", Form: &bad}); err == nil {
			t.Errorf("%s: a list page holding it marshalled", name)
		}
	}
}

// The hidden-character rule is Core's hiddenRune (core internal/plugin/plaintext.go), the one validateForm applies to
// every word of a form: the SDK's list of invisible and direction-changing characters is Core's, character for
// character.
func TestTheFormsHiddenCharacterRuleIsCores(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "plaintext.go")
	body := upTo(t, from(t, src, "func isBidiOrInvisible(r rune) bool {", "core plaintext.go"), "return false",
		"core plaintext.go isBidiOrInvisible")
	if !strings.Contains(src, "unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r), isBidiOrInvisible(r)") {
		t.Error("core's hiddenRune no longer refuses Cc, Cf and isBidiOrInvisible: hold the SDK's hiddenRune to what it does now")
	}
	var core []rune
	for _, lit := range regexp.MustCompile(`'(?:\\u[0-9A-Fa-f]{4}|[^'\\])'`).FindAllString(body, -1) {
		s, err := strconv.Unquote(lit)
		if err != nil || utf8.RuneCountInString(s) != 1 {
			t.Fatalf("core's isBidiOrInvisible holds %s, which this guard cannot read: %v", lit, err)
		}
		core = append(core, []rune(s)[0])
	}
	if len(core) == 0 {
		t.Fatal("read no character from core's isBidiOrInvisible — repoint this guard")
	}
	for _, r := range core {
		if !isBidiOrInvisible(r) {
			t.Errorf("core treats %U as invisible; the SDK's isBidiOrInvisible does not", r)
		}
	}
	if n := len(bidiOrInvisible); n != len(core) {
		t.Errorf("the SDK lists %d invisible characters and core %d", n, len(core))
	}
}
