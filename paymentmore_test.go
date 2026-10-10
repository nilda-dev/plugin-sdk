package nilda

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// checkReservedNames holds the payment types to the names reserved for what the contract will carry (credential,
// capture, void): no exported field and no json tag may take one before it is built. A function over types so a
// test can feed it a type that does.
func checkReservedNames(types ...any) []string {
	var bad []string
	for _, v := range types {
		t := reflect.TypeOf(v)
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")[0]
			for _, name := range reservedPaymentNames {
				if strings.EqualFold(f.Name, strings.ReplaceAll(name, "_", "")) || tag == name {
					bad = append(bad, t.Name()+"."+f.Name+" takes the reserved name "+name)
				}
			}
		}
	}
	return bad
}

var paymentTypes = []any{PaymentMethod{}, PaymentOption{}, PaymentSession{}, PaymentRefund{}, PaymentSessionParams{},
	PaymentRefundParams{}, PaymentResolution{}, PaymentRejection{}, PaymentStartResult{}, PaymentRefundResult{},
	PaymentDispute{}, PaymentDisputeReport{}, PaymentExternalRefund{}, PaymentAddress{}}

func TestReservedPaymentNamesAreUnused(t *testing.T) {
	for _, b := range checkReservedNames(paymentTypes...) {
		t.Error(b)
	}
}

func TestTheReservedNameGuardFailsOnPurpose(t *testing.T) {
	type sneaky struct {
		Credential string `json:"credential"`
	}
	type sneakyTag struct {
		Token string `json:"credential_types"`
	}
	if len(checkReservedNames(sneaky{})) == 0 || len(checkReservedNames(sneakyTag{})) == 0 {
		t.Fatal("the guard let a reserved name through")
	}
}

func TestSupportsOfReadsTheListOrTheOldBool(t *testing.T) {
	if got := SupportsOf(PaymentMethod{Refunds: true}); !Supports(got, SupportRefund) || !Supports(got, SupportPartialRefund) {
		t.Errorf("a method that lists nothing and refunds supports %v", got)
	}
	if got := SupportsOf(PaymentMethod{}); len(got) != 0 {
		t.Errorf("a method that lists nothing and does not refund supports %v", got)
	}
	if got := SupportsOf(PaymentMethod{Refunds: true, Supports: []string{SupportRefund}}); Supports(got, SupportPartialRefund) {
		t.Errorf("a method's own list was widened by its bool: %v", got)
	}
}

func TestTheDisputeMachine(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{DisputeNeedsResponse, DisputeUnderReview, true},
		{DisputeUnderReview, DisputeNeedsResponse, true},
		{DisputeUnderReview, DisputeWon, true},
		{DisputeNeedsResponse, DisputeClosed, true},
		{DisputeWon, DisputeLost, false},
		{DisputeLost, DisputeUnderReview, false},
		{DisputeClosed, DisputeNeedsResponse, false},
	} {
		if got := DisputeCanMove(tc.from, tc.to); got != tc.ok {
			t.Errorf("%s → %s = %v, want %v", tc.from, tc.to, got, tc.ok)
		}
	}
}

