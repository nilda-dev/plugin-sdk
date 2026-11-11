package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// ShopEvents is a fake shop for a plugin that subscribes to shop.events/v1 (door D1, SHOP_EVENTS.md): it records
// order events as the shop would — each with its event_id and the next sequence, checked by the SDK — delivers them to
// the plugin's HandleEvent as Core would, and answers events.read so the plugin's catch-up can be tested. Register it
// on a Services desk: desk.Provider("ecommerce", events).
type ShopEvents struct {
	mu     sync.Mutex
	events []nilda.ShopOrderEvent
	seq    int64
}

// NewShopEvents is an empty feed.
func NewShopEvents() *ShopEvents { return &ShopEvents{} }

// Record keeps one event: it is given the next sequence, and an event_id when it has none. An event the contract
// refuses fails the test's call, as the shop's own check would refuse to emit it.
func (s *ShopEvents) Record(e nilda.ShopOrderEvent) (nilda.ShopOrderEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	e.Sequence = s.seq
	if e.EventID == "" {
		e.EventID = "evt-" + strconv.FormatInt(s.seq, 10)
	}
	if e.Lines == nil {
		e.Lines = []nilda.ShopEventLine{}
	}
	if err := e.Validate(); err != nil {
		s.seq--
		return nilda.ShopOrderEvent{}, err
	}
	s.events = append(s.events, e)
	return e, nil
}

// Deliver hands one recorded event to a subscriber as Core delivers it: its type and its data as JSON. Deliver it twice
// to test that a repeat is dropped.
func (s *ShopEvents) Deliver(ctx context.Context, h nilda.Handler, e nilda.ShopOrderEvent) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return h.HandleEvent(ctx, e.Type, raw)
}

// Events is every recorded event, oldest first.
func (s *ShopEvents) Events() []nilda.ShopOrderEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]nilda.ShopOrderEvent(nil), s.events...)
}

// ShopEvents answers events.read from what was recorded.
func (s *ShopEvents) ShopEvents(_ context.Context, req nilda.ShopEventsRequest) (nilda.ShopEventsAnswer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	limit := req.Limit
	if limit == 0 {
		limit = nilda.DefaultShopEventsRead
	}
	var out nilda.ShopEventsAnswer
	for _, e := range s.events {
		if e.Sequence <= req.AfterSequence {
			continue
		}
		if len(out.Events) == limit {
			out.More = true
			break
		}
		out.Events = append(out.Events, e)
	}
	return out, nil
}

// ServiceMethods answers shop.events/v1, each request and page checked by the SDK.
func (s *ShopEvents) ServiceMethods() []nilda.ServiceMethod { return nilda.ShopEventsMethods(s) }

// String names the feed in a failure message.
func (s *ShopEvents) String() string {
	return fmt.Sprintf("nildatest.ShopEvents(%d events)", len(s.Events()))
}
