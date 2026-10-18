package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestARowActionReachesTheHandlerAndAnswersInTheFieldTheAdminReads.
//
// The admin shows `message` and nothing else — an action returning `{"result":"refunded"}` runs perfectly
// and the person who pressed the button is told "Done". That is the silent failure this typed reply exists
// to prevent, so the field name is asserted on the WIRE rather than through the struct.
func TestARowActionReachesTheHandlerAndAnswersInTheFieldTheAdminReads(t *testing.T) {
	var got AdminAction
	out, handled, err := DispatchAdminAction(context.Background(), AdminActionHook,
		[]byte(`{"page":"orders","action":"refund","id":"ord_42"}`),
		func(_ context.Context, a AdminAction) (AdminActionResult, error) {
			got = a
			return AdminActionResult{Message: "Refunded order ord_42"}, nil
		})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got.Page != "orders" || got.Action != "refund" || got.ID != "ord_42" {
		t.Errorf("the press arrived as %+v", got)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("the reply is not JSON: %v (%s)", err, out)
	}
	if wire["message"] != "Refunded order ord_42" {
		t.Errorf("the admin reads `message`; the reply was %s", out)
	}
}

// A handler's error must reach the owner as the reason, not be swallowed into a success toast.
func TestARowActionErrorReachesTheOwner(t *testing.T) {
	boom := errors.New("the payment gateway refused")
	_, handled, err := DispatchAdminAction(context.Background(), AdminActionHook,
		[]byte(`{"page":"orders","action":"refund","id":"1"}`),
		func(context.Context, AdminAction) (AdminActionResult, error) { return AdminActionResult{}, boom })
	if !handled || !errors.Is(err, boom) {
		t.Errorf("handled=%v err=%v — the reason must survive", handled, err)
	}
}

// A plugin with hooks of its own must be able to pass a non-action hook through.
func TestANonActionHookIsNotClaimedByTheAdminDispatcher(t *testing.T) {
	if _, handled, _ := DispatchAdminAction(context.Background(), "content.saved", nil, nil); handled {
		t.Error("a non-action hook must not be claimed")
	}
}

// A button that is declared and has no handler must SAY so rather than answering "Done" — an owner
// pressing refund and being told it worked is worse than an error.
func TestAPressWithNoHandlerIsAnErrorNotASilentOK(t *testing.T) {
	_, handled, err := DispatchAdminAction(context.Background(), AdminActionHook, []byte(`{}`), nil)
	if !handled || err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("handled=%v err=%v", handled, err)
	}
}

// The payload crosses a process boundary, so it is untrusted.
func TestAMalformedAdminActionPayloadIsAnErrorNotAPanic(t *testing.T) {
	if _, handled, err := DispatchAdminAction(context.Background(), AdminActionHook, []byte("{"),
		func(context.Context, AdminAction) (AdminActionResult, error) { return AdminActionResult{}, nil }); !handled || err == nil {
		t.Errorf("handled=%v err=%v", handled, err)
	}
}

// TestAReportOpenReachesTheHandlerAndAnswersInTheFieldsTheAdminReads mirrors the row-action test above: the
// request's Page/Params arrive intact, and the reply's columns/rows/summary are what actually crosses,
// asserted on the wire rather than through the struct.
func TestAReportOpenReachesTheHandlerAndAnswersInTheFieldsTheAdminReads(t *testing.T) {
	var got AdminReportRequest
	out, handled, err := DispatchAdminReport(context.Background(), AdminReportHook,
		[]byte(`{"page":"sales","params":{"from":"2026-08-01","to":"2026-09-01"}}`),
		func(_ context.Context, req AdminReportRequest) (AdminReportResult, error) {
			got = req
			return AdminReportResult{
				Columns: []ListColumn{{Key: "day", Label: "Day"}, {Key: "total", Label: "Revenue", Type: "number"}},
				Rows:    []map[string]any{{"day": "2026-08-01", "total": 4200}},
				Summary: map[string]any{"total_revenue": 4200},
			}, nil
		})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got.Page != "sales" || got.Params["from"] != "2026-08-01" || got.Params["to"] != "2026-09-01" {
		t.Errorf("the request arrived as %+v", got)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("the reply is not JSON: %v (%s)", err, out)
	}
	if _, ok := wire["columns"]; !ok {
		t.Errorf("the admin reads `columns`; the reply was %s", out)
	}
	if _, ok := wire["rows"]; !ok {
		t.Errorf("the admin reads `rows`; the reply was %s", out)
	}
	summary, _ := wire["summary"].(map[string]any)
	if summary["total_revenue"] != float64(4200) {
		t.Errorf("the admin reads `summary`; the reply was %s", out)
	}
}

// A handler's error must reach the owner as the reason a report page failed to load, not an empty table.
func TestAReportErrorReachesTheOwner(t *testing.T) {
	boom := errors.New("the reporting query timed out")
	_, handled, err := DispatchAdminReport(context.Background(), AdminReportHook,
		[]byte(`{"page":"sales"}`),
		func(context.Context, AdminReportRequest) (AdminReportResult, error) { return AdminReportResult{}, boom })
	if !handled || !errors.Is(err, boom) {
		t.Errorf("handled=%v err=%v — the reason must survive", handled, err)
	}
}

