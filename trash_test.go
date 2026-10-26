package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// trashWireExample is the bin's wire as docs/PLUGIN_SDK.md shows it and as Core reads it (core
// internal/plugintrash's TestTheTrashWireIsTheSDKsDocumentedOne holds the same text until Core pins the tag that
// carries this file): the body of PUT /plugin-trash/{kind}/{id}, and what the two hooks receive.
const trashWireExample = `{"put":{"name":"SUMMER10","expires_after_seconds":86400,"request_id":"req-1"},"ref":{"kind":"discount","id":"d-1"}}`

func TestTheTrashWireIsTheDocumentedOne(t *testing.T) {
	var in struct {
		Put trashPut `json:"put"`
		Ref TrashRef `json:"ref"`
	}
	dec := json.NewDecoder(strings.NewReader(trashWireExample))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		t.Fatalf("the documented wire does not decode into the SDK's types: %v", err)
	}
	out, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != trashWireExample {
		t.Errorf("the SDK's types do not write the documented wire:\n got %s\nwant %s", out, trashWireExample)
	}
	doc := readText(t, "docs/PLUGIN_SDK.md")
	for _, line := range []string{"### The site's recycle bin (`trash`)", `nilda.DispatchTrash(ctx, hook, payload, p.restore, p.purge)`,
		"| `trash` |", `"kind": "discount"`} {
		if !strings.Contains(doc, line) {
			t.Errorf("docs/PLUGIN_SDK.md does not show %s", line)
		}
	}
}

func TestATrashItemCoreWouldRefuseIsRefusedHere(t *testing.T) {
	ok := TrashItem{Kind: "discount", ID: "d-1", Name: "SUMMER10"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a plain item was refused: %v", err)
	}
	for name, it := range map[string]TrashItem{
		"no kind":         {ID: "d-1", Name: "x"},
		"a dotted kind":   {Kind: "shop.discount", ID: "d-1", Name: "x"},
		"an upper kind":   {Kind: "Discount", ID: "d-1", Name: "x"},
		"no id":           {Kind: "discount", Name: "x"},
		"a slash in id":   {Kind: "discount", ID: "a/b", Name: "x"},
		"no name":         {Kind: "discount", ID: "d-1", Name: "  "},
		"a long name":     {Kind: "discount", ID: "d-1", Name: strings.Repeat("n", MaxTrashName+1)},
		"a minute's stay": {Kind: "discount", ID: "d-1", Name: "x", ExpiresAfter: time.Minute},
		"a longer stay":   {Kind: "discount", ID: "d-1", Name: "x", ExpiresAfter: TrashRetention + time.Hour},
	} {
		if err := it.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, it)
		}
	}
	if err := (TrashItem{Kind: "discount", ID: "d-1", Name: "x", ExpiresAfter: 7 * 24 * time.Hour}).Validate(); err != nil {
		t.Errorf("a week's stay was refused: %v", err)
	}
}

func TestDispatchTrashRoutesBothHooksAndPassesARefusalOn(t *testing.T) {
	var restored, purged TrashRef
	refuse := errors.New("The code SUMMER10 is used by another discount now")
	restore := func(_ context.Context, r TrashRef) error {
		restored = r
		if r.ID == "taken" {
			return refuse
		}
		return nil
	}
	purge := func(_ context.Context, r TrashRef) error { purged = r; return nil }
	ctx := context.Background()

	out, handled, err := DispatchTrash(ctx, TrashRestoreHook, []byte(`{"kind":"discount","id":"d-1"}`), restore, purge)
	if !handled || err != nil || string(out) != "{}" || restored != (TrashRef{Kind: "discount", ID: "d-1"}) {
		t.Fatalf("restore: handled=%v err=%v out=%s got %+v", handled, err, out, restored)
	}
	if _, _, err := DispatchTrash(ctx, TrashRestoreHook, []byte(`{"kind":"discount","id":"taken"}`), restore, purge); !errors.Is(err, refuse) {
		t.Errorf("a refusal did not come back as the plugin's own words: %v", err)
	}
	if _, handled, err := DispatchTrash(ctx, TrashPurgeHook, []byte(`{"kind":"discount","id":"d-2"}`), restore, purge); !handled || err != nil || purged.ID != "d-2" {
		t.Errorf("purge: handled=%v err=%v got %+v", handled, err, purged)
	}
	if _, handled, _ := DispatchTrash(ctx, "admin.action", nil, restore, purge); handled {
		t.Error("another hook was taken as a bin hook")
	}
	if _, handled, err := DispatchTrash(ctx, TrashPurgeHook, []byte(`{}`), restore, nil); !handled || err == nil {
		t.Error("a purge with no handler was answered as done")
	}
}
