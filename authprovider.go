package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// CONTRIBUTING A WAY TO SIGN IN.
//
// Your plugin can put a button on the site's login page and run whatever protocol dance stands behind it —
// OpenID Connect against a company directory, an OAuth2 provider Core has never heard of. What it cannot do,
// by construction, is decide who somebody is.
//
// # The one rule, and why it is not negotiable
//
// You return an ASSERTION. Core decides what follows. You never mint a session, never name a Nilda user,
// never set a role, and never see the `state` Core is using to bind the flow to a browser.
//
// The reason is arithmetic rather than distrust: a plugin that could say "this person is the owner" would be
// a takeover primitive guarded by one capability string. So the boundary is drawn where it can be CHECKED —
// on an OIDC flow you hand Core the identity provider's own signed token, and Core verifies it against the
// keys that provider publishes. Your plugin could not forge that if it wanted to, which is a much better
// property than a promise not to.
//
// # Two flows, and you declare which
//
//	flow: "oidc"    You return the raw id_token. Core verifies the signature, the issuer, the audience,
//	                the expiry and the nonce, and reads the claims out of the verified token. This is what
//	                SSO uses, and what NewOIDCClient below implements for you.
//
//	flow: "oauth2"  For a provider with no ID token to hand over — GitHub is the usual example. You return
//	                the attributes you fetched. Core cannot verify them, so a new site trusts them for
//	                nothing until its owner says otherwise: a provider on this flow signs in somebody who
//	                has already linked it by hand, and nobody else.
//
// A plugin declaring `oidc` and returning attributes is refused, and vice versa. Declaring the wrong one is
// caught at INSTALL with a sentence, rather than at the callback where you are not looking.
//
// # What Core still owns on your behalf
//
// The `state` and its double-submit cookie, the PKCE verifier, the nonce, the callback URL, the single-use
// server-side record, the account policy, the session, and the cookie. You are handed what you need for one
// request and nothing that would let you replay it.

// Hook names Core dispatches for a declared sign-in method. `ServeAuthProvider` handles them for you; they
// are exported because a plugin that also handles other hooks may prefer its own switch.
const (
	// HookAuthDescribe asks which of your declared methods are usable right now. Answer honestly: a
	// provider whose API key the owner has not typed yet should say so, and its button will not appear.
	HookAuthDescribe = "auth.describe"
	// HookAuthStart asks for the URL to send the browser to.
	HookAuthStart = "auth.start"
	// HookAuthComplete hands you the authorization code and asks for an assertion.
	HookAuthComplete = "auth.complete"
)

// AuthProviderStatus is one declared sign-in method's readiness.
type AuthProviderStatus struct {
	// Key is the LOCAL key from your manifest. Core namespaces it with your plugin key, so two plugins can
	// both offer "sso" and neither can claim the other's identities.
	Key string `json:"key"`
	// Ready false hides the button. A button that always fails reads to a visitor as "this site is broken",
	// not "this plugin is unconfigured".
	Ready bool `json:"ready"`
	// UnreadyReason is for the OWNER, on your plugin's settings page — not for the visitor. "No issuer URL
	// configured" belongs where the person who can fix it is looking.
	UnreadyReason string `json:"unready_reason,omitempty"`
}

// AuthStartRequest is what Core minted for one sign-in attempt.
//
// Every value here is Core's. You put them in the authorization URL and keep none of them.
type AuthStartRequest struct {
	Provider string `json:"provider"` // your LOCAL key
	State    string `json:"state"`
	// Challenge is the PKCE S256 code_challenge. The verifier it came from stays in Core until Complete.
	Challenge string `json:"challenge"`
	// Nonce binds the ID TOKEN to this request, which state does not do. Put it in the authorization
	// request; Core checks the token carries it back.
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirect_uri"`
	// Link is true when somebody already signed in is attaching this provider to their account, rather
	// than signing in with it. Most providers need no different behaviour; some want a different prompt.
	Link bool `json:"link,omitempty"`
	// Locale is the admin's language, for a provider that can be asked to render its own screen in it.
	Locale string `json:"locale,omitempty"`
}

// AuthCompleteRequest is the authorization response, plus what Core kept for you.
//
// Note what is NOT here: the `state`. Checking it is Core's job and it has already been done by the time
// this reaches you.
type AuthCompleteRequest struct {
	Provider    string `json:"provider"`
	Code        string `json:"code"`
	Verifier    string `json:"verifier"` // the PKCE code_verifier, released for exactly this exchange
	Nonce       string `json:"nonce"`
	RedirectURI string `json:"redirect_uri"`
}

