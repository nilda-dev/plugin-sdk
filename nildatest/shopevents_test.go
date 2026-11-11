package nildatest

import (
	"context"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

type eventsHeard struct{ got []string }

func (h *eventsHeard) Init(context.Context, *nilda.Core) (nilda.InitResult, error) {
	return nilda.InitResult{}, nil
}
func (h *eventsHeard) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, nil
}
func (h *eventsHeard) HandleEvent(_ context.Context, typ string, data []byte) error {
	e, err := nilda.DecodeShopOrderEvent(typ, data)
	if err != nil {
		return err
	}
	h.got = append(h.got, e.EventID)
	return nil
}

func placed(ref string) nilda.ShopOrderEvent {
	return nilda.ShopOrderEvent{Type: nilda.EventShopOrderPlaced, OrderID: "id-" + ref, Ref: ref, OccurredAt: time.Now(),
		Currency: "EUR", NetMinor: 1000, TaxMinor: 190, TotalMinor: 1190,
		Lines:   []nilda.ShopEventLine{{SKU: "MUG", Quantity: 1, NetMinor: 1000, TaxMinor: 190}},
		Consent: nilda.NotToldConsent(time.Now())}
}

// The fake shop numbers what it records, refuses what the contract refuses, delivers as Core does, and pages its read
// through the service desk the way the real shop answers it.
func TestTheFakeShopFeedsASubscriber(t *testing.T) {
	ctx := context.Background()
	feed := NewShopEvents()
	for _, ref := range []string{"A", "B", "C"} {
		if _, err := feed.Record(placed(ref)); err != nil {
			t.Fatal(err)
		}
	}
	bad := placed("D")
	bad.Currency = "nope"
	if _, err := feed.Record(bad); err == nil {
		t.Fatal("an event the contract refuses was recorded")
	}
	all := feed.Events()
	if len(all) != 3 || all[2].Sequence != 3 || all[0].EventID == "" {
		t.Fatalf("recorded %+v", all)
	}
	h := &eventsHeard{}
	if err := feed.Deliver(ctx, h, all[0]); err != nil || len(h.got) != 1 {
		t.Fatalf("delivery: %v %v", h.got, err)
	}
	desk := NewServices()
	desk.Provider("ecommerce", feed)
	res, err := desk.Ask(ctx, nilda.ContractShopEvents, nilda.ShopEventsRead, nilda.ShopEventsRequest{AfterSequence: 1, Limit: 1}, nilda.ServiceCallOptions{})
	if err != nil || len(res.Answers) != 1 {
		t.Fatalf("read: %+v %v", res, err)
	}
	var page nilda.ShopEventsAnswer
	if err := res.Answers[0].Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Sequence != 2 || !page.More {
		t.Fatalf("page %+v", page)
	}
}
