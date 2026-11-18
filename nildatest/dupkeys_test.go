package nildatest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// ONE KEY, ONE SPELLING, as Core's desk refuses it (dupkeys.go): an exact repeat anywhere, and inside an object whose
// schema declares properties a repeat under case folding — but a free-form map's keys ("abc" and "ABC" in sku_pairs or
// tax_class_rates) are the caller's data and two keys. The grant is asked first, then the keys, then the schema.
func TestTheDeskRefusesAKeyGivenTwiceAsCoreDoes(t *testing.T) {
	desk := NewServices()
	shop := &tagShop{}
	desk.Provider("ecommerce", shop)
	_, _, srv := desk.ImportPlugin("site_import", nilda.ContractShopImport)
	defer srv.Close()
	importer := desk.routes("site_import", []string{"use:" + nilda.ContractShopImport})
	stranger := desk.routes("stranger", []string{"use:" + nilda.ContractShopImport})
	post := func(h http.Handler, method, input string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/services/shop.import/v1/"+method,
			strings.NewReader(`{"input":`+input+`}`)))
		var body struct {
			Error struct {
				Details map[string]any `json:"details"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body.Error.Details
	}
	raw, err := json.Marshal(tagBatch(t))
	if err != nil {
		t.Fatal(err)
	}
	batch := string(raw)
	if code, d := post(importer, nilda.ShopImportRecordsPut, batch); code != http.StatusOK {
		t.Fatalf("the batch as sent: %d %v", code, d)
	}
	asked := len(shop.got)

	for name, c := range map[string]struct{ from, to, at string }{
		"the import object in another case": {`"source":`, `"IMPORT":{"id":"other-job","planned":1000000},"source":`, "$"},
		"a record's data in another case":   {`"name":"Linen"`, `"name":"Linen","NAME":"Wool"`, "$.records[0].data"},
		"an exact repeat":                   {`"kind":"tag"`, `"kind":"tag","kind":"product"`, "$"},
		"the kelvin sign":                   {`"kind":"tag"`, `"kind":"tag","Kind":"product"`, "$"},
	} {
		in := strings.Replace(batch, c.from, c.to, 1)
		if in == batch {
			t.Fatalf("%s: the request was not changed", name)
		}
		if code, d := post(importer, nilda.ShopImportRecordsPut, in); code != http.StatusUnprocessableEntity ||
			d["reason"] != "key_given_twice" || d["at"] != c.at {
			t.Errorf("%s: %d %v, want Core's 422 key_given_twice at %s", name, code, d, c.at)
		}
		// The grant first: a plugin without it hears the 403, not the key.
		if code, d := post(stranger, nilda.ShopImportRecordsPut, in); code != http.StatusForbidden || d["reason"] != "import_not_granted" {
			t.Errorf("%s from a plugin without import: %d %v, want the grant's 403 first", name, code, d)
		}
	}
	if len(shop.got) != asked {
		t.Fatalf("the shop was asked %d time(s) with a key given twice", len(shop.got)-asked)
	}
	// The keys before the schema: a request the schema would also refuse is refused for its key.
	if code, d := post(importer, nilda.ShopImportRecordsPut, `{"kind":"tag","kind":"tag","nonsense":1}`); code != http.StatusUnprocessableEntity ||
		d["reason"] != "key_given_twice" {
		t.Errorf("a key given twice in a request the schema refuses: %d %v, want key_given_twice first", code, d)
	}

	// A FREE-FORM MAP: two SKU pairs whose source ids differ only by case are two pairs, relayed; an exact repeat is not.
	apply := `{"source":"woocommerce:shop.example.com","marker":"act-1","choices":{"publish_converted":false,` +
		`"switch_on_per_person":true,"sku_pairs":{"abc":"keep_both","ABC":"keep_mine"}}}`
	if code, d := post(importer, nilda.ShopImportPublishApply, apply); code != http.StatusOK {
		t.Errorf("sku_pairs keyed abc and ABC: %d %v, want relayed (a map's keys are the caller's data)", code, d)
	}
	twice := strings.Replace(apply, `"ABC":"keep_mine"`, `"abc":"keep_mine"`, 1)
	if code, d := post(importer, nilda.ShopImportPublishApply, twice); code != http.StatusUnprocessableEntity ||
		d["reason"] != "key_given_twice" || d["at"] != "$.choices.sku_pairs" {
		t.Errorf("sku_pairs keyed abc twice: %d %v, want 422 key_given_twice at $.choices.sku_pairs", code, d)
	}
	// The choices object itself declares properties: its own keys fold.
	folded := strings.Replace(apply, `"publish_converted":false,`, `"publish_converted":false,"Publish_Converted":true,`, 1)
	if code, d := post(importer, nilda.ShopImportPublishApply, folded); code != http.StatusUnprocessableEntity ||
		d["reason"] != "key_given_twice" || d["at"] != "$.choices" {
		t.Errorf("publish_converted in two cases: %d %v, want 422 key_given_twice at $.choices", code, d)
	}
	// tax_class_rates, the other map: "reduced" and "Reduced" are two tax classes.
	yes := true
	req, err := json.Marshal(nilda.ShopImportSettingsRequest{Source: "woocommerce:shop.example.com", Platform: nilda.ShopImportWooCommerce,
		Settings: nilda.ShopImportSettings{Currency: "EUR", PricesIncludeTax: &yes, From: "site", Money: "convert",
			Convert: &nilda.ShopImportConvert{To: "USD", Rate: "1.0842", RateDay: "2026-11-17", RateSource: "owner", Ending: "exact",
				TaxClassRates: map[string]string{"reduced": "7", "zzzzzzz": "10"}}}})
	if err != nil {
		t.Fatal(err)
	}
	rates := strings.Replace(string(req), `"zzzzzzz"`, `"Reduced"`, 1)
	if code, d := post(importer, nilda.ShopImportSettingsAnswer, rates); code != http.StatusOK {
		t.Errorf("tax_class_rates keyed reduced and Reduced: %d %v, refused as one key", code, d)
	}
	if code, d := post(importer, nilda.ShopImportSettingsAnswer, strings.Replace(rates, `"Reduced"`, `"reduced"`, 1)); code != http.StatusUnprocessableEntity ||
		d["reason"] != "key_given_twice" {
		t.Errorf("tax_class_rates keyed reduced twice: %d %v, want 422 key_given_twice", code, d)
	}
}
