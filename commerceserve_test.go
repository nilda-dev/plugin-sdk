package nilda

import (
	"context"
	"io"
	"net/http"
	"testing"
)

type catalogueOnly struct{}

func (catalogueOnly) Products(context.Context, CommerceQuery) ([]CommerceProduct, error) { return nil, nil }
func (catalogueOnly) Product(context.Context, string) (CommerceProduct, bool)            { return CommerceProduct{}, false }
func (catalogueOnly) Endpoints(context.Context) CommerceEndpoints {
	return CommerceEndpoints{Cart: "/shop/cart", AddToCart: "/shop/add", Checkout: "/shop/checkout"}
}

// shopWithCart is a Commerce that also answers the paths its Endpoints name.
type shopWithCart struct{ catalogueOnly }

func (shopWithCart) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "cart at "+r.URL.Path)
}

// M15 (2026-09-23 plugin hunt): ServeCommerce served the hooks and no route, so the endpoints its own example
// names answered 502 through Core. A Commerce that is an http.Handler is now served on the plugin's route; one
// that is not serves none.
func TestServeCommerceServesTheCartItNames(t *testing.T) {
	res, err := (&commerceOnlyHandler{c: shopWithCart{}}).Init(context.Background(), &Core{PluginKey: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RouteAddr == "" {
		t.Fatal("a shop that is an http.Handler was given no route — its cart endpoints have nothing behind them")
	}
	resp, err := http.Get("http://" + res.RouteAddr + "/shop/cart")
	if err != nil {
		t.Fatalf("GET the cart: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "cart at /shop/cart" {
		t.Fatalf("the cart answered %q", body)
	}

	res, err = (&commerceOnlyHandler{c: catalogueOnly{}}).Init(context.Background(), &Core{PluginKey: "shop"})
	if err != nil || res.RouteAddr != "" {
		t.Fatalf("a catalogue-only Commerce got route %q (err %v); it has nothing to serve", res.RouteAddr, err)
	}
}
