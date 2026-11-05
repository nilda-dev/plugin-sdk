package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// pointCarrier delivers to service points: "std" to the door, "std_point" to a point it lists.
type pointCarrier struct {
	fakeCarrier
	points   ServicePointsResult
	finder   bool // false: it describes a point service and cannot list points — the bug CarrierMethods refuses
	labelled []LabelRequest
}

func (p *pointCarrier) DescribeCarrier(context.Context) (CarrierDescription, error) {
	return CarrierDescription{Name: "Points", Services: []CarrierService{{Key: "std", Label: "Standard"},
		{Key: "std_point", Label: "To a parcel shop", Supports: []string{CarrierSupportLabels, CarrierSupportServicePoints}}}}, nil
}
func (p *pointCarrier) CreateLabel(_ context.Context, req LabelRequest) (LabelResult, error) {
	p.labelled = append(p.labelled, req)
	return LabelResult{Status: LabelPurchased, ProviderRef: "r1", TrackingNumber: "T1"}, nil
}

// finderCarrier is a pointCarrier that lists its points.
type finderCarrier struct{ *pointCarrier }

func (f finderCarrier) ServicePoints(context.Context, ServicePointsRequest) (ServicePointsResult, error) {
	return f.points, nil
}

type anyCarrierPlugin struct{ c Carrier }

func (anyCarrierPlugin) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (anyCarrierPlugin) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("reached HandleHook")
}
func (anyCarrierPlugin) HandleEvent(context.Context, string, []byte) error { return nil }
func (p anyCarrierPlugin) ServiceMethods() []ServiceMethod                 { return CarrierMethods(p.c) }

func callAnyCarrier(t *testing.T, c Carrier, method string, in any) ([]byte, error) {
	t.Helper()
	raw, _ := json.Marshal(in)
	s := &pluginServer{handler: anyCarrierPlugin{c}}
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: ServiceHook(ContractShippingCarrier, method), Payload: raw})
	if err != nil {
		return nil, err
	}
	return res.Payload, nil
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func nearVienna() ServicePointsRequest {
	return ServicePointsRequest{Service: "std_point", Country: "AT", PostalCode: "1010", City: "Wien", Limit: 2}
}

func shopPoint(id string) ServicePoint {
	return ServicePoint{ID: id, Name: "Trafik am Ring", Carrier: "DHL", Kind: ServicePointShop,
		Address:  ShipAddress{Line1: "Ring 3", City: "Wien", PostalCode: "1010", Country: "AT"},
		Latitude: f64(48.2082), Longitude: f64(16.3738), DistanceMeters: i64(85),
		Hours: []ServicePointHours{{Day: "monday", Opens: "09:00", Closes: "17:00"}, {Day: "saturday", Opens: "00:00", Closes: "24:00"}}}
}

