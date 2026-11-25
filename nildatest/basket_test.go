package nildatest

import (
	"net/http"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// basketShop is a shop's basket door answering body under the Cache-Control it is given.
type basketShop struct{ cache, body string }

func (s basketShop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.Path != "/shop/api/cart" || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", s.cache)
	_, _ = w.Write([]byte(s.body))
}

func TestBasketAsksTheShopsDoorAsABrowserDoes(t *testing.T) {
	a := Basket(t, basketShop{cache: "no-store", body: `{"lines":[{"sku":"COAT-M","qty":1,"title":"Coat"}]}`}, "/shop/api/cart")
	if len(a.Lines) != 1 || a.Lines[0] != (nilda.BasketLine{SKU: "COAT-M", Qty: 1}) {
		t.Fatalf("answer %+v", a)
	}
}

func TestBasketFailsAShopsBrokenAnswer(t *testing.T) {
	for name, shop := range map[string]basketShop{
		"a shared cache":    {cache: "public, max-age=60", body: `{"lines":[]}`},
		"Go's default keys": {cache: "no-store", body: `{"lines":[{"SKU":"COAT-M","Qty":1}]}`},
		"no lines":          {cache: "no-store", body: `{}`},
	} {
		if basketFails(t, shop) == "" {
			t.Errorf("%s: Basket passed it", name)
		}
	}
}

func basketFails(tb testing.TB, shop http.Handler) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	Basket(f, shop, "/shop/api/cart")
	return ""
}
