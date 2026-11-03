package nilda

import (
	"net/http"
	"net/http/httptest"
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
