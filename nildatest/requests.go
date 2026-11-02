package nildatest

import (
	"net/http"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// REQUESTS SHAPED LIKE CORE'S PROXY. Core's reverse proxy is the only way a request reaches a plugin's resident
// server, and it strips every X-Nilda-* header a client sent before setting its own: who is signed in, the
// identified agent, the visitor's country, language and key. A plugin's own test puts a request in that shape with
// these, instead of spelling header names the SDK keeps to itself (and that nilda.CurrentUser, AgentOf,
// VisitorCountry, VisitorLocale and VisitorKey read back).

// AsUser makes r a request from the signed-in v, as Core's proxy asserts one. It returns r.
func AsUser(r *http.Request, v nilda.Viewer) *http.Request {
	r.Header.Set("X-Nilda-User", v.UserID)
	if v.DisplayName != "" {
		r.Header.Set("X-Nilda-User-Name", v.DisplayName)
	}
	if v.ImpersonatedBy != "" {
		r.Header.Set("X-Nilda-Impersonated-By", v.ImpersonatedBy)
	}
	return r
}

// AsAgent makes r a request from the AI agent a, as Core identified it (Web Bot Auth) and placed it in a tier. It
// returns r.
func AsAgent(r *http.Request, a nilda.Agent) *http.Request {
	r.Header.Set("X-Nilda-Agent", a.Operator)
	r.Header.Set("X-Nilda-Agent-Tier", string(a.Tier))
	return r
}

// Visitor is what Core's proxy says about an anonymous visitor: the country it placed their address in (ISO 3166-1
// alpha-2), the language it served them, and the key it keeps per visitor. An empty field is not sent.
type Visitor struct {
	Country string
	Locale  string
	Key     string
}

// FromVisitor makes r a request from the visitor v. It returns r.
func FromVisitor(r *http.Request, v Visitor) *http.Request {
	for header, value := range map[string]string{"X-Nilda-Visitor-Country": v.Country, "X-Nilda-Visitor-Locale": v.Locale,
		"X-Nilda-Visitor-Key": v.Key} {
		if value != "" {
			r.Header.Set(header, value)
		}
	}
	return r
}
