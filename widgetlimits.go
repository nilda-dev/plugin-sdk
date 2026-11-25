package nilda

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
)

// What a widget may declare about its values and its render (v0.73.0). All of it is optional: a widget that declares
// none of it is the widget it always was, on the wire and on Core's side.

// Bounds on the widget limits.
const (
	// MaxWidgetHTMLBytesCeiling is the most a widget may raise its own render cap to (WidgetDef.MaxHTMLBytes):
	// bytes. A widget that declares no cap is held to MaxWidgetHTMLBytes.
	MaxWidgetHTMLBytesCeiling = 256 << 10
	// MaxWidgetFieldLengthCeiling bounds WidgetField.MaxLength: characters.
	MaxWidgetFieldLengthCeiling = 100_000
	// MaxWidgetFieldItemsCeiling bounds WidgetField.MinItems and MaxItems: a count of rows or items.
	MaxWidgetFieldItemsCeiling = 1000
	// MaxWidgetFieldPatternLen bounds WidgetField.Pattern: bytes.
	MaxWidgetFieldPatternLen = 500
	// MaxWidgetFieldPatternInsts bounds the program WidgetField.Pattern compiles to (v0.75.0): instructions, counted as
	// Core counts them (regexp/syntax, Perl flags, simplified). Go's regexp is linear in the text TIMES the program, so
	// a short pattern can still be slow: `(?:\w|\s|\d){1000}z` is 19 bytes and about a thousand instructions.
	MaxWidgetFieldPatternInsts = 2000
	// MaxWidgetFieldPatternWork bounds a pattern's instructions times its field's MaxLength (v0.75.0): the steps one
	// value's match may take, about 13 ms on Core. A pattern needs a MaxLength, so the text it runs on is bounded.
	MaxWidgetFieldPatternWork = 1_000_000
)

// widgetTextLimitTypes are the types Core holds to max_length and pattern; widgetItemLimitTypes the ones it holds
// to min_items and max_items. A limit on any other type would be stored and never applied, so it is refused.
// Both lists are held to Core's source by TestTheWidgetFieldLimitsAreCores.
func widgetTextLimitTypes() []string { return []string{FieldEmail, FieldText, FieldTextarea} }
func widgetItemLimitTypes() []string {
	return []string{FieldCheckbox, FieldGallery, FieldMultiSelect, FieldRepeater}
}

// Validate reports whether Core can honour what this widget declares about its values and its render: each
// field's MaxLength, MinItems, MaxItems and Pattern (at every level) and the widget's MaxHTMLBytes. It checks
// those and nothing else — Core's other bounds on a describe answer are §4.1's — so a widget that declares none of
// them always passes. Serve refuses Init for a plugin whose widgets fail it, naming the widget and the field (and,
// should Widgets change after Init, drops such a widget from the describe answer and logs why); call it in your own
// test (nildatest.Widgets does) to hear it earlier.
func (d WidgetDef) Validate() error {
	if d.MaxHTMLBytes < 0 || d.MaxHTMLBytes > MaxWidgetHTMLBytesCeiling {
		return fmt.Errorf("nilda: widget %q: max_html_bytes %d is negative or above %d", d.Type, d.MaxHTMLBytes, MaxWidgetHTMLBytesCeiling)
	}
	for _, f := range d.Fields {
		if err := validateWidgetFieldLimits(f, ""); err != nil {
			return fmt.Errorf("nilda: widget %q: %w", d.Type, err)
		}
	}
	return nil
}

