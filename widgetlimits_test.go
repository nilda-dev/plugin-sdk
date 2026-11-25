package nilda

import (
	"context"
	"encoding/json"
	"regexp/syntax"
	"strings"
	"testing"
)

// The widget limits (v0.73.0): what a widget field may declare about its VALUE (max_length, min_items, max_items,
// pattern), and a widget's own render cap.

func TestTheWidgetLimitsCrossWithCoresValidationNames(t *testing.T) {
	b, err := json.Marshal(WidgetDef{Type: "list", Label: "List", MaxHTMLBytes: 128 << 10, Fields: []WidgetField{
		{Key: "title", Type: FieldText, MaxLength: 80, Pattern: `^[A-Z]`},
		{Key: "rows", Type: FieldRepeater, MinItems: 1, MaxItems: 12},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"max_html_bytes":131072`, `"max_length":80`, `"pattern":"^[A-Z]"`,
		`"min_items":1`, `"max_items":12`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("%s does not carry %s: Core would read no limit", b, want)
		}
	}
	// A widget declaring none of them is the bytes it always was.
	plain, _ := json.Marshal(WidgetDef{Type: "t", Label: "l", Fields: []WidgetField{{Key: "k", Label: "K", Type: FieldText}}})
	if want := `{"type":"t","label":"l","fields":[{"key":"k","label":"K","type":"text"}]}`; string(plain) != want {
		t.Errorf("a widget without limits marshals as %s, want %s", plain, want)
	}
}

func TestAWidgetsLimitsAreValidated(t *testing.T) {
	ok := func(f WidgetField) WidgetDef { return WidgetDef{Type: "w", Label: "W", Fields: []WidgetField{f}} }
	for _, good := range []WidgetDef{
		ok(WidgetField{Key: "k", Type: FieldText}),
		ok(WidgetField{Key: "k", Type: FieldTextarea, MaxLength: MaxWidgetFieldLengthCeiling, Pattern: `^\d{4}$`}),
		ok(WidgetField{Key: "k", Type: FieldEmail, MaxLength: 200}),
		ok(WidgetField{Key: "k", Type: FieldRepeater, MinItems: 2, MaxItems: 2,
			Fields: []WidgetField{{Key: "s", Type: FieldText, MaxLength: 40}}}),
		ok(WidgetField{Key: "k", Type: FieldGallery, MaxItems: MaxWidgetFieldItemsCeiling}),
		ok(WidgetField{Key: "k", Type: FieldMultiSelect, Choices: []string{"a"}, MinItems: 1}),
		{Type: "w", Label: "W", MaxHTMLBytes: MaxWidgetHTMLBytesCeiling},
	} {
		if err := good.Validate(); err != nil {
			t.Errorf("%+v: %v", good, err)
		}
	}
	for want, bad := range map[string]WidgetDef{
		"max_length":     ok(WidgetField{Key: "k", Type: FieldText, MaxLength: -1}),
		"above":          ok(WidgetField{Key: "k", Type: FieldText, MaxLength: MaxWidgetFieldLengthCeiling + 1}),
		"not a text":     ok(WidgetField{Key: "k", Type: FieldNumber, MaxLength: 3}),
		"pattern":        ok(WidgetField{Key: "k", Type: FieldText, Pattern: `(`}),
		"bytes":          ok(WidgetField{Key: "k", Type: FieldText, Pattern: strings.Repeat("a", MaxWidgetFieldPatternLen+1)}),
		"no list":        ok(WidgetField{Key: "k", Type: FieldText, MaxItems: 2}),
		"min_items":      ok(WidgetField{Key: "k", Type: FieldRepeater, MinItems: -1}),
		"above its":      ok(WidgetField{Key: "k", Type: FieldRepeater, MinItems: 3, MaxItems: 2}),
		"ceiling":        ok(WidgetField{Key: "k", Type: FieldRepeater, MaxItems: MaxWidgetFieldItemsCeiling + 1}),
		`"rows.s"`:       ok(WidgetField{Key: "rows", Type: FieldRepeater, Fields: []WidgetField{{Key: "s", Type: FieldText, Pattern: `[`}}}),
		"max_html_bytes": {Type: "w", Label: "W", MaxHTMLBytes: MaxWidgetHTMLBytesCeiling + 1},
		"negative":       {Type: "w", Label: "W", MaxHTMLBytes: -1},
	} {
		err := bad.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: got %v, want an error saying %q", bad, err, want)
		}
	}
}

// A widget whose limits Core could not honour is dropped from the describe answer and logged; the rest stay.
func TestDescribeDropsAWidgetWhoseLimitsAreInvalid(t *testing.T) {
	out, ok, err := dispatchProvided(context.Background(), limitWidgets{}, HookWidgetDescribe, []byte(`{}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	var res widgetDescribeResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Widgets) != 1 || res.Widgets[0].Type != "good" {
		t.Errorf("describe answered %s, want only the widget whose limits hold", out)
	}
}

type limitWidgets struct{ widgetOnly }

func (limitWidgets) Widgets() []WidgetDef {
	return []WidgetDef{
		{Type: "bad", Label: "Bad", Fields: []WidgetField{{Key: "k", Type: FieldText, Pattern: `(`}}},
		{Type: "good", Label: "Good", Fields: []WidgetField{{Key: "k", Type: FieldText, MaxLength: 40, Pattern: `^a`}}},
	}
}

// Init refuses a widget whose limits Core could not honour, naming the widget and the field — the author hears it
// when the plugin starts, not from a log line about a widget that quietly never appears (review of #74).
func TestInitRefusesAWidgetWhoseLimitsAreInvalid(t *testing.T) {
	s := &pluginServer{handler: limitWidgets{}}
	_, err := s.wire(InitResult{})
	if err == nil || !strings.Contains(err.Error(), `widget "bad"`) || !strings.Contains(err.Error(), `field "k"`) {
		t.Fatalf("Init answered %v, want a refusal naming widget \"bad\" and field \"k\"", err)
	}
	// A provider whose widgets hold, and one declaring no limits at all, start as they always did.
	for _, h := range []Handler{widgetOnly{}, &provider{}} {
		if _, err := (&pluginServer{handler: h}).wire(InitResult{}); err != nil {
			t.Errorf("%T: %v", h, err)
		}
	}
}

// A PATTERN COSTS ITS PROGRAM TIMES THE TEXT (Core's patternsCostLittle, security review 2026-11-25 M1). Go's regexp is
// linear in the text TIMES the compiled program, so a 19-byte pattern can take 0.4 s on one long value — on every save.
// Core refuses a pattern on a field with no max_length, one past MaxWidgetFieldPatternInsts instructions, and one whose
// instructions times max_length is past MaxWidgetFieldPatternWork; the SDK refuses the same widgets at Init, naming the
// field and the number, rather than letting Core drop the widget from the describe answer.
func TestAWidgetPatternsCostIsBounded(t *testing.T) {
	insts := func(pattern string) int {
		re, err := syntax.Parse(pattern, syntax.Perl)
		if err != nil {
			t.Fatal(err)
		}
		prog, err := syntax.Compile(re.Simplify())
		if err != nil {
			t.Fatal(err)
		}
		return len(prog.Inst)
	}
	const costly = `(?:\w|\s|\d){1000}z` // 19 bytes, about a thousand instructions
	n := insts(costly)
	ok := func(f WidgetField) WidgetDef { return WidgetDef{Type: "w", Label: "W", Fields: []WidgetField{f}} }
	for name, good := range map[string]WidgetDef{
		"at the bound": ok(WidgetField{Key: "k", Type: FieldText, MaxLength: MaxWidgetFieldPatternWork / n, Pattern: costly}),
		"a cheap one":  ok(WidgetField{Key: "k", Type: FieldTextarea, MaxLength: MaxWidgetFieldLengthCeiling, Pattern: `^\S`}),
	} {
		if err := good.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	for want, bad := range map[string]WidgetDef{
		"needs a max_length": ok(WidgetField{Key: "k", Type: FieldText, Pattern: `^a`}),
		"instructions; the bound is 2000": ok(WidgetField{Key: "k", Type: FieldText, MaxLength: 10,
			Pattern: `a{1000}b{1000}c{100}`}),
		"lower max_length to": ok(WidgetField{Key: "k", Type: FieldText, MaxLength: MaxWidgetFieldPatternWork/n + 1, Pattern: costly}),
		`field "rows.s"`: ok(WidgetField{Key: "rows", Type: FieldRepeater,
			Fields: []WidgetField{{Key: "s", Type: FieldEmail, Pattern: `@`}}}),
	} {
		err := bad.Validate()
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: got %v, want an error saying %q", bad, err, want)
		}
	}
}
