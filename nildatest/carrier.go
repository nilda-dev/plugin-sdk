package nildatest

import (
	"context"
	"fmt"
	"sync"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// StubCarrier is a shipping carrier in memory, for a shop's tests and for a carrier author's own (SHIPPING.md): it
// quotes its Quotes for every service, buys a label once per Key — a label.create repeated with the same Key returns the
// same label, as a real carrier's lookup does — voids, and tracks through Events. Install it on a desk:
//
//	desk := nildatest.NewServices()
//	carrier := nildatest.NewStubCarrier("Stub Post", nilda.CarrierRate{Service: "standard", Title: "Standard",
//		AmountMinor: 490, Currency: "EUR", DaysMin: 2, DaysMax: 4})
//	desk.Provider("stubpost", carrier)
//
// Set Latency to make every call slow, or Down to make rates answer "unavailable".
type StubCarrier struct {
	Name string
	// Quotes are the rates it gives, one per service.
	Quotes []nilda.CarrierRate
	// Latency delays every call — past nilda.CarrierRatesBudget, rates answers "unavailable".
	Latency time.Duration
	// Down makes rates answer "unavailable" and labels "rejected", as a carrier whose API is out.
	Down bool
	// Events are what Track reports, oldest first.
	Events []nilda.TrackEvent

	mu      sync.Mutex
	labels  map[string]nilda.LabelResult // by request Key
	voided  map[string]bool              // by provider ref
	bought  int
	asked   []nilda.RatesRequest
	nextRef int
}

// NewStubCarrier is a carrier quoting rates for its services.
func NewStubCarrier(name string, rates ...nilda.CarrierRate) *StubCarrier {
	return &StubCarrier{Name: name, Quotes: rates, labels: map[string]nilda.LabelResult{}, voided: map[string]bool{}}
}

// ServiceMethods makes it a provider of shipping.carrier/v1.
func (c *StubCarrier) ServiceMethods() []nilda.ServiceMethod { return nilda.CarrierMethods(c) }

// Bought is how many labels it actually bought — a repeated Key does not buy another.
func (c *StubCarrier) Bought() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bought
}

// Asked is every rates question it was asked, oldest first.
func (c *StubCarrier) Asked() []nilda.RatesRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]nilda.RatesRequest(nil), c.asked...)
}

func (c *StubCarrier) wait(ctx context.Context) error {
	if c.Latency == 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.Latency):
		return nil
	}
}

// DescribeCarrier describes one service per rate, labels and tracking on, test credentials.
func (c *StubCarrier) DescribeCarrier(ctx context.Context) (nilda.CarrierDescription, error) {
	if err := c.wait(ctx); err != nil {
		return nilda.CarrierDescription{}, err
	}
	d := nilda.CarrierDescription{Name: c.Name, Services: []nilda.CarrierService{}}
	for _, r := range c.Quotes {
		d.Services = append(d.Services, nilda.CarrierService{Key: r.Service, Label: r.Title, Test: true,
			Supports: []string{nilda.CarrierSupportRates, nilda.CarrierSupportLabels, nilda.CarrierSupportVoid,
				nilda.CarrierSupportTrack}})
	}
	return d, nil
}

// Rates quotes every rate whose service is switched on (all of them when the request names none).
func (c *StubCarrier) Rates(ctx context.Context, req nilda.RatesRequest) (nilda.RatesResult, error) {
	c.mu.Lock()
	c.asked = append(c.asked, req)
	c.mu.Unlock()
	if err := c.wait(ctx); err != nil {
		return nilda.RatesResult{}, err
	}
	if c.Down {
		return nilda.RatesResult{Status: nilda.RatesUnavailable, FailureCode: nilda.CarrierUnavailable}, nil
	}
	on := map[string]bool{}
	for _, s := range req.Services {
		on[s] = true
	}
	var out []nilda.CarrierRate
	for _, r := range c.Quotes {
		if len(on) == 0 || on[r.Service] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nilda.RatesResult{Status: nilda.RatesNoService, FailureCode: nilda.CarrierNoService}, nil
	}
	return nilda.RatesResult{Status: nilda.RatesOK, Rates: out}, nil
}

// CreateLabel buys a label once per Key.
func (c *StubCarrier) CreateLabel(ctx context.Context, req nilda.LabelRequest) (nilda.LabelResult, error) {
	if err := c.wait(ctx); err != nil {
		return nilda.LabelResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.labels[req.Key]; ok {
		return l, nil
	}
	if c.Down {
		return nilda.LabelResult{Status: nilda.LabelRejected, FailureCode: nilda.CarrierUnavailable}, nil
	}
	c.nextRef++
	c.bought++
	ref := fmt.Sprintf("stub_%d", c.nextRef)
	l := nilda.LabelResult{Status: nilda.LabelPurchased, ProviderRef: ref, TrackingNumber: fmt.Sprintf("TEST%08d", c.nextRef),
		CarrierName: c.Name, Service: req.Service, Documents: []nilda.DocumentRef{nilda.DocumentData("label", "pdf", []byte("%PDF-1.4 stub label "+ref))}}
	c.labels[req.Key] = l
	return l, nil
}

// VoidLabel cancels a label it bought.
func (c *StubCarrier) VoidLabel(ctx context.Context, req nilda.VoidRequest) (nilda.VoidResult, error) {
	if err := c.wait(ctx); err != nil {
		return nilda.VoidResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.labels {
		if l.ProviderRef == req.ProviderRef {
			c.voided[req.ProviderRef] = true
			return nilda.VoidResult{Status: nilda.VoidVoided}, nil
		}
	}
	return nilda.VoidResult{Status: nilda.VoidRejected, FailureCode: nilda.CarrierNotVoidable,
		FailureMessage: "no such label"}, nil
}

// Track reports Events, the latest as the status — "label_printed" while there are none.
func (c *StubCarrier) Track(ctx context.Context, _ nilda.TrackRequest) (nilda.TrackResult, error) {
	if err := c.wait(ctx); err != nil {
		return nilda.TrackResult{}, err
	}
	if len(c.Events) == 0 {
		return nilda.TrackResult{Status: nilda.TrackLabelPrinted}, nil
	}
	return nilda.TrackResult{Status: c.Events[len(c.Events)-1].Status, Events: c.Events}, nil
}
