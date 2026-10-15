package nilda

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

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
	visitorCountryHeader  = "X-Nilda-Visitor-Country"
	visitorKeyHeader      = "X-Nilda-Visitor-Key"
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

// VisitorCountry is the country Core placed the visitor in from their internet address — ISO 3166-1 alpha-2 ("AT") —
// for a request your resident HTTP server received. Core resolves it as it does for its own analytics (a country
// database, the address trimmed to its /24 first) and sends the country, never the address; it strips any value a
// client sent, as it does every X-Nilda-* header.
//
// It is EVIDENCE, not identity: one of the items EU VAT law accepts for where a buyer of a digital service is
// (Implementing Regulation 282/2011 Art. 24f(b)) — keep it with the sale. ok is false when Core could not place the
// address (a private network, an install without the country database).
func VisitorCountry(r *http.Request) (string, bool) {
	c := r.Header.Get(visitorCountryHeader)
	if len(c) != 2 || c[0] < 'A' || c[0] > 'Z' || c[1] < 'A' || c[1] > 'Z' {
		return "", false
	}
	return c, true
}

// VisitorKey is an opaque key for the visitor's internet address, for a request your resident HTTP server received —
// to LIMIT a public write by (a form that mails whoever it is given, a sign-up), never to identify anyone. Core sends
// a keyed hash of the address (the whole IPv4 address, an IPv6 address's /64) that changes every UTC day, never the
// address; the same visitor reads the same key all day. It strips any value a client sent, as it does every X-Nilda-*
// header. "" when Core sent none (an older Core). From v0.23.0.
func VisitorKey(r *http.Request) string {
	k := r.Header.Get(visitorKeyHeader)
	if len(k) > 64 {
		return ""
	}
	return k
}

// AGENTS THAT PROVE WHO THEY ARE (Web Bot Auth). An AI agent — ChatGPT's agent, Google's — can sign its requests
// with a key its operator publishes; Core checks the signature and tells your resident server who it is and what the
// site's owner lets it do (from the release built on plugin-sdk v0.20.0). An unsigned request, or a signature that
// does not check, is an ordinary visitor: ok false. Core strips any value a client sent, as it does every X-Nilda-*
// header, so this cannot be claimed.

// AgentTier is what the site's owner lets an identified agent do.
type AgentTier string

const (
	// AgentBrowse: read, build a basket, get a quote — what any visitor may. The default.
	AgentBrowse AgentTier = "browse"
	// AgentBuy: also pay for an order with a payment token it carries; the buyer it acts for still pays.
	AgentBuy AgentTier = "buy"
	// AgentBlocked: the owner refuses it. Core refuses a blocked agent that signs; API().AgentTier answers it for one
	// a plugin knows by other means (an API key), which the plugin then refuses itself. From v0.23.0.
	AgentBlocked AgentTier = "blocked"
)

// AgentTier asks Core what the site's owner lets an AI agent's operator do — the one list on SEO → Shopping agents
// (from the release built on plugin-sdk v0.23.0). A plugin that serves an agent platform by an API key rather than a
// signature (an ACP adapter) asks it for its platform's operator instead of keeping a "may pay" switch of its own; a
// shop asks it for the agent a checkout names rather than trusting what a calling plugin claims. An operator the owner
// never named browses.
func (a *API) AgentTier(ctx context.Context, operator string) (AgentTier, error) {
	var out struct {
		Data struct {
			Tier AgentTier `json:"tier"`
		} `json:"data"`
	}
	if err := a.Get(ctx, "/agents/tier", url.Values{"operator": {operator}}, &out); err != nil {
		return "", err
	}
	switch out.Data.Tier {
	case AgentBrowse, AgentBuy, AgentBlocked:
		return out.Data.Tier, nil
	}
	return "", fmt.Errorf("nilda: Core answered the tier %q for %s", out.Data.Tier, operator)
}

// Agent is an identified AI agent: the host of its operator ("chatgpt.com") and its tier.
type Agent struct {
	Operator string    `json:"operator"`
	Tier     AgentTier `json:"tier"`
}

const (
	agentHeader     = "X-Nilda-Agent"
	agentTierHeader = "X-Nilda-Agent-Tier"
)

// AgentOf is the identified agent behind a request your resident server received.
func AgentOf(r *http.Request) (Agent, bool) {
	op := r.Header.Get(agentHeader)
	if op == "" {
		return Agent{}, false
	}
	return Agent{Operator: op, Tier: AgentTier(r.Header.Get(agentTierHeader))}, true
}