// AuthAssertion is what you hand back: proof, or attributes.
type AuthAssertion struct {
	// IDToken is the raw, unmodified ID token, for a plugin on the `oidc` flow. Hand it over exactly as the
	// provider issued it — Core verifies the signature itself, which is the entire reason this field is a
	// token and not a struct you filled in.
	IDToken string `json:"id_token,omitempty"`

	// The fields below are the `oauth2` flow's answer, for a provider with no signed token. Core cannot
	// check any of them, which is why a site trusts them for nothing until its owner decides otherwise.
	Subject string `json:"subject,omitempty"`
	Email   string `json:"email,omitempty"`
	// EmailVerified is your claim that the PROVIDER verified this address. Say false when you do not know.
	// On a new install it changes nothing either way; on a site whose owner has raised the trust level it
	// decides whether an existing account can be linked to automatically, so guessing is not free.
	EmailVerified bool     `json:"email_verified,omitempty"`
	Name          string   `json:"name,omitempty"`
	AvatarURL     string   `json:"avatar_url,omitempty"`
	Groups        []string `json:"groups,omitempty"`
}

// AuthProvider is what you implement. Three methods, no switch.
type AuthProvider interface {
	// Describe reports which declared methods are usable. Called at startup and whenever the plugin's
	// lifecycle changes; cheap and side-effect-free.
	Describe(ctx context.Context, core *Core) []AuthProviderStatus
	// Start returns the URL to send the browser to.
	Start(ctx context.Context, core *Core, req AuthStartRequest) (string, error)
	// Complete turns the authorization code into an assertion.
	Complete(ctx context.Context, core *Core, req AuthCompleteRequest) (AuthAssertion, error)
}

// ServeAuthProvider is Serve for a plugin whose whole job is a sign-in method.
//
// It dispatches the three hooks so you write three methods rather than a switch with three JSON shapes in
// it. A plugin that ALSO handles other hooks should use Serve and call DispatchAuthHook from its own
// HandleHook — the dispatch is the same either way.
func ServeAuthProvider(p AuthProvider) {
	Serve(&authOnlyHandler{p: p})
}

// DispatchAuthHook handles the three auth hooks and reports whether it recognised this one.
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchAuthHook(ctx, p.core, p, hook, payload); handled {
//			return out, err
//		}
//		// ... your own hooks
//	}
func DispatchAuthHook(ctx context.Context, core *Core, p AuthProvider, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookAuthDescribe:
		body, err := json.Marshal(map[string]any{"providers": p.Describe(ctx, core)})
		return body, true, err

	case HookAuthStart:
		var req AuthStartRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, fmt.Errorf("auth.start: %w", err)
		}
		url, err := p.Start(ctx, core, req)
		if err != nil {
			return nil, true, err
		}
		body, err := json.Marshal(map[string]string{"authorize_url": url})
		return body, true, err

	case HookAuthComplete:
		var req AuthCompleteRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, fmt.Errorf("auth.complete: %w", err)
		}
		assertion, err := p.Complete(ctx, core, req)
		if err != nil {
			return nil, true, err
		}
		body, err := json.Marshal(map[string]any{"assertion": assertion})
		return body, true, err
	}
	return nil, false, nil
}

// authOnlyHandler adapts an AuthProvider onto the full Handler interface, so a plugin that does nothing
// else writes nothing else.
type authOnlyHandler struct {
	p    AuthProvider
	core *Core
}

func (h *authOnlyHandler) Init(ctx context.Context, core *Core) (InitResult, error) {
	h.core = core
	// The three hooks are subscribed unconditionally. Core delivers them only to a plugin granted
	// `auth_provider` (it filters on the grant, not on what Init asked for), so asking for them without the
	// capability is harmless — and NOT asking for them with it would be a plugin that installs cleanly and
	// then never hears from the login page.
	return InitResult{Hooks: []string{HookAuthDescribe, HookAuthStart, HookAuthComplete}}, nil
}

func (h *authOnlyHandler) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, handled, err := DispatchAuthHook(ctx, h.core, h.p, hook, payload)
	if !handled {
		return nil, fmt.Errorf("unknown hook %q", hook)
	}
	return out, err
}

func (h *authOnlyHandler) HandleEvent(context.Context, string, []byte) error { return nil }
