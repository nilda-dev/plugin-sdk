package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// AN OPENID CONNECT CLIENT, SO YOU DO NOT WRITE ONE.
//
// A standards-compliant SSO plugin is three HTTP calls: read the provider's discovery document, build an
// authorization URL, exchange a code for a token. None of it is hard and all of it is easy to get subtly
// wrong — and Nilda has the evidence, because Core shipped a function called `OIDC` for a year that did the
// exchange, threw the ID token away, and asked a userinfo endpoint who the person was. That is not a
// criticism of whoever wrote it; it is what happens when every author re-derives the same three calls.
//
// So the SDK does them. What is left for you is the part that is actually yours: which claim your directory
// puts groups in, whether your IdP wants an extra scope, and what to call the button.
//
// # What this deliberately does NOT do
//
// It does not verify the ID token. Core does that, against the keys the issuer publishes, and that division
// is the whole security model rather than a division of labour: a plugin that verified its own token would
// be asking to be believed, which is exactly what the boundary exists to avoid. So `Exchange` hands the raw
// token back untouched.

// OIDCClient is a discovery-backed OpenID Connect client for one identity provider.
//
// Safe for concurrent use. Discovery happens once and is cached for the life of the process — a plugin is
// resident, and re-reading the document on every sign-in would make the provider's availability your
// availability.
type OIDCClient struct {
	issuer       string
	clientID     string
	clientSecret string
	scopes       []string

	http *http.Client

	mu   sync.Mutex
	meta *oidcMetadata
}

type oidcMetadata struct {
	Issuer        string `json:"issuer"`
	AuthorizeURL  string `json:"authorization_endpoint"`
	TokenURL      string `json:"token_endpoint"`
	EndSessionURL string `json:"end_session_endpoint"`
}

// NewOIDCClient builds a client for one issuer.
//
// `issuer` is the URL the provider publishes as its own identity — https://acme.okta.com, not the
// authorize endpoint. Everything else is discovered from it. Take it from your plugin's settings page so
// the SITE OWNER types it: it is the fact that decides which provider a site trusts, and a plugin choosing
// it for them is a plugin choosing who may sign in.
//
// Scopes are always the three every OIDC provider understands — openid, email, profile — plus any you pass.
// Add to them when your directory needs it: Entra wants "GroupMember.Read.All" territory for some group
// configurations, Okta may want a custom scope. What you pass is ADDED, never a replacement: a list that
// dropped `openid` would not be an OIDC request at all, and one without `email` signs nobody in.
func NewOIDCClient(issuer, clientID, clientSecret string, scopes ...string) *OIDCClient {
	all := []string{"openid", "email", "profile"}
	for _, s := range scopes {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(all, s) {
			all = append(all, s)
		}
	}
	return &OIDCClient{
		issuer: strings.TrimRight(issuer, "/"), clientID: clientID, clientSecret: clientSecret,
		scopes: all,
		// HTTPClient: it carries the plugin's key as a header as well as a timeout. The egress proxy has to
		// know who is calling to apply the right manifest's hosts, and today any client that takes its
		// proxy from the environment is identified by the proxy address Core hands the process
		// (HTTPS_PROXY=http://<key>@…) — a bare http.Client included.
		//
		// That was not always so. This client shipped with a bare `&http.Client{}` while the header was the
		// ONLY identity the proxy read, and the mistake was invisible in tests, which have no proxy, and fatal
		// in production, which does: the plugin reported itself not ready, and the reason was Forbidden
		// from a proxy that could not tell who was asking. The key in the proxy address (core's
		// faultisolation.go, the 2026-09-23 plugin hunt's M6) is what closed that for every client.
		//
		// What bounds a login is the context of the call Core made — its per-call deadline (5s by default,
		// PLUGIN_CALL_TIMEOUT) travels in ctx, and every request below is made with it. This client's own
		// timeout is only a backstop for a call made without one; it used to be 10s and was described as
		// what kept a login short, which it never was, since Core's deadline always fires first.
		http: HTTPClient(0),
	}
}

