package main

import (
	"bytes"
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

func addr(country, phone string) nilda.ShipAddress {
	return nilda.ShipAddress{Name: "A", Line1: "L1", City: "C", PostalCode: "1010", Country: country, Phone: phone}
}

// The whole carrier through the desk a shop calls it by: rates, a label bought once, a refusal as a result.
func TestTablePostThroughTheDesk(t *testing.T) {
	ctx := context.Background()
	tp := &TablePost{labels: map[string]nilda.LabelResult{}}
	desk := nildatest.NewServices()
	desk.Provider("tablepost", tp)
	core, _, srv := desk.Core("shop", nilda.ContractShippingCarrier)
	defer srv.Close()
	ship := core.API().Services()

	answers, missed, err := ship.ShippingRates(ctx, nilda.RatesRequest{Origin: addr("AT", ""), Destination: addr("DE", ""),
		Parcels: []nilda.Parcel{{WeightGrams: 2500}}, Currency: "EUR"}, nilda.ServiceCallOptions{})
	if err != nil || len(missed) != 0 || len(answers) != 1 || len(answers[0].Result.Rates) != 2 {
		t.Fatalf("rates: %+v missed %+v err %v", answers, missed, err)
	}
	if got := answers[0].Result.Rates[0].AmountMinor; got != 990+2*198 {
		t.Fatalf("a 2.5 kg EU parcel costs %d, want %d", got, 990+2*198)
	}

	answers, _, _ = ship.ShippingRates(ctx, nilda.RatesRequest{Origin: addr("AT", ""), Destination: addr("US", ""),
		Parcels: []nilda.Parcel{{WeightGrams: 500}}, Currency: "EUR"}, nilda.ServiceCallOptions{})
	if len(answers) != 1 || answers[0].Result.FailureCode != nilda.CarrierCustomsMissing {
		t.Fatalf("a parcel to the US with no customs declaration: %+v", answers)
	}

	req := nilda.LabelRequest{Key: "order-7-1", Service: "standard", Origin: addr("AT", ""), Destination: addr("DE", ""),
		Parcels: []nilda.Parcel{{WeightGrams: 500}}}
	l1, err := ship.CreateShippingLabel(ctx, "tablepost", req)
	if err != nil || l1.Status != nilda.LabelPurchased {
		t.Fatalf("label: %+v %v", l1, err)
	}
	pdf, err := l1.Documents[0].Bytes()
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) || !bytes.HasSuffix(pdf, []byte("%%EOF\n")) {
		t.Fatalf("the label is not a PDF: %v %q", err, pdf)
	}
	l2, _ := ship.CreateShippingLabel(ctx, "tablepost", req)
	if l2.ProviderRef != l1.ProviderRef {
		t.Fatalf("the same key bought a second label: %s then %s", l1.ProviderRef, l2.ProviderRef)
	}

	req.Key, req.Service = "order-8-1", "express"
	if l, err := ship.CreateShippingLabel(ctx, "tablepost", req); err != nil || l.FailureCode != nilda.CarrierInvalidAddress {
		t.Fatalf("express with no phone: %+v %v — a refusal is a result", l, err)
	}
	if v, err := ship.VoidShippingLabel(ctx, "tablepost", nilda.VoidRequest{ProviderRef: l1.ProviderRef}); err != nil || v.Status != nilda.VoidVoided {
		t.Fatalf("void: %+v %v", v, err)
	}
}
