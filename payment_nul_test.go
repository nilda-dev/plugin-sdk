package nilda

import "testing"

// Core refuses a NUL character in any payment text (its validate.go's noNUL, review 2026-10-30): Postgres keeps one
// in neither text nor jsonb, so a value that passed every bound reached the database and came back as a 500. Every
// validator Core holds to the SDK's refuses it here too, in Core's words — a plugin told "valid" by Validate and then
// refused by Core has been told something false. Each case is a value Validate accepts, with one NUL put in one field.
func TestEveryPaymentTextRefusesANUL(t *testing.T) {
	const nul = "a\x00b"
	const words = "text in a payment may not hold a NUL character"
	refused := func(name string, err error, coreWords bool) {
		t.Helper()
		switch {
		case err == nil:
			t.Errorf("%s: a NUL was accepted — Core refuses it", name)
		case coreWords && err.Error() != "nilda: "+words:
			t.Errorf("%s: refused as %q, want Core's %q", name, err, words)
		}
	}

	session := func(edit func(*PaymentSessionParams)) error {
		p := validParams()
		p.Email = "payer@example.com"
		p.BillingAddress = &PaymentAddress{Name: "A", Line1: "Ring 1", Line2: "Top 2", City: "Wien", PostalCode: "1010",
			Region: "W", Country: "AT"}
		edit(&p)
		return p.Validate()
	}
	if err := session(func(*PaymentSessionParams) {}); err != nil {
		t.Fatalf("the control session was refused: %v", err)
	}
	for name, edit := range map[string]func(*PaymentSessionParams){
		"gateway":     func(p *PaymentSessionParams) { p.Gateway = nul },
		"method":      func(p *PaymentSessionParams) { p.Method = nul },
		"reference":   func(p *PaymentSessionParams) { p.Reference = nul },
		"description": func(p *PaymentSessionParams) { p.Description = nul },
		// A NUL in an address is a NUL whether net/mail reads it or not; Core asks noNUL before it parses.
		"email":               func(p *PaymentSessionParams) { p.Email = "pay\x00er@example.com" },
		"address name":        func(p *PaymentSessionParams) { p.BillingAddress.Name = nul },
		"address line1":       func(p *PaymentSessionParams) { p.BillingAddress.Line1 = nul },
		"address line2":       func(p *PaymentSessionParams) { p.BillingAddress.Line2 = nul },
		"address city":        func(p *PaymentSessionParams) { p.BillingAddress.City = nul },
		"address postal_code": func(p *PaymentSessionParams) { p.BillingAddress.PostalCode = "10\x0010" },
		"address region":      func(p *PaymentSessionParams) { p.BillingAddress.Region = nul },
	} {
		refused("session "+name, session(edit), true)
	}
	// A mandate's text, under Save: Core asks it beside the SDK's PaymentMandate.Validate.
	saved := validParams()
	saved.Save = &PaymentMandate{Purpose: MandatePreOrder, Text: "Charged when it ships.", AmountMinor: 1234, Currency: "EUR"}
	if err := saved.Validate(); err != nil {
		t.Fatalf("the control session with Save was refused: %v", err)
	}
	saved.Save.Text = "Charged when\x00 it ships."
	refused("session save.text", saved.Validate(), true)
	// A credential's type is a token, and a token holds no NUL (Core's credential sentence, not the NUL one).
	cred := PaymentCredential{Type: CredentialStripeSPT, Token: "spt_1"}
	if err := cred.Validate(); err != nil {
		t.Fatalf("the control credential was refused: %v", err)
	}
	cred.Type = "stripe.shared\x00payment_token"
	refused("credential type", cred.Validate(), false)

	refused("refund reason", PaymentRefundParams{AmountMinor: 1, Reason: nul}.Validate(), true)
	refused("resolution provider_ref", PaymentResolution{AmountMinor: 1, Currency: "EUR", ProviderRef: nul}.Validate(), true)
	refused("resolution saved.label", PaymentResolution{AmountMinor: 1, Currency: "EUR",
		Saved: &PaymentSaved{Token: "cus_1/pm_1", Label: "Visa\x00 4242"}}.Validate(), true)
	refused("rejection failure_message", PaymentRejection{FailureCode: PaymentFailDeclined, FailureMessage: nul}.Validate(), true)
	refused("rejection provider_ref", PaymentRejection{FailureCode: PaymentFailDeclined, ProviderRef: nul}.Validate(), true)
	dispute := PaymentDisputeReport{ProviderRef: "dp_1", Stage: DisputeChargeback, Status: DisputeNeedsResponse,
		AmountMinor: 100, Currency: "EUR"}
	if err := dispute.Validate(); err != nil {
		t.Fatalf("the control dispute was refused: %v", err)
	}
	dispute.Reason = nul
	refused("dispute reason", dispute.Validate(), true)
	dispute.Reason, dispute.ProviderRef = "", "dp\x001"
	refused("dispute provider_ref", dispute.Validate(), true)
	refused("external refund reason", PaymentExternalRefund{AmountMinor: 1, Currency: "EUR", ProviderRef: "re_1",
		Reason: nul}.Validate(), true)
	refused("external refund provider_ref", PaymentExternalRefund{AmountMinor: 1, Currency: "EUR",
		ProviderRef: "re\x001"}.Validate(), true)
	charge := func(edit func(*PaymentChargeParams)) error {
		p := PaymentChargeParams{SavedMethod: "sm_1", Reference: "order-1", AmountMinor: 1, Currency: "EUR"}
		edit(&p)
		return p.Validate()
	}
	if err := charge(func(*PaymentChargeParams) {}); err != nil {
		t.Fatalf("the control charge was refused: %v", err)
	}
	refused("charge reference", charge(func(p *PaymentChargeParams) { p.Reference = nul }), true)
	refused("charge description", charge(func(p *PaymentChargeParams) { p.Description = nul }), true)
	refused("charge shipping address", charge(func(p *PaymentChargeParams) { p.ShippingAddress = &PaymentAddress{City: nul} }), true)
	refused("idempotency key", validIdempotencyKey("attempt\x001"), true)

	// What a GATEWAY answers, checked by the dispatch before Core reads it (Core's validMethods, validStart and
	// validRefundResult refuse the whole answer).
	method := func(edit func(*PaymentMethod)) error {
		m := PaymentMethod{Key: "card", Label: "Card", Description: "Visa, Mastercard", Supports: []string{SupportRefund},
			CredentialTypes: []string{CredentialStripeSPT}, TestHint: map[string]string{"en": "4242 4242 4242 4242"}}
		edit(&m)
		return validMethods([]PaymentMethod{m})
	}
	if err := method(func(*PaymentMethod) {}); err != nil {
		t.Fatalf("the control method was refused: %v", err)
	}
	for name, edit := range map[string]func(*PaymentMethod){
		"key":             func(m *PaymentMethod) { m.Key = "ca\x00rd" },
		"label":           func(m *PaymentMethod) { m.Label = nul },
		"description":     func(m *PaymentMethod) { m.Description = nul },
		"test hint":       func(m *PaymentMethod) { m.TestHint = map[string]string{"en": nul} },
		"supports":        func(m *PaymentMethod) { m.Supports = []string{"refund\x00"} },
		"credential type": func(m *PaymentMethod) { m.CredentialTypes = []string{"spt\x00"} },
	} {
		refused("method "+name, method(edit), false)
	}
	refused("start failure_message", validStart(PaymentStartResult{Status: PaymentPending, FailureMessage: nul}), true)
	refused("start provider_ref", validStart(PaymentStartResult{Status: PaymentPending, ProviderRef: nul}), true)
	refused("refund result failure_message", validRefundResult(PaymentRefundResult{Status: RefundPending, FailureMessage: nul}), true)
	refused("refund result provider_ref", validRefundResult(PaymentRefundResult{Status: RefundPending, ProviderRef: nul}), true)
}
