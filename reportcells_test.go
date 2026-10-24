package nilda

import (
	"encoding/json"
	"strings"
	"testing"
)

// A link cell is one of three shapes on the wire — {label, page}, {label, screen}, {label, install} — and exactly
// one target: a cell naming two would open whichever Core read first, and one naming none is a link to nowhere.
func TestReportLinkCellShapes(t *testing.T) {
	for _, c := range []struct {
		link ReportLink
		wire string
	}{
		{ReportLink{Label: "Open", Page: "orders"}, `{"label":"Open","page":"orders"}`},
		{ReportLink{Label: "Payments", Screen: ScreenPayments}, `{"label":"Payments","screen":"payments"}`},
		{ReportLink{Label: "Install", Install: "payment_gateway"}, `{"label":"Install","install":"payment_gateway"}`},
	} {
		b, err := json.Marshal(c.link)
		if err != nil || string(b) != c.wire {
			t.Errorf("%+v went on the wire as %s (%v), want %s", c.link, b, err, c.wire)
		}
		if err := c.link.Validate(); err != nil {
			t.Errorf("%s is a good link and was refused: %v", c.wire, err)
		}
	}
	for name, bad := range map[string]ReportLink{
		"no label":       {Page: "orders"},
		"no target":      {Label: "Open"},
		"two targets":    {Label: "Open", Page: "orders", Screen: ScreenPayments},
		"unknown screen": {Label: "Open", Screen: "wp-admin"},
		"install path":   {Label: "Install", Install: "../plugins"},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: %+v was accepted", name, bad)
		}
	}
	for _, s := range ReportScreens() {
		if err := (ReportLink{Label: "x", Screen: s}).Validate(); err != nil {
			t.Errorf("ReportScreens lists %q and Validate refuses it: %v", s, err)
		}
	}
}

// An action cell names one of the page's declared actions, or is empty (no button on that row). A key the page
// does not declare would be a button Core refuses to run — so the report is refused before it reaches the owner.
func TestReportActionNeedsADeclaredAction(t *testing.T) {
	page := AdminPage{Key: "ready", Kind: AdminPageReport, Actions: []RowAction{{Key: "add_zones", Label: "Add zones"}}}
	cols := []ListColumn{{Key: "check", Label: "Check"}, {Key: "do", Label: "Do", Type: ColumnAction},
		{Key: "go", Label: "Go", Type: ColumnLink}}
	good := AdminReportResult{Columns: cols, Rows: []map[string]any{
		{"key": "zones", "check": "Shipping", "do": "add_zones", "go": ReportLink{Label: "Zones", Page: "zones"}},
		{"key": "tax", "check": "Tax", "do": "", "go": map[string]any{"label": "Payments", "screen": "payments"}},
		{"key": "imprint", "check": "Imprint"},
	}}
	if err := good.Validate(page); err != nil {
		t.Fatalf("a report whose action cells name declared actions was refused: %v", err)
	}
	bad := AdminReportResult{Columns: cols, Rows: []map[string]any{{"key": "x", "do": "refund"}}}
	if err := bad.Validate(page); err == nil || !strings.Contains(err.Error(), "refund") {
		t.Errorf("an action cell naming an undeclared action was accepted (err=%v)", err)
	}
	noKey := AdminReportResult{Columns: cols, Rows: []map[string]any{{"do": "add_zones"}}}
	if err := noKey.Validate(page); err == nil {
		t.Error("a row with a button and no key was accepted — the press could not say which row it was")
	}
	badLink := AdminReportResult{Columns: cols, Rows: []map[string]any{{"key": "x", "go": map[string]any{"label": "x"}}}}
	if err := badLink.Validate(page); err == nil {
		t.Error("a link cell with no target was accepted")
	}
}