// Discover reads and caches the provider's metadata.
//
// Called for you by AuthorizeURL and Exchange; exported because it is also the honest answer to "is this
// configured correctly", which is what Describe wants to report.
func (c *OIDCClient) Discover(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.meta != nil {
		return nil
	}
	if c.issuer == "" {
		return errors.New("oidc: no issuer configured")
	}
	if c.clientID == "" || c.clientSecret == "" {
		return errors.New("oidc: no client id or secret configured")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("oidc: reading the discovery document: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("oidc: %s answered %d for its discovery document", c.issuer, res.StatusCode)
	}

	var meta oidcMetadata
	// Bounded: a discovery document is a few hundred bytes and an unbounded read from a host you were told
	// about is a way to be handed a gigabyte.
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&meta); err != nil {
		return fmt.Errorf("oidc: the discovery document did not parse: %w", err)
	}
	// A document claiming to speak for a different issuer than the one it was fetched from is the first
	// move in an issuer-confusion attack, and it costs one comparison to refuse.
	if strings.TrimRight(meta.Issuer, "/") != c.issuer {
		return fmt.Errorf("oidc: %s publishes a document claiming to be %q", c.issuer, meta.Issuer)
	}
	if meta.AuthorizeURL == "" || meta.TokenURL == "" {
		return errors.New("oidc: the discovery document names no authorization or token endpoint")
	}
	c.meta = &meta
	return nil
}

// Ready reports whether this client is configured and its provider reachable — the answer Describe wants.
//
// The reason is written for the SITE OWNER, on your settings page, because they are the only person who can
// act on it.
func (c *OIDCClient) Ready(ctx context.Context) (bool, string) {
	if err := c.Discover(ctx); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// EndSessionURL is the provider's logout endpoint, if it publishes one.
//
// For RP-initiated logout: sending somebody to it ends their session AT THE DIRECTORY, not just here. Empty
// when the provider does not offer it, which is common enough that a plugin must handle the empty case
// rather than assume.
func (c *OIDCClient) EndSessionURL(ctx context.Context) string {
	if err := c.Discover(ctx); err != nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.meta.EndSessionURL
}

// AuthorizeURL builds the URL to send the browser to, from Core's own request.
//
// Every security parameter comes from the AuthStartRequest: Core minted them and holds what it needs to
// check them. You are assembling, not deciding.
func (c *OIDCClient) AuthorizeURL(ctx context.Context, req AuthStartRequest) (string, error) {
	if err := c.Discover(ctx); err != nil {
		return "", err
	}
	if req.Nonce == "" {
		// Core always sends one. An empty nonce means something is wrong upstream, and building the URL
		// anyway would silently drop the check that stops a token from another attempt being accepted.
		return "", errors.New("oidc: Core sent no nonce")
	}
	c.mu.Lock()
	authorize := c.meta.AuthorizeURL
	c.mu.Unlock()

	q := url.Values{}
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", req.RedirectURI)
	q.Set("response_type", "code")
	q.Set("scope", strings.Join(c.scopes, " "))
	q.Set("state", req.State)
	q.Set("nonce", req.Nonce)
	q.Set("code_challenge", req.Challenge)
	q.Set("code_challenge_method", "S256")
	if req.Locale != "" {
		q.Set("ui_locales", req.Locale)
	}

	sep := "?"
	if strings.Contains(authorize, "?") {
		sep = "&"
	}
	return authorize + sep + q.Encode(), nil
}

// Exchange trades the authorization code for the provider's ID token.
//
// It returns the token RAW and unexamined. Core verifies it — signature, issuer, audience, expiry, nonce —
// against the keys the provider publishes. Do not parse it here and do not fill in the attribute fields
// from it: a claim you copied out is a claim Core would have to take your word for, and the whole point of
// handing over the token is that it does not have to.
func (c *OIDCClient) Exchange(ctx context.Context, req AuthCompleteRequest) (AuthAssertion, error) {
	if err := c.Discover(ctx); err != nil {
		return AuthAssertion{}, err
	}
	c.mu.Lock()
	tokenURL := c.meta.TokenURL
	c.mu.Unlock()

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {req.Code},
		"redirect_uri":  {req.RedirectURI},
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		// The PKCE verifier: proof this exchange belongs to the browser that started the flow. Core held it
		// and released it for exactly this call.
		"code_verifier": {req.Verifier},
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return AuthAssertion{}, err
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("Accept", "application/json")

	res, err := c.http.Do(httpReq)
	if err != nil {
		return AuthAssertion{}, fmt.Errorf("oidc: exchanging the authorization code: %w", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return AuthAssertion{}, err
	}
	if res.StatusCode != http.StatusOK {
		// The body is NOT included: a failed token exchange can echo the code, and an error string is the
		// most likely thing in this whole flow to end up in a log somebody pastes into an issue.
		return AuthAssertion{}, fmt.Errorf("oidc: the token endpoint answered %d", res.StatusCode)
	}

	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return AuthAssertion{}, fmt.Errorf("oidc: the token response did not parse: %w", err)
	}
	if out.IDToken == "" {
		// A provider answering an `openid` request with no ID token is not speaking OIDC. Say so here
		// rather than handing Core an empty assertion it can only report as a verification failure.
		return AuthAssertion{}, errors.New("oidc: the provider returned no id_token")
	}
	return AuthAssertion{IDToken: out.IDToken}, nil
}
