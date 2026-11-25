package nildatest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// lookShop is a shop's look-quote door: a coat and a scarf at fixed prices, 10% off a look of both.
type lookShop struct {
	cache string // the Cache-Control it answers with
	wrong bool   // answers a total that does not add up
}

func (s lookShop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.URL.Path != "/shop/look/quote" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", s.cache)
	req, err := nilda.DecodeLookQuoteRequest(r.Body)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(nilda.LookQuoteRefusal{Code: nilda.LookQuoteBadRequest})
		return
	}
	prices := map[string]int64{"COAT": 20000, "SCARF": 3000}
	a := nilda.LookQuoteAnswer{Currency: "EUR", PricesIncludeTax: true}
	for _, l := range req.Lines {
		p, ok := prices[strings.ToUpper(l.SKU)]
		if !ok {
			ref := nilda.LookQuoteRefusal{Code: nilda.LookQuoteUnavailable, SKU: l.SKU}
			w.WriteHeader(ref.Status())
			_ = json.NewEncoder(w).Encode(ref)
			return
		}
		a.Lines = append(a.Lines, nilda.LookQuoteAnswerLine{SKU: strings.ToUpper(l.SKU), Qty: l.Qty, UnitMinor: p,
			LineMinor: p * l.Qty, Availability: nilda.AvailabilityInStock})
		a.SubtotalMinor += p * l.Qty
	}
	a.TotalMinor = a.SubtotalMinor
	if len(a.Lines) == 2 {
		a.Discounts = []nilda.LookQuoteDiscount{{Label: "The look", AmountMinor: a.SubtotalMinor / 10}}
		a.TotalMinor -= a.SubtotalMinor / 10
	}
	if s.wrong {
		a.TotalMinor++
	}
	_ = json.NewEncoder(w).Encode(a)
}

func TestLookQuoteAsksTheShopsDoorAsABrowserDoes(t *testing.T) {
	shop := lookShop{cache: "no-store"}
	ans, ref := LookQuote(t, shop, "/shop/look/quote", nilda.LookQuoteRequest{Lines: []nilda.LookQuoteLine{{SKU: "coat", Qty: 1}, {SKU: "scarf", Qty: 2}}})
	if ref != nil || ans.TotalMinor != 23400 || len(ans.Discounts) != 1 {
		t.Fatalf("answer %+v, refusal %+v", ans, ref)
	}
	_, ref = LookQuote(t, shop, "/shop/look/quote", nilda.LookQuoteRequest{Lines: []nilda.LookQuoteLine{{SKU: "hat", Qty: 1}}})
	if ref == nil || ref.Code != nilda.LookQuoteUnavailable || ref.SKU != "hat" {
		t.Fatalf("refusal %+v", ref)
	}
}

func TestLookQuoteFailsAShopsBrokenAnswer(t *testing.T) {
	two := nilda.LookQuoteRequest{Lines: []nilda.LookQuoteLine{{SKU: "coat", Qty: 1}, {SKU: "scarf", Qty: 1}}}
	for name, shop := range map[string]lookShop{
		"a shared cache": {cache: "public, max-age=60"},
		"a wrong total":  {cache: "no-store", wrong: true},
	} {
		if lookQuoteFails(t, shop, two) == "" {
			t.Errorf("%s: LookQuote passed it", name)
		}
	}
	if lookQuoteFails(t, lookShop{cache: "no-store"}, nilda.LookQuoteRequest{}) == "" {
		t.Error("LookQuote sent a request nilda.LookQuoteRequest.Validate refuses")
	}
}

func lookQuoteFails(tb testing.TB, shop http.Handler, req nilda.LookQuoteRequest) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	LookQuote(f, shop, "/shop/look/quote", req)
	return ""
}
