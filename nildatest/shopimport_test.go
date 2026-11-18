package nildatest

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// tagShop answers shop.import/v1 as a shop would shape it, and keeps what it was sent.
type tagShop struct {
	mu  sync.Mutex
	got []nilda.ShopImportBatch
}

func (s *tagShop) ShopImportSettings(context.Context, nilda.ShopImportSettingsRequest) (nilda.ShopImportSettingsResult, error) {
	return nilda.ShopImportSettingsResult{Decision: nilda.ShopImportMoneySame, Currency: "EUR", Empty: true, Losses: []nilda.ShopImportLoss{}}, nil
}
func (s *tagShop) PutShopImportRecords(_ context.Context, b nilda.ShopImportBatch) (nilda.ShopImportAnswer, error) {
	s.mu.Lock()
	s.got = append(s.got, b)
	s.mu.Unlock()
	a := nilda.ShopImportAnswer{Kind: b.Kind, Results: []nilda.ShopImportResult{}}
	for _, r := range b.Records {
		a.Results = append(a.Results, nilda.ShopImportResult{SourceID: r.SourceID, Outcome: nilda.ShopImportCreated,
			Refs: nilda.ShopImportRefs{TermID: "0d7c2a1e-5f64-4b8a-9b2e-1f5c3e9d7a10"}, Losses: []nilda.ShopImportLoss{}})
	}
	return a, nil
}
func (s *tagShop) ShopImportRecords(context.Context, nilda.ShopImportListRequest) (nilda.ShopImportListAnswer, error) {
	return nilda.ShopImportListAnswer{Rows: []nilda.ShopImportRow{}}, nil
}
func (s *tagShop) PlanShopImport(context.Context, nilda.ShopImportActRequest) (nilda.ShopImportPlan, error) {
	return nilda.ShopImportPlan{Groups: nilda.ShopImportGroups{SKUPairs: []nilda.ShopImportSKUPair{}, SaleSoon: []nilda.ShopImportSaleSoon{}},
		Losses: []nilda.ShopImportLoss{}}, nil
}
func (s *tagShop) ShopImportPrices(context.Context, nilda.ShopImportActRequest) (nilda.ShopImportPrices, error) {
	return nilda.ShopImportPrices{Rows: []nilda.ShopImportPriceRow{}}, nil
}
func (s *tagShop) ApplyShopImport(context.Context, nilda.ShopImportApplyRequest) (nilda.ShopImportActPage, error) {
	return nilda.ShopImportActPage{Done: true, Losses: []nilda.ShopImportLoss{}}, nil
}
func (s *tagShop) UndoShopImport(context.Context, nilda.ShopImportUndoRequest) (nilda.ShopImportActPage, error) {
	return nilda.ShopImportActPage{Done: true, Losses: []nilda.ShopImportLoss{}}, nil
}
func (s *tagShop) ServiceMethods() []nilda.ServiceMethod { return nilda.ShopImportMethods(s) }

func tagBatch(t *testing.T) nilda.ShopImportBatch {
	t.Helper()
	r, err := nilda.ShopImportRecordOf("shop.example.com#tag:1", "", nilda.ShopImportTagData{Name: "Linen", Slug: "linen"})
	if err != nil {
		t.Fatal(err)
	}
	return nilda.ShopImportBatch{Import: nilda.ImportCall{ID: "job-7", Planned: 40}, Source: "woocommerce:shop.example.com",
		Platform: nilda.ShopImportWooCommerce, Kind: nilda.ShopImportTag, Apply: true, Records: []nilda.ShopImportRecord{r}}
}

// THE DESK RELAYS AN IMPORT CALL AS CORE DOES (SHOP_IMPORT.md §1.1): to a plugin the owner granted import, under a marker
// namespaced by its key, to one shop — and refuses everyone else before the shop is asked.
func TestTheDeskRelaysAnImportCallAsCoreDoes(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	shop := &tagShop{}
	desk.Provider("ecommerce", shop)

	importer, _, srv := desk.ImportPlugin("site_import", nilda.ContractShopImport)
	defer srv.Close()
	if !importer.HasCapability(nilda.CapImport) {
		t.Fatal("an import plugin's Core does not hold the import capability")
	}
	a, err := importer.API().Services().PutShopImportRecords(ctx, "", tagBatch(t))
	if err != nil || len(a.Results) != 1 || a.Results[0].Outcome != nilda.ShopImportCreated {
		t.Fatalf("a granted plugin's batch: %+v, %v", a, err)
	}
	calls := desk.Calls()
	if len(calls) != 1 || calls[0].ImportMarker != "plugin:site_import:job-7" || calls[0].ImportPlanned != 40 || calls[0].Caller != "site_import" {
		t.Fatalf("the call was opened as %+v; want the marker plugin:site_import:job-7 and 40 planned", calls)
	}

	// Not granted: refused with the reason, and the shop never asked.
	stranger, _, srv2 := desk.Core("stranger", nilda.ContractShopImport)
	defer srv2.Close()
	if _, err := stranger.API().Services().PutShopImportRecords(ctx, "", tagBatch(t)); err == nil || !strings.Contains(err.Error(), "import_not_granted") && !strings.Contains(err.Error(), "import capability") {
		t.Fatalf("a plugin without import: %v", err)
	}
	if _, err := desk.Ask(ctx, nilda.ContractShopImport, nilda.ShopImportRecordsPut, tagBatch(t), nilda.ServiceCallOptions{}); err == nil {
		t.Fatal("Core's own call of an import method was relayed; Core holds no import grant")
	}
	if len(shop.got) != 1 {
		t.Fatalf("the shop was asked %d times; only the granted plugin's batch reaches it", len(shop.got))
	}
	// An ordinary method of the same contract needs no grant.
	if _, err := stranger.API().Services().ShopImportRecords(ctx, "", nilda.ShopImportListRequest{Filter: nilda.ShopImportListAll}); err != nil {
		t.Fatalf("records.list from a consumer without import: %v", err)
	}

	// Two shops and none named: refused, nothing written twice; naming one asks that one.
	other := &tagShop{}
	desk.Provider("second_shop", other)
	if _, err := importer.API().Services().PutShopImportRecords(ctx, "", tagBatch(t)); err == nil {
		t.Fatal("a batch with two shops and none named was relayed")
	}
	if _, err := importer.API().Services().PutShopImportRecords(ctx, "second_shop", tagBatch(t)); err != nil || len(other.got) != 1 || len(shop.got) != 1 {
		t.Fatalf("naming one shop: %v (asked %d and %d)", err, len(shop.got), len(other.got))
	}

	// A batch whose import object breaks the contract never reaches a shop.
	b := tagBatch(t)
	b.Import.ID = "job 7"
	raw, _ := json.Marshal(map[string]any{"import": b.Import, "source": b.Source, "platform": b.Platform, "kind": b.Kind, "records": b.Records})
	if _, err := desk.call(ctx, "site_import", nilda.ContractShopImport, nilda.ShopImportRecordsPut, raw, nilda.ServiceCallOptions{Provider: "ecommerce"}); err == nil {
		t.Fatal("an import id with a space was relayed")
	}
	if len(shop.got) != 1 {
		t.Fatal("a refused batch reached the shop")
	}
}
