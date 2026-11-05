package nilda

import (
	"fmt"
	"unicode/utf8"
)

// THE PROCESSOR'S RISK VERDICT ON A PAYMENT (v0.58.0, EXPERIMENTAL; commerce plan 10.3, step F1; docs/PAYMENTS.md §4
// "The processor's risk verdict").
//
// A processor that screens payments for fraud says how risky it found one: Stripe Radar's outcome.risk_level and
// outcome.risk_score on the charge. The gateway reports it beside the money — with its hold (PaymentAuthorization.Risk)
// or its resolution (PaymentResolution.Risk) — Core keeps it on the session (PaymentSession.Risk) and the consumer reads
// it there: the shop holds an elevated order from shipping until a person looks. Nil everywhere means the gateway did not
// say; RiskNotAssessed means the processor said it did not look. The two are different facts and never share a value.

// PaymentRisk is the processor's verdict on how likely a payment is to be fraudulent.
type PaymentRisk struct {
	// Level is one of the five Risk constants: the processor's words mapped onto Stripe Radar's (outcome.risk_level).
	Level string `json:"level"`
	// Score is the processor's score, 0 (least risky) to 100, when it gives one — Stripe's outcome.risk_score, which
	// Radar for Fraud Teams returns. Nil when the processor gave none; never beside not_assessed or unknown.
	Score *int `json:"score,omitempty"`
	// Reason is the processor's own word for its verdict — Stripe's outcome.reason ("elevated_risk_level", "rule") or a
	// rule's name — one plain line of at most 200 characters, for the owner to read. Never the payer's data.
	Reason string `json:"reason,omitempty"`
}

// The five levels (PaymentRisk.Level) — Stripe Radar's outcome.risk_level, read on docs.stripe.com/api/charges/object
// (outcome.risk_level) on 2026-11-05. Another processor maps its own verdict onto them, or reports no risk at all.
const (
	// RiskNormal: the processor assessed the payment and found nothing unusual.
	RiskNormal = "normal"
	// RiskElevated: the processor found it riskier than usual (Radar's default rule places it in review).
	RiskElevated = "elevated"
	// RiskHighest: the processor found it most likely fraudulent (Radar's default rule blocks it; one that was paid
	// anyway — a custom rule allowed it — is the one a shop most needs to see).
	RiskHighest = "highest"
	// RiskNotAssessed: the processor did not assess it — a bank debit, a wallet, a payment from before it scored.
	RiskNotAssessed = "not_assessed"
	// RiskUnknown: the processor tried and its assessment failed.
	RiskUnknown = "unknown"
)

// maxRiskScore is the top of a processor's score: Stripe's risk_score is "between 0 and 100".
const maxRiskScore = 100

// maxRiskReason is the longest reason, in characters: a processor's word for its verdict, not a paragraph.
const maxRiskReason = 200

// Validate refuses a level outside the five, a score outside 0 to 100 or beside a payment nobody assessed, and a reason
// past its bound or holding a control or direction-changing character.
func (r PaymentRisk) Validate() error {
	switch r.Level {
	case RiskNormal, RiskElevated, RiskHighest, RiskNotAssessed, RiskUnknown:
	default:
		return fmt.Errorf("nilda: a payment's risk level is normal, elevated, highest, not_assessed or unknown, got %q", r.Level)
	}
	if r.Score != nil {
		if *r.Score < 0 || *r.Score > maxRiskScore {
			return fmt.Errorf("nilda: a payment's risk score is 0 to %d, got %d", maxRiskScore, *r.Score)
		}
		if r.Level == RiskNotAssessed || r.Level == RiskUnknown {
			return fmt.Errorf("nilda: a payment whose risk is %s has no score", r.Level)
		}
	}
	if utf8.RuneCountInString(r.Reason) > maxRiskReason || !plainLine(r.Reason) {
		return fmt.Errorf("nilda: a payment's risk reason is one plain line of at most %d characters", maxRiskReason)
	}
	return nil
}

// validRisk accepts no risk at all — the gateway did not say — and a valid one.
func validRisk(r *PaymentRisk) error {
	if r == nil {
		return nil
	}
	return r.Validate()
}
