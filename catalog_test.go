package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// fakeShop answers the catalogue: one product with one variant, or a broken answer when broken is set.
type fakeShop struct{ broken bool }

func (f fakeShop) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f fakeShop) HandleEvent(context.Context, string, []byte) error         { return nil }
func (f fakeShop) Init(context.Context, *Core) (InitResult, error)           { return InitResult{}, nil }

func (f fakeShop) DescribeCatalog(context.Context) (CatalogDescription, error) {
	return CatalogDescription{Name: "Atelier Kari", Origin: "https://kari.example", Currency: "EUR", ShipsTo: []string{"*"}, ReturnDays: 14, Locale: "en"}, nil
}

func (f fakeShop) CatalogPage(_ context.Context, req CatalogPageRequest) (CatalogPage, error) {
	v := CatalogVariant{SKU: "mug-blue", PriceMinor: 1500, Currency: "EUR", Availability: AvailabilityInStock, RequiresShipping: true}
	if f.broken {
		v.Availability = "maybe"
	}
	return CatalogPage{Products: []CatalogProduct{{SKU: "mug", Title: "Mug", URL: "https://kari.example/mug",
		UpdatedAt: time.Now(), Variants: []CatalogVariant{v}}, {SKU: "gone", Removed: true}}}, nil
}

func (f fakeShop) CatalogProduct(context.Context, CatalogProductRequest) (CatalogProductResult, error) {
	return CatalogProductResult{}, nil
}

// THE SHOP IS FOUND BY ITSELF: a plugin implementing CatalogProvider and writing no ServiceMethods is answered, its
// requests and answers checked.
func TestServeAnswersAShopThatImplementsTheCatalogue(t *testing.T) {
	ctx := context.Background()
	sp := serviceProviderOf(fakeShop{})
	if sp == nil {
		t.Fatal("a CatalogProvider is not found")
	}
	out, ok, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopCatalog, CatalogProductsPage), []byte(`{"limit":10}`))
	if err != nil || !ok {
		t.Fatalf("products.page: %v %v", ok, err)
	}
	var page CatalogPage
	if err := json.Unmarshal(out, &page); err != nil || len(page.Products) != 2 || !page.Products[1].Removed {
		t.Fatalf("the page: %s %v", out, err)
	}
	if _, _, err := DispatchServiceHook(ctx, sp, ServiceHook(ContractShopCatalog, CatalogProductsPage), []byte(`{"limit":500}`)); err == nil {
		t.Error("a page of 500 was asked of the shop")
	}
	if _, _, err := DispatchServiceHook(ctx, serviceProviderOf(fakeShop{broken: true}), ServiceHook(ContractShopCatalog, CatalogProductsPage), []byte(`{}`)); err == nil ||
		!strings.Contains(err.Error(), "availability") {
		t.Errorf("an answer a channel could not use left the shop: %v", err)
	}
	if _, ok, _ := DispatchServiceHook(ctx, sp, ServiceHook(ContractShippingCarrier, CarrierRates), []byte(`{}`)); ok {
		t.Error("a shop answered a carrier's method")
	}
}

func TestACatalogueAnswerAChannelCannotUseIsRefused(t *testing.T) {
	sale := int64(2000)
	for name, p := range map[string]CatalogProduct{
		"no title":        {SKU: "a", URL: "https://x", Variants: []CatalogVariant{{SKU: "a", Currency: "EUR", Availability: AvailabilityInStock}}},
		"a relative URL":  {SKU: "a", Title: "A", URL: "/a", Variants: []CatalogVariant{{SKU: "a", Currency: "EUR", Availability: AvailabilityInStock}}},
		"no variant":      {SKU: "a", Title: "A", URL: "https://x"},
		"a sale above":    {SKU: "a", Title: "A", URL: "https://x", Variants: []CatalogVariant{{SKU: "a", PriceMinor: 1000, SalePriceMinor: &sale, Currency: "EUR", Availability: AvailabilityInStock}}},
		"no currency":     {SKU: "a", Title: "A", URL: "https://x", Variants: []CatalogVariant{{SKU: "a", Availability: AvailabilityInStock}}},
		"a condition":     {SKU: "a", Title: "A", URL: "https://x", Condition: "like new", Variants: []CatalogVariant{{SKU: "a", Currency: "EUR", Availability: AvailabilityInStock}}},
	} {
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (CatalogProductResult{Found: true}).Validate() == nil {
		t.Error("found with no product")
	}
	if (CatalogProduct{SKU: "gone", Removed: true}).Validate() != nil {
		t.Error("a removed product needs nothing but its SKU")
	}
}
