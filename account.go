package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// CONNECTING THE OWNER'S OWN ACCOUNT ELSEWHERE — a sign-in on another service (Higgsfield, a Google account, an
// Instagram business account) whose tokens your plugin keeps.
//
// # Core runs the road; your plugin speaks to the service
//
// Declare it in plugin.json, and the owner gets a Connect button on your plugin's screen in the admin AND in the
// Nilda app, with its status — no page of yours, no link in a row:
//
//	"account": {"name": "Higgsfield", "host": "higgsfield.ai"}
//
// When the owner presses Connect, Core — never your plugin — makes a single-use link to its own route, opens it in a
// browser (on a phone, the phone's own sign-in sheet), and asks you for where to send that browser
// (AccountStartHook). It sends the browser there only if the address is https on your declared host or a subdomain
// of it, and binds the sign-in to that browser with a cookie. The service sends the browser back to Core's callback;
// Core checks the cookie and the state, and hands you what came back (AccountFinishHook) to trade for tokens, which
// you keep with SetSecret. Then Core asks you how the account stands (AccountStatusHook) and shows it.
//
// The press opens the link at once, and it works once and for two minutes; the callback refuses any browser that did
// not open it. So a link copied after it was opened, or a sign-in finished in another browser, connects nobody's
// account to the site. That is the attack the
// old "sign-in link in a report row" left open (login CSRF, RFC 9700 §2.1), and why a plugin no longer mints the
// link itself.
//
// # Implementing AccountHandler is the whole job
//
// Serve subscribes the four hooks for a Handler that implements AccountHandler and answers them before your
// HandleHook sees them, as it does for a payment gateway. DispatchAccount is there for a plugin that routes hooks by
// hand. The grant is the `account` block itself; no capability is asked for.

// AccountStartHook asks where to send the owner's browser to sign in.
const AccountStartHook = "account.start"

// AccountFinishHook hands over what the service sent back, to trade for tokens.
const AccountFinishHook = "account.finish"

// AccountStatusHook asks how the account stands now. Core asks it whenever a screen shows the account, so answer
// from what you keep — a call to the service on every open is a call too many.
const AccountStatusHook = "account.status"

// AccountDisconnectHook forgets the account: drop the tokens you keep, and revoke them at the service if it lets you.
const AccountDisconnectHook = "account.disconnect"

// PluginAccount is the plugin-level `account` block of plugin.json.
type PluginAccount struct {
	// Name is the service, as the owner knows it: "Higgsfield", "Google". The app says "Connect your <Name>
	// account" and "A secure sign-in opens from <Host>".
	Name string `json:"name"`
	// Host is where the sign-in lives, without a scheme: "higgsfield.ai". Core sends the owner's browser only to
	// https on this host or a subdomain of it, whatever your start hook answers.
	Host string `json:"host"`
}

// AccountStart is Core's question: where should the browser go? RedirectURI is Core's callback on this site, the one
// address to register with the service and to send it back to — it is the same every time for your plugin.
type AccountStart struct {
	RedirectURI string `json:"redirect_uri"`
}

// AccountStartResult is your answer. AuthorizeURL is the service's sign-in address, with your client, PKCE challenge
// and State already in it; State is the value you put there, which Core binds to the owner's browser and gives back
// to you in AccountFinish. Keep what you need to finish (a PKCE verifier) under that state, for ten minutes.
type AccountStartResult struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// AccountFinish is what came back to Core's callback, after Core checked the browser and the state: the state you
// gave, and the service's `code` — or its `error` when the owner pressed Cancel or the service refused.
//
// Iss is the `iss` the service sent with it (RFC 9207), "" when it sent none. Compare it with the issuer of the
// server you sent the browser to: a mismatch MUST be refused, and when that server's metadata says
// `authorization_response_iss_parameter_supported`, so must a missing one (RFC 9207 §2.4). It is what tells two
// sign-in servers apart for a plugin that talks to more than one (a mix-up attack, RFC 9700 §4.4); with one server
// and Core's callback per plugin, it is a second check. An `error` with a wrong or missing Iss is not the service's.
type AccountFinish struct {
	RedirectURI string `json:"redirect_uri"`
	State       string `json:"state"`
	Code        string `json:"code,omitempty"`
	Error       string `json:"error,omitempty"`
	Iss         string `json:"iss,omitempty"`
}

// AccountStatus is how the account stands. Who names the connected account as the owner would recognise it — an
// email, a handle — and never a token; Problem says in one fixed sentence why a connected account does not work
// (its sign-in ran out, the service refuses it), translated through your manifest's translations.
type AccountStatus struct {
	Connected bool   `json:"connected"`
	Who       string `json:"who,omitempty"`
	Problem   string `json:"problem,omitempty"`
}

// AccountHandler is your side of the road; DispatchAccount routes Core's four hooks to it.
type AccountHandler interface {
	AccountStart(context.Context, AccountStart) (AccountStartResult, error)
	AccountFinish(context.Context, AccountFinish) (AccountStatus, error)
	AccountStatus(context.Context) (AccountStatus, error)
	AccountDisconnect(context.Context) error
}

// DispatchAccount routes the account hooks to h — Serve does it for you; this is for a plugin that routes hooks by
// hand, the shape DispatchAdminAction uses:
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchAccount(ctx, hook, payload, p); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
// An error you return is your answer, shown to the owner as the reason (a fixed sentence, translated through
// plugin.json): "Higgsfield's sign-in could not be started". handled=false means the hook was not one of these.
func DispatchAccount(ctx context.Context, hook string, payload []byte, h AccountHandler) (out []byte, handled bool, err error) {
	switch hook {
	case AccountStartHook, AccountFinishHook, AccountStatusHook, AccountDisconnectHook:
	default:
		return nil, false, nil
	}
	if h == nil {
		return nil, true, fmt.Errorf("nilda: %s was called and this plugin registered no account handler", hook)
	}
	var res any
	switch hook {
	case AccountStartHook:
		var in AccountStart
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: %s payload: %w", hook, err)
		}
		res, err = h.AccountStart(ctx, in)
	case AccountFinishHook:
		var in AccountFinish
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: %s payload: %w", hook, err)
		}
		res, err = h.AccountFinish(ctx, in)
	case AccountStatusHook:
		res, err = h.AccountStatus(ctx)
	case AccountDisconnectHook:
		res, err = struct{}{}, h.AccountDisconnect(ctx)
	}
	if err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}
