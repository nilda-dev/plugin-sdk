package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// fakeCheckout answers the checkout with one ready basket; broken answers a state an adapter could not report.
type fakeCheckout struct {
	broken bool
	asked  *CheckoutCompleteRequest
}

func (f *fakeCheckout) HandleHook(context.Context, string, []byte) ([]byte, error) { return nil, nil }
func (f *fakeCheckout) HandleEvent(context.Context, string, []byte) error          { return nil }
func (f *fakeCheckout) Init(context.Context, *Core) (InitResult, error)            { return InitResult{}, nil }

func (f *fakeCheckout) state(status string) CheckoutResult {
	s := &CheckoutState{Token: "c1", Status: status, Currency: "EUR", ContinueURL: "https://kari.example/checkout/resume?token=r",
		Lines:  []CheckoutLine{{SKU: "mug-blue", Title: "Mug", Quantity: 1, UnitPriceMinor: 1500, TotalMinor: 1500}},
		Totals: CheckoutTotals{SubtotalMinor: 1500, TotalMinor: 1500}}
	if f.broken {
		s.Currency = ""
	}
	if status == CheckoutCompleted {
		s.Order = &CheckoutOrder{ID: "AB12-CD34-EF56", Status: "processing", Paid: true}
	}
	return CheckoutResult{State: s}
}

func (f *fakeCheckout) DescribeCheckout(context.Context) (CheckoutDescription, error) {
	return CheckoutDescription{Name: "Atelier Kari", Origin: "https://kari.example", Currency: "EUR", Locale: "en",
		CredentialTypes: []string{CredentialStripeSPT}}, nil
}
func (f *fakeCheckout) CreateCheckout(context.Context, CheckoutCreateRequest) (CheckoutResult, error) {
	return f.state(CheckoutRequiresEscalation), nil
}
func (f *fakeCheckout) UpdateCheckout(context.Context, CheckoutUpdateRequest) (CheckoutResult, error) {
	return f.state(CheckoutRequiresEscalation), nil
}
func (f *fakeCheckout) GetCheckout(context.Context, CheckoutRef) (CheckoutResult, error) {
	return CheckoutResult{Refused: &CheckoutRefusal{Code: CheckoutRefusedNotFound, Message: "no such checkout"}}, nil
}
func (f *fakeCheckout) CompleteCheckout(_ context.Context, req CheckoutCompleteRequest) (CheckoutResult, error) {
	f.asked = &req
	return f.state(CheckoutCompleted), nil
}
func (f *fakeCheckout) CancelCheckout(context.Context, CheckoutRef) (CheckoutResult, error) {
	return f.state(CheckoutCanceled), nil
}

// THE SHOP IS FOUND BY ITSELF, and every request is checked before it sees it: a request the checks refuse comes back
// as a refusal the adapter can read, never as the shop's call; an answer an adapter could not report never leaves.
func TestServeAnswersAShopThatImplementsTheCheckout(t *testing.T) {
	ctx := context.Background()
	shop := &fakeCheckout{}
	sp := serviceProviderOf(shop)
	if sp == nil {
		t.Fatal("a CheckoutProvider is not found")
	}
	ask := func(p ServiceProvider, method, in string) (CheckoutResult, error) {
		out, ok, err := DispatchServiceHook(ctx, p, ServiceHook(ContractShopCheckout, method), []byte(in))
		if err != nil || !ok {
			return CheckoutResult{}, err
		}
		var r CheckoutResult
		return r, json.Unmarshal(out, &r)
	}
	r, err := ask(sp, CheckoutSessionCreate, `{"lines":[{"sku":"mug-blue","quantity":1}]}`)
	if err != nil || r.State == nil || r.State.Status != CheckoutRequiresEscalation {
		t.Fatalf("session.create: %+v %v", r, err)
	}
	for name, in := range map[string]string{
		"no lines":            `{"lines":[]}`,
		"a zero line":         `{"lines":[{"sku":"mug-blue","quantity":0}]}`,
		"a blank SKU":         `{"lines":[{"sku":" ","quantity":1}]}`,
		"51 lines":            `{"lines":[` + strings.Repeat(`{"sku":"a","quantity":1},`, 50) + `{"sku":"a","quantity":1}]}`,
		"an empty credential": `{"token":"c1","terms_accepted":true,"credential":{"type":"stripe.shared_payment_token","token":""}}`,
	} {
		method := CheckoutSessionCreate
		if strings.Contains(in, "credential") {
			method = CheckoutSessionComplete
		}
		r, err := ask(sp, method, in)
		if err != nil || r.Refused == nil || r.Refused.Code != CheckoutRefusedInvalid {
			t.Errorf("%s: %+v %v", name, r, err)
		}
	}
	if shop.asked != nil {
		t.Error("the shop was asked to complete with an empty credential")
	}
	if r, err := ask(sp, CheckoutSessionComplete, `{"token":"c1","terms_accepted":true,"credential":{"type":"stripe.shared_payment_token","token":"spt_1"}}`); err != nil ||
		r.State == nil || r.State.Order == nil || shop.asked == nil || shop.asked.Credential.Token != "spt_1" {
		t.Errorf("session.complete: %+v %v", r, err)
	}
	if r, err := ask(sp, CheckoutSessionGet, `{"token":"zz"}`); err != nil || r.Refused == nil || r.Refused.Code != CheckoutRefusedNotFound {
		t.Errorf("session.get of an unknown token: %+v %v", r, err)
	}
	if _, err := ask(serviceProviderOf(&fakeCheckout{broken: true}), CheckoutSessionUpdate, `{"token":"c1"}`); err == nil ||
		!strings.Contains(err.Error(), "currency") {
		t.Errorf("an answer an adapter could not report left the shop: %v", err)
	}
}

// What a state must say for an adapter to report it honestly.
func TestACheckoutStateAnAdapterCannotReportIsRefused(t *testing.T) {
	ok := CheckoutState{Token: "c1", Status: CheckoutIncomplete, Currency: "EUR"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a plain incomplete checkout: %v", err)
	}
	for name, mutate := range map[string]func(*CheckoutState){
		"no token":                 func(s *CheckoutState) { s.Token = "" },
		"a status":                 func(s *CheckoutState) { s.Status = "open" },
		"completed with no order":  func(s *CheckoutState) { s.Status = CheckoutCompleted },
		"ready with no credential": func(s *CheckoutState) { s.Status = CheckoutReadyForPayment },
		"escalated with no link":   func(s *CheckoutState) { s.Status = CheckoutRequiresEscalation },
		"a severity":               func(s *CheckoutState) { s.Messages = []CheckoutMessage{{Severity: "fatal", Text: "x"}} },
	} {
		s := ok
		mutate(&s)
		if s.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, r := range map[string]CheckoutResult{
		"neither":     {},
		"both":        {State: &ok, Refused: &CheckoutRefusal{Code: CheckoutRefusedInvalid}},
		"a bare code": {Refused: &CheckoutRefusal{Code: "nope"}},
	} {
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	blocked := CheckoutState{Messages: []CheckoutMessage{{Severity: "warning"}, {Severity: "error"}}}
	if !blocked.Blocked() || (CheckoutState{Messages: []CheckoutMessage{{Severity: "info"}}}).Blocked() {
		t.Error("Blocked reads the error messages, and only them")
	}
}
