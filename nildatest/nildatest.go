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
//     the worst place to find out. Here it fails in the test. The same goes for the event names a plugin may
//     emit: its own key's, or a namespace a capability it holds owns.
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
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"

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
	// Lang is what SendLocalizedEmail passed (SendEmail leaves it "") — see plugin-sdk's own doc comment
	// on that method for what it is for.
	Lang string
}

// Event is one event a plugin emitted.
type Event struct {
	Type string
	Data []byte
}

// Host is a fake Core: the plugin-facing half, in memory, with the real capability rules.
type Host struct {
	mu      sync.Mutex
	key     string // the plugin's key: its own event namespace
	granted map[string]bool
	kv      map[string]kvEntry
	events  []Event
	emails  []Email
	revoked []Revocation
	// clock is how far Advance has moved this host's time past the wall clock, so a KV expiry can be
	// asserted without the test sleeping through it.
	clock time.Duration
	// sessions is what RevokeIdentity reports for this host; nil falls back to SessionsPerIdentity.
	sessions *int
	// Fail, when set, makes every host call return it. For asserting what a plugin does when Core is having
	// a bad day — a plugin that ignores a failed SendEmail loses a customer's receipt silently.
	Fail error
}

// kvEntry is one stored value and when it expires (zero: never), as Core's KV keeps it.
type kvEntry struct {
	val string
	exp time.Time
}

// now is this host's time: the wall clock, moved on by Advance. Callers hold h.mu.
func (h *Host) now() time.Time { return time.Now().Add(h.clock) }

// live returns a key's entry unless it has expired, dropping it if it has. Callers hold h.mu.
func (h *Host) live(key string) (kvEntry, bool) {
	e, ok := h.kv[key]
	if ok && !e.exp.IsZero() && !h.now().Before(e.exp) {
		delete(h.kv, key)
		return kvEntry{}, false
	}
	return e, ok
}

// Advance moves this host's clock forward, so a KV entry written with a TTL expires exactly as it would in
// Core — without the test sleeping for it. An expiry a plugin relies on (a rate-limit window, a one-time code)
// is behaviour worth asserting, and a fake that kept every value forever could not show it breaking.
func (h *Host) Advance(d time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clock += d
}

