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
