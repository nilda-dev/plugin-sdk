package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// groupedShop keeps price groups: the bakery's account is in b2b:g1, everybody else sees the public price. It prices
// by the viewer it is handed, the way a shop must — by the group alone.
type groupedShop struct {
	catalogueOnly
	err  error
	name string
}

func (g groupedShop) PriceGroup(_ context.Context, userID string) (string, error) {
	if g.err != nil {
		return "", g.err
	}
	if g.name != "" {
		return g.name, nil
	}
	if userID == "u-bakery" {
		return "b2b:g1", nil
	}
	return "", nil
}

func (groupedShop) Products(_ context.Context, q CommerceQuery) ([]CommerceProduct, error) {
	price := "€20.00"
	if q.Viewer != nil && q.Viewer.PriceGroup == "b2b:g1" {
		price = "€15.00"
	}
	return []CommerceProduct{{ID: "flour", Title: "Flour", Price: price}}, nil
}

func (groupedShop) Product(ctx context.Context, id string) (CommerceProduct, bool) {
	price := "€20.00"
	if v, ok := CommerceViewerOf(ctx); ok && v.PriceGroup == "b2b:g1" && v.UserID == "u-bakery" {
		price = "€15.00"
	}
	return CommerceProduct{ID: id, Title: "Flour", Price: price}, true
}

func dispatchShop(t *testing.T, c Commerce, hook, payload string) (string, error) {
	t.Helper()
	out, handled, err := DispatchCommerceHook(context.Background(), nil, c, hook, []byte(payload))
	if !handled {
		t.Fatalf("%s was not handled", hook)
	}
	return string(out), err
}

// A SIGNED-IN BUYER'S PRICE GROUP, AND THEIR PRICES: the shop names the bakery's group and nobody else's; the
// product page and the grid it is then asked for carry the viewer, and price it; a shop without groups answers
// none, and a group name Core could not keep apart is refused rather than passed on.
func TestAPriceGroupReachesTheShopsPrices(t *testing.T) {
	if got, err := dispatchShop(t, groupedShop{}, HookCommercePriceGroup, `{"user_id":"u-bakery"}`); err != nil || got != `{"price_group":"b2b:g1"}` {
		t.Fatalf("the bakery's group: %s %v", got, err)
	}
	if got, _ := dispatchShop(t, groupedShop{}, HookCommercePriceGroup, `{"user_id":"u-eva"}`); got != `{"price_group":""}` {
		t.Errorf("a visitor with no group: %s", got)
	}
	if got, _ := dispatchShop(t, catalogueOnly{}, HookCommercePriceGroup, `{"user_id":"u-bakery"}`); got != `{"price_group":""}` {
		t.Errorf("a shop without price groups: %s", got)
	}
	if _, err := dispatchShop(t, groupedShop{err: errors.New("down")}, HookCommercePriceGroup, `{"user_id":"u-bakery"}`); err == nil {
		t.Error("a shop that could not say answered as if it had")
	}
	for _, bad := range []string{"has space", strings.Repeat("g", MaxPriceGroupLength+1), "naïve"} {
		if _, err := dispatchShop(t, groupedShop{name: bad}, HookCommercePriceGroup, `{"user_id":"u-bakery"}`); err == nil {
			t.Errorf("a group named %q was passed to Core's cache keys", bad)
		}
	}

	viewer := `"viewer":{"user_id":"u-bakery","price_group":"b2b:g1"}`
	if got, _ := dispatchShop(t, groupedShop{}, HookCommerceProduct, `{"id":"flour",`+viewer+`}`); !strings.Contains(got, "€15.00") {
		t.Errorf("the bakery's product page: %s", got)
	}
	if got, _ := dispatchShop(t, groupedShop{}, HookCommerceProduct, `{"id":"flour"}`); !strings.Contains(got, "€20.00") {
		t.Errorf("the public product page: %s", got)
	}
	if got, _ := dispatchShop(t, groupedShop{}, HookCommerceProducts, `{`+viewer+`}`); !strings.Contains(got, "€15.00") {
		t.Errorf("the bakery's grid: %s", got)
	}
	// A viewer with no group is no viewer: nothing personal is priced for one.
	if got, _ := dispatchShop(t, groupedShop{}, HookCommerceProduct, `{"id":"flour","viewer":{"user_id":"u-bakery"}}`); !strings.Contains(got, "€20.00") {
		t.Errorf("a viewer with no group was priced as a member: %s", got)
	}
}

// Serve subscribes commerce.price_group only for a shop that keeps price groups, so Core asks no other.
func TestOnlyAShopWithPriceGroupsIsAskedForOne(t *testing.T) {
	has := func(c Commerce) bool {
		res, err := (&commerceOnlyHandler{c: c}).Init(context.Background(), &Core{PluginKey: "shop"})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Hooks {
			if h == HookCommercePriceGroup {
				return true
			}
		}
		return false
	}
	if !has(groupedShop{}) || has(catalogueOnly{}) {
		t.Error("commerce.price_group is subscribed by the wrong shops")
	}
	b, _ := json.Marshal(CommerceQuery{})
	if strings.Contains(string(b), "viewer") {
		t.Errorf("a query with no viewer sends one: %s", b)
	}
}
