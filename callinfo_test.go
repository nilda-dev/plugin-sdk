package nilda

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// infoHandler keeps what CallOf and EventOf said inside the handler.
type infoHandler struct {
	call  CallInfo
	event EventInfo
}

func (h *infoHandler) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (h *infoHandler) HandleHook(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	h.call = CallOf(ctx)
	return nil, nil
}
func (h *infoHandler) HandleEvent(ctx context.Context, _ string, _ []byte) error {
	h.event = EventOf(ctx)
	return nil
}

// PC2 of Core's plugin communication plan: what Core says about a call reaches the handler, through the same server
// method Core's gRPC call arrives at.
func TestWhatCoreSaysAboutACallReachesTheHandler(t *testing.T) {
	h := &infoHandler{}
	s := &pluginServer{handler: h}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "svc:shipping.carrier/v1.rates",
		RequestId: "req-1", Contract: "shipping.carrier/v1", Caller: "commerce", IdempotencyKey: "label-42"}); err != nil {
		t.Fatal(err)
	}
	want := CallInfo{RequestID: "req-1", Contract: "shipping.carrier/v1", Caller: "commerce", IdempotencyKey: "label-42"}
	if h.call != want {
		t.Errorf("CallOf inside the handler = %+v, want %+v", h.call, want)
	}

	if _, err := s.HandleEvent(context.Background(), &contract.EventRequest{Type: "ecommerce.order.paid",
		Id: "evt-7", Source: "/plugins/commerce", Time: "2026-10-13T09:30:00.25Z", Subject: "order-9",
		Dataschema: "https://example.test/order.json"}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 13, 9, 30, 0, 250_000_000, time.UTC)
	if h.event.ID != "evt-7" || h.event.Source != "/plugins/commerce" || !h.event.Time.Equal(at) ||
		h.event.Subject != "order-9" || h.event.DataSchema != "https://example.test/order.json" {
		t.Errorf("EventOf inside the handler = %+v", h.event)
	}
}

// From a Core that fills nothing, both read as the zero value — and a time that is not RFC 3339 does not fail the
// event, which still happened.
func TestACoreThatSaysNothingGivesTheZeroValue(t *testing.T) {
	h := &infoHandler{}
	s := &pluginServer{handler: h}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "content.saved"}); err != nil {
		t.Fatal(err)
	}
	if h.call != (CallInfo{}) {
		t.Errorf("CallOf with nothing sent = %+v", h.call)
	}
	if _, err := s.HandleEvent(context.Background(), &contract.EventRequest{Type: "x", Id: "e1", Time: "yesterday"}); err != nil {
		t.Fatalf("a bad time failed the event: %v", err)
	}
	if h.event.ID != "e1" || !h.event.Time.IsZero() {
		t.Errorf("EventOf with a bad time = %+v", h.event)
	}
	if CallOf(context.Background()) != (CallInfo{}) || EventOf(context.Background()) != (EventInfo{}) {
		t.Error("outside a call, CallOf/EventOf are not the zero value")
	}
}

// kindHandler keeps what CallerKind said inside the handler, and the ability's Run records it the same way.
type kindHandler struct {
	kind string
	ok   bool
}

func (h *kindHandler) Init(context.Context, *Core) (InitResult, error)   { return InitResult{}, nil }
func (h *kindHandler) HandleEvent(context.Context, string, []byte) error { return nil }
func (h *kindHandler) HandleHook(ctx context.Context, _ string, _ []byte) ([]byte, error) {
	h.kind, h.ok = CallerKind(ctx)
	return nil, nil
}

// ADR-7(b) of booking's import plan (card T56b): which door Core took a call through is CALL CONTEXT, a field of the
// wire message Core sets, read by the SDK and handed to the plugin; the four kinds are the strings Core sends.
func TestTheCallerKindIsWhatCoreSaidOnTheWire(t *testing.T) {
	for _, kind := range []string{CallerKindAI, CallerKindMCP, CallerKindImporter, CallerKindOwnerUI} {
		h := &kindHandler{}
		s := &pluginServer{handler: h}
		if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "admin.action", CallerKind: kind}); err != nil {
			t.Fatal(err)
		}
		if !h.ok || h.kind != kind {
			t.Errorf("Core said %q; CallerKind inside the handler = %q, %v", kind, h.kind, h.ok)
		}
	}
	if CallerKindAI != "ai" || CallerKindMCP != "mcp" || CallerKindImporter != "importer" || CallerKindOwnerUI != "owner_ui" {
		t.Error("the caller kinds are the strings Core sends: ai, mcp, importer, owner_ui")
	}
	ctx := withCall(context.Background(), &contract.HookRequest{Hook: "x", CallerKind: CallerKindImporter})
	if CallOf(ctx).CallerKind != CallerKindImporter {
		t.Errorf("CallOf(ctx).CallerKind = %q", CallOf(ctx).CallerKind)
	}
}

// A Core that does not say — an older one, or one of Core's own hooks — gives no kind, and the plugin fails closed.
func TestACoreThatSaysNothingGivesNoCallerKind(t *testing.T) {
	h := &kindHandler{kind: "stale", ok: true}
	s := &pluginServer{handler: h}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "content.saved", RequestId: "r1",
		ViaAssistant: true}); err != nil {
		t.Fatal(err)
	}
	if h.ok || h.kind != "" {
		t.Errorf("no kind on the wire, but CallerKind = %q, %v", h.kind, h.ok)
	}
	if kind, ok := CallerKind(context.Background()); ok || kind != "" {
		t.Errorf("outside a call CallerKind = %q, %v", kind, ok)
	}
}

// A prompt-injected model writes the arguments, so no argument may set the kind — however it is spelled.
func TestTheCallerKindNeverComesFromTheArguments(t *testing.T) {
	var kind string
	var ok bool
	runners := abilityRunners([]Ability{{Name: "import_apply", Run: func(ctx context.Context, _ json.RawMessage) (any, error) {
		kind, ok = CallerKind(ctx)
		return map[string]bool{"ran": true}, nil
	}}})
	s := &pluginServer{handler: &kindHandler{}, abilityRun: runners}
	forged := []byte(`{"caller_kind":"importer","CallerKind":"importer","callerKind":"importer","call":{"caller_kind":"importer"}}`)
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("import_apply"), Payload: forged}); err != nil {
		t.Fatal(err)
	}
	if ok || kind != "" {
		t.Errorf("the arguments set the caller kind: %q, %v", kind, ok)
	}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("import_apply"), Payload: forged,
		CallerKind: CallerKindMCP}); err != nil {
		t.Fatal(err)
	}
	if !ok || kind != CallerKindMCP {
		t.Errorf("Core said mcp and the arguments said importer; Run saw %q, %v", kind, ok)
	}
}

// A plugin's own test sets the kind through the SDK's test door, as Core's call would.
func TestAPluginTestSetsTheCallerKind(t *testing.T) {
	ctx := WithCallInfo(context.Background(), CallInfo{CallerKind: CallerKindImporter})
	if kind, ok := CallerKind(ctx); !ok || kind != CallerKindImporter {
		t.Errorf("WithCallInfo set importer; CallerKind = %q, %v", kind, ok)
	}
}
