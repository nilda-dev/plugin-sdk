package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestDispatchAdminBulkActionRoutesABatch(t *testing.T) {
	var got AdminBulkAction
	out, handled, err := DispatchAdminBulkAction(context.Background(), AdminBulkActionHook,
		[]byte(`{"page":"products","action":"price","ids":["a","b"],"values":{"op":"set"},"run":"r1","batch":2,"last":true,"total":52}`),
		func(_ context.Context, a AdminBulkAction) (AdminBulkActionResult, error) {
			got = a
			return AdminBulkActionResult{Run: "r1", Rows: []BulkRowResult{{ID: "b", Skipped: true, Message: "Not this one"}},
				Open: &BulkOpen{Page: "bulk_changes", Row: "r1"}}, nil
		})
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if got.Page != "products" || len(got.IDs) != 2 || got.Run != "r1" || got.Batch != 2 || !got.Last || got.Total != 52 || got.Values["op"] != "set" {
		t.Fatalf("payload not decoded: %+v", got)
	}
	var res AdminBulkActionResult
	if err := json.Unmarshal(out, &res); err != nil || res.Open == nil || res.Open.Row != "r1" || !res.Rows[0].Skipped {
		t.Fatalf("answer not shaped: %s (%v)", out, err)
	}
}

func TestDispatchAdminBulkActionLeavesOtherHooksAndPassesARefusal(t *testing.T) {
	if _, handled, _ := DispatchAdminBulkAction(context.Background(), AdminActionHook, nil, nil); handled {
		t.Fatal("a row action's hook was taken for a bulk batch")
	}
	refusal := errors.New("This change is not available")
	_, handled, err := DispatchAdminBulkAction(context.Background(), AdminBulkActionHook, []byte(`{"ids":[]}`),
		func(context.Context, AdminBulkAction) (AdminBulkActionResult, error) {
			return AdminBulkActionResult{}, refusal
		})
	if !handled || !errors.Is(err, refusal) {
		t.Fatalf("handled=%v err=%v, want the plugin's refusal", handled, err)
	}
	if _, handled, err := DispatchAdminBulkAction(context.Background(), AdminBulkActionHook, []byte(`{`), nil); !handled || err == nil {
		t.Fatalf("no handler must fail the batch: handled=%v err=%v", handled, err)
	}
}
