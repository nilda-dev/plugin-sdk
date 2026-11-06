package nilda

import (
	"fmt"
	"slices"
	"strings"
)

// PAYMENT MARKS (commerce plan 1C, ST15; v0.58.0): which brands' acceptance marks a way to pay shows — the strip of
// card and wallet logos under a price or in a footer. A CLOSED list, because Core draws each mark from its own set of
// files, drawn to the brand's own acceptance-mark guidelines: a mark Core has no file for is a mark nobody can draw,
// and a free-text brand would be a logo nobody checked the licence of.
//
// A gateway names the marks of each of its methods (PaymentMethod.Marks); Core hands them to the shop on every
// PaymentOption; the shop names the marks of everything its checkout takes — those options it offers and its own
// offline methods — on CommerceEndpoints.PaymentMarks, which is all Core's strip draws.

// The payment marks, each the brand as a buyer recognises it at a till.
const (
	PaymentMarkVisa           = "visa"
	PaymentMarkMastercard     = "mastercard"
	PaymentMarkAmex           = "amex"
	PaymentMarkMaestro        = "maestro"
	PaymentMarkPayPal         = "paypal"
	PaymentMarkKlarna         = "klarna"
	PaymentMarkApplePay       = "apple-pay"
	PaymentMarkGooglePay      = "google-pay"
	PaymentMarkSEPA           = "sepa"
	PaymentMarkIDEAL          = "ideal"
	PaymentMarkBancontact     = "bancontact"
	PaymentMarkEPS            = "eps"
	PaymentMarkBankTransfer   = "bank-transfer"
	PaymentMarkCashOnDelivery = "cash-on-delivery"
)

// PaymentMarks is every mark there is, in the order a strip draws them when nothing else decides.
func PaymentMarks() []string {
	return []string{PaymentMarkVisa, PaymentMarkMastercard, PaymentMarkAmex, PaymentMarkMaestro, PaymentMarkPayPal,
		PaymentMarkKlarna, PaymentMarkApplePay, PaymentMarkGooglePay, PaymentMarkSEPA, PaymentMarkIDEAL,
		PaymentMarkBancontact, PaymentMarkEPS, PaymentMarkBankTransfer, PaymentMarkCashOnDelivery}
}

// ValidPaymentMark reports whether m is one of PaymentMarks.
func ValidPaymentMark(m string) bool {
	return slices.Contains(PaymentMarks(), m)
}

// validMarks refuses a list of marks with one that is not a mark, or one named twice.
func validMarks(marks []string) error {
	seen := map[string]bool{}
	for _, m := range marks {
		if !ValidPaymentMark(m) {
			return fmt.Errorf("%q is not a payment mark; the marks are %s", m, strings.Join(PaymentMarks(), ", "))
		}
		if seen[m] {
			return fmt.Errorf("the mark %q is named twice", m)
		}
		seen[m] = true
	}
	return nil
}
