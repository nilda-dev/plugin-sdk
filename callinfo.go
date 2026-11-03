package nilda

import (
	"context"
	"time"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// CallInfo is what Core said about the hook call being handled, beside its name and payload: which request led to
// it, and — for a call relayed by Core's service desk from another plugin — the contract, the asking plugin and the
// caller's idempotency key. Every field is empty from a Core that does not fill it, and Core's own hooks carry no
// Contract or Caller.
type CallInfo struct {
	// RequestID is the id of the request that led to this call, as Core's own log lines carry it — log it beside
	// yours and one search finds both halves of a failure.
	RequestID string
	// Contract is the service-desk contract the call belongs to, e.g. "shipping.carrier/v1".
	Contract string
	// Caller is the key of the plugin that asked, for a service-desk call.
	Caller string
	// IdempotencyKey is the caller's key for this operation: a call repeated with the same key must have the effect
	// of one — the second label purchase, the second refund, must not happen.
	IdempotencyKey string
	// ViaAssistant is true when the site's assistant — the panel's chat, the app's, or a connected AI app — asked for
	// this call rather than a person's own press (v0.41.0): hold the assistant to your own limits on every door it
	// can reach, an admin screen's button as much as an ability.
	ViaAssistant bool
	// CallerKind is which door Core took this call through (v0.53.0): CallerKindAI, CallerKindMCP,
	// CallerKindImporter or CallerKindOwnerUI. Core's own code sets it on the wire, never from the payload, so a
	// model that writes an ability's arguments cannot write this; empty from a Core that does not say and on Core's
	// own hooks. Read it with CallerKind.
	CallerKind string
}

// The caller kinds Core sends (CallInfo.CallerKind, Ability.Callers). A kind names a door, never a person.
const (
	// CallerKindAI is the site's assistant: the panel's chat, the app's, or Core's admin tools pressing a button or
	// saving a form for it.
	CallerKindAI = "ai"
	// CallerKindMCP is a connected AI app over Remote MCP, or a connector's direct REST invoke.
	CallerKindMCP = "mcp"
	// CallerKindImporter is Core's own importer, which hands a plugin a document the owner uploaded and no model
	// has held.
	CallerKindImporter = "importer"
	// CallerKindOwnerUI is a person's own press in the panel or the app: a row action, a form save.
	CallerKindOwnerUI = "owner_ui"
)

// CallerKind returns which door Core took the call ctx belongs to, and false when Core did not say — an older Core,
// one of Core's own hooks, or outside a call. Never read the kind from your arguments: they are what a model wrote.
// A door that must not open to a model fails closed on false:
//
//	kind, ok := nilda.CallerKind(ctx)
//	if !ok || kind != nilda.CallerKindImporter {
//		return nil, errors.New("this action takes calls from Core's importer only")
//	}
//
// In a test, nilda.WithCallInfo(ctx, nilda.CallInfo{CallerKind: nilda.CallerKindImporter}) sets it.
func CallerKind(ctx context.Context) (string, bool) {
	kind := CallOf(ctx).CallerKind
	return kind, kind != ""
}

// knownCallerKind reports whether kind is one Core sends.
func knownCallerKind(kind string) bool {
	switch kind {
	case CallerKindAI, CallerKindMCP, CallerKindImporter, CallerKindOwnerUI:
		return true
	}
	return false
}

// EventInfo is the CloudEvents 1.0 context of the event being handled (Core owner decision D13): the type and data
// are HandleEvent's own arguments. Empty from a Core that does not fill it.
type EventInfo struct {
	// ID is unique per event, and a redelivery of the same event keeps it: an event seen before is a repeat to drop.
	ID string
	// Source is who emitted it, set by Core and never by the emitter: "/core" or "/plugins/<key>".
	Source string
	// Time is when it happened; zero when Core did not say.
	Time time.Time
	// Subject is what the event is about within its source, e.g. an order id.
	Subject string
	// DataSchema is the URI of the data's schema, when one is published.
	DataSchema string
}

type callInfoKey struct{}
type eventInfoKey struct{}

// CallOf returns what Core said about the hook call ctx belongs to. Read it inside HandleHook, an ability's Run, or
// any provider method the SDK calls for a hook; anywhere else it is the zero value.
func CallOf(ctx context.Context) CallInfo {
	ci, _ := ctx.Value(callInfoKey{}).(CallInfo)
	return ci
}

// WithCallInfo returns ctx carrying ci, as Core's call would — for a test (nildatest's service desk uses it) that
// drives a provider method directly and needs CallOf to answer who asked. From v0.23.0.
func WithCallInfo(ctx context.Context, ci CallInfo) context.Context {
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// EventOf returns the CloudEvents context of the event ctx belongs to. Read it inside HandleEvent; anywhere else it
// is the zero value.
func EventOf(ctx context.Context) EventInfo {
	ei, _ := ctx.Value(eventInfoKey{}).(EventInfo)
	return ei
}

// withCall puts a hook request's call context where CallOf finds it.
func withCall(ctx context.Context, req *contract.HookRequest) context.Context {
	ci := CallInfo{RequestID: req.GetRequestId(), Contract: req.GetContract(), Caller: req.GetCaller(),
		IdempotencyKey: req.GetIdempotencyKey(), ViaAssistant: req.GetViaAssistant(), CallerKind: req.GetCallerKind()}
	if ci == (CallInfo{}) {
		return ctx
	}
	return context.WithValue(ctx, callInfoKey{}, ci)
}

// withEvent puts an event request's CloudEvents context where EventOf finds it. A time that is not RFC 3339 is left
// zero rather than failing the event: the event itself still happened.
func withEvent(ctx context.Context, req *contract.EventRequest) context.Context {
	ei := EventInfo{ID: req.GetId(), Source: req.GetSource(), Subject: req.GetSubject(), DataSchema: req.GetDataschema()}
	if t, err := time.Parse(time.RFC3339Nano, req.GetTime()); err == nil {
		ei.Time = t
	}
	if ei == (EventInfo{}) {
		return ctx
	}
	return context.WithValue(ctx, eventInfoKey{}, ei)
}
