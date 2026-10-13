package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nilda-dev/plugin-sdk/contract"
)

func shipFrom() ShipAddress {
	return ShipAddress{Name: "Shop", Line1: "Ring 1", City: "Wien", PostalCode: "1010", Country: "AT"}
}
func shipTo() ShipAddress {
	return ShipAddress{Name: "Kari", Line1: "Gate 2", City: "Oslo", PostalCode: "0150", Country: "NO", Phone: "+4712345678"}
}

func ratesReq() RatesRequest {
	return RatesRequest{Origin: shipFrom(), Destination: shipTo(), Parcels: []Parcel{{WeightGrams: 1200}}, Currency: "EUR"}
}

// fakeCarrier answers whatever a test sets.
type fakeCarrier struct {
	rates RatesResult
	label LabelResult
	wait  time.Duration
	seen  int
}

func (f *fakeCarrier) DescribeCarrier(context.Context) (CarrierDescription, error) {
	return CarrierDescription{Name: "Fake", Services: []CarrierService{{Key: "std", Label: "Standard"}}}, nil
}
func (f *fakeCarrier) Rates(ctx context.Context, _ RatesRequest) (RatesResult, error) {
	f.seen++
	if f.wait > 0 {
		select {
		case <-ctx.Done():
			return RatesResult{}, ctx.Err()
		case <-time.After(f.wait):
		}
	}
	return f.rates, nil
}
func (f *fakeCarrier) CreateLabel(context.Context, LabelRequest) (LabelResult, error) {
	return f.label, nil
}
func (f *fakeCarrier) VoidLabel(context.Context, VoidRequest) (VoidResult, error) {
	return VoidResult{Status: VoidVoided}, nil
}

type carrierPlugin struct{ *fakeCarrier }

func (carrierPlugin) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (carrierPlugin) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("reached HandleHook")
}
func (carrierPlugin) HandleEvent(context.Context, string, []byte) error { return nil }
func (p carrierPlugin) ServiceMethods() []ServiceMethod                 { return CarrierMethods(p.fakeCarrier) }

func callCarrier(t *testing.T, c *fakeCarrier, method string, in any) ([]byte, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	s := &pluginServer{handler: carrierPlugin{c}}
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: ServiceHook(ContractShippingCarrier, method), Payload: raw})
	if err != nil {
		return nil, err
	}
	return res.Payload, nil
}

// An answer the shop could not use is the carrier's own bug, caught where the carrier's author runs Serve — never a
// price of nothing at checkout or a label with no tracking number in an order.
func TestDispatchRefusesAnAnswerCoreCannotUse(t *testing.T) {
	bad := []struct {
		name  string
		rates RatesResult
	}{
		{"a rate with no price", RatesResult{Status: RatesOK, Rates: []CarrierRate{{Service: "std", Title: "S", AmountMinor: 0, Currency: "EUR"}}}},
		{"a currency ISO 4217 does not price", RatesResult{Status: RatesOK, Rates: []CarrierRate{{Service: "std", Title: "S", AmountMinor: 500, Currency: "EURO"}}}},
		{"two rates for one service", RatesResult{Status: RatesOK, Rates: []CarrierRate{
			{Service: "std", Title: "S", AmountMinor: 500, Currency: "EUR"}, {Service: "std", Title: "S", AmountMinor: 600, Currency: "EUR"}}}},
		{"ok with no rate", RatesResult{Status: RatesOK}},
		{"a status the contract does not know", RatesResult{Status: "maybe"}},
		{"a failure code the contract does not know", RatesResult{Status: RatesNoService, FailureCode: "sorry"}},
	}
	for _, b := range bad {
		if _, err := callCarrier(t, &fakeCarrier{rates: b.rates}, CarrierRates, ratesReq()); err == nil {
			t.Errorf("%s: the answer left the carrier", b.name)
		}
	}
	for _, l := range []LabelResult{
		{Status: LabelPurchased, ProviderRef: "r1"}, // no tracking number
		{Status: LabelRejected},                     // no reason
		{Status: LabelPurchased, ProviderRef: "r", TrackingNumber: "T", // a document both ways
			Documents: []DocumentRef{{Kind: "label", Format: "pdf", URL: "https://x.test/l.pdf", Data: "eA=="}}},
	} {
		req := LabelRequest{Key: "order-1", Service: "std", Origin: shipFrom(), Destination: shipTo(), Parcels: []Parcel{{WeightGrams: 500}}}
		if _, err := callCarrier(t, &fakeCarrier{label: l}, CarrierLabelCreate, req); err == nil {
			t.Errorf("label %+v left the carrier", l)
		}
	}
	good := &fakeCarrier{rates: RatesResult{Status: RatesOK, Rates: []CarrierRate{{Service: "std", Title: "Standard", AmountMinor: 790, Currency: "EUR"}}}}
	if out, err := callCarrier(t, good, CarrierRates, ratesReq()); err != nil || !strings.Contains(string(out), `"amount_minor":790`) {
		t.Fatalf("a good answer: %s %v", out, err)
	}
}

