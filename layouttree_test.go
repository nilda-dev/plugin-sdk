package nilda

import (
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// The tree's JSON names are Core's LayoutNode's (a subset: the fields a plugin writes), and the kinds are Core's.
func TestTheLayoutTreeIsCoresShape(t *testing.T) {
	src := coreSource(t, "internal", "pagebuilder", "layout.go")
	node := upTo(t, from(t, src, "type LayoutNode struct {", "core pagebuilder/layout.go"), "\n}", "LayoutNode")
	core := map[string]bool{}
	for _, m := range regexp.MustCompile("json:\"([a-z_]+)").FindAllStringSubmatch(node, -1) {
		core[m[1]] = true
	}
	rt := reflect.TypeOf(LayoutNode{})
	for i := 0; i < rt.NumField(); i++ {
		name := regexp.MustCompile(`^[a-z_]+`).FindString(rt.Field(i).Tag.Get("json"))
		if !core[name] {
			t.Errorf("LayoutNode.%s (%q) is not a field of Core's LayoutNode", rt.Field(i).Name, name)
		}
	}
	tree := upTo(t, from(t, src, "type LayoutTree struct {", "core pagebuilder/layout.go"), "\n}", "LayoutTree")
	if !regexp.MustCompile("Nodes +\\[\\]LayoutNode +`json:\"nodes\"`").MatchString(tree) {
		t.Error("Core's LayoutTree no longer holds its nodes under \"nodes\"")
	}
	for _, k := range []string{`KindSection = "section"`, `KindColumn  = "column"`, `KindWidget  = "widget"`} {
		if !regexp.MustCompile(regexp.QuoteMeta(k)).MatchString(src) {
			t.Errorf("Core's node kinds changed: %s", k)
		}
	}
}

// Every widget key is one Core's registry lists, and every shop widget Core lists is named here.
func TestTheWidgetKeysAreInCoresRegistry(t *testing.T) {
	var reg struct {
		Widgets []struct {
			Type      string `json:"type"`
			NeedsShop bool   `json:"needs_shop"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal([]byte(coreSource(t, "docs", "WIDGET_REGISTRY.json")), &reg); err != nil {
		t.Fatal(err)
	}
	known, shop := map[string]bool{}, []string{}
	for _, w := range reg.Widgets {
		known[w.Type] = true
		if w.NeedsShop {
			shop = append(shop, w.Type)
		}
	}
	ours := []string{WidgetAccount, WidgetAddToCart, WidgetCart, WidgetCartCount, WidgetCheckout, WidgetProductField,
		WidgetProductGallery, WidgetProductLoop, WidgetProductReviews, WidgetProductTerms, WidgetExpressCheckout,
		WidgetProductFilter, WidgetPaymentMethods}
	for _, k := range append(ours, WidgetButton, WidgetPostContent) {
		if !known[k] {
			t.Errorf("%q is not a widget in Core's registry", k)
		}
	}
	sort.Strings(shop)
	sort.Strings(ours)
	if !reflect.DeepEqual(shop, ours) {
		t.Errorf("Core's shop widgets are %v; the SDK names %v", shop, ours)
	}
}
