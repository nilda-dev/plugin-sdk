package nildatest

import (
	"encoding/json"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// keepRisk keeps the processor's risk verdict on a session as Core does (v0.58.0, EXPERIMENTAL, commerce plan 10.3): the
// first one a hold or a resolution reports, exactly as reported; a later report never moves it, and a report without one
// never clears it. It says whether the session changed, so a report that changed nothing else still tells the consumer.
func keepRisk(s *nilda.PaymentSession, r *nilda.PaymentRisk) bool {
	if r == nil || s.Risk != nil {
		return false
	}
	kept := *r
	if r.Score != nil {
		n := *r.Score
		kept.Score = &n
	}
	s.Risk = &kept
	s.UpdatedAt = time.Now().UTC()
	return true
}

// reportedRisk is the risk a hold or a resolution carries — both name it "risk" — already checked by checkReport.
func reportedRisk(body []byte) *nilda.PaymentRisk {
	var r struct {
		Risk *nilda.PaymentRisk `json:"risk"`
	}
	if json.Unmarshal(body, &r) != nil {
		return nil
	}
	return r.Risk
}
