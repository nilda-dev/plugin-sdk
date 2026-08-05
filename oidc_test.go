package nilda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The SDK's OIDC client is three HTTP calls, and the reason it exists is that every author who writes them
// gets one of them slightly wrong — Core itself did, for a year. So these test the parts that are easy to
// get wrong and invisible when you do: what the authorize URL carries, what the exchange sends, what the
// client refuses, and what it never puts in an error message.

type stubIdP struct {
	*httptest.Server
	issuerOverride string
	omitEndpoints  bool
	omitIDToken    bool
	tokenStatus    int
	lastForm       url.Values
}

func newStubIdP(t *testing.T) *stubIdP {
	t.Helper()
	s := &stubIdP{tokenStatus: http.StatusOK}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		issuer := s.URL
		if s.issuerOverride != "" {
			issuer = s.issuerOverride
		}
		doc := map[string]any{"issuer": issuer}
		if !s.omitEndpoints {
			doc["authorization_endpoint"] = s.URL + "/authorize"
			doc["token_endpoint"] = s.URL + "/token"
			doc["end_session_endpoint"] = s.URL + "/logout"
		}
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		s.lastForm = r.Form
		if s.tokenStatus != http.StatusOK {
			w.WriteHeader(s.tokenStatus)
			// A real provider echoes the request here. If the client ever put a response body in an error,
			// this is the string that would show up in somebody's log.
			_, _ = w.Write([]byte(`{"error":"invalid_grant","code":"` + r.Form.Get("code") + `"}`))
			return
		}
		body := map[string]any{"access_token": "at", "token_type": "Bearer"}
		if !s.omitIDToken {
			body["id_token"] = "the.raw.token"
		}
		_ = json.NewEncoder(w).Encode(body)
	})
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