// Points are a carrier's that delivers to them: one that does not answers "unsupported" — a result, not a failure —
// one that describes a point service and cannot list its points is refused at describe, and a point rate reaches only a
// shop that said it can let the buyer choose one, so a shop built before never sells a delivery it cannot complete.
func TestServicePointsAreAskedOnlyOfACarrierThatHasThem(t *testing.T) {
	out, err := callCarrier(t, &fakeCarrier{}, CarrierServicePointsGet, nearVienna())
	if err != nil || !strings.Contains(string(out), `"failure_code":"unsupported"`) {
		t.Fatalf("service_points of a carrier without points: %s %v", out, err)
	}
	if _, err := callAnyCarrier(t, &pointCarrier{}, CarrierDescribe, struct{}{}); err == nil {
		t.Error("a carrier describing a point service that cannot list points was answered")
	}
	pc := &pointCarrier{points: ServicePointsResult{Points: []ServicePoint{shopPoint("dhl-1"), shopPoint("dhl-2")}}}
	finder := finderCarrier{pc}
	if out, err := callAnyCarrier(t, finder, CarrierDescribe, struct{}{}); err != nil || !strings.Contains(string(out), `"service_points"`) {
		t.Fatalf("a carrier that lists its points: %s %v", out, err)
	}
	out, err = callAnyCarrier(t, finder, CarrierServicePointsGet, nearVienna())
	var got ServicePointsResult
	if err != nil || json.Unmarshal(out, &got) != nil || len(got.Points) != 2 || got.Points[0].ID != "dhl-1" ||
		*got.Points[0].DistanceMeters != 85 || got.Points[0].Hours[1].Closes != "24:00" {
		t.Fatalf("the points: %s %v", out, err)
	}

	// A point rate to a shop that did not ask for points is the carrier's bug, caught before the shop sees it.
	pc.rates = RatesResult{Status: RatesOK, Rates: []CarrierRate{{Service: "std", Title: "S", AmountMinor: 490, Currency: "EUR"},
		{Service: "std_point", Title: "P", AmountMinor: 390, Currency: "EUR", NeedsServicePoint: true}}}
	if _, err := callAnyCarrier(t, finder, CarrierRates, ratesReq()); err == nil {
		t.Error("a point rate reached a shop that never asked for points")
	}
	asked := ratesReq()
	asked.ServicePoints = true
	if out, err := callAnyCarrier(t, finder, CarrierRates, asked); err != nil || !strings.Contains(string(out), `"needs_service_point":true`) {
		t.Errorf("a point rate to a shop that asked: %s %v", out, err)
	}
	// A carrier that cannot list points offers no rate that needs one.
	if _, err := callCarrier(t, &fakeCarrier{rates: pc.rates}, CarrierRates, asked); err == nil {
		t.Error("a point rate from a carrier that lists no points left it")
	}

	// The answer is bounded and names each point once; "none near" is an answer, a failure names its code.
	for name, res := range map[string]ServicePointsResult{
		"more than asked":     {Points: []ServicePoint{shopPoint("a"), shopPoint("b"), shopPoint("c")}},
		"one point twice":     {Points: []ServicePoint{shopPoint("a"), shopPoint("a")}},
		"points and failure":  {Points: []ServicePoint{shopPoint("a")}, FailureCode: CarrierUnavailable},
		"an unknown failure":  {FailureCode: "closed"},
		"a point with no id":  {Points: []ServicePoint{func() ServicePoint { p := shopPoint(""); return p }()}},
		"a point of no kind":  {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Kind = "kiosk"; return p }()}},
		"a point nowhere":     {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Address.Country = ""; return p }()}},
		"half a position":     {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Longitude = nil; return p }()}},
		"a day off the week":  {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Hours[0].Day = "mon"; return p }()}},
		"a time off the dial": {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Hours[0].Opens = "9am"; return p }()}},
		"a span backwards":    {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.Hours[0].Closes = "08:00"; return p }()}},
		"a negative distance": {Points: []ServicePoint{func() ServicePoint { p := shopPoint("a"); p.DistanceMeters = i64(-1); return p }()}},
	} {
		pc.points = res
		if _, err := callAnyCarrier(t, finder, CarrierServicePointsGet, nearVienna()); err == nil {
			t.Errorf("%s: the answer left the carrier", name)
		}
	}
	for _, res := range []ServicePointsResult{{}, {FailureCode: CarrierUnavailable, FailureMessage: "Sendcloud is down"}} {
		pc.points = res
		if _, err := callAnyCarrier(t, finder, CarrierServicePointsGet, nearVienna()); err != nil {
			t.Errorf("%+v was refused: %v", res, err)
		}
	}
}

