package nilda

import (
	"net/http/httptest"
	"testing"
)

// TestCurrentUserReadsCoresAssertedHeaders proves the exact header names Core's proxy actually sets
// (internal/plugin/proxy.go / auth.PluginIdentityHeaders in Core) are what this reads — a plugin author
// guessing "x-nilda-user-id" instead of "X-Nilda-User" is the real bug this helper exists to make
// impossible, and this test is what would catch either side ever drifting from the other again.
func TestCurrentUserReadsCoresAssertedHeaders(t *testing.T) {
	r := httptest.NewRequest("GET", "/shop/", nil)
	r.Header.Set("X-Nilda-User", "user-123")
	r.Header.Set("X-Nilda-User-Name", "Ada Lovelace")

	v, ok := CurrentUser(r)
	if !ok {
		t.Fatal("ok = false, want true for a request carrying both headers")
	}
	if v.UserID != "user-123" || v.DisplayName != "Ada Lovelace" {
		t.Errorf("Viewer = %+v, want {user-123, Ada Lovelace}", v)
	}
}

// TestCurrentUserAnonymousWithNoHeader: no X-Nilda-User at all (guest storefront traffic, the common case)
// must answer ok=false, not a Viewer with an empty UserID a caller might use by mistake.
func TestCurrentUserAnonymousWithNoHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/shop/", nil)
	if _, ok := CurrentUser(r); ok {
		t.Error("ok = true for a request with no X-Nilda-User header at all")
	}
}

// TestCurrentUserWithNoDisplayName: a display name is optional (DisplayName could legitimately be empty
// for a real user), but the id alone is enough to say ok=true.
func TestCurrentUserWithNoDisplayName(t *testing.T) {
	r := httptest.NewRequest("GET", "/shop/", nil)
	r.Header.Set("X-Nilda-User", "user-456")

	v, ok := CurrentUser(r)
	if !ok || v.UserID != "user-456" || v.DisplayName != "" {
		t.Errorf("CurrentUser = %+v, %v, want {user-456, \"\"}, true", v, ok)
	}
}