// A request no carrier could answer never reaches the carrier.
func TestARequestWithNoParcelNeverReachesTheCarrier(t *testing.T) {
	c := &fakeCarrier{rates: RatesResult{Status: RatesNoService, FailureCode: CarrierNoService}}
	req := ratesReq()
	req.Parcels = nil
	if _, err := callCarrier(t, c, CarrierRates, req); err == nil {
		t.Fatal("a rates request with no parcel was answered")
	}
	req = ratesReq()
	req.Destination.Country = "Norway"
	if _, err := callCarrier(t, c, CarrierRates, req); err == nil {
		t.Fatal("a destination that is not an ISO country was answered")
	}
	if c.seen != 0 {
		t.Fatalf("the carrier was asked %d time(s)", c.seen)
	}
	if _, err := callCarrier(t, c, CarrierLabelCreate, LabelRequest{Service: "std", Origin: shipFrom(), Destination: shipTo(),
		Parcels: []Parcel{{WeightGrams: 1}}}); err == nil || !strings.Contains(err.Error(), "key") {
		t.Fatalf("a label request with no key — the thing that makes a repeat safe — was accepted: %v", err)
	}
}

// A carrier that runs out of its time answers "unavailable": a slow carrier is a result the shop falls back from,
// not a failure counted against the plugin.
func TestARatesDeadlineIsAResultNotAnError(t *testing.T) {
	carrierRatesBudget = 50 * time.Millisecond
	defer func() { carrierRatesBudget = CarrierRatesBudget }()
	out, err := callCarrier(t, &fakeCarrier{wait: time.Second}, CarrierRates, ratesReq())
	if err != nil {
		t.Fatalf("a slow carrier failed the call: %v", err)
	}
	var r RatesResult
	if json.Unmarshal(out, &r) != nil || r.Status != RatesUnavailable || r.FailureCode != CarrierUnavailable {
		t.Fatalf("a slow carrier answered %s", out)
	}
}

// A carrier that does not track says so as a result.
func TestACarrierThatDoesNotTrackSaysUnsupported(t *testing.T) {
	out, err := callCarrier(t, &fakeCarrier{}, CarrierTrack, TrackRequest{TrackingNumber: "T1"})
	if err != nil || !strings.Contains(string(out), `"failure_code":"unsupported"`) {
		t.Fatalf("track on a carrier without CarrierTracker: %s %v", out, err)
	}
}

// The contract file names exactly the methods CarrierMethods answers, and every one is a relayed method.
func TestTheCarrierContractIsWhatCarrierMethodsAnswer(t *testing.T) {
	c, ok := ContractNamed(ContractShippingCarrier)
	if !ok {
		t.Fatal("this SDK does not ship shipping.carrier/v1")
	}
	answered := map[string]bool{}
	for _, m := range CarrierMethods(&fakeCarrier{}) {
		answered[m.Method] = true
	}
	for _, m := range c.Methods {
		if m.Hook != "" || m.AnsweredBy != "provider" {
			t.Errorf("%s rides a hook or is not the carrier's", m.Name)
		}
		if !answered[m.Name] {
			t.Errorf("the contract has %s and CarrierMethods does not answer it", m.Name)
		}
		delete(answered, m.Name)
	}
	if len(answered) != 0 {
		t.Errorf("CarrierMethods answers methods the contract does not have: %v", answered)
	}
}

// A plugin that implements Carrier and wrote no ServiceMethods is still answered: the one forgotten line must not make
// every call fail the way an unsubscribed WidgetProvider once did.
type bareCarrierPlugin struct{ *fakeCarrier }

func (bareCarrierPlugin) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (bareCarrierPlugin) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("reached HandleHook")
}
func (bareCarrierPlugin) HandleEvent(context.Context, string, []byte) error { return nil }

func TestACarrierWithoutServiceMethodsIsStillAnswered(t *testing.T) {
	s := &pluginServer{handler: bareCarrierPlugin{&fakeCarrier{}}}
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: ServiceHook(ContractShippingCarrier, CarrierDescribe), Payload: []byte(`{}`)})
	if err != nil || !strings.Contains(string(res.Payload), `"name":"Fake"`) {
		t.Fatalf("describe on a bare Carrier: %v %v", res, err)
	}
}
