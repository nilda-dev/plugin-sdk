package nilda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// THE TAX TABLE IS READ FOR THE DAY IN BRUSSELS, and each kind of row reads as its own value — never as another's.
func TestTheTaxTableIsReadForTheDayInBrussels(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/reference/tax-table" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		asked = r.URL.Query().Get("date")
		_, _ = w.Write([]byte(`{"data":{"version":"v1","on":"2026-10-19","rows":[
 {"key":"GB:rate:standard","kind":"rate","country":"GB","label":"VAT standard rate","value":{"rate_percent":20,"scheme":"GB:vat","priority":1},
  "valid_from":"2011-01-04","source_url":"https://www.gov.uk/vat-rates","source_read_on":"2026-10-19"},
 {"key":"CA-QC:rate:qst","kind":"rate","country":"CA","subdivision":"QC","label":"QST","value":{"rate_percent":9.975,"scheme":"CA-QC:qst","priority":2},
  "valid_from":"2026-10-19","source_url":"https://www.canada.ca/","source_read_on":"2026-10-19",
  "next":{"valid_from":"2026-10-20","value":{"rate_percent":10,"scheme":"CA-QC:qst","priority":2}}},
 {"key":"NO:import_regime:voec","kind":"import_regime","country":"NO","label":"VOEC","value":{"threshold_minor":300000,"currency":"NOK","basis":"item","scheme":"NO:voec"},
  "valid_from":"2026-10-19","source_url":"https://www.skatteetaten.no/","source_read_on":"2026-10-19"},
 {"key":"CA:threshold:digital","kind":"threshold","country":"CA","label":"line","value":{"amount_minor":3000000,"currency":"CAD","period":"rolling_12m","counts":"b2c_digital","scheme":"CA:gst"},
  "valid_from":"2026-10-19","source_url":"https://www.canada.ca/","source_read_on":"2026-10-19"},
 {"key":"GB:registration:vat","kind":"registration","country":"GB","label":"UK VAT","value":{"scheme":"GB:vat"},
  "valid_from":"2026-10-19","source_url":"https://www.gov.uk/","source_read_on":"2026-10-19"},
 {"key":"US:notice:sales_tax","kind":"notice","country":"US","label":"Sales tax","value":{"text":"No federal VAT."},
  "valid_from":"2026-10-19","source_url":"https://www.streamlinedsalestax.org/","source_read_on":"2026-10-19"},
 {"key":"XX:future:kind","kind":"future","country":"XX","label":"?","value":{},"valid_from":"2026-10-19","source_url":"https://x.example/","source_read_on":"2026-10-19"}
]}}`))
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", nil, nil, srv.URL, "t", nil)
	// 22:30 UTC on the 18th is already the 19th in Brussels (summer time).
	table, err := core.API().Reference().TaxTable(context.Background(), time.Date(2026, 10, 18, 22, 30, 0, 0, time.UTC))
	if err != nil || asked != "2026-10-19" || table.Version != "v1" || len(table.Rows) != 7 {
		t.Fatalf("asked %q, table %+v (%v)", asked, table, err)
	}
	gb := table.Of("gb")
	if len(gb) != 2 {
		t.Fatalf("GB rows: %+v", gb)
	}
	if v, ok := gb[0].Rate(); !ok || v.RatePercent != 20 || v.Scheme != "GB:vat" || v.Priority != 1 {
		t.Fatalf("GB rate: %+v %v", v, ok)
	}
	if _, ok := gb[0].Threshold(); ok {
		t.Fatal("a rate row read as a threshold")
	}
	if v, ok := gb[1].Registration(); !ok || v.Scheme != "GB:vat" {
		t.Fatalf("GB registration: %+v %v", v, ok)
	}
	qst := table.Rows[1]
	if v, ok := qst.Rate(); !ok || v.RatePercent != 9.975 || v.Compound || qst.Subdivision != "QC" {
		t.Fatalf("QST: %+v %v", v, ok)
	}
	var next TaxRateValue
	if qst.Next == nil || qst.Next.ValidFrom != "2026-10-20" || json.Unmarshal(qst.Next.Value, &next) != nil || next.RatePercent != 10 {
		t.Fatalf("QST next: %+v", qst.Next)
	}
	if v, ok := table.Rows[2].ImportRegime(); !ok || v.ThresholdMinor != 300000 || v.Basis != "item" {
		t.Fatalf("VOEC line: %+v %v", v, ok)
	}
	if v, ok := table.Rows[3].Threshold(); !ok || v.AmountMinor != 3000000 || v.Period != "rolling_12m" {
		t.Fatalf("CA line: %+v %v", v, ok)
	}
	if v, ok := table.Rows[5].Notice(); !ok || v.Text != "No federal VAT." {
		t.Fatalf("US notice: %+v %v", v, ok)
	}
	future := table.Rows[6]
	if _, ok := future.Rate(); ok {
		t.Fatal("a kind the reader does not know read as a rate")
	}
}