// A search names the place and nothing about the person, and a bounded number of points: a country and a postal code or
// a city, or coordinates — never a country alone, which is a search of a whole country.
func TestAServicePointSearchIsBounded(t *testing.T) {
	good := []ServicePointsRequest{nearVienna(),
		{Service: "std_point", Country: "AT", City: "Wien"},
		{Service: "std_point", Country: "AT", Latitude: f64(48.2), Longitude: f64(16.37), Limit: MaxServicePoints}}
	for _, r := range good {
		if err := r.Validate(); err != nil {
			t.Errorf("%+v was refused: %v", r, err)
		}
	}
	for name, r := range map[string]ServicePointsRequest{
		"no service":            {Country: "AT", PostalCode: "1010"},
		"no country":            {Service: "std_point", PostalCode: "1010"},
		"a country by its name": {Service: "std_point", Country: "Austria", PostalCode: "1010"},
		"a country alone":       {Service: "std_point", Country: "AT"},
		"a street alone":        {Service: "std_point", Country: "AT", Line1: "Ring 1"},
		"half a position":       {Service: "std_point", Country: "AT", Latitude: f64(48.2)},
		"off the globe":         {Service: "std_point", Country: "AT", Latitude: f64(91), Longitude: f64(16)},
		"past the bound":        {Service: "std_point", Country: "AT", City: "Wien", Limit: MaxServicePoints + 1},
		"a negative limit":      {Service: "std_point", Country: "AT", City: "Wien", Limit: -1},
		"a city of two lines":   {Service: "std_point", Country: "AT", City: "Wien\nforged"},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: %+v was accepted", name, r)
		}
		if _, err := callAnyCarrier(t, finderCarrier{&pointCarrier{}}, CarrierServicePointsGet, r); err == nil {
			t.Errorf("%s: the carrier was asked", name)
		}
	}
}

// A label for a service that delivers to a point carries the point the buyer chose, or it is not bought — the parcel
// would go to a door the buyer never asked for. A point on a door service, or on a return, is refused the same way.
func TestALabelForAPointRateNeedsThePoint(t *testing.T) {
	pc := &pointCarrier{}
	finder := finderCarrier{pc}
	label := func(service string, point *ChosenServicePoint) LabelRequest {
		return LabelRequest{Key: "order-1-" + service, Service: service, Origin: shipFrom(), Destination: shipTo(),
			Parcels: []Parcel{{WeightGrams: 500}}, ServicePoint: point}
	}
	for name, req := range map[string]LabelRequest{
		"a point service with no point": label("std_point", nil),
		"a door service with a point":   label("std", &ChosenServicePoint{ID: "dhl-1"}),
	} {
		out, err := callAnyCarrier(t, finder, CarrierLabelCreate, req)
		var got LabelResult
		if err != nil || json.Unmarshal(out, &got) != nil || got.Status != LabelRejected || got.FailureCode != CarrierInvalidServicePoint {
			t.Errorf("%s: %s %v", name, out, err)
		}
	}
	if len(pc.labelled) != 0 {
		t.Fatalf("the carrier was asked to buy %d label(s) it could not deliver as asked", len(pc.labelled))
	}
	want := &ChosenServicePoint{ID: "dhl-1", PostNumber: "12345678"}
	out, err := callAnyCarrier(t, finder, CarrierLabelCreate, label("std_point", want))
	if err != nil || !strings.Contains(string(out), `"status":"purchased"`) || len(pc.labelled) != 1 ||
		*pc.labelled[0].ServicePoint != *want {
		t.Fatalf("a point label: %s %v %+v", out, err, pc.labelled)
	}
	if _, err := callAnyCarrier(t, finder, CarrierLabelCreate, label("std", nil)); err != nil {
		t.Errorf("a door label: %v", err)
	}
	for name, p := range map[string]*ChosenServicePoint{
		"no id":                  {},
		"an id of two lines":     {ID: "dhl\n1"},
		"an id past its bound":   {ID: strings.Repeat("x", maxCarrierKey+1)},
		"a post number too long": {ID: "dhl-1", PostNumber: strings.Repeat("1", maxPostNumber+1)},
	} {
		if err := label("std_point", p).Validate(); err == nil {
			t.Errorf("a label with %s was accepted", name)
		}
		r := ratesReq()
		r.ServicePoint = p
		if err := r.Validate(); err == nil {
			t.Errorf("a rates question with %s was accepted", name)
		}
	}
	ret := label("std_point", &ChosenServicePoint{ID: "dhl-1"})
	ret.Return = true
	if err := ret.Validate(); err == nil {
		t.Error("a return label with a service point was accepted: a return is dropped off, not delivered to a point")
	}
}
