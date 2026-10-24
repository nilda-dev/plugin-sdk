package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// A BUTTON, A FORM OR A SETTING CAN ALWAYS ASK (v0.41.0). An ability that moves money or lets data out asks the person
// before every call (v0.40.0, AlwaysAsk); the same act reached through an admin screen — a row's Refund button, the
// form that issues a gift card, the setting that says where order alerts go — asks too, whatever a connector was
// granted, so the screen is not a second door around the question.
func TestAdminButtonsFormsAndSettingsCanAlwaysAsk(t *testing.T) {
	page := AdminPage{Key: "orders", Label: "Orders", Kind: "list",
		Actions: []RowAction{{Key: "refund", Label: "Refund", AlwaysAsk: true}, {Key: "note", Label: "Note"}},
		Form:    &PageForm{Create: true, AlwaysAsk: true, Fields: []FormField{{Key: "amount", Label: "Amount"}}},
		Fields:  []SettingField{{Key: "alert_email", Label: "Alerts to", Type: SettingText, AlwaysAsk: true}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if strings.Count(s, `"always_ask":true`) != 3 {
		t.Fatalf("the manifest says always_ask %d times, want 3 (button, form, setting): %s", strings.Count(s, `"always_ask":true`), s)
	}
	var back AdminPage
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !back.Actions[0].AlwaysAsk || back.Actions[1].AlwaysAsk || !back.Form.AlwaysAsk || !back.Fields[0].AlwaysAsk {
		t.Errorf("read back: %+v", back)
	}
}

// Core says when a hook call was asked for by the assistant rather than by a person's own press, so a plugin can hold
// the assistant to its own limits on every door.
func TestCallOfSaysTheAssistantAsked(t *testing.T) {
	ctx := withCall(context.Background(), &contract.HookRequest{Hook: "admin.action", ViaAssistant: true})
	if !CallOf(ctx).ViaAssistant {
		t.Fatal("a call the assistant asked for does not say so")
	}
	if CallOf(withCall(context.Background(), &contract.HookRequest{Hook: "admin.action", RequestId: "r1"})).ViaAssistant {
		t.Fatal("a person's own press says the assistant asked")
	}
}