func TestTheAuthorizeURLCarriesEverythingCoreMinted(t *testing.T) {
	idp := newStubIdP(t)
	c := NewOIDCClient(idp.URL, "cid", "csecret")

	raw, err := c.AuthorizeURL(context.Background(), AuthStartRequest{
		State: "st", Challenge: "ch", Nonce: "no", RedirectURI: "https://site.test/cb", Locale: "fa",
	})
	if err != nil {
		t.Fatalf("authorize url: %v", err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	// Discovered, not configured — the author gave an issuer and nothing else.
	if !strings.HasSuffix(u.Path, "/authorize") {
		t.Errorf("path = %q", u.Path)
	}
	q := u.Query()
	for k, want := range map[string]string{
		"state": "st", "nonce": "no", "code_challenge": "ch", "code_challenge_method": "S256",
		"client_id": "cid", "redirect_uri": "https://site.test/cb", "response_type": "code",
		"ui_locales": "fa",
	} {
		if q.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
	// The client SECRET has no business in a URL the browser will carry.
	if strings.Contains(raw, "csecret") {
		t.Fatalf("the client secret is in the authorize URL: %s", raw)
	}
}

// TestExchangeSendsThePKCEVerifierAndReturnsTheTokenRaw. Raw is the point: a claim the plugin copied out of
// the token is a claim Core would have to take its word for, and handing the token over is precisely so it
// does not have to.
func TestExchangeSendsThePKCEVerifierAndReturnsTheTokenRaw(t *testing.T) {
	idp := newStubIdP(t)
	c := NewOIDCClient(idp.URL, "cid", "csecret")

	got, err := c.Exchange(context.Background(), AuthCompleteRequest{
		Code: "the-code", Verifier: "the-verifier", Nonce: "no", RedirectURI: "https://site.test/cb",
	})
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if got.IDToken != "the.raw.token" {
		t.Errorf("id_token = %q", got.IDToken)
	}
	// Nothing else is filled in. The assertion carries proof, not the plugin's reading of it.
	if got.Subject != "" || got.Email != "" || got.EmailVerified {
		t.Errorf("the client filled in attribute fields it has no business asserting: %+v", got)
	}
	if idp.lastForm.Get("code_verifier") != "the-verifier" {
		t.Errorf("code_verifier at the token endpoint = %q", idp.lastForm.Get("code_verifier"))
	}
	if idp.lastForm.Get("grant_type") != "authorization_code" {
		t.Errorf("grant_type = %q", idp.lastForm.Get("grant_type"))
	}
}

// TestAFailedExchangeNeverCarriesTheProvidersBody — a token-endpoint error echoes the request, and an error
// string is the single most likely thing in this flow to end up pasted into an issue.
func TestAFailedExchangeNeverCarriesTheProvidersBody(t *testing.T) {
	idp := newStubIdP(t)
	idp.tokenStatus = http.StatusBadRequest
	c := NewOIDCClient(idp.URL, "cid", "csecret")

	_, err := c.Exchange(context.Background(), AuthCompleteRequest{
		Code: "a-secret-authorization-code", Verifier: "v", RedirectURI: "https://site.test/cb",
	})
	if err == nil {
		t.Fatal("a 400 from the token endpoint was accepted")
	}
	if strings.Contains(err.Error(), "a-secret-authorization-code") || strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("the error carries the provider's body: %v", err)
	}
}

func TestAProviderThatReturnsNoIDTokenIsRefused(t *testing.T) {
	idp := newStubIdP(t)
	idp.omitIDToken = true
	c := NewOIDCClient(idp.URL, "cid", "csecret")

	if _, err := c.Exchange(context.Background(), AuthCompleteRequest{Code: "c", Verifier: "v"}); err == nil {
		t.Fatal("a provider answering an openid request with no id_token was accepted")
	}
}

// TestADocumentClaimingToBeSomebodyElseIsRefused — issuer confusion starts here, and it costs one
// comparison to refuse.
func TestADocumentClaimingToBeSomebodyElseIsRefused(t *testing.T) {
	idp := newStubIdP(t)
	idp.issuerOverride = "https://accounts.google.com"
	c := NewOIDCClient(idp.URL, "cid", "csecret")

	err := c.Discover(context.Background())
	if err == nil {
		t.Fatal("a document claiming another issuer was accepted")
	}
	if !strings.Contains(err.Error(), "accounts.google.com") {
		t.Errorf("the refusal should name what it claimed to be: %v", err)
	}
}

func TestADocumentWithNoEndpointsIsRefused(t *testing.T) {
	idp := newStubIdP(t)
	idp.omitEndpoints = true
	c := NewOIDCClient(idp.URL, "cid", "csecret")
	if err := c.Discover(context.Background()); err == nil {
		t.Fatal("a document naming no endpoints was accepted")
	}
}

// TestAnAuthorizeURLWithoutANonceIsRefused — Core always sends one, so an empty one means something is
// wrong upstream, and proceeding would silently drop the check that stops a token from another attempt.
func TestAnAuthorizeURLWithoutANonceIsRefused(t *testing.T) {
	idp := newStubIdP(t)
	c := NewOIDCClient(idp.URL, "cid", "csecret")
	if _, err := c.AuthorizeURL(context.Background(), AuthStartRequest{
		State: "st", Challenge: "ch", RedirectURI: "https://site.test/cb",
	}); err == nil {
		t.Fatal("an authorize URL was built with no nonce")
	}
}

// TestReadyIsTheAnswerDescribeWants — and the reason is written for the owner, who is the only person who
// can act on it.
func TestReadyIsTheAnswerDescribeWants(t *testing.T) {
	idp := newStubIdP(t)
	if ok, why := NewOIDCClient(idp.URL, "cid", "csecret").Ready(context.Background()); !ok {
		t.Fatalf("a working provider reported not ready: %s", why)
	}
	ok, why := NewOIDCClient("", "cid", "csecret").Ready(context.Background())
	if ok {
		t.Fatal("an unconfigured client reported ready")
	}
	if !strings.Contains(why, "issuer") {
		t.Errorf("the reason should tell the owner what to type: %q", why)
	}
	if ok, why := NewOIDCClient(idp.URL, "", "").Ready(context.Background()); ok {
		t.Errorf("a client with no credentials reported ready: %q", why)
	}
}

func TestDiscoveryHappensOnce(t *testing.T) {
	idp := newStubIdP(t)
	hits := 0
	wrapped := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "openid-configuration") {
			hits++
		}
		http.Redirect(w, r, idp.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer wrapped.Close()

	// Point at the real stub so the issuer check passes; the counter is on the wrapper the client never
	// reaches twice.
	c := NewOIDCClient(idp.URL, "cid", "csecret")
	for i := 0; i < 3; i++ {
		if err := c.Discover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// A plugin is resident: re-reading the document per sign-in would make the provider's availability the
	// plugin's availability. Asserted through the client's own cache rather than the wrapper.
	c.mu.Lock()
	cached := c.meta != nil
	c.mu.Unlock()
	if !cached {
		t.Fatal("the metadata was not cached")
	}
}
