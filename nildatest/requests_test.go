package nildatest

import (
	"net/http/httptest"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// What the helpers set is what the SDK's readers read: a renamed header breaks this, not a plugin's test.
func TestRequestsReadBackThroughTheSDK(t *testing.T) {
	r := AsUser(httptest.NewRequest("GET", "/shop", nil), nilda.Viewer{UserID: "u-1", DisplayName: "Eva", ImpersonatedBy: "staff-2"})
	if v, ok := nilda.CurrentUser(r); !ok || v.UserID != "u-1" || v.DisplayName != "Eva" || v.ImpersonatedBy != "staff-2" {
		t.Errorf("CurrentUser read %+v %v", v, ok)
	}
	r = AsAgent(httptest.NewRequest("GET", "/shop", nil), nilda.Agent{Operator: "chatgpt.com", Tier: nilda.AgentBuy})
	if a, ok := nilda.AgentOf(r); !ok || a.Operator != "chatgpt.com" || a.Tier != nilda.AgentBuy {
		t.Errorf("AgentOf read %+v %v", a, ok)
	}
	r = FromVisitor(httptest.NewRequest("GET", "/shop", nil), Visitor{Country: "AT", Locale: "de", Key: "k-9"})
	if c, ok := nilda.VisitorCountry(r); !ok || c != "AT" {
		t.Errorf("VisitorCountry read %q %v", c, ok)
	}
	if l, ok := nilda.VisitorLocale(r); !ok || l != "de" {
		t.Errorf("VisitorLocale read %q %v", l, ok)
	}
	if k := nilda.VisitorKey(r); k != "k-9" {
		t.Errorf("VisitorKey read %q", k)
	}
	if nilda.ShowsPricesIncludingTax(r) {
		t.Error("a visitor on a site where net is allowed reads as gross")
	}
	if !nilda.ShowsPricesIncludingTax(FromVisitor(httptest.NewRequest("GET", "/shop/cart", nil), Visitor{ShowPricesIncludingTax: true})) {
		t.Error("ShowPricesIncludingTax does not read back through nilda.ShowsPricesIncludingTax")
	}
	if _, ok := nilda.CurrentUser(FromVisitor(httptest.NewRequest("GET", "/", nil), Visitor{})); ok {
		t.Error("an anonymous visitor reads as signed in")
	}
}
