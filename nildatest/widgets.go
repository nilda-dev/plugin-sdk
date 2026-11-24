package nildatest

import (
	"context"
	"encoding/json"
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

// CheckWidget asks a WidgetChecker about one placed widget the way Core does — the request crosses as JSON, so the
// config holds what the checker will really be sent (a number is a float64) — and fails the test on an error or on
// an answer nilda.ValidateWidgetWarnings refuses. It returns the warnings.
func CheckWidget(t testing.TB, c nilda.WidgetChecker, req nilda.WidgetCheckRequest) []nilda.WidgetWarning {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("widget.check request: %v", err)
	}
	var sent nilda.WidgetCheckRequest
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("widget.check request: %v", err)
	}
	ws, err := c.CheckWidget(context.Background(), sent)
	if err != nil {
		t.Fatalf("widget.check %q: %v", req.Type, err)
	}
	if err := nilda.ValidateWidgetWarnings(ws); err != nil {
		t.Fatalf("widget.check %q: %v", req.Type, err)
	}
	return ws
}
