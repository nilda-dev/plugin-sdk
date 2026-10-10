package nilda

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// THE CONTRACT'S v0.10.4 ADDITIONS (plugin-stripe's docs/architecture-payments.md §1, and commerce's
// DESIGN_REVIEW_2026-10-10 rows A2, A3, B1–B4). All additive: a field an older side does not know is ignored on the
// wire, a route an older Core does not have answers 404, and a hook nobody subscribed to is never sent.
//
//   - WHAT A METHOD CAN DO is a list, Supports, rather than one bool per capability (PaymentMethod.Refunds stays,
//     and a method that lists nothing is read as Refunds says). Core copies the list onto the session when it is
//     created, so a refund of an old payment is decided by what the method could do THEN, never by asking the
//     gateway again (a gateway whose key was removed made every old payment unrefundable).
//   - THE PAYER, when the consumer knows them: a billing and a shipping address and what is being fulfilled — a
//     processor's fraud checks and seller protection read them (PayPal's needs the shipping address).
//   - A DISPUTE is a second object beside its session; the session's state machine is untouched (resolved stays
//     final). A gateway reports each dispute as a SNAPSHOT; Core keeps one row per dispute and tells the consumer.
//   - A REFUND CAN BE REVERSED after it succeeded (a card refund the bank sent back): it stays resolved, gains
//     ReversedAt, and the consumer is told again.
//   - A REFUND MADE AT THE PROCESSOR, outside Nilda (in the Stripe Dashboard), is reported by the gateway, so the
//     consumer's books and Core's refunded total stay right.
//   - A MISSED WEBHOOK has a pull path: Core asks a gateway that implements PaymentSyncer to look a session up
//     at its processor (payment.sync), and the gateway reports what it finds through the usual calls.
//   - A GATEWAY'S OWN SECRET (a webhook signing secret its processor showed once) is kept by Core, encrypted, and
//     survives a restart and a lost kv.

// What a method can do: the vocabulary of PaymentMethod.Supports. Core acts on the tokens it knows and keeps any
// other — a gateway may name a capability before Core does anything with it.
const (
	// SupportRefund — money paid this way can be given back through Core.
	SupportRefund = "refund"
	// SupportPartialRefund — and less than the whole of it.
	SupportPartialRefund = "partial_refund"
	// SupportDisputes — the gateway reports disputes (ReportDispute), so the owner learns of one in Nilda rather
	// than only at the processor.
	SupportDisputes = "disputes"
	// SupportSync — the gateway answers payment.sync (it implements PaymentSyncer).
	SupportSync = "sync"
)

// The tokens and names reserved for what the contract will carry and does not yet (architecture §1.2, §1.3).
// Nothing may use them for anything else: TestReservedPaymentNamesAreUnused fails on an exported field or a json
// tag named after them.
var reservedPaymentNames = []string{"credential", "credential_types", "capture", "partial_capture", "void"}

// SupportsOf is what a method can do: its Supports, or — for a gateway that lists nothing — what its Refunds bool
// says, so every gateway written before the list existed keeps working unchanged.
func SupportsOf(m PaymentMethod) []string {
	if len(m.Supports) > 0 {
		return m.Supports
	}
	if m.Refunds {
		return []string{SupportRefund, SupportPartialRefund}
	}
	return nil
}

// Supports reports whether a list names one capability.
func Supports(list []string, capability string) bool {
	for _, s := range list {
		if s == capability {
			return true
		}
	}
	return false
}

// What is being paid for, as a processor's fraud checks and seller protection read it: PaymentSession.Fulfilment.
const (
	FulfilmentPhysical = "physical" // goods shipped to an address
	FulfilmentDigital  = "digital"  // a download, a licence, access
	FulfilmentNone     = "none"     // a service, a donation, a deposit
)

// PaymentAddress is a payer's billing or shipping address. Country is ISO 3166-1 alpha-2 ("AT"); every other
// field is free text, bounded.
type PaymentAddress struct {
	Name       string `json:"name,omitempty"`
	Line1      string `json:"line1,omitempty"`
	Line2      string `json:"line2,omitempty"`
	City       string `json:"city,omitempty"`
	PostalCode string `json:"postal_code,omitempty"`
	Region     string `json:"region,omitempty"`
	Country    string `json:"country,omitempty"`
}

// The bounds on an address. Core holds the same numbers.
const (
	maxAddressLine   = 200 // a name, a street line
	maxAddressCity   = 100 // a city, a region
	maxAddressPostal = 20
)

var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

