package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// tagShop answers shop.import/v1 as a shop would shape it, and keeps what it was sent.
type tagShop struct {
	mu   sync.Mutex
	got  []nilda.ShopImportBatch
	acts []nilda.CallInfo // how each act's page was called
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
func (s *tagShop) ApplyShopImport(ctx context.Context, _ nilda.ShopImportApplyRequest) (nilda.ShopImportActPage, error) {
	s.mu.Lock()
	s.acts = append(s.acts, nilda.CallOf(ctx))
	s.mu.Unlock()
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
	// EVERY method of the import contract needs the grant (caller_capability import): the read, the plan, the act and the
	// undo as much as records.put — and the grant is checked before the request is read, so a malformed request from a
	// plugin without it is refused for the grant, not for its shape.
	for name, call := range map[string]func() error{
		"records.list": func() error {
			_, err := stranger.API().Services().ShopImportRecords(ctx, "", nilda.ShopImportListRequest{Filter: nilda.ShopImportListAll})
			return err
		},
		"publish.plan": func() error {
			_, err := stranger.API().Services().PlanShopImport(ctx, "", nilda.ShopImportActRequest{Source: "woocommerce:shop.example.com"})
			return err
		},
		"publish.undo": func() error {
			_, err := stranger.API().Services().UndoShopImport(ctx, "", nilda.ShopImportUndoRequest{Import: nilda.ImportCall{ID: "job-1", Planned: 1},
				Source: "woocommerce:shop.example.com", Marker: "act-1"})
			return err
		},
	} {
		var apiErr *nilda.APIError
		if err := call(); !errors.As(err, &apiErr) || apiErr.Status != 403 || !strings.Contains(apiErr.Body, "import_not_granted") {
			t.Errorf("%s from a plugin without import: %v, want Core's 403 import_not_granted", name, err)
		}
	}
	// Core's own call (the owner's assistant) reaches the read and nothing else, as Core relays it.
	if _, err := desk.Ask(ctx, nilda.ContractShopImport, nilda.ShopImportRecordsList, nilda.ShopImportListRequest{Filter: nilda.ShopImportListAll},
		nilda.ServiceCallOptions{}); err != nil {
		t.Errorf("Core's own records.list: %v, want the owner's read answered", err)
	}
	if _, err := desk.Ask(ctx, nilda.ContractShopImport, nilda.ShopImportPublishPlan, nilda.ShopImportActRequest{Source: "woocommerce:shop.example.com"},
		nilda.ServiceCallOptions{}); !errors.Is(err, ErrImportNotGranted) {
		t.Errorf("Core's own publish.plan: %v, want refused as ungranted", err)
	}
	stranger2 := desk.routes("stranger", []string{"use:" + nilda.ContractShopImport})
	rec := httptest.NewRecorder()
	stranger2.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/services/shop.import/v1/records.put", strings.NewReader(`not json`)))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "import_not_granted") {
		t.Errorf("a malformed request from a plugin without import: %d %s, want the grant's 403 first", rec.Code, rec.Body.String())
	}
	if _, err := importer.API().Services().ShopImportRecords(ctx, "", nilda.ShopImportListRequest{Filter: nilda.ShopImportListAll}); err != nil {
		t.Fatalf("records.list from the import plugin: %v", err)
	}

	// The switch off (PLUGIN_IMPORT_ENABLED): every call of a plugin refused with Core's 409 plugin_import_off, which the
	// client does not retry; the shop never asked. The owner's own read still answers.
	desk.SetImportEnabled(false)
	asked := len(shop.got)
	var off *nilda.APIError
	if _, err := importer.API().Services().PutShopImportRecords(ctx, "", tagBatch(t)); !errors.As(err, &off) || off.Status != 409 ||
		!strings.Contains(off.Body, "plugin_import_off") || len(shop.got) != asked {
		t.Errorf("a batch with imports switched off: %v (asked %d → %d), want Core's 409 plugin_import_off", err, asked, len(shop.got))
	}
	if _, err := desk.Ask(ctx, nilda.ContractShopImport, nilda.ShopImportRecordsList, nilda.ShopImportListRequest{Filter: nilda.ShopImportListAll},
		nilda.ServiceCallOptions{}); err != nil {
		t.Errorf("the owner's read with imports switched off: %v", err)
	}
	desk.SetImportEnabled(true)

	// Two shops and none named: refused, nothing written twice; naming one asks that one.
	other := &tagShop{}
	desk.Provider("second_shop", other)
	var unnamed *nilda.APIError
	if _, err := importer.API().Services().PutShopImportRecords(ctx, "", tagBatch(t)); !errors.As(err, &unnamed) ||
		unnamed.Status != 422 || !strings.Contains(unnamed.Body, "provider_not_named") {
		t.Fatalf("a batch with two shops and none named: %v, want Core's 422 provider_not_named", err)
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

// The owner's act is an import call too (D10 §1.4 amendment 17): the desk opens each page of publish.apply and publish.undo under
// the job's marker with its planned rows, and names the call with a fresh request id, so what the shop writes back through Core
// while it serves the page carries nilda.CallHeader — the quiet call Core opens (callquiet.go's beginImportCall). An act's page
// without the import object is refused before the shop is asked.
func TestTheDeskOpensTheActAsAQuietImportCall(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	shop := &tagShop{}
	desk.Provider("ecommerce", shop)
	importer, _, srv := desk.ImportPlugin("site_import", nilda.ContractShopImport)
	defer srv.Close()
	svc := importer.API().Services()
	act := nilda.ShopImportApplyRequest{Import: nilda.ImportCall{ID: "job-7", Planned: 12}, Source: "woocommerce:shop.example.com",
		Marker: "act-job-7", Choices: nilda.ShopImportChoices{SwitchOnPerPerson: true}}
	if _, err := svc.ApplyShopImport(ctx, "", act); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UndoShopImport(ctx, "", nilda.ShopImportUndoRequest{Import: nilda.ImportCall{ID: "job-7", Planned: 12},
		Source: "woocommerce:shop.example.com", Marker: "act-job-7"}); err != nil {
		t.Fatal(err)
	}
	calls := desk.Calls()
	if len(calls) != 2 {
		t.Fatalf("%d calls", len(calls))
	}
	for _, c := range calls {
		if c.ImportMarker != "plugin:site_import:job-7" || c.ImportPlanned != 12 || c.RequestID == "" {
			t.Errorf("%s was opened as %+v; want the job's marker, its 12 rows and a request id", c.Method, c)
		}
	}
	if len(shop.acts) != 1 || shop.acts[0].RequestID == "" || shop.acts[0].RequestID != calls[0].RequestID {
		t.Errorf("the shop's act page was called as %+v: its callbacks would not be quiet", shop.acts)
	}
	raw, _ := json.Marshal(map[string]any{"source": "woocommerce:shop.example.com", "marker": "act-job-7",
		"choices": map[string]any{"publish_converted": false, "switch_on_per_person": true}})
	if _, err := svc.Call(ctx, nilda.ContractShopImport, nilda.ShopImportPublishApply, json.RawMessage(raw), nilda.ServiceCallOptions{}); err == nil {
		t.Error("an act's page without its import object was relayed")
	}
	if len(shop.acts) != 1 {
		t.Errorf("the shop was asked %d times", len(shop.acts))
	}
}
