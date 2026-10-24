package nilda

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// v0.43.0 (Core's M3 card A8): the language the visitor is reading, as Core's proxy sends it — one of the site's
// languages, a lowercase tag, or nothing from an older Core.
func TestVisitorLocaleIsTheSitesLanguageOrNothing(t *testing.T) {
	for in, want := range map[string]string{"fr": "fr", "pt": "pt", "": "", "FR": "", "fr-FR;q=1": "", "x": "", "<b>": ""} {
		r := httptest.NewRequest(http.MethodGet, "/shop/checkout", nil)
		if in != "" {
			r.Header.Set("X-Nilda-Visitor-Locale", in)
		}
		got, ok := VisitorLocale(r)
		if got != want || ok != (want != "") {
			t.Errorf("%q → %q %v", in, got, ok)
		}
	}
	if src := coreSource(t, "internal", "plugin", "visitorlocale.go"); !strings.Contains(src, `visitorLocaleHeader = "X-Nilda-Visitor-Locale"`) {
		t.Error("core's proxy no longer sends X-Nilda-Visitor-Locale — VisitorLocale reads nothing")
	}
}
