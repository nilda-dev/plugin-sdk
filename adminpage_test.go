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
