// Package nildatest is the test kit for Nilda plugins: a working fake Core, so a plugin's behaviour can be
// asserted with `go test` and nothing running.
//
// It exists because the SDK's own test helper was a trap. NewCoreForTest takes a gRPC client, and the obvious
// thing to pass — nil — produces a *Core whose every host call dereferences a nil pointer. The panic names
// gRPC internals, not the missing dependency, so the author it was written for is the author least able to
// read it. And a Core built that way had no API client at all, which meant the half of a plugin most worth
// testing was the half that could not be.
//
// Two things this deliberately does that a hand-rolled stub would not:
//
//   - It ENFORCES capabilities, the same deny-by-default way Core does. A plugin that quietly depends on a
//     capability its manifest never declared works in a permissive test and fails on a real install, which is
//     the worst place to find out. Here it fails in the test.
//   - It records what the plugin DID — events emitted, mail sent, keys written — because "did it send the
//     confirmation" is the assertion an author actually wants, and a stub that only returns success cannot
//     answer it.
//
// Typical use:
//
//	core, host := nildatest.New("shop", "hooks", "kv", "email")
//	if _, err := p.Init(ctx, core); err != nil { t.Fatal(err) }
//	if _, err := p.HandleHook(ctx, "content.saved", payload); err != nil { t.Fatal(err) }
//	if len(host.Emails()) != 1 { t.Fatal("no confirmation was sent") }
package nildatest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
	"gitlab.com/nildalabs/nilda-sdk/plugin-sdk/contract"
)

// Email is one message a plugin asked Core to send.
type Email struct {
	To      string
	Subject string
	Body    string
}

// Event is one event a plugin emitted.
type Event struct {
	Type string
	Data []byte
}

// Host is a fake Core: the plugin-facing half, in memory, with the real capability rules.
type Host struct {
	mu      sync.Mutex
	granted map[string]bool
	kv      map[string]string
	events  []Event
	emails  []Email
	revoked []Revocation
	// Fail, when set, makes every host call return it. For asserting what a plugin does when Core is having
	// a bad day — a plugin that ignores a failed SendEmail loses a customer's receipt silently.
	Fail error
}

// New builds a fake Core and the Host behind it, granted exactly the capabilities named.
//
// Name only what the manifest declares. The point of passing them explicitly is that a test then fails the
// same way a real install would when the plugin reaches for something it never asked for.
func New(pluginKey string, granted ...string) (*nilda.Core, *Host) {
	h := newHost(granted)
	return nilda.NewCoreForTest(pluginKey, granted, h, "", "", nil), h
}

// SetSettings puts the values a SITE OWNER would have typed on your admin pages into a test Core.
//
// A Core built for a test had none, which meant the half of a plugin an author most wants to test — what it
// does with the API key, the issuer, the account id somebody entered — was the half they could not reach.
// The keys are the bare field keys your manifest declares ("issuer"), the same shape Core delivers at Init.
//
// Call it before the code under test reads a setting; a real plugin is RESTARTED when settings change, so
// mid-run mutation is not a state production can be in.
func SetSettings(core *nilda.Core, values map[string]any) { nilda.SetSettingsForTest(core, values) }

// NewWithAPI is New plus an API client pointed at handler.
//
// The returned server must be closed by the caller — `defer srv.Close()`. handler stands in for Core's
// /api/rest/v1, so an author can assert the requests their plugin makes and control what comes back,
// including the failures: a 403 from a missing scope and a 500 from a bad day are different bugs and a
// plugin should behave differently for each.
func NewWithAPI(pluginKey string, handler http.Handler, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(granted)
	srv := httptest.NewServer(handler)
	core := nilda.NewCoreForTest(pluginKey, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

func newHost(granted []string) *Host {
	set := make(map[string]bool, len(granted))
	for _, g := range granted {
		set[g] = true
	}
	return &Host{granted: set, kv: map[string]string{}}
}

// scopesFor mirrors Core's capability→scope mapping, so a test Core's reported scopes match what a real one
// would carry. An author checking HasScope in Init gets the same answer here as in production.
func scopesFor(granted []string) []string {
	mapping := map[string][]string{
		"content.read":   {"read:content"},
		"content.write":  {"read:content", "write:content"},
		"media.read":     {"read:media"},
		"media.write":    {"read:media", "write:media"},
		"users.read":     {"read:users"},
		"taxonomy.read":  {"read:taxonomy"},
		"taxonomy.write": {"read:taxonomy", "write:taxonomy"},
		"menus.read":     {"read:menus"},
		"menus.write":    {"read:menus", "write:menus"},
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range granted {
		for _, s := range mapping[g] {
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// ---- assertions an author actually wants -------------------------------------------------------------

// Events returns every event the plugin emitted, in order.
func (h *Host) Events() []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Event(nil), h.events...)
}

// Emails returns every message the plugin asked Core to send, in order.
func (h *Host) Emails() []Email {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Email(nil), h.emails...)
}

// KV returns a snapshot of the plugin's key-value namespace.
func (h *Host) KV() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.kv))
	for k, v := range h.kv {
		out[k] = v
	}
	return out
}