// New builds a fake Core and the Host behind it, granted exactly the capabilities named.
//
// Name only what the manifest declares. The point of passing them explicitly is that a test then fails the
// same way a real install would when the plugin reaches for something it never asked for.
func New(pluginKey string, granted ...string) (*nilda.Core, *Host) {
	h := newHost(pluginKey, granted)
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
	h := newHost(pluginKey, granted)
	srv := httptest.NewServer(handler)
	core := nilda.NewCoreForTest(pluginKey, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

func newHost(pluginKey string, granted []string) *Host {
	set := make(map[string]bool, len(granted))
	for _, g := range granted {
		set[g] = true
	}
	return &Host{key: pluginKey, granted: set, kv: map[string]kvEntry{}}
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
		// The payment contract: a consumer creates and reads its sessions, a gateway reads and reports on
		// the ones routed to it — never the other's power.
		"payment_session": {"read:payments", "write:payments"},
		"payment_gateway": {"read:payments", "report:payments"},
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

// KV returns a snapshot of the plugin's key-value namespace — what has not expired, as Core would answer.
func (h *Host) KV() map[string]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[string]string, len(h.kv))
	for k := range h.kv {
		if e, ok := h.live(k); ok {
			out[k] = e.val
		}
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
	e, found := h.live(in.Key)
	return &contract.KVGetResponse{Value: e.val, Found: found}, nil
}

// maxKVValueBytes is Core's cap on one KV value (core's internal/plugin/kvquota.go); a guard in this package
// reads that file and fails when the two differ. Core also caps keys and bytes per plugin, on every install —
// this fake does not model the store's memory, so it does not refuse those.
const maxKVValueBytes = 64 << 10

func (h *Host) KVSet(_ context.Context, in *contract.KVSetRequest, _ ...grpc.CallOption) (*contract.KVSetResponse, error) {
	if err := h.enforce("kv"); err != nil {
		return nil, err
	}
	if len(in.Value) > maxKVValueBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"plugin kv: value too large — kv is for small state; declare `datastore` for more")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	// The TTL is kept, as Core keeps it (the 2026-09-23 plugin hunt's M11): this fake used to drop it, so a
	// value meant to expire lived forever in every test and the expiry a plugin relied on was never exercised.
	e := kvEntry{val: in.Value}
	if in.TtlSeconds > 0 {
		e.exp = h.now().Add(time.Duration(in.TtlSeconds) * time.Second)
	}
	h.kv[in.Key] = e
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
	// key for both a string and a counter discovers the collision here, with the code Core answers
	// (FailedPrecondition; core's kvStatus, held equal by a guard in this package).
	var n int64
	existing, ok := h.live(in.Key)
	if ok {
		parsed, err := strconv.ParseInt(existing.val, 10, 64)
		if err != nil {
			return nil, status.Errorf(codes.FailedPrecondition, "plugin kv: value is not an integer")
		}
		n = parsed
	}
	n++
	// INCR leaves an existing expiry alone, in Core as in Redis.
	h.kv[in.Key] = kvEntry{val: strconv.FormatInt(n, 10), exp: existing.exp}
	return &contract.KVIncrResponse{Value: n}, nil
}

func (h *Host) EmitEvent(_ context.Context, in *contract.EmitEventRequest, _ ...grpc.CallOption) (*contract.EmitEventResponse, error) {
	if err := h.enforce("events"); err != nil {
		return nil, err
	}
	if err := h.mayEmit(in.Type); err != nil {
		return nil, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, Event{Type: in.Type, Data: in.DataJson})
	return &contract.EmitEventResponse{}, nil
}

// capabilityEventNamespaces is Core's table of event namespaces a capability owns (core's
// internal/plugin/eventprovenance.go). A guard in this package reads that file and fails when the two differ.
var capabilityEventNamespaces = map[string]string{
	"commerce":  "commerce",
	"ecommerce": "commerce",
}

// mayEmit is Core's rule for which event names a plugin may emit, with the same codes: its own key's
// namespace (`shop` emits `shop.*`), or a namespace a capability it holds owns (`commerce.*` is the shop's).
// An event name anyone could use would let one plugin forge another's — an order nobody paid, announced to
// every plugin that ships goods.
//
// Core refuses one thing more that this fake cannot know: its own namespaces (`content.*`, `form.*`, …), even
// to a plugin keyed after one. Emit under your own key and that never comes up.
func (h *Host) mayEmit(eventType string) error {
	ns, rest, dotted := strings.Cut(eventType, ".")
	if !dotted || ns == "" || rest == "" {
		return status.Errorf(codes.InvalidArgument, "event %q has no namespace — name it %s.<something>", eventType, h.key)
	}
	if capability, bound := capabilityEventNamespaces[ns]; bound {
		h.mu.Lock()
		held := h.granted[capability]
		h.mu.Unlock()
		if held {
			return nil
		}
		return status.Errorf(codes.PermissionDenied,
			"%s.* events belong to the plugin holding the %q capability; emit yours as %s.<something>",
			ns, capability, h.key)
	}
	if ns != h.key {
		return status.Errorf(codes.PermissionDenied,
			"plugin %q may emit only its own events, named %s.<something> — %q is not one", h.key, h.key, eventType)
	}
	return nil
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

// SessionsPerIdentity is what RevokeIdentity reports back on a host that has not been told otherwise.
// Default 1.
//
// Deprecated: use Host.SetSessionsPerIdentity. A package variable is shared by every test in the binary, so
// two parallel tests that set it race, and each can read the other's value.
var SessionsPerIdentity = 1

// SetSessionsPerIdentity sets what this host's RevokeIdentity reports back. Set it to 0 to rehearse revoking
// somebody who was not signed in, which must not read as a failure.
func (h *Host) SetSessionsPerIdentity(n int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions = &n
}

// RevokeIdentity and SendEmail refuse what Core refuses, with its code (the 2026-09-23 plugin hunt's M16). They
// accepted empty required fields, so a plugin whose bug sent a receipt to nobody, or revoked nobody's
// sessions, passed every test here and was refused by the first real install.
func (h *Host) RevokeIdentity(_ context.Context, in *contract.RevokeIdentityRequest, _ ...grpc.CallOption) (*contract.RevokeIdentityResponse, error) {
	if err := h.enforce("auth_provider"); err != nil {
		return nil, err
	}
	if in.Provider == "" || in.Subject == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and subject are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revoked = append(h.revoked, Revocation{Provider: in.Provider, Subject: in.Subject, Reason: in.Reason})
	n := SessionsPerIdentity
	if h.sessions != nil {
		n = *h.sessions
	}
	return &contract.RevokeIdentityResponse{SessionsEnded: int32(n)}, nil
}

func (h *Host) SendEmail(_ context.Context, in *contract.SendEmailRequest, _ ...grpc.CallOption) (*contract.SendEmailResponse, error) {
	if err := h.enforce("email"); err != nil {
		return nil, err
	}
	if in.To == "" || in.Subject == "" {
		return nil, status.Error(codes.InvalidArgument, "to and subject are required")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.emails = append(h.emails, Email{To: in.To, Subject: in.Subject, Body: in.Body, Lang: in.Lang})
	return &contract.SendEmailResponse{}, nil
}