// Validate refuses an address past its bounds or with a country that is not two capital letters.
func (a PaymentAddress) Validate() error {
	for name, v := range map[string]string{"name": a.Name, "line1": a.Line1, "line2": a.Line2} {
		if utf8.RuneCountInString(v) > maxAddressLine {
			return fmt.Errorf("nilda: an address's %s is at most %d characters", name, maxAddressLine)
		}
	}
	for name, v := range map[string]string{"city": a.City, "region": a.Region} {
		if utf8.RuneCountInString(v) > maxAddressCity {
			return fmt.Errorf("nilda: an address's %s is at most %d characters", name, maxAddressCity)
		}
	}
	if utf8.RuneCountInString(a.PostalCode) > maxAddressPostal {
		return fmt.Errorf("nilda: an address's postal_code is at most %d characters", maxAddressPostal)
	}
	if a.Country != "" && !countryCode.MatchString(a.Country) {
		return fmt.Errorf("nilda: an address's country is an ISO 3166-1 alpha-2 code (\"AT\"), got %q", a.Country)
	}
	return nil
}

func validFulfilment(f string) error {
	switch f {
	case "", FulfilmentPhysical, FulfilmentDigital, FulfilmentNone:
		return nil
	}
	return fmt.Errorf("nilda: fulfilment is physical, digital or none, got %q", f)
}

// ---- disputes -------------------------------------------------------------------------------------------

// A dispute's stage: an inquiry (the payer's bank asks; no money has moved) or a chargeback (the money is being
// taken back). A dispute moves from inquiry to chargeback and never back.
const (
	DisputeInquiry    = "inquiry"
	DisputeChargeback = "chargeback"
)

// A dispute's status. won, lost and closed are final — closed is a dispute that ended without a verdict: an
// inquiry the payer dropped, one the processor prevented.
const (
	DisputeNeedsResponse = "needs_response"
	DisputeUnderReview   = "under_review"
	DisputeWon           = "won"
	DisputeLost          = "lost"
	DisputeClosed        = "closed"
)

// disputeMoves is the dispute's state machine, one table, held equal to Core's by a test on each side.
var disputeMoves = map[string][]string{
	DisputeNeedsResponse: {DisputeUnderReview, DisputeWon, DisputeLost, DisputeClosed},
	DisputeUnderReview:   {DisputeNeedsResponse, DisputeWon, DisputeLost, DisputeClosed},
}

// DisputeCanMove reports whether a dispute's status may go from one to another.
func DisputeCanMove(from, to string) bool { return canMove(disputeMoves, from, to) }

// DisputeFinal reports whether a status ends a dispute.
func DisputeFinal(status string) bool {
	return status == DisputeWon || status == DisputeLost || status == DisputeClosed
}

