package nilda

import (
	"context"
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
