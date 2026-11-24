package nildatest

import (
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Widgets holds every widget a WidgetProvider offers to nilda.WidgetDef.Validate — the limits its fields declare
// (max_length, min_items, max_items, pattern) and its own render cap — and fails the test naming the widget and the
// field. Serve drops such a widget from the describe answer and only logs it; this is where you hear it.
func Widgets(t testing.TB, wp nilda.WidgetProvider) {
	t.Helper()
	for _, d := range wp.Widgets() {
		if err := d.Validate(); err != nil {
			t.Fatalf("%v", err)
		}
	}
}
