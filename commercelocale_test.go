package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// localeShop answers each hook with the language its context carries, so the test reads what the dispatcher lifted.
type localeShop struct{ seen []string }

func (s *localeShop) Products(ctx context.Context, q CommerceQuery) ([]CommerceProduct, error) {
	l, _ := CommerceLocaleOf(ctx)
	s.seen = append(s.seen, "products:"+q.Locale+"/"+l)
	return nil, nil
}
func (s *localeShop) Product(ctx context.Context, id string) (CommerceProduct, bool) {
	l, _ := CommerceLocaleOf(ctx)
	s.seen = append(s.seen, "product:"+l)
	return CommerceProduct{ID: id, Title: "t-" + l}, true
}
func (s *localeShop) ProductForContent(ctx context.Context, id string) (CommerceProduct, bool) {
	l, _ := CommerceLocaleOf(ctx)
	s.seen = append(s.seen, "content:"+l)
	return CommerceProduct{ID: id, Title: "t-" + l}, true
}
func (s *localeShop) CountProducts(ctx context.Context, q CommerceQuery) (int, bool) {
	l, _ := CommerceLocaleOf(ctx)
	s.seen = append(s.seen, "count:"+l)
	return 0, true
}
func (s *localeShop) Endpoints(context.Context) CommerceEndpoints {
	return CommerceEndpoints{SearchSuggest: "/shop/search/suggest"}
}

// THE PAGE'S LANGUAGE REACHES EVERY COMMERCE HOOK: on the query, and in the context for the hooks without one; a value
// that is no language code is no language.
func TestTheCommerceHooksCarryThePagesLanguage(t *testing.T) {
	s := &localeShop{}
	ctx := context.Background()
	for _, c := range []struct{ hook, payload string }{
		{HookCommerceProducts, `{"locale":"fr"}`},
		{HookCommerceProduct, `{"id":"A","locale":"fa"}`},
		{HookCommerceProductForContent, `{"content_id":"x","locale":"de"}`},
		{HookCommerceCount, `{"locale":"es"}`},
		{HookCommerceProduct, `{"id":"A","locale":"FR"}`},
		{HookCommerceProduct, `{"id":"A"}`},
	} {
		if _, handled, err := DispatchCommerceHook(ctx, nil, s, c.hook, []byte(c.payload)); !handled || err != nil {
			t.Fatalf("%s %s: handled=%v err=%v", c.hook, c.payload, handled, err)
		}
	}
	want := "products:fr/fr content:de count:es product:fa product: product:"
	got := strings.Join(s.seen, " ")
	for _, w := range strings.Fields(want) {
		if !strings.Contains(got, w) {
			t.Fatalf("hooks saw %q, want each of %q", got, want)
		}
	}
	out, _, _ := DispatchCommerceHook(ctx, nil, s, HookCommerceEndpoints, nil)
	var ep CommerceEndpoints
	if err := json.Unmarshal(out, &ep); err != nil || ep.SearchSuggest != "/shop/search/suggest" {
		t.Fatalf("endpoints = %s (%v)", out, err)
	}
}

// A SEARCH ANSWER IS HELD TO WHAT THE BOX DRAWS.
func TestASearchAnswerIsBounded(t *testing.T) {
	ok := CommerceSearchAnswer{Query: "candel", Corrected: "candle", Suggestions: []string{"candle holder"},
		Products: []CommerceSearchHit{{ID: "C1", Title: "Candle", URL: "/product/candle", Price: "€5.00"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a good answer refused: %v", err)
	}
	for name, bad := range map[string]CommerceSearchAnswer{
		"repeats the query": {Query: "bowl", Suggestions: []string{"Bowl"}},
		"twice":             {Query: "b", Suggestions: []string{"bowl", "bowl"}},
		"no page":           {Query: "b", Products: []CommerceSearchHit{{ID: "x", Title: "X", URL: "javascript:alert(1)"}}},
		"another site":      {Query: "b", Products: []CommerceSearchHit{{ID: "x", Title: "X", URL: "//evil.example/x"}}},
		"no title":          {Query: "b", Products: []CommerceSearchHit{{ID: "x", URL: "/x"}}},
		"too many":          {Query: "b", Suggestions: strings.Fields("a b2 c d e f g h i")},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
