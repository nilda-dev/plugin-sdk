package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// DOOR D5 (commerce plan 2026-11-05 §1J): a charge with the payer absent says which kind of merchant-initiated charge it
// is, and a card taken in person at a reader is a session of its own kind.

// A kept method's charge says what the payer's agreement is FOR (review 2026-11-05): a subscription's renewal is
// recurring, a pre-order's charge at shipment unscheduled — whatever interval the mandate carries, which the consumer
// writes — and an update's is the kind of the agreement it replaces. An update kept before it named one is read by its
// interval, the rhythm Core holds its charges to.
func TestAChargesKindIsWhatTheMandateIsFor(t *testing.T) {
	for _, c := range []struct {
		m        PaymentMandate
		replaced string
		want     string
	}{
		{PaymentMandate{Purpose: MandateSubscription, Interval: IntervalMonth, IntervalCount: 1}, "", OffSessionRecurring},
		{PaymentMandate{Purpose: MandatePreOrder}, "", OffSessionUnscheduled},
		{PaymentMandate{Purpose: MandatePreOrder, Interval: IntervalMonth, IntervalCount: 1}, "", OffSessionUnscheduled},
		{PaymentMandate{Purpose: MandateUpdate, Replaces: "pm_1"}, OffSessionRecurring, OffSessionRecurring},
		{PaymentMandate{Purpose: MandateUpdate, Replaces: "pm_1", Interval: IntervalYear, IntervalCount: 1}, OffSessionUnscheduled, OffSessionUnscheduled},
		{PaymentMandate{Purpose: MandateUpdate, Interval: IntervalYear, IntervalCount: 1}, "", OffSessionRecurring},
		{PaymentMandate{Purpose: MandateUpdate}, "", OffSessionUnscheduled},
	} {
		if got := OffSessionOf(c.m, c.replaced); got != c.want {
			t.Errorf("OffSessionOf(%+v, %q) = %q, want %q", c.m, c.replaced, got, c.want)
		}
	}
}

