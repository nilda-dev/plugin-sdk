package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// What an author writes, and what Core sees on the wire. The dispatch is small enough that the risk is not
// complexity — it is the shapes: a field renamed on one side and silently absent on the other is a plugin
// that installs, runs, and never signs anybody in.

type recordingProvider struct {
	statuses []AuthProviderStatus
	startURL string
	assert   AuthAssertion
	err      error

	sawStart    AuthStartRequest
	sawComplete AuthCompleteRequest
	described   int
}

func (p *recordingProvider) Describe(context.Context, *Core) []AuthProviderStatus {
	p.described++
	return p.statuses
}

func (p *recordingProvider) Start(_ context.Context, _ *Core, req AuthStartRequest) (string, error) {
	p.sawStart = req
	return p.startURL, p.err
}

func (p *recordingProvider) Complete(_ context.Context, _ *Core, req AuthCompleteRequest) (AuthAssertion, error) {
	p.sawComplete = req
	return p.assert, p.err
}

func dispatch(t *testing.T, p AuthProvider, hook string, payload any) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	out, handled, err := DispatchAuthHook(context.Background(), nil, p, hook, body)
	if !handled {
		t.Fatalf("%s was not handled", hook)
	}
	if err != nil {
		t.Fatalf("%s: %v", hook, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s answered with something that is not JSON: %s", hook, out)
	}
	return got
}

func TestDescribeAnswersTheShapeCoreReads(t *testing.T) {
	p := &recordingProvider{statuses: []AuthProviderStatus{
		{Key: "sso", Ready: true},
		{Key: "legacy", Ready: false, UnreadyReason: "no issuer configured"},
	}}
	got := dispatch(t, p, HookAuthDescribe, struct{}{})

	list, _ := got["providers"].([]any)
	if len(list) != 2 {
		t.Fatalf("providers = %v", got["providers"])
	}
	first, _ := list[0].(map[string]any)
	if first["key"] != "sso" || first["ready"] != true {
		t.Errorf("first = %v", first)
	}
	// The reason is for the OWNER on the settings page, not for the visitor — but it has to reach Core to
	// get there, so it must be on the wire.
	second, _ := list[1].(map[string]any)
	if second["unready_reason"] != "no issuer configured" {
		t.Errorf("the unready reason did not survive: %v", second)
	}
}

func TestStartReceivesEverythingAndAnswersWithAURL(t *testing.T) {
	p := &recordingProvider{startURL: "https://idp.test/authorize?x=1"}
	got := dispatch(t, p, HookAuthStart, AuthStartRequest{
		Provider: "sso", State: "st", Challenge: "ch", Nonce: "no",
		RedirectURI: "https://site.test/cb", Link: true, Locale: "fa",
	})

	if got["authorize_url"] != "https://idp.test/authorize?x=1" {
		t.Errorf("authorize_url = %v", got["authorize_url"])
	}
	// Every field survived the JSON round trip. A rename on one side is a plugin that installs, runs, and
	// never signs anybody in.
	if p.sawStart.State != "st" || p.sawStart.Challenge != "ch" || p.sawStart.Nonce != "no" {
		t.Errorf("start request = %+v", p.sawStart)
	}
	if !p.sawStart.Link || p.sawStart.Locale != "fa" {
		t.Errorf("the optional fields were lost: %+v", p.sawStart)
	}
}

func TestCompleteReturnsTheAssertionUnderTheKeyCoreReads(t *testing.T) {
	p := &recordingProvider{assert: AuthAssertion{IDToken: "the.raw.token"}}
	got := dispatch(t, p, HookAuthComplete, AuthCompleteRequest{
		Provider: "sso", Code: "c", Verifier: "v", Nonce: "no", RedirectURI: "https://site.test/cb",
	})

	assertion, _ := got["assertion"].(map[string]any)
	if assertion["id_token"] != "the.raw.token" {
		t.Fatalf("assertion = %v", got["assertion"])
	}
	if p.sawComplete.Verifier != "v" || p.sawComplete.Nonce != "no" {
		t.Errorf("complete request = %+v", p.sawComplete)
	}
}

// TestTheAssertionCarriesNoStateField — the state is Core's CSRF surface. The plugin needs it in the
// authorize URL and has no business receiving it back, because checking it is Core's job.
func TestCompleteRequestCarriesNoState(t *testing.T) {
	body, err := json.Marshal(AuthCompleteRequest{Provider: "p", Code: "c", Verifier: "v"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"state"`) {
		t.Fatalf("the complete request carries the state: %s", body)
	}
}

// TestAnEmptyAssertionSerialisesEmpty — a plugin on the oauth2 flow that fills in nothing must not send a
// wall of zero values that read as claims. omitempty is doing real work here.
func TestAnEmptyAssertionSerialisesEmpty(t *testing.T) {
	body, err := json.Marshal(AuthAssertion{})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{}" {
		t.Fatalf("an empty assertion is not empty on the wire: %s", body)
	}
}

func TestAnUnknownHookIsNotClaimed(t *testing.T) {
	// So a plugin that also handles its own hooks can chain the dispatch and keep its own switch.
	_, handled, _ := DispatchAuthHook(context.Background(), nil, &recordingProvider{}, "content.saved", nil)
	if handled {
		t.Fatal("the auth dispatch claimed a hook that is not its own")
	}
}

func TestAnAuthorErrorReachesCoreRatherThanBeingSwallowed(t *testing.T) {
	p := &recordingProvider{err: errors.New("the IdP is unreachable")}
	body, _ := json.Marshal(AuthStartRequest{Provider: "sso", Nonce: "n"})

	_, handled, err := DispatchAuthHook(context.Background(), nil, p, HookAuthStart, body)
	if !handled {
		t.Fatal("not handled")
	}
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("the author's error did not reach Core: %v", err)
	}
}

// TestMalformedPayloadIsAnErrorNotAPanic — the payload comes from another process. A plugin that panics on
// a shape it did not expect is one the supervisor restarts in a loop.
func TestMalformedPayloadIsAnErrorNotAPanic(t *testing.T) {
	for _, hook := range []string{HookAuthStart, HookAuthComplete} {
		_, handled, err := DispatchAuthHook(context.Background(), nil, &recordingProvider{}, hook, []byte("not json"))
		if !handled || err == nil {
			t.Errorf("%s: handled=%v err=%v", hook, handled, err)
		}
	}
}

// TestTheAuthOnlyHandlerSubscribesToAllThree — a plugin whose whole job is signing in must not install
// cleanly and then never hear from the login page.
func TestTheAuthOnlyHandlerSubscribesToAllThree(t *testing.T) {
	h := &authOnlyHandler{p: &recordingProvider{}}
	res, err := h.Init(context.Background(), &Core{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{HookAuthDescribe: false, HookAuthStart: false, HookAuthComplete: false}
	for _, hook := range res.Hooks {
		want[hook] = true
	}
	for hook, subscribed := range want {
		if !subscribed {
			t.Errorf("%s was not subscribed", hook)
		}
	}
}
