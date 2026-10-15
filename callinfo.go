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
		IdempotencyKey: req.GetIdempotencyKey()}
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
