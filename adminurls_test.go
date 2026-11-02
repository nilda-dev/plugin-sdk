package nilda

import (
	"strings"
	"testing"
)

func TestTheAdminAddressesAreTheAdminsOwn(t *testing.T) {
	if got := AdminPageURL("https://shop.example/", "nilda_ecommerce", "quotes"); got != "https://shop.example/admin/plugin/nilda_ecommerce/quotes" {
		t.Errorf("a page: %s", got)
	}
	if got := AdminFormURL("https://shop.example", "nilda_ecommerce", "orders", "a b"); got != "https://shop.example/admin/plugin/nilda_ecommerce/orders/edit/a%20b" {
		t.Errorf("a form: %s", got)
	}
	if got := AdminFormURL("https://shop.example", "k", "p", ""); !strings.HasSuffix(got, "/admin/plugin/k/p/new") {
		t.Errorf("a new record's form: %s", got)
	}
	spa := coreSource(t, "web", "admin", "src", "data", "pluginadmin.ts")
	for _, want := range []string{"`/plugin/${encodeURIComponent(pluginKey)}/${encodeURIComponent(pageKey)}`",
		"`edit/${encodeURIComponent(id)}` : 'new'"} {
		if !strings.Contains(spa, want) {
			t.Errorf("Core's admin no longer draws a plugin page at %s", want)
		}
	}
	if base := coreSource(t, "web", "admin", "src", "adminPath.ts"); !strings.Contains(base, "export const ADMIN_BASENAME = '/admin';") {
		t.Error("Core's admin is no longer under /admin")
	}
}
