package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// The kit answers Payments.Readiness as Core does: every installed gateway with its switch, the methods switched
// on that take the currency, and whether any of them is a test method.
func TestPaymentsReadinessFake(t *testing.T) {
	ctx := context.Background()
	pay := NewPayments()
	shopCore, _, srv := pay.Core("shop", "payment_session")
	defer srv.Close()
	api := shopCore.API().Payments()

	r, err := api.Readiness(ctx, "eur")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Gateways) != 0 || r.MethodsOn != 0 || r.Test {
		t.Errorf("no gateway installed read as %+v", r)
	}

	pay.Gateway("testpay", &StubGateway{}) // one test card, any currency
	pay.Gateway("bank", &StubGateway{Methods: []nilda.PaymentMethod{{Key: "sepa", Label: "SEPA", Currencies: []string{"EUR"}}}})
	r, err = api.Readiness(ctx, "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Gateways) != 2 || !r.Gateways[0].Enabled || r.Gateways[0].Key != "testpay" || r.Gateways[0].Name == "" ||
		r.MethodsOn != 2 || !r.Test {
		t.Errorf("two gateways on read as %+v", r)
	}
	if r, _ = api.Readiness(ctx, "USD"); r.MethodsOn != 1 {
		t.Errorf("only the test card takes USD; read %+v", r)
	}

	pay.SwitchGateway("testpay", false)
	r, _ = api.Readiness(ctx, "EUR")
	if len(r.Gateways) != 2 || r.Gateways[0].Enabled || !r.Gateways[1].Enabled || r.MethodsOn != 1 || r.Test {
		t.Errorf("testpay switched off read as %+v", r)
	}
	if opts, _ := api.Methods(ctx, "EUR"); len(opts) != 1 {
		t.Errorf("a switched-off gateway still offered methods: %+v", opts)
	}

	if _, err := api.Readiness(ctx, "EURO"); err == nil {
		t.Error("a currency ISO 4217 does not price was accepted")
	}
	gwCore, _, srv2 := pay.Core("bank", "payment_gateway")
	defer srv2.Close()
	if _, err := gwCore.API().Payments().Readiness(ctx, "EUR"); httpStatus(t, err) != 403 {
		t.Errorf("a gateway asked for readiness and was answered: %v", err)
	}
}
