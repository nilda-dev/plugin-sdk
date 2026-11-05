package nildatest

import (
	"slices"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// atReader is Core's in-person rule for a session (v0.58.0, EXPERIMENTAL, door D5): a session naming a reader only on
// a method that takes cards in person and lists that reader; a method that takes cards in person takes no session
// without one — its payer is at a till, not on a page.
func atReader(m nilda.PaymentMethod, params nilda.PaymentSessionParams) error {
	inPerson := nilda.Supports(nilda.SupportsOf(m), nilda.SupportCardPresent)
	switch {
	case params.Reader == "" && inPerson:
		return invalid("%q of %q takes a card at a reader; name one of its readers", params.Method, params.Gateway)
	case params.Reader == "":
		return nil
	case !inPerson:
		return invalid("%q of %q does not take a card in person", params.Method, params.Gateway)
	case !slices.ContainsFunc(m.Readers, func(r nilda.PaymentReader) bool { return r.ID == params.Reader }):
		return invalid("%q of %q has no reader %q", params.Method, params.Gateway, params.Reader)
	}
	return nil
}

// voidable is Core's rule for what a void may let go: a hold (authorized), or a payment still waiting at its card reader
// (pending, in person) — never one held for a person because the processor took another amount.
func voidable(s nilda.PaymentSession) bool {
	return s.Status == nilda.PaymentAuthorized ||
		(s.Status == nilda.PaymentPending && s.Reader != "" && s.FailureCode != nilda.PaymentFailAmountMismatch)
}