func TestValidationRefusesWhatCoreRefuses(t *testing.T) {
	good := PaymentDisputeReport{ProviderRef: "dp_1", Stage: DisputeChargeback, Status: DisputeNeedsResponse,
		AmountMinor: 100, Currency: "eur", URL: "https://dashboard.stripe.com/disputes/dp_1"}
	if err := good.Validate(); err != nil {
		t.Fatalf("a good report was refused: %v", err)
	}
	for name, change := range map[string]func(r *PaymentDisputeReport){
		"no provider ref":  func(r *PaymentDisputeReport) { r.ProviderRef = "" },
		"unknown stage":    func(r *PaymentDisputeReport) { r.Stage = "warning" },
		"unknown status":   func(r *PaymentDisputeReport) { r.Status = "warning_needs_response" },
		"no currency":      func(r *PaymentDisputeReport) { r.Currency = "XXX" },
		"a plain-http url": func(r *PaymentDisputeReport) { r.URL = "http://dashboard.stripe.com/x" },
		"negative amount":  func(r *PaymentDisputeReport) { r.AmountMinor = -1 },
		"a long reason":    func(r *PaymentDisputeReport) { r.Reason = strings.Repeat("x", 101) },
	} {
		r := good
		change(&r)
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	params := PaymentSessionParams{Gateway: "g", Method: "m", Reference: "r", AmountMinor: 1, Currency: "EUR",
		ReturnURL: "https://site.test/x", Fulfilment: FulfilmentPhysical,
		ShippingAddress: &PaymentAddress{Name: "A", Line1: "Ring 1", City: "Wien", PostalCode: "1010", Country: "AT"}}
	if err := params.Validate(); err != nil {
		t.Fatalf("good params refused: %v", err)
	}
	for name, change := range map[string]func(p *PaymentSessionParams){
		"lowercase country":  func(p *PaymentSessionParams) { p.ShippingAddress = &PaymentAddress{Country: "at"} },
		"three-letter code":  func(p *PaymentSessionParams) { p.BillingAddress = &PaymentAddress{Country: "AUT"} },
		"a long street":      func(p *PaymentSessionParams) { p.BillingAddress = &PaymentAddress{Line1: strings.Repeat("x", 201)} },
		"unknown fulfilment": func(p *PaymentSessionParams) { p.Fulfilment = "shipping" },
	} {
		p := params
		change(&p)
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if (PaymentExternalRefund{AmountMinor: 100, Currency: "EUR"}).Validate() == nil {
		t.Error("an external refund with no processor reference was accepted")
	}
	if validSecret("Bad-Name", "v") == nil || validSecret("ok", "") == nil || validSecret("ok", strings.Repeat("x", 4097)) == nil {
		t.Error("a bad secret was accepted")
	}
}

type syncingGateway struct {
	stubPaymentGateway
	synced []string
}

func (g *syncingGateway) SyncPayment(_ context.Context, s PaymentSession) error {
	g.synced = append(g.synced, s.ID)
	return nil
}

type stubPaymentGateway struct{}

func (stubPaymentGateway) DescribePayments(context.Context) ([]PaymentMethod, error) { return nil, nil }
func (stubPaymentGateway) StartPayment(context.Context, PaymentSession) (PaymentStartResult, error) {
	return PaymentStartResult{}, nil
}
func (stubPaymentGateway) RefundPayment(context.Context, PaymentRefund, PaymentSession) (PaymentRefundResult, error) {
	return PaymentRefundResult{}, nil
}

func TestPaymentSyncReachesOnlyASyncer(t *testing.T) {
	payload, _ := json.Marshal(PaymentSyncRequest{Session: PaymentSession{ID: "s1", AmountMinor: 1, Currency: "EUR"}})
	g := &syncingGateway{}
	if out, handled, err := DispatchPaymentGatewayHook(context.Background(), nil, g, HookPaymentSync, payload); !handled || err != nil || string(out) != "{}" {
		t.Fatalf("a syncer: %s %v %v", out, handled, err)
	}
	if len(g.synced) != 1 {
		t.Fatalf("synced %v", g.synced)
	}
	if _, handled, _ := DispatchPaymentGatewayHook(context.Background(), nil, stubPaymentGateway{}, HookPaymentSync, payload); handled {
		t.Fatal("a gateway that does not sync answered payment.sync")
	}
	if got := withProvidedHooks(syncHandler{}, nil); !reflect.DeepEqual(got, []string{HookPaymentDescribe,
		HookPaymentStart, HookPaymentRefund, HookPaymentSync}) {
		t.Fatalf("a syncing gateway subscribed %v", got)
	}
	for _, h := range withProvidedHooks(plainGatewayHandler{}, nil) {
		if h == HookPaymentSync {
			t.Fatal("a gateway that does not sync was subscribed to payment.sync")
		}
	}
}

// plainGatewayHandler is a Handler that is a gateway; syncHandler is one that also syncs.
type plainGatewayHandler struct{ stubPaymentGateway }

func (plainGatewayHandler) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (plainGatewayHandler) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, nil
}
func (plainGatewayHandler) HandleEvent(context.Context, string, []byte) error { return nil }

type syncHandler struct{ plainGatewayHandler }

func (syncHandler) SyncPayment(context.Context, PaymentSession) error { return nil }

func TestADisputeTimeSurvivesTheWire(t *testing.T) {
	at := time.Date(2026, 10, 20, 23, 59, 59, 0, time.UTC)
	b, _ := json.Marshal(PaymentDisputeReport{ProviderRef: "dp", Stage: DisputeInquiry, Status: DisputeNeedsResponse,
		AmountMinor: 1, Currency: "EUR", RespondBy: &at})
	var back PaymentDisputeReport
	if err := json.Unmarshal(b, &back); err != nil || back.RespondBy == nil || !back.RespondBy.Equal(at) {
		t.Fatalf("respond_by did not survive: %s → %+v %v", b, back, err)
	}
}
