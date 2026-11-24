package nilda

import (
	"regexp"
	"slices"
	"sort"
	"testing"
)

// The widget field limits are Core's own validation rules, named as Core names them and offered only on the types
// Core enforces them on. Read from Core's source beside this module (skipped without it, as every truth test is).
func TestTheWidgetFieldLimitsAreCores(t *testing.T) {
	types := coreSource(t, "internal", "contenttype", "types.go")
	validation := from(t, types, "type Validation struct", "core/internal/contenttype/types.go")
	for _, tag := range []string{`json:"max_length,omitempty"`, `json:"min_items,omitempty"`,
		`json:"max_items,omitempty"`, `json:"pattern,omitempty"`} {
		if !regexp.MustCompile(regexp.QuoteMeta(tag)).MatchString(upTo(t, validation, "\n}", "Validation")) {
			t.Errorf("core's contenttype.Validation has no %s: WidgetField sends a limit Core no longer reads", tag)
		}
	}

	fieldtypes := coreSource(t, "internal", "contenttype", "fieldtypes.go")
	registered := func(checkers ...string) []string {
		var out []string
		for _, c := range checkers {
			for _, m := range regexp.MustCompile(`reg\("([a-z_]+)", [A-Za-z]+, (?:true|false), `+c+`\)`).FindAllStringSubmatch(fieldtypes, -1) {
				if slices.Contains(FieldTypes(), m[1]) { // password is Core's and not for plugins
					out = append(out, m[1])
				}
			}
		}
		sort.Strings(out)
		return out
	}
	if got, want := widgetTextLimitTypes(), registered("checkText", "checkEmail"); !slices.Equal(got, want) {
		t.Errorf("max_length and pattern: the SDK takes them on %v, Core enforces them on %v", got, want)
	}
	// The repeater's rows are counted in validate.go (structuralErrors), not by a registered checker.
	validate := coreSource(t, "internal", "contenttype", "validate.go")
	from(t, validate, "f.Validation.MaxItems != nil && len(rows)", "core/internal/contenttype/validate.go")
	if got, want := widgetItemLimitTypes(), append(registered("checkMultiChoice", "checkUUIDList"), FieldRepeater); !sameSet(got, want) {
		t.Errorf("min_items and max_items: the SDK takes them on %v, Core enforces them on %v", got, want)
	}

	// The default render cap is Core's, and the ceiling is above it.
	widgets := coreSource(t, "internal", "plugin", "widgets.go")
	from(t, widgets, "const maxWidgetHTMLBytes = 64 << 10", "core/internal/plugin/widgets.go")
	if MaxWidgetHTMLBytesCeiling < MaxWidgetHTMLBytes {
		t.Errorf("the ceiling %d is below the default %d", MaxWidgetHTMLBytesCeiling, MaxWidgetHTMLBytes)
	}
}

func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	sort.Strings(a)
	sort.Strings(b)
	return slices.Equal(a, b)
}
