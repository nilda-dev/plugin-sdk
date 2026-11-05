package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func addr(country string) nilda.ShipAddress {
	return nilda.ShipAddress{Name: "A", Line1: "L1", City: "C", PostalCode: "1010", Country: country}
}

// A shop and two carriers, with the desk between them: rates from both, a label bought once whatever the retries.
func TestStubCarrierDrivenThroughServe(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	post := NewStubCarrier("Stub Post", nilda.CarrierRate{Service: "standard", Title: "Standard", AmountMinor: 490,
		Currency: "EUR", DaysMin: 2, DaysMax: 4})
	express := NewStubCarrier("Stub Express", nilda.CarrierRate{Service: "express", Title: "Express", AmountMinor: 1990,
		Currency: "EUR", DaysMin: 1, DaysMax: 1})
	desk.Provider("stubpost", post)
	desk.Provider("stubexpress", express)
	core, _, srv := desk.Core("shop", nilda.ContractShippingCarrier)
	defer srv.Close()
	ship := core.API().Services()

	req := nilda.RatesRequest{Origin: addr("AT"), Destination: addr("DE"), Parcels: []nilda.Parcel{{WeightGrams: 800}}, Currency: "EUR"}
	answers, missed, err := ship.ShippingRates(ctx, req, nilda.ServiceCallOptions{})
	if err != nil || len(answers) != 2 || len(missed) != 0 {
		t.Fatalf("rates: %+v missed %+v err %v", answers, missed, err)
	}

	express.Down = true
	answers, _, err = ship.ShippingRates(ctx, req, nilda.ServiceCallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range answers {
		if a.Provider == "stubexpress" && (a.Result.Status != nilda.RatesUnavailable || a.Result.FailureCode != nilda.CarrierUnavailable) {
			t.Fatalf("a carrier that is down answered %+v; it is a result the shop falls back from", a.Result)
		}
	}

	label := nilda.LabelRequest{Key: "order-1042-1", Service: "standard", Origin: addr("AT"), Destination: addr("DE"),
		Parcels: []nilda.Parcel{{WeightGrams: 800}}, Reference: "1042"}
	first, err := ship.CreateShippingLabel(ctx, "stubpost", label)
	if err != nil || first.Status != nilda.LabelPurchased || first.TrackingNumber == "" {
		t.Fatalf("label: %+v %v", first, err)
	}
	again, err := ship.CreateShippingLabel(ctx, "stubpost", label)
	if err != nil || again.ProviderRef != first.ProviderRef || post.Bought() != 1 {
		t.Fatalf("a repeated label.create bought %d labels (%+v)", post.Bought(), again)
	}
	if calls := desk.Calls(); calls[len(calls)-1].IdempotencyKey != label.Key {
		t.Fatalf("the label's key did not travel as the idempotency key: %+v", calls[len(calls)-1])
	}

	v, err := ship.VoidShippingLabel(ctx, "stubpost", nilda.VoidRequest{ProviderRef: first.ProviderRef, Key: label.Key})
	if err != nil || v.Status != nilda.VoidVoided {
		t.Fatalf("void: %+v %v", v, err)
	}
	tr, err := ship.TrackShipment(ctx, "stubpost", nilda.TrackRequest{TrackingNumber: first.TrackingNumber})
	if err != nil || tr.Status != nilda.TrackLabelPrinted {
		t.Fatalf("track: %+v %v", tr, err)
	}
	if _, err := ship.CreateShippingLabel(ctx, "", label); err == nil {
		t.Fatal("a label with no carrier named was sent")
	}
}

// A return label asked paperless of a carrier that gives one comes back as a code and a QR to show at the drop-off
// point; asked of one that cannot, or asked printed, it is a return label to print — never a refusal (v0.58.0).
func TestStubCarrierGivesAPaperlessReturn(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	post := NewStubCarrier("Stub Post", nilda.CarrierRate{Service: "standard", Title: "Standard", AmountMinor: 490, Currency: "EUR"})
	post.Paperless = true
	plain := NewStubCarrier("Plain Post", nilda.CarrierRate{Service: "standard", Title: "Standard", AmountMinor: 490, Currency: "EUR"})
	desk.Provider("stubpost", post)
	desk.Provider("plainpost", plain)
	core, _, srv := desk.Core("shop", nilda.ContractShippingCarrier)
	defer srv.Close()
	ship := core.API().Services()

	d, err := post.DescribeCarrier(ctx)
	if err != nil || !nilda.Supports(d.Services[0].Supports, nilda.CarrierSupportPaperlessReturns) {
		t.Fatalf("a paperless carrier does not say so: %+v %v", d, err)
	}
	ret := nilda.LabelRequest{Key: "return-1042-1", Service: "standard", Origin: addr("AT"), Destination: addr("DE"),
		Parcels: []nilda.Parcel{{WeightGrams: 800}}, Return: true, Paperless: true}
	got, err := ship.CreateShippingLabel(ctx, "stubpost", ret)
	if err != nil || got.Status != nilda.LabelPurchased || got.DropOffCode == "" || len(got.Documents) != 1 ||
		got.Documents[0].Kind != "return_qr" {
		t.Fatalf("a paperless return: %+v %v", got, err)
	}
	for name, c := range map[string]struct {
		provider  string
		paperless bool
	}{"asked of a carrier that cannot": {"plainpost", true}, "asked printed": {"stubpost", false}} {
		req := ret
		req.Key, req.Paperless = "return-"+name, c.paperless
		got, err := ship.CreateShippingLabel(ctx, c.provider, req)
		if err != nil || got.Status != nilda.LabelPurchased || got.DropOffCode != "" || len(got.Documents) != 1 ||
			got.Documents[0].Kind != "return_label" {
			t.Errorf("a return %s: %+v %v", name, got, err)
		}
	}
}

// A carrier that delivers to service points, through the desk: its point rate only to a shop that asks for points, the
// nearest points of that service, bounded, and a label that carries the chosen one — a point it never listed refused in
// the contract's words (v0.58.0).
func TestStubCarrierListsItsServicePoints(t *testing.T) {
	ctx := context.Background()
	desk := NewServices()
	pt := func(id string, metres int64) nilda.ServicePoint {
		return nilda.ServicePoint{ID: id, Name: "Shop " + id, Carrier: "Stub Post", Kind: nilda.ServicePointShop,
			Address: nilda.ShipAddress{Line1: "Ring 1", City: "Wien", PostalCode: "1010", Country: "AT"}, DistanceMeters: &metres}
	}
	post := NewStubCarrier("Stub Post",
		nilda.CarrierRate{Service: "standard", Title: "Standard", AmountMinor: 490, Currency: "EUR"},
		nilda.CarrierRate{Service: "standard_point", Title: "To a parcel shop", AmountMinor: 390, Currency: "EUR", NeedsServicePoint: true})
	post.Points = []nilda.ServicePoint{pt("p1", 80), pt("p2", 300), pt("p3", 900)}
	plain := NewStubCarrier("Plain Post", nilda.CarrierRate{Service: "standard", Title: "Standard", AmountMinor: 490, Currency: "EUR"})
	desk.Provider("stubpost", post)
	desk.Provider("plainpost", plain)
	core, _, srv := desk.Core("shop", nilda.ContractShippingCarrier)
	defer srv.Close()
	ship := core.API().Services()

	req := nilda.RatesRequest{Origin: addr("AT"), Destination: addr("AT"), Parcels: []nilda.Parcel{{WeightGrams: 800}}, Currency: "EUR"}
	rates := func(points bool) []nilda.CarrierRate {
		r := req
		r.ServicePoints = points
		answers, _, err := ship.ShippingRates(ctx, r, nilda.ServiceCallOptions{Provider: "stubpost"})
		if err != nil || len(answers) != 1 {
			t.Fatalf("rates: %+v %v", answers, err)
		}
		return answers[0].Result.Rates
	}
	if got := rates(false); len(got) != 1 || got[0].NeedsServicePoint {
		t.Fatalf("a shop that did not ask for points was offered %+v", got)
	}
	if got := rates(true); len(got) != 2 || !got[1].NeedsServicePoint {
		t.Fatalf("a shop that asked for points was offered %+v", got)
	}

	near := nilda.ServicePointsRequest{Service: "standard_point", Country: "AT", PostalCode: "1010", Limit: 2}
	points, err := ship.ShippingServicePoints(ctx, "stubpost", near)
	if err != nil || len(points.Points) != 2 || points.Points[0].ID != "p1" || points.FailureCode != "" {
		t.Fatalf("the two nearest points: %+v %v", points, err)
	}
	if got, err := ship.ShippingServicePoints(ctx, "plainpost", near); err != nil || got.FailureCode != nilda.CarrierUnsupported {
		t.Errorf("a carrier without points answered %+v %v", got, err)
	}
	if _, err := ship.ShippingServicePoints(ctx, "", near); err == nil {
		t.Error("points were asked of no carrier in particular")
	}

	label := nilda.LabelRequest{Key: "order-7-1", Service: "standard_point", Origin: addr("AT"), Destination: addr("AT"),
		Parcels: []nilda.Parcel{{WeightGrams: 800}}, ServicePoint: &nilda.ChosenServicePoint{ID: "p2"}}
	got, err := ship.CreateShippingLabel(ctx, "stubpost", label)
	if err != nil || got.Status != nilda.LabelPurchased {
		t.Fatalf("a label to a point: %+v %v", got, err)
	}
	if asked := post.LabelsAsked(); len(asked) != 1 || asked[0].ServicePoint == nil || asked[0].ServicePoint.ID != "p2" {
		t.Fatalf("the carrier was asked %+v", asked)
	}
	for name, p := range map[string]*nilda.ChosenServicePoint{"no point": nil, "a point it never listed": {ID: "elsewhere"}} {
		l := label
		l.Key, l.ServicePoint = "order-7-"+name, p
		got, err := ship.CreateShippingLabel(ctx, "stubpost", l)
		if err != nil || got.Status != nilda.LabelRejected || got.FailureCode != nilda.CarrierInvalidServicePoint {
			t.Errorf("a label with %s: %+v %v", name, got, err)
		}
	}
	if post.Bought() != 1 {
		t.Errorf("the carrier bought %d labels, want the one to a point it listed", post.Bought())
	}
}
