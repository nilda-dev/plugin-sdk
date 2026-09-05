package nilda

import "net/http"

// Viewer is the identity Core asserts about whoever made a request to your resident HTTP server — see
// CurrentUser. Deliberately minimal: an id and a display name, never a token, session, or permission set
// you could replay against Core (SPEC_98 §2's own limit on what this channel carries).
type Viewer struct {
	UserID      string
	DisplayName string
}

// currentUserHeader/currentUserNameHeader are the exact header names Core's proxy sets — spelled out here
// once so a plugin author never has to know or guess them (a mismatched header name — a plugin reading
// "x-nilda-user-id" while Core asserts "X-Nilda-User" — was a real, live bug this constant exists to make
// impossible to repeat).
const (
	currentUserHeader     = "X-Nilda-User"
	currentUserNameHeader = "X-Nilda-User-Name"
)

// CurrentUser reports the Core user behind a request your resident HTTP server received — the `route`
// capability's identity channel (SPEC_98 §2). Read it from the SAME *http.Request your handler was called
// with; there is nowhere else it could come from, since Core's reverse proxy (internal/plugin/proxy.go) is
// the ONLY way traffic reaches this server in production, and it strips every inbound X-Nilda-* header
// before asserting its own — a client cannot forge this by sending the header itself.
//
// ok is false for an anonymous visitor, which is most public/storefront traffic — not an error. There is
// nothing wrong with an anonymous request; this simply has nothing to report about who it is.
//
//	if viewer, ok := nilda.CurrentUser(r); ok {
//	    // viewer.UserID is a Core user id — key your own per-customer data to it directly.
//	}
func CurrentUser(r *http.Request) (Viewer, bool) {
	id := r.Header.Get(currentUserHeader)
	if id == "" {
		return Viewer{}, false
	}
	return Viewer{UserID: id, DisplayName: r.Header.Get(currentUserNameHeader)}, true
}
