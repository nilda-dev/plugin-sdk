package nilda

import (
	"context"
	"strings"
	"testing"
)

// pagedShop knows its product pages: the product whose own page has the content id it is asked for.
type pagedShop struct{ catalogueOnly }

func (pagedShop) ProductForContent(_ context.Context, contentID string) (CommerceProduct, bool) {
	if contentID == "content-42" {
		return CommerceProduct{ID: "vase", Title: "Vase", Price: "€40.00"}, true
	}
	return CommerceProduct{}, false
}

func TestAShopThatKnowsItsPagesAnswersProductForContent(t *testing.T) {
	got, err := dispatchShop(t, pagedShop{}, HookCommerceProductForContent, `{"content_id":"content-42"}`)
	if err != nil || !strings.Contains(got, `"vase"`) || !strings.Contains(got, "€40.00") {
		t.Fatalf("a product's own page: %s %v", got, err)
	}
	if got, _ := dispatchShop(t, pagedShop{}, HookCommerceProductForContent, `{"content_id":"content-7"}`); got != `{"product":null}` {
		t.Errorf("a page that is no product's: %s, want an explicit null", got)
	}
	// An empty content id is not a question: not found, and the shop is not called.
	if got, _ := dispatchShop(t, pagedShop{}, HookCommerceProductForContent, `{"content_id":""}`); got != `{"product":null}` {
		t.Errorf("an empty content id: %s", got)
	}
}

func TestAShopThatDoesNotIsNotAsked(t *testing.T) {
	has := func(c Commerce) bool {
		res, err := (&commerceOnlyHandler{c: c}).Init(context.Background(), &Core{PluginKey: "shop"})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Hooks {
			if h == HookCommerceProductForContent {
				return true
			}
		}
		return false
	}
	if !has(pagedShop{}) || has(catalogueOnly{}) {
		t.Error("commerce.product_for_content is subscribed by the wrong shops")
	}
	// A shop that does not implement it answers the hook as "no such product" if it is asked anyway.
	if got, _ := dispatchShop(t, catalogueOnly{}, HookCommerceProductForContent, `{"content_id":"content-42"}`); got != `{"product":null}` {
		t.Errorf("a shop that does not know its pages: %s", got)
	}
}