// Grant adds a capability mid-test, for asserting the before and after of the same call.
func (h *Host) Grant(capability string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.granted[capability] = true
}

// Revoke removes one.
func (h *Host) Revoke(capability string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.granted, capability)
}

// ---- the HostServiceClient the SDK talks to ----------------------------------------------------------

// enforce mirrors Core's deny-by-default gate, including its error code, so a plugin's handling of a denial
// is exercised rather than imagined.
func (h *Host) enforce(capability string) error {
	if h.Fail != nil {
		return h.Fail
	}
	h.mu.Lock()
	ok := h.granted[capability]
	h.mu.Unlock()
	if !ok {
		return status.Errorf(codes.PermissionDenied,
			"plugin lacks the %q capability — declare it in plugin.json", capability)
	}
	return nil
}

func (h *Host) KVGet(_ context.Context, in *contract.KVGetRequest, _ ...grpc.CallOption) (*contract.KVGetResponse, error) {
	if err := h.enforce("kv"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	v, found := h.kv[in.Key]
	return &contract.KVGetResponse{Value: v, Found: found}, nil
}

func (h *Host) KVSet(_ context.Context, in *contract.KVSetRequest, _ ...grpc.CallOption) (*contract.KVSetResponse, error) {
	if err := h.enforce("kv"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.kv[in.Key] = in.Value
	return &contract.KVSetResponse{}, nil
}

func (h *Host) KVDel(_ context.Context, in *contract.KVDelRequest, _ ...grpc.CallOption) (*contract.KVDelResponse, error) {
	if err := h.enforce("kv"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.kv, in.Key)
	return &contract.KVDelResponse{}, nil
}

func (h *Host) KVIncr(_ context.Context, in *contract.KVIncrRequest, _ ...grpc.CallOption) (*contract.KVIncrResponse, error) {
	if err := h.enforce("kv"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// Counters live in the same namespace as everything else, as they do in Core — so a plugin that uses one
	// key for both a string and a counter discovers the collision here.
	var n int64
	if existing, ok := h.kv[in.Key]; ok {
		if _, err := fmt.Sscanf(existing, "%d", &n); err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "key %q does not hold a number", in.Key)
		}
	}
	n++
	h.kv[in.Key] = fmt.Sprintf("%d", n)
	return &contract.KVIncrResponse{Value: n}, nil
}

func (h *Host) EmitEvent(_ context.Context, in *contract.EmitEventRequest, _ ...grpc.CallOption) (*contract.EmitEventResponse, error) {
	if err := h.enforce("events"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, Event{Type: in.Type, Data: in.DataJson})
	return &contract.EmitEventResponse{}, nil
}

// Revocation is one identity a plugin asked Core to sign out.
type Revocation struct {
	Provider string
	Subject  string
	Reason   string
}

// Revocations is every identity the plugin asked to have signed out, in order.
//
// Worth asserting on. A back-channel logout handler that validates the provider's token perfectly and then
// forgets to call RevokeIdentity is the failure this whole path exists to prevent, and it looks exactly
// like success from the outside: the endpoint returns 200 and nobody is signed out.
func (h *Host) Revocations() []Revocation {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Revocation(nil), h.revoked...)
}

// SessionsPerIdentity is what RevokeIdentity reports back. Default 1; set it to 0 to rehearse revoking
// somebody who was not signed in, which must not read as a failure.
var SessionsPerIdentity = 1

func (h *Host) RevokeIdentity(_ context.Context, in *contract.RevokeIdentityRequest, _ ...grpc.CallOption) (*contract.RevokeIdentityResponse, error) {
	if err := h.enforce("auth_provider"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revoked = append(h.revoked, Revocation{Provider: in.Provider, Subject: in.Subject, Reason: in.Reason})
	return &contract.RevokeIdentityResponse{SessionsEnded: int32(SessionsPerIdentity)}, nil
}

func (h *Host) SendEmail(_ context.Context, in *contract.SendEmailRequest, _ ...grpc.CallOption) (*contract.SendEmailResponse, error) {
	if err := h.enforce("email"); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emails = append(h.emails, Email{To: in.To, Subject: in.Subject, Body: in.Body})
	return &contract.SendEmailResponse{}, nil
}
