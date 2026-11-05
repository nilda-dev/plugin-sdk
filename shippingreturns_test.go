package nilda

import (
	"encoding/json"
	"strings"
	"testing"
)

func returnLabelReq() LabelRequest {
	return LabelRequest{Key: "return-1042-1", Service: "std", Origin: shipFrom(), Destination: shipTo(),
		Parcels: []Parcel{{WeightGrams: 500}}, Return: true, Paperless: true}
}

func paperlessLabel() LabelResult {
	return LabelResult{Status: LabelPurchased, ProviderRef: "r1", TrackingNumber: "JVGL1234", DropOffCode: "8K3F-2Q9A",
		Documents: []DocumentRef{DocumentData("return_qr", "png", []byte("\x89PNG qr")),
			{Kind: "return_qr", Format: "pdf", URL: "https://shippo-delivery.example/96_qr_code.pdf"}}}
}

// A paperless return label leaves the carrier as the carrier gave it: the code the buyer reads out at the counter, the
// QR they show — by its bytes or by the carrier's link — beside the tracking number the shop follows the parcel by.
func TestAPaperlessReturnCarriesItsDropOffCode(t *testing.T) {
	out, err := callCarrier(t, &fakeCarrier{label: paperlessLabel()}, CarrierLabelCreate, returnLabelReq())
	if err != nil {
		t.Fatalf("a paperless return label was refused: %v", err)
	}
	var got LabelResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.DropOffCode != "8K3F-2Q9A" || len(got.Documents) != 2 || got.Documents[0].Kind != "return_qr" ||
		!strings.Contains(string(out), `"drop_off_code":"8K3F-2Q9A"`) {
		t.Fatalf("the label left as %s", out)
	}
	// A carrier that gives only the QR (Shippo, Sendcloud) or only the code (ShipEngine's handoff code) is as good.
	qrOnly := paperlessLabel()
	qrOnly.DropOffCode = ""
	codeOnly := paperlessLabel()
	codeOnly.Documents = nil
	for _, l := range []LabelResult{qrOnly, codeOnly} {
		if _, err := callCarrier(t, &fakeCarrier{label: l}, CarrierLabelCreate, returnLabelReq()); err != nil {
			t.Errorf("label %+v was refused: %v", l, err)
		}
	}
	// A carrier that cannot answers paperless with a printed label: never a refusal.
	printed := LabelResult{Status: LabelPurchased, ProviderRef: "r1", TrackingNumber: "T",
		Documents: []DocumentRef{DocumentData("return_label", "pdf", []byte("%PDF"))}}
	if _, err := callCarrier(t, &fakeCarrier{label: printed}, CarrierLabelCreate, returnLabelReq()); err != nil {
		t.Errorf("a printed answer to a paperless ask was refused: %v", err)
	}
	if err := (CarrierDescription{Name: "C", Services: []CarrierService{{Key: "std", Label: "S",
		Supports: []string{CarrierSupportReturns, CarrierSupportPaperlessReturns}}}}).Validate(); err != nil {
		t.Errorf("a service offering paperless returns was refused: %v", err)
	}
}

// The code and the QR belong to a return the buyer drops off: on an outbound label a drop-off code is a carrier bug the
// shop would mail a buyer for nothing, so it never leaves the carrier — nor a code that is not one plain short line, nor
// one on a label that was not bought, nor a QR a phone cannot show.
func TestADropOffCodeIsOnlyOnAPurchasedReturnLabel(t *testing.T) {
	outbound := returnLabelReq()
	outbound.Return, outbound.Paperless = false, false
	withQR := LabelResult{Status: LabelPurchased, ProviderRef: "r1", TrackingNumber: "T",
		Documents: []DocumentRef{DocumentData("return_qr", "png", []byte("qr"))}}
	if _, err := callCarrier(t, &fakeCarrier{label: paperlessLabel()}, CarrierLabelCreate, outbound); err == nil {
		t.Error("an outbound label with a drop-off code left the carrier")
	}
	if _, err := callCarrier(t, &fakeCarrier{label: withQR}, CarrierLabelCreate, outbound); err == nil {
		t.Error("an outbound label with a return QR left the carrier")
	}
	if err := paperlessLabel().ValidateFor(outbound); err == nil {
		t.Error("ValidateFor let a drop-off code ride an outbound label")
	}
	bad := map[string]LabelResult{}
	for name, code := range map[string]string{
		"two lines":       "8K3F\n2Q9A",
		"past 64":         strings.Repeat("A", maxDropOffCode+1),
		"turned around":   "8K3F‮2Q9A",
		"only whitespace": "   ",
	} {
		l := paperlessLabel()
		l.DropOffCode = code
		bad["a code of "+name] = l
	}
	pending := paperlessLabel()
	pending.Status, pending.ProviderRef, pending.TrackingNumber, pending.Documents = LabelPending, "", "", nil
	bad["a code on a pending label"] = pending
	rejected := LabelResult{Status: LabelRejected, FailureCode: CarrierDeclined, DropOffCode: "8K3F"}
	bad["a code on a rejected label"] = rejected
	zpl := paperlessLabel()
	zpl.Documents = []DocumentRef{DocumentData("return_qr", "zpl", []byte("^XA"))}
	bad["a QR for a label printer"] = zpl
	for name, l := range bad {
		if _, err := callCarrier(t, &fakeCarrier{label: l}, CarrierLabelCreate, returnLabelReq()); err == nil {
			t.Errorf("%s left the carrier", name)
		}
	}
	// Paperless is a return's: an outbound label asked paperless never reaches the carrier.
	ask := returnLabelReq()
	ask.Return = false
	if err := ask.Validate(); err == nil || !strings.Contains(err.Error(), "paperless") {
		t.Errorf("an outbound label asked paperless: %v", err)
	}
}
