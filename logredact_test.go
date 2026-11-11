package nilda

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestTheLoggerNeverWritesAPaymentToken is the 2026-11-11 review's finding 3: fmt redacted the tokens, the JSON
// log line did not.
func TestTheLoggerNeverWritesAPaymentToken(t *testing.T) {
	var buf bytes.Buffer
	log := newLogger(&buf, "gw")
	cred := PaymentCredential{Type: CredentialStripeSPT, Token: "spt_LEAK1"}
	start := PaymentStartRequest{
		Session:    PaymentSession{ID: "ps_1", ClientAction: "pi_1_secret_LEAK2", AmountMinor: 500, Currency: "EUR"},
		Credential: &cred,
		Saved:      &PaymentSavedToken{Token: "cus_1/pm_LEAK3"},
	}
	log.Info("charging", "req", start, "cred", cred, "forget", PaymentForgetRequest{Token: "tok_LEAK4"},
		"saved", &PaymentSaved{Token: "LEAK5", Label: "Visa 4242"}, "action", ClientAction("LEAK6"),
		"list", []any{map[string]any{"c": cred}}, "nilptr", (*PaymentSavedToken)(nil))
	line := buf.String()
	for _, leak := range []string{"LEAK1", "LEAK2", "LEAK3", "LEAK4", "LEAK5", "LEAK6"} {
		if strings.Contains(line, leak) {
			t.Fatalf("the log line carries a token (%s):\n%s", leak, line)
		}
	}
	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("the line is not JSON: %v\n%s", err, line)
	}
	req := got["req"].(map[string]any)
	if req["session"].(map[string]any)["id"] != "ps_1" || req["credential"] != cred.String() {
		t.Fatalf("the redacted value lost its shape: %v", req)
	}
	if got["saved"] != "Visa 4242 [token redacted]" || got["nilptr"] != nil {
		t.Fatalf("saved %v, nilptr %v", got["saved"], got["nilptr"])
	}
}

// TestTheLoggerLeavesOrdinaryValuesAlone: a value with nothing secret in its type is written as json.Marshal writes it.
func TestTheLoggerLeavesOrdinaryValuesAlone(t *testing.T) {
	var buf bytes.Buffer
	when := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	refund := PaymentRefund{ID: "r1", AmountMinor: 100, Currency: "EUR", CreatedAt: when}
	newLogger(&buf, "gw").Info("refund", "refund", refund, "lic", Licence{State: LicenceActive})
	var got map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(refund)
	if string(got["refund"]) != string(want) {
		t.Fatalf("an ordinary value changed shape:\n got %s\nwant %s", got["refund"], want)
	}
	if string(got["lic"]) != `{"state":"active"}` {
		t.Fatalf("a json.Marshaler was not used: %s", got["lic"])
	}
}