func validateWidgetFieldLimits(f WidgetField, parent string) error {
	path := f.Key
	if parent != "" {
		path = parent + "." + f.Key
	}
	if f.MaxLength < 0 || f.MaxLength > MaxWidgetFieldLengthCeiling {
		return fmt.Errorf("field %q: max_length %d is negative or above %d", path, f.MaxLength, MaxWidgetFieldLengthCeiling)
	}
	if (f.MaxLength != 0 || f.Pattern != "") && !slices.Contains(widgetTextLimitTypes(), f.Type) {
		return fmt.Errorf("field %q: max_length and pattern apply to %v; %q is not a text Core holds to them", path, widgetTextLimitTypes(), f.Type)
	}
	if len(f.Pattern) > MaxWidgetFieldPatternLen {
		return fmt.Errorf("field %q: its pattern is %d bytes; the bound is %d", path, len(f.Pattern), MaxWidgetFieldPatternLen)
	}
	if f.Pattern != "" {
		if _, err := regexp.Compile(f.Pattern); err != nil {
			return fmt.Errorf("field %q: its pattern does not compile as a Go (RE2) regexp: %v", path, err)
		}
		if err := patternCost(f, path); err != nil {
			return err
		}
	}
	for _, n := range []struct {
		name string
		v    int
	}{{"min_items", f.MinItems}, {"max_items", f.MaxItems}} {
		if n.v < 0 || n.v > MaxWidgetFieldItemsCeiling {
			return fmt.Errorf("field %q: %s %d is negative or above the ceiling %d", path, n.name, n.v, MaxWidgetFieldItemsCeiling)
		}
	}
	if (f.MinItems != 0 || f.MaxItems != 0) && !slices.Contains(widgetItemLimitTypes(), f.Type) {
		return fmt.Errorf("field %q: min_items and max_items apply to %v; %q holds no list", path, widgetItemLimitTypes(), f.Type)
	}
	if f.MaxItems != 0 && f.MinItems > f.MaxItems {
		return fmt.Errorf("field %q: min_items %d is above its max_items %d", path, f.MinItems, f.MaxItems)
	}
	for _, sub := range f.Fields {
		if err := validateWidgetFieldLimits(sub, path); err != nil {
			return err
		}
	}
	return nil
}

// patternCost is Core's rule for what a field's pattern costs at match time (core internal/plugin/widgets.go,
// patternsCostLittle, held to it by TestTheWidgetFieldLimitsAreCores): a program of at most MaxWidgetFieldPatternInsts
// instructions, on a field with a MaxLength, whose instructions times MaxLength is at most MaxWidgetFieldPatternWork.
// Checked in Core's order with Core's numbers, so a widget the SDK passes at Init is one Core keeps.
func patternCost(f WidgetField, path string) error {
	re, err := syntax.Parse(f.Pattern, syntax.Perl)
	if err != nil {
		return fmt.Errorf("field %q: its pattern does not parse: %v", path, err)
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return fmt.Errorf("field %q: its pattern does not compile: %v", path, err)
	}
	switch n := len(prog.Inst); {
	case n > MaxWidgetFieldPatternInsts:
		return fmt.Errorf("field %q: its pattern compiles to %d instructions; the bound is %d", path, n, MaxWidgetFieldPatternInsts)
	case f.MaxLength <= 0:
		return fmt.Errorf("field %q: a pattern needs a max_length, so the text it is matched against is bounded", path)
	case f.MaxLength*n > MaxWidgetFieldPatternWork:
		return fmt.Errorf("field %q: its pattern (%d instructions) on a max_length of %d is %d steps a value; "+
			"the bound is %d — lower max_length to %d or simplify the pattern",
			path, n, f.MaxLength, f.MaxLength*n, MaxWidgetFieldPatternWork, MaxWidgetFieldPatternWork/n)
	}
	return nil
}

// checkWidgetLimits stops Init for a WidgetProvider whose widgets fail WidgetDef.Validate, with the sentence naming
// the widget and the field: the author hears it when the plugin starts, not from a widget that never appears. A
// provider declaring no limits always passes.
func checkWidgetLimits(h Handler) error {
	wp, is := h.(WidgetProvider)
	if !is {
		return nil
	}
	for _, d := range wp.Widgets() {
		if err := d.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// describedWidgets is the describe answer: the widgets whose limits Core can honour, the rest dropped and logged.
// A widget declaring no limits always passes, so this changes nothing for a widget that does not use them.
func describedWidgets(defs []WidgetDef) []WidgetDef {
	kept := make([]WidgetDef, 0, len(defs)) // an empty answer is [], never null
	for _, d := range defs {
		if err := d.Validate(); err != nil {
			Log().Warn("dropping a widget whose limits Core cannot honour; fix it and it will be offered",
				"widget", d.Type, "error", err)
			continue
		}
		kept = append(kept, d)
	}
	return kept
}