// PaymentDispute is one dispute, as Core holds it. Its money fields — FundsHeld, AmountMinor, Refundable,
// RespondBy, URL — are facts the processor reports and stay updatable after the verdict (a processor gives the
// money back after it says "won").
type PaymentDispute struct {
	ID          string `json:"id"`
	Session     string `json:"session"`
	ProviderRef string `json:"provider_ref"`
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	// Reason is the processor's own code, verbatim: the lists differ between processors.
	Reason      string `json:"reason,omitempty"`
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// RespondBy is when evidence is due at the processor; absent when there is nothing to answer.
	RespondBy           *time.Time `json:"respond_by,omitempty"`
	EvidenceSubmittedAt *time.Time `json:"evidence_submitted_at,omitempty"`
	// FundsHeld is whether the processor has taken the disputed money from the merchant right now.
	FundsHeld bool `json:"funds_held"`
	// Refundable is whether a refund can still stop the loss.
	Refundable bool `json:"refundable"`
	// URL is where a PERSON answers the dispute at the processor.
	URL       string     `json:"url,omitempty"`
	OpenedAt  *time.Time `json:"opened_at,omitempty"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// PaymentDisputeReport is what a GATEWAY reports (Payments.ReportDispute): the dispute as the processor holds it
// NOW. Read it again from the processor on every event rather than trusting the event's copy — dispute events
// arrive out of order.
type PaymentDisputeReport struct {
	ProviderRef         string     `json:"provider_ref"`
	Stage               string     `json:"stage"`
	Status              string     `json:"status"`
	Reason              string     `json:"reason,omitempty"`
	AmountMinor         int64      `json:"amount_minor"`
	Currency            string     `json:"currency"`
	RespondBy           *time.Time `json:"respond_by,omitempty"`
	EvidenceSubmittedAt *time.Time `json:"evidence_submitted_at,omitempty"`
	FundsHeld           bool       `json:"funds_held"`
	Refundable          bool       `json:"refundable"`
	URL                 string     `json:"url,omitempty"`
	OpenedAt            *time.Time `json:"opened_at,omitempty"`
}

// The bounds on a dispute. Core holds the same numbers.
const (
	maxDisputeReason = 100
	// MaxDisputesPerSession is how many disputes Core keeps for one payment: a split dispute is rare, a loop is not.
	MaxDisputesPerSession = 10
)

// Validate refuses a report Core would refuse without reading anything.
func (r PaymentDisputeReport) Validate() error {
	switch {
	case strings.TrimSpace(r.ProviderRef) == "":
		return fmt.Errorf("nilda: a dispute report needs the processor's dispute id as provider_ref")
	case r.Stage != DisputeInquiry && r.Stage != DisputeChargeback:
		return fmt.Errorf("nilda: a dispute's stage is inquiry or chargeback, got %q", r.Stage)
	case !validDisputeStatus(r.Status):
		return fmt.Errorf("nilda: a dispute's status is needs_response, under_review, won, lost or closed, got %q", r.Status)
	case utf8.RuneCountInString(r.Reason) > maxDisputeReason:
		return fmt.Errorf("nilda: a dispute's reason is at most %d characters", maxDisputeReason)
	}
	if r.AmountMinor < 0 {
		return fmt.Errorf("nilda: a dispute's amount_minor is not negative, got %d", r.AmountMinor)
	}
	if _, ok := MinorUnits(strings.ToUpper(strings.TrimSpace(r.Currency))); !ok {
		return fmt.Errorf("nilda: %q is not a currency ISO 4217 gives a minor unit to", r.Currency)
	}
	if r.URL != "" {
		u, err := url.Parse(r.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(r.URL) > maxPayerURL {
			return fmt.Errorf("nilda: a dispute's url is an absolute https URL, got %q", r.URL)
		}
	}
	return validProviderRef(r.ProviderRef)
}

func validDisputeStatus(s string) bool {
	switch s {
	case DisputeNeedsResponse, DisputeUnderReview, DisputeWon, DisputeLost, DisputeClosed:
		return true
	}
	return false
}

// PaymentDisputeUpdate is what HookPaymentDisputeUpdated carries to a consumer.
type PaymentDisputeUpdate struct {
	Dispute PaymentDispute `json:"dispute"`
	Session PaymentSession `json:"session"`
}

// PaymentDisputeConsumer is what a consumer implements to hear about disputes on its payments. OPTIONAL: a
// consumer that does not implement it is never sent the hook, and Core shows the owner that this consumer does
// not take dispute news. Repeated until you return nil, like PaymentUpdated: key what you do by (d.ID, d.Stage,
// d.Status, d.FundsHeld), never by arrival.
type PaymentDisputeConsumer interface {
	DisputeUpdated(ctx context.Context, d PaymentDispute, s PaymentSession) error
}

// ---- refunds made elsewhere, and refunds undone ----------------------------------------------------------

// PaymentExternalRefund is a refund a GATEWAY saw happen at its processor that Nilda did not ask for — one made by
// hand in the processor's dashboard (Payments.ReportExternalRefund). Core records it as a resolved refund marked
// External, adds it to the session's refunded total, and tells the consumer.
type PaymentExternalRefund struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
	// ProviderRef is the processor's refund id: required, and what makes a repeated report a no-op.
	ProviderRef string `json:"provider_ref"`
	Reason      string `json:"reason,omitempty"`
}

// Validate refuses a report with no positive amount, no currency, no processor reference, or past a bound.
func (r PaymentExternalRefund) Validate() error {
	if err := validAmount(r.AmountMinor, strings.ToUpper(strings.TrimSpace(r.Currency))); err != nil {
		return err
	}
	if strings.TrimSpace(r.ProviderRef) == "" {
		return fmt.Errorf("nilda: an external refund needs the processor's refund id as provider_ref")
	}
	if utf8.RuneCountInString(r.Reason) > maxPaymentText {
		return fmt.Errorf("nilda: a refund reason is at most %d characters", maxPaymentText)
	}
	return validProviderRef(r.ProviderRef)
}

// ---- the pull path ----------------------------------------------------------------------------------------

// PaymentSyncRequest is what HookPaymentSync carries to a gateway.
type PaymentSyncRequest struct {
	Session PaymentSession `json:"session"`
}

// PaymentSyncer is what a gateway implements to answer payment.sync: look the session up at the processor and
// report what you find with the ordinary calls (Resolve, Reject, MarkPending — the same report twice is a no-op).
// Core asks when a payer is back on the return page before the webhook, and in a sweep of payments nothing has
// decided for a while, so a webhook the processor gave up on is not a payment lost. OPTIONAL: declare
// SupportSync on your methods when you implement it. An error means "could not look now"; Core asks again later
// and does not count it against the plugin.
type PaymentSyncer interface {
	SyncPayment(ctx context.Context, s PaymentSession) error
}

// ---- a gateway's own secrets ------------------------------------------------------------------------------

// The bounds on a gateway secret (Payments.SetSecret): a name of lowercase letters, digits and underscores, and a
// value Core encrypts at rest.
const maxSecretValue = 4096

var secretName = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

func validSecret(name, value string) error {
	if !secretName.MatchString(name) {
		return fmt.Errorf("nilda: a secret's name is 1–64 lowercase letters, digits or underscores, got %q", name)
	}
	if value == "" || len(value) > maxSecretValue {
		return fmt.Errorf("nilda: a secret's value is 1–%d bytes", maxSecretValue)
	}
	return nil
}