// A plugin with hooks of its own must be able to pass a non-report hook through.
func TestANonReportHookIsNotClaimedByTheAdminReportDispatcher(t *testing.T) {
	if _, handled, _ := DispatchAdminReport(context.Background(), "content.saved", nil, nil); handled {
		t.Error("a non-report hook must not be claimed")
	}
}

// A report page declared with no handler must SAY so rather than answering an empty table — an owner
// opening a report and seeing "no data" is worse than an error naming a plugin bug.
func TestAReportOpenWithNoHandlerIsAnErrorNotASilentEmptyTable(t *testing.T) {
	_, handled, err := DispatchAdminReport(context.Background(), AdminReportHook, []byte(`{}`), nil)
	if !handled || err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("handled=%v err=%v", handled, err)
	}
}

// The payload crosses a process boundary, so it is untrusted.
func TestAMalformedAdminReportPayloadIsAnErrorNotAPanic(t *testing.T) {
	if _, handled, err := DispatchAdminReport(context.Background(), AdminReportHook, []byte("{"),
		func(context.Context, AdminReportRequest) (AdminReportResult, error) { return AdminReportResult{}, nil }); !handled || err == nil {
		t.Errorf("handled=%v err=%v", handled, err)
	}
}

// A plugin with a report page AND a calendar page routes the two opens with their own dispatchers in one
// HandleHook, so each must pass the other's hook through untouched.
func TestDispatchAdminCalendarIgnoresOtherHooks(t *testing.T) {
	if _, handled, err := DispatchAdminCalendar(context.Background(), AdminReportHook, nil, nil); handled || err != nil {
		t.Errorf("a report open reached the calendar dispatcher: handled=%v err=%v", handled, err)
	}
}

// A calendar page declared with no handler must SAY so rather than drawing an empty grid — an owner
// opening the calendar and seeing nothing is worse than an error naming a plugin bug.
func TestDispatchAdminCalendarNeedsAHandler(t *testing.T) {
	_, handled, err := DispatchAdminCalendar(context.Background(), AdminCalendarHook, []byte(`{}`), nil)
	if !handled || err == nil || !strings.Contains(err.Error(), "no handler") {
		t.Errorf("handled=%v err=%v", handled, err)
	}
}

// TestDispatchAdminCalendarRoundTrip mirrors the report round-trip: the open's range and filters arrive
// intact, and the answer crosses in exactly the JSON keys §19.1 pins — asserted against a literal, not
// through the struct, because what Core and the app read is the wire.
func TestDispatchAdminCalendarRoundTrip(t *testing.T) {
	var got AdminCalendarRequest
	out, handled, err := DispatchAdminCalendar(context.Background(), AdminCalendarHook,
		[]byte(`{"page":"calendar","from":"2026-10-19T00:00:00Z","to":"2026-10-26T00:00:00Z","params":{"show":"Active"}}`),
		func(_ context.Context, req AdminCalendarRequest) (AdminCalendarResult, error) {
			got = req
			return AdminCalendarResult{
				Events: []CalendarEvent{
					{ID: "bk_7", Title: "Dinner · 2 guests", Subtitle: "Table 4",
						Start: "2026-10-19T19:00:00+02:00", End: "2026-10-19T21:00:00+02:00",
						Lane: "Table 4", Tone: "blue"},
					{ID: "stay_2", Title: "Stay · 2 guests", StartDate: "2026-10-20", EndDate: "2026-10-23",
						Tone: "green"},
				},
				Shaded: []CalendarShade{{StartDate: "2026-10-25", EndDate: "2026-10-26", Label: "Closed"}},
			}, nil
		})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got.Page != "calendar" || got.From != "2026-10-19T00:00:00Z" || got.To != "2026-10-26T00:00:00Z" ||
		got.Params["show"] != "Active" {
		t.Errorf("the open arrived as %+v", got)
	}
	want := `{"events":[{"id":"bk_7","title":"Dinner · 2 guests","subtitle":"Table 4","start":"2026-10-19T19:00:00+02:00","end":"2026-10-19T21:00:00+02:00","lane":"Table 4","tone":"blue"},` +
		`{"id":"stay_2","title":"Stay · 2 guests","start_date":"2026-10-20","end_date":"2026-10-23","tone":"green"}],` +
		`"shaded":[{"start_date":"2026-10-25","end_date":"2026-10-26","label":"Closed"}]}`
	if string(out) != want {
		t.Errorf("the answer crosses as\n%s\nwant\n%s", out, want)
	}
}

// The kind's name is the wire word Core and the app switch on, and a calendar page's rows_page crosses
// in the key they read it from.
func TestCalendarKindIsAKind(t *testing.T) {
	if AdminPageCalendar != "calendar" {
		t.Fatalf("AdminPageCalendar is %q, not \"calendar\"", AdminPageCalendar)
	}
	b, err := json.Marshal(AdminPage{Key: "calendar", Label: "Calendar", Kind: AdminPageCalendar, RowsPage: "bookings"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"rows_page":"bookings"`) {
		t.Errorf("a calendar page's manifest crosses as %s", b)
	}
}
