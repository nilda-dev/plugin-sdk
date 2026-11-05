package nilda

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// A CARD TAKEN IN PERSON, AT A READER (v0.58.0, EXPERIMENTAL; commerce's door D5, for the till of plugin P17). Inside
// payments/v1 — the same session, the same state machine, the same reports — with three optional fields, and outside
// its no-breaking promise until a till built by Nilda has shipped green on it: the fields' meaning may still change in a
// minor release, said in CHANGELOG.md.
//
// The market's server-side shape, which this is: the server names the reader and the processor pushes the amount to it;
// the payer taps or inserts there; the outcome arrives by webhook. Stripe Terminal's server-driven integration
// (create a PaymentIntent with card_present, then POST /v1/terminal/readers/{id}/process_payment_intent; the result by
// terminal.reader.action_succeeded / action_failed; cancel_action to call it off — docs.stripe.com/terminal/payments/
// collect-card-payment?terminal-sdk-platform=server-driven, read 2026-11-05), Adyen's cloud Terminal API (a request to
// a terminal's POIID) and Square's Terminal API (a checkout sent to a device_id) all work so.
//
//	CONSUMER  offers a method whose Supports lists SupportCardPresent at its till — never online — and starts a session
//	          naming one of its Readers (PaymentSessionParams.Reader), with no return address. It hears the outcome in
//	          PaymentUpdated like any other; the till's Cancel is Payments.Void.
//	CORE      refuses an in-person session on any other method or reader, and an online session on such a method; asks
//	          the gateway once; holds a start whose answer was lost pending and looks it up (the reader may have taken
//	          the card); lets a session still waiting at its reader be voided.
//	GATEWAY   lists the readers its owner registered (from its settings — never a live probe at describe), sends the
//	          amount to the named reader and answers PENDING (the payer decides there) or rejected; reports the outcome
//	          with Resolve, Reject or — for a session asked with capture manual — Authorize; and answers payment.void by
//	          calling the reader's action off and cancelling the payment at the processor. It implements PaymentCapturer.
//
// Not in it: keeping a card taken at a till for later (Save — Stripe's generated_card is the processor's own), a
// delegated credential, a reader driven from the till device by the processor's own SDK (Stripe's Tap to Pay: the
// gateway's own route may hand the till app a connection token; nothing of it crosses Core), and payments a reader
// stores while offline.

// SupportCardPresent — the method takes a card in person, at one of the readers it lists (PaymentMethod.Readers). A
// method that lists it belongs to a gateway that implements PaymentCapturer: the till's cancel is a void.
const SupportCardPresent = "card_present"

// maxReaders is the most readers one method lists, and maxReaderLabel the longest name of one: Nilda's figures — a shop
// with a hundred tills runs more than one site. A reader's id is bounded like a provider_ref (255 bytes): it is the
// processor's id ("tmr_…").
const (
	maxReaders     = 100
	maxReaderLabel = 100
)

// PaymentReader is one card reader a gateway drives, as its owner registered it at the gateway.
type PaymentReader struct {
	// ID is the processor's id for the reader (Stripe: "tmr_…"): what a session names.
	ID string `json:"id"`
	// Label is how the till's staff know it: "Front counter".
	Label string `json:"label"`
}

// validReaders holds a described method to the in-person rule: a method that takes cards in person lists 1 to
// maxReaders readers, each with an id and a label, no id twice; a method that does not lists none.
func validReaders(m PaymentMethod) error {
	inPerson := Supports(m.Supports, SupportCardPresent)
	switch {
	case inPerson && len(m.Readers) == 0:
		return fmt.Errorf("nilda: payment.describe: method %q supports %s and lists no reader; offer it once a reader "+
			"is registered", m.Key, SupportCardPresent)
	case !inPerson && len(m.Readers) > 0:
		return fmt.Errorf("nilda: payment.describe: method %q lists readers and does not support %s", m.Key, SupportCardPresent)
	case len(m.Readers) > maxReaders:
		return fmt.Errorf("nilda: payment.describe: method %q lists %d readers; at most %d", m.Key, len(m.Readers), maxReaders)
	}
	seen := map[string]bool{}
	for _, r := range m.Readers {
		if err := validReaderID(r.ID); err != nil {
			return fmt.Errorf("nilda: payment.describe: method %q: %w", m.Key, err)
		}
		if strings.TrimSpace(r.Label) == "" || utf8.RuneCountInString(r.Label) > maxReaderLabel || !noNUL(r.Label) {
			return fmt.Errorf("nilda: payment.describe: method %q: a reader has a label of 1 to %d characters, with no NUL",
				m.Key, maxReaderLabel)
		}
		if seen[r.ID] {
			return fmt.Errorf("nilda: payment.describe: method %q lists the reader %q twice", m.Key, r.ID)
		}
		seen[r.ID] = true
	}
	return nil
}

// validReaderID is the rule for a reader's id, listed or named by a session: 1 to 255 bytes, not blank, no NUL.
func validReaderID(id string) error {
	if strings.TrimSpace(id) == "" || len(id) > maxProviderRef || !noNUL(id) {
		return fmt.Errorf("a reader's id is 1 to %d bytes, not blank, with no NUL", maxProviderRef)
	}
	return nil
}

// validInPerson holds an in-person session's parameters to what a till can do: a reader named by its id, and neither a
// method kept for later nor a delegated credential — the payer is there, with a card.
func validInPerson(p PaymentSessionParams) error {
	if err := validReaderID(p.Reader); err != nil {
		return fmt.Errorf("nilda: %w", err)
	}
	if p.Save != nil {
		return fmt.Errorf("nilda: a card taken at a reader is not kept for later through this contract")
	}
	if p.Credential != nil {
		return fmt.Errorf("nilda: a payment at a reader carries no delegated credential; the payer is there")
	}
	return nil
}
