package nilda

import (
	"encoding/json"
	"errors"
	"net/http"
)

// The reasons a RefundRefusal names (EXPERIMENTAL, v0.69.0).
const (
	// RefundRefusalExceedsPaid: the refund would give back more than the payment less the refunds already committed.
	RefundRefusalExceedsPaid = "exceeds_paid"
	// RefundRefusalLostChargeback: the same, beside a lost chargeback — the payer's bank already returned
	// ChargedBackMinor of it, so only RefundableMinor is still the consumer's to give back.
	RefundRefusalLostChargeback = "lost_chargeback"
)

// RefundRefusal is what Core decided, under the session's lock, when it refused a refund for being larger than the
// payment can still give back (EXPERIMENTAL, v0.69.0). Its sentence says only that the bank returned "part"; these
// numbers say how much, so a consumer that must tell its owner what is still owed to the payer never guesses.
type RefundRefusal struct {
	Reason           string `json:"reason"`
	PaidMinor        int64  `json:"paid_minor"`
	ChargedBackMinor int64  `json:"charged_back_minor"`
	RefundedMinor    int64  `json:"refunded_minor"`
	RefundableMinor  int64  `json:"refundable_minor"`
	Currency         string `json:"currency"`
}

// RefundRefusalOf reads the numbers of a refund Core refused for being too large, from the error CreateRefund
// returned. ok is false for any other error, and for a Core older than v0.69.0, whose refusal carries no numbers —
// the consumer then has nothing but the sentence and must not decide what the payer is owed from it.
func RefundRefusalOf(err error) (RefundRefusal, bool) {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusUnprocessableEntity {
		return RefundRefusal{}, false
	}
	var body struct {
		Error struct {
			Details *RefundRefusal `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(ae.Body), &body) != nil || body.Error.Details == nil {
		return RefundRefusal{}, false
	}
	r := *body.Error.Details
	if r.Reason != RefundRefusalExceedsPaid && r.Reason != RefundRefusalLostChargeback {
		return RefundRefusal{}, false
	}
	return r, true
}
