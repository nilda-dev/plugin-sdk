package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The two answers carry their new fields to Core under the names Core's admin reads, and leave an answer without
// them as it was.
func TestARevealAndANoticeTravelUnderTheirNames(t *testing.T) {
	out, handled, err := DispatchAdminAction(context.Background(), AdminActionHook, []byte(`{"page":"keys","action":"new","id":"acp"}`),
		func(context.Context, AdminAction) (AdminActionResult, error) {
			return AdminActionResult{Message: "A new key is ready", Reveal: "sk_live_123"}, nil
		})
	if err != nil || !handled || !strings.Contains(string(out), `"reveal":"sk_live_123"`) {
		t.Errorf("the action's answer: %s %v %v", out, handled, err)
	}
	raw, _ := json.Marshal(AdminActionResult{Message: "Done"})
	if string(raw) != `{"message":"Done"}` {
		t.Errorf("an answer without a reveal changed: %s", raw)
	}
	raw, _ = json.Marshal(AdminReportResult{Columns: []ListColumn{}, Rows: []map[string]any{}, Notice: "Requests are switched off"})
	if !strings.Contains(string(raw), `"notice":"Requests are switched off"`) {
		t.Errorf("the report: %s", raw)
	}
	if MaxAdminRevealLen != 512 || MaxAdminNoticeLen != 300 {
		t.Error("the bounds moved; Core cuts at these")
	}
}
