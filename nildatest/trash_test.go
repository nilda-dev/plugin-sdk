package nildatest_test

import (
	"context"
	"errors"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

// binningPlugin keeps discounts the way a plugin on the bin does: a delete marks the row and reports it, a restore
// re-runs its own check, a purge deletes the row.
type binningPlugin struct {
	core    *nilda.Core
	rows    map[string]string // id -> code
	deleted map[string]bool
	codeUse map[string]bool // a code another live discount uses now
}

func (p *binningPlugin) delete(ctx context.Context, id string) error {
	p.deleted[id] = true
	return p.core.Trash(ctx, nilda.TrashItem{Kind: "discount", ID: id, Name: p.rows[id]})
}

func (p *binningPlugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, _, err := nilda.DispatchTrash(ctx, hook, payload,
		func(_ context.Context, r nilda.TrashRef) error {
			if p.codeUse[p.rows[r.ID]] {
				return errors.New("The code " + p.rows[r.ID] + " is used by another discount now")
			}
			delete(p.deleted, r.ID)
			return nil
		},
		func(_ context.Context, r nilda.TrashRef) error {
			delete(p.rows, r.ID)
			delete(p.deleted, r.ID)
			return nil
		})
	return out, err
}

// THE ROUND TRIP: a delete lands in the bin under the plugin's own key, a restore runs the plugin's own check and a
// refusal comes back in its words with the entry kept, a purge deletes the row and empties the entry.
func TestTheBinRoundTrip(t *testing.T) {
	ctx := context.Background()
	bin := nildatest.NewTrash()
	core, _, srv := bin.Core("shop", "trash")
	defer srv.Close()
	p := &binningPlugin{core: core, rows: map[string]string{"d-1": "SUMMER10", "d-2": "WINTER5"},
		deleted: map[string]bool{}, codeUse: map[string]bool{}}

	if err := p.delete(ctx, "d-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.delete(ctx, "d-2"); err != nil {
		t.Fatal(err)
	}
	items := bin.Items("shop")
	if len(items) != 2 || items[0].Name != "SUMMER10" || items[0].ExpiresAfter != nilda.TrashRetention {
		t.Fatalf("the bin holds %+v", items)
	}
	if len(bin.Items("other")) != 0 {
		t.Error("another plugin sees the shop's items")
	}

	p.codeUse["SUMMER10"] = true
	err := bin.Restore(ctx, "shop", p.HandleHook, items[0])
	if err == nil || err.Error() != "The code SUMMER10 is used by another discount now" {
		t.Fatalf("the refusal did not come back in the plugin's words: %v", err)
	}
	if len(bin.Items("shop")) != 2 || !p.deleted["d-1"] {
		t.Fatal("a refused restore emptied the entry or brought the row back")
	}
	p.codeUse["SUMMER10"] = false
	if err := bin.Restore(ctx, "shop", p.HandleHook, items[0]); err != nil {
		t.Fatal(err)
	}
	if p.deleted["d-1"] || len(bin.Items("shop")) != 1 {
		t.Fatal("the restore did not bring the row back and spend the entry")
	}

	if err := bin.Purge(ctx, "shop", p.HandleHook, items[1]); err != nil {
		t.Fatal(err)
	}
	if _, kept := p.rows["d-2"]; kept || len(bin.Items("shop")) != 0 {
		t.Fatal("the purge kept the row or the entry")
	}
}

func TestTheBinRefusesAPluginWithoutTheGrantAndAShortStay(t *testing.T) {
	ctx := context.Background()
	bin := nildatest.NewTrash()
	core, _, srv := bin.Core("shop", "admin_page")
	defer srv.Close()
	err := core.Trash(ctx, nilda.TrashItem{Kind: "discount", ID: "d-1", Name: "SUMMER10"})
	var apiErr *nilda.APIError
	if !errors.As(err, &apiErr) || !apiErr.Forbidden() {
		t.Fatalf("a plugin without `trash` was not refused 403: %v", err)
	}

	core, _, srv2 := bin.Core("shop", "trash")
	defer srv2.Close()
	if err := core.Trash(ctx, nilda.TrashItem{Kind: "discount", ID: "d-1", Name: "x", ExpiresAfter: time.Minute}); err == nil {
		t.Error("a minute's stay was accepted")
	}
	if err := core.Trash(ctx, nilda.TrashItem{Kind: "discount", ID: "d-1", Name: "x", ExpiresAfter: 2 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if got := bin.Items("shop"); len(got) != 1 || got[0].ExpiresAfter != 2*time.Hour {
		t.Errorf("the stay asked for was not kept: %+v", got)
	}
	if err := core.Untrash(ctx, "discount", "d-1"); err != nil {
		t.Fatal(err)
	}
	if err := core.Untrash(ctx, "discount", "d-1"); err != nil {
		t.Errorf("taking out an item that is not there is an error: %v", err)
	}
	if len(bin.Items("shop")) != 0 {
		t.Error("Untrash left the entry")
	}
}
