package nilda

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func TestATextBodyRoundTripsAndCoreTakesIt(t *testing.T) {
	text := "A mug for the morning.\nDishwasher safe.\n\n  Made in Graz.  "
	body := TextBody(text)
	if err := CheckBody(body); err != nil {
		t.Fatalf("Core would refuse %s: %v", body, err)
	}
	if got := BodyText(body); got != "A mug for the morning.\nDishwasher safe.\n\nMade in Graz." {
		t.Errorf("read back %q", got)
	}
	if string(TextBody("  \n\n ")) != "{}" || BodyText(json.RawMessage(`{}`)) != "" {
		t.Error("an empty text is Core's empty body, and reads back empty")
	}
	if !strings.Contains(string(TextBody("فنجان قهوه")), `"direction":"rtl"`) {
		t.Error("Persian words are a right-to-left paragraph")
	}
	for _, bad := range []string{`[]`, `"x"`, `{"children":[]}`, `{"root":"x"}`} {
		if CheckBody(json.RawMessage(bad)) == nil {
			t.Errorf("Core refuses %s; CheckBody took it", bad)
		}
	}
}

// A heading, a list and bold text written in Core's editor read as their words.
func TestBodyTextReadsWhatCoresEditorWrites(t *testing.T) {
	body := json.RawMessage(`{"root":{"type":"root","children":[
		{"type":"heading","tag":"h2","children":[{"type":"text","text":"Care","format":1}]},
		{"type":"list","listType":"bullet","children":[
			{"type":"listitem","children":[{"type":"text","text":"Hand wash"}]},
			{"type":"listitem","children":[{"type":"text","text":"No "},{"type":"text","text":"bleach","format":1}]}]}]}}`)
	if got := BodyText(body); got != "Care\n\nHand wash\n\nNo bleach" {
		t.Errorf("read %q", got)
	}
}

// CheckBody is Core's rule (internal/content bodyOrEmpty), and LocaleDirection uses Core's script list.
func TestTheBodyRuleAndTheDirectionAreCores(t *testing.T) {
	src := coreSource(t, "internal", "content", "service.go")
	rule := from(t, src, "func bodyOrEmpty(b json.RawMessage) ([]byte, error) {", "core content/service.go")
	for _, want := range []string{"if len(b) == 0 {", "if len(doc) == 0 {", "if !lexdoc.HasRoot(doc) {"} {
		if !strings.Contains(upTo(t, rule, "\n}", "bodyOrEmpty"), want) {
			t.Errorf("Core's body rule no longer has %q", want)
		}
	}
	loc := coreSource(t, "internal", "i18n", "locales.go")
	list := upTo(t, from(t, loc, "var rtlScripts = map[string]bool{", "core i18n/locales.go"), "}", "rtlScripts")
	core := regexp.MustCompile(`"([A-Z][a-z]{3})": true`).FindAllStringSubmatch(list, -1)
	if len(core) != len(rtlScripts) {
		t.Fatalf("Core lists %d right-to-left scripts, the SDK %d", len(core), len(rtlScripts))
	}
	for _, m := range core {
		if !rtlScripts[m[1]] {
			t.Errorf("Core's right-to-left script %s is missing", m[1])
		}
	}
	for code, want := range map[string]string{"fa": "rtl", "ar": "rtl", "he": "rtl", "ur": "rtl", "ckb": "rtl", "ku-Latn": "ltr", "de": "ltr", "zh": "ltr", "klingon": "ltr"} {
		if got := LocaleDirection(code); got != want {
			t.Errorf("LocaleDirection(%q) = %s, want %s", code, got, want)
		}
	}
}