// A new mandate cannot steer its kind: a pre-order names no interval, an update names the kept method it replaces,
// and nothing else names one.
func TestANewMandateCannotSteerItsKind(t *testing.T) {
	base := PaymentMandate{Purpose: MandatePreOrder, Text: "€4.50 when it ships.", AmountMinor: 450, Currency: "EUR"}
	if err := base.Validate(); err != nil {
		t.Fatalf("a pre-order was refused: %v", err)
	}
	upd := base
	upd.Purpose, upd.Replaces = MandateUpdate, "01a0d2d2-64f9-79c4-b460-0369f4fce253"
	if err := upd.Validate(); err != nil {
		t.Fatalf("an update naming what it replaces was refused: %v", err)
	}
	for name, change := range map[string]func(*PaymentMandate){
		"a pre-order with an interval":   func(m *PaymentMandate) { m.Interval, m.IntervalCount = IntervalMonth, 1 },
		"an update replacing nothing":    func(m *PaymentMandate) { m.Purpose = MandateUpdate },
		"a pre-order replacing a method": func(m *PaymentMandate) { m.Replaces = "01a0d2d2-64f9-79c4-b460-0369f4fce253" },
		"an update replacing a NUL":      func(m *PaymentMandate) { m.Purpose, m.Replaces = MandateUpdate, "a\x00" },
	} {
		m := base
		change(&m)
		if err := m.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A reader's id and label are one plain line: no control character (a newline forges a log line) and no direction
// override or isolate (U+202A–U+202E, U+2066–U+2069), which shows staff a name that is not the reader's.
func TestAReadersNameIsOnePlainLine(t *testing.T) {
	for _, bad := range []string{"\n", "\t", "\u202e", "\u202a", "\u2066", "\u2069", "\x7f"} {
		m := readerMethod()
		m.Readers = []PaymentReader{{ID: "tmr_1", Label: "Front" + bad + "counter"}}
		if err := validMethods([]PaymentMethod{m}); err == nil {
			t.Errorf("a reader labelled %q was accepted", m.Readers[0].Label)
		}
		m.Readers = []PaymentReader{{ID: "tmr" + bad + "1", Label: "Front"}}
		if err := validMethods([]PaymentMethod{m}); err == nil {
			t.Errorf("a reader with the id %q was accepted", m.Readers[0].ID)
		}
		p := inPerson()
		p.Reader = "tmr" + bad + "1"
		if err := p.Validate(); err == nil {
			t.Errorf("a session at the reader %q was accepted", p.Reader)
		}
	}
}

func inPerson() PaymentSessionParams {
	return PaymentSessionParams{Gateway: "stripe", Method: "card_present", Reference: "till-1#1", AmountMinor: 450,
		Currency: "EUR", Reader: "tmr_1"}
}

// An in-person session names its reader and needs no return address: the payer is at the till, not in a browser.
// One that names no reader is an online session and still needs one.
func TestAnInPersonSessionNamesItsReaderAndNeedsNoReturnAddress(t *testing.T) {
	if err := inPerson().Validate(); err != nil {
		t.Fatalf("an in-person session was refused: %v", err)
	}
	online := inPerson()
	online.Reader = ""
	if err := online.Validate(); err == nil {
		t.Fatal("an online session with no return address was accepted")
	}
	bad := map[string]func(*PaymentSessionParams){
		"a reader past 255 bytes": func(p *PaymentSessionParams) { p.Reader = strings.Repeat("r", 256) },
		"a reader with a NUL":     func(p *PaymentSessionParams) { p.Reader = "tmr\x00" },
		"a reader of spaces":      func(p *PaymentSessionParams) { p.Reader = "   " },
		"a method kept at a till": func(p *PaymentSessionParams) {
			p.Save = &PaymentMandate{Purpose: MandatePreOrder, Text: "later", AmountMinor: 450, Currency: "EUR"}
		},
		"a delegated credential at a till": func(p *PaymentSessionParams) {
			p.Credential = &PaymentCredential{Type: CredentialStripeSPT, Token: "spt_1"}
		},
		"a return address off its form": func(p *PaymentSessionParams) { p.ReturnURL = "not a url" },
	}
	for name, change := range bad {
		p := inPerson()
		change(&p)
		if err := p.Validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func readerMethod() PaymentMethod {
	return PaymentMethod{Key: "card_present", Label: "Card reader", Supports: []string{SupportRefund, SupportCardPresent},
		Readers: []PaymentReader{{ID: "tmr_1", Label: "Front counter"}}}
}

// A method that takes cards in person lists the readers it drives, and only such a method lists readers.
func TestAMethodThatTakesCardsInPersonListsItsReaders(t *testing.T) {
	if err := validMethods([]PaymentMethod{readerMethod()}); err != nil {
		t.Fatalf("a reader method was refused: %v", err)
	}
	many := make([]PaymentReader, maxReaders+1)
	for i := range many {
		many[i] = PaymentReader{ID: "tmr_" + strings.Repeat("x", i+1), Label: "Till"}
	}
	bad := map[string]func(*PaymentMethod){
		"card_present with no reader":      func(m *PaymentMethod) { m.Readers = nil },
		"readers without card_present":     func(m *PaymentMethod) { m.Supports = []string{SupportRefund} },
		"two readers with one id":          func(m *PaymentMethod) { m.Readers = append(m.Readers, m.Readers[0]) },
		"a reader with no id":              func(m *PaymentMethod) { m.Readers[0].ID = " " },
		"a reader with no label":           func(m *PaymentMethod) { m.Readers[0].Label = "" },
		"a reader label past 100":          func(m *PaymentMethod) { m.Readers[0].Label = strings.Repeat("é", 101) },
		"a reader id past 255 bytes":       func(m *PaymentMethod) { m.Readers[0].ID = strings.Repeat("r", 256) },
		"a reader label with a NUL":        func(m *PaymentMethod) { m.Readers[0].Label = "a\x00" },
		"more readers than one method has": func(m *PaymentMethod) { m.Readers = many },
	}
	for name, change := range bad {
		m := readerMethod()
		m.Readers = append([]PaymentReader(nil), m.Readers...)
		change(&m)
		if err := validMethods([]PaymentMethod{m}); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// onlyStarts is a gateway that cannot be asked to let anything go.
type onlyStarts struct{ methods []PaymentMethod }

func (g onlyStarts) DescribePayments(context.Context) ([]PaymentMethod, error) { return g.methods, nil }
func (g onlyStarts) StartPayment(context.Context, PaymentSession) (PaymentStartResult, error) {
	return PaymentStartResult{Status: PaymentPending}, nil
}
func (g onlyStarts) RefundPayment(context.Context, PaymentRefund, PaymentSession) (PaymentRefundResult, error) {
	return PaymentRefundResult{Status: RefundPending}, nil
}

// letsGo is the same gateway, able to void.
type letsGo struct{ onlyStarts }

func (letsGo) CapturePayment(context.Context, PaymentCaptureRequest) (PaymentCaptureResult, error) {
	return PaymentCaptureResult{Status: PaymentPending}, nil
}
func (letsGo) VoidPayment(context.Context, PaymentVoidRequest) (PaymentVoidResult, error) {
	return PaymentVoidResult{Status: PaymentRejected}, nil
}

// The till's cancel is a void (payment.void): a gateway that cannot be asked one does not take cards in person, or a
// payment left waiting at a reader could never be called off.
func TestOnlyAGatewayThatCanVoidTakesCardsInPerson(t *testing.T) {
	ctx := context.Background()
	if _, _, err := DispatchPaymentGatewayHook(ctx, nil, onlyStarts{[]PaymentMethod{readerMethod()}}, HookPaymentDescribe, []byte(`{}`)); err == nil {
		t.Fatal("a gateway that cannot void listed card_present")
	}
	out, _, err := DispatchPaymentGatewayHook(ctx, nil, letsGo{onlyStarts{[]PaymentMethod{readerMethod()}}}, HookPaymentDescribe, []byte(`{}`))
	if err != nil {
		t.Fatalf("a gateway that can void was refused: %v", err)
	}
	var res PaymentDescribeResponse
	if err := json.Unmarshal(out, &res); err != nil || len(res.Methods) != 1 || len(res.Methods[0].Readers) != 1 ||
		res.Methods[0].Readers[0].ID != "tmr_1" {
		t.Fatalf("the readers did not reach the wire: %s, %v", out, err)
	}
}
