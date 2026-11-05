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
