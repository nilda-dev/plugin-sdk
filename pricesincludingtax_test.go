package nilda

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// v0.50.0 (the shop's Unit 12 end review, EU Directive 98/6/EC): whether a consumer price on this site must include tax,
// as Core's proxy tells the shop's own routes — the answer its hook queries carry as ShowPricesIncludingTax. "1" is
// yes; anything else, or nothing (an older Core, a site where net is allowed), is no.
func TestShowsPricesIncludingTaxIsCoresOneOrNothing(t *testing.T) {
	for in, want := range map[string]bool{"1": true, "": false, "0": false, "true": false, "yes": false, " 1": false} {
		r := httptest.NewRequest(http.MethodGet, "/shop/cart", nil)
		if in != "" {
			r.Header.Set("X-Nilda-Prices-Include-Tax", in)
		}
		if got := ShowsPricesIncludingTax(r); got != want {
			t.Errorf("%q → %v, want %v", in, got, want)
		}
	}
}

// The header and its one "yes" are Core's: its proxy sends pricesIncludeTaxHeader with "1" to the shop's own routes
// (internal/plugin/commerce.go RouteHeaders, core#65). A rename on either side would leave the shop showing net again
// with every test of its own still green.
func TestThePricesIncludeTaxHeaderIsCores(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "commerce.go")
	for _, want := range []string{
		`pricesIncludeTaxHeader = "` + pricesIncludeTaxHeader + `"`,
		`map[string]string{pricesIncludeTaxHeader: "1"}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("core's internal/plugin/commerce.go no longer has %q — re-read ShowsPricesIncludingTax against it", want)
		}
	}
}
