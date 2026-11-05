package nilda

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func riskScore(n int) *int { return &n }

// The verdict a gateway reports reaches Core byte for byte: the level, the score and the processor's reason, on the
// resolution and on the hold — what the shop holds an order on is what the processor said, not a copy of it.
func TestRiskRidesTheResolutionUnchanged(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, r.URL.Path+" "+string(b))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"id":"s1","status":"resolved","risk":{"level":"elevated","score":72,"reason":"elevated_risk_level"}}}`)
	}))
	defer srv.Close()
	pay := newAPI(srv.URL, "tok", nil).Payments()
	ctx := context.Background()
	risk := &PaymentRisk{Level: RiskElevated, Score: riskScore(72), Reason: "elevated_risk_level"}
	s, err := pay.Resolve(ctx, "s1", PaymentResolution{AmountMinor: 1200, Currency: "EUR", ProviderRef: "pi_1", Risk: risk})
	if err != nil {
		t.Fatal(err)
	}
	if s.Risk == nil || s.Risk.Level != RiskElevated || s.Risk.Score == nil || *s.Risk.Score != 72 || s.Risk.Reason != "elevated_risk_level" {
		t.Errorf("the session read back carries risk %+v", s.Risk)
	}
	if _, err := pay.Authorize(ctx, "s2", PaymentAuthorization{AmountMinor: 1200, Currency: "EUR",
		Risk: &PaymentRisk{Level: RiskNormal, Score: riskScore(0)}}); err != nil {
		t.Fatal(err)
	}
	// A not-assessed payment is a verdict too, and goes as one: no score, never dropped as "nothing".
	if _, err := pay.Resolve(ctx, "s3", PaymentResolution{AmountMinor: 900, Currency: "EUR",
		Risk: &PaymentRisk{Level: RiskNotAssessed}}); err != nil {
		t.Fatal(err)
	}
	// A gateway that does not say sends no risk at all.
	if _, err := pay.Resolve(ctx, "s4", PaymentResolution{AmountMinor: 900, Currency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`/payments/sessions/s1/resolve {"amount_minor":1200,"currency":"EUR","provider_ref":"pi_1","risk":{"level":"elevated","score":72,"reason":"elevated_risk_level"}}`,
		// A score of 0 is the least risky payment, not an absent score.
		`/payments/sessions/s2/authorize {"amount_minor":1200,"currency":"EUR","risk":{"level":"normal","score":0}}`,
		`/payments/sessions/s3/resolve {"amount_minor":900,"currency":"EUR","risk":{"level":"not_assessed"}}`,
		`/payments/sessions/s4/resolve {"amount_minor":900,"currency":"EUR"}`,
	}
	if len(bodies) != len(want) {
		t.Fatalf("sent %d reports, want %d: %v", len(bodies), len(want), bodies)
	}
	for i := range want {
		if strings.TrimSpace(bodies[i]) != want[i] {
			t.Errorf("report %d went as\n  %s\nwant\n  %s", i+1, bodies[i], want[i])
		}
	}
	var back PaymentSession
	if err := json.Unmarshal([]byte(`{"risk":{"level":"highest","score":91,"reason":"rule"}}`), &back); err != nil ||
		back.Risk == nil || back.Risk.Level != RiskHighest || *back.Risk.Score != 91 || back.Risk.Reason != "rule" {
		t.Errorf("a session's risk did not decode: %+v %v", back.Risk, err)
	}
}

// A level the contract does not know is refused before it leaves the gateway — a typo'd "elevate" would otherwise read
// as no risk at all on an order the shop should have held — and so is a score outside Stripe's 0 to 100, a score on a
// payment nobody assessed, and a reason that is not one plain line.
func TestAnUnknownRiskLevelIsRefused(t *testing.T) {
	sent := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{}}`)
	}))
	defer srv.Close()
	pay := newAPI(srv.URL, "tok", nil).Payments()
	ctx := context.Background()
	for name, r := range map[string]PaymentRisk{
		"a level outside the five":       {Level: "elevate"},
		"an upper-case level":            {Level: "ELEVATED"},
		"no level":                       {Score: riskScore(40)},
		"a score past 100":               {Level: RiskHighest, Score: riskScore(101)},
		"a negative score":               {Level: RiskNormal, Score: riskScore(-1)},
		"a score nobody assessed":        {Level: RiskNotAssessed, Score: riskScore(10)},
		"a score of a failed assessment": {Level: RiskUnknown, Score: riskScore(10)},
		"a reason of two lines":          {Level: RiskElevated, Reason: "elevated\nforged log line"},
		"a reason turned around":         {Level: RiskElevated, Reason: "rule ‮gnp.exe"},
		"a reason past its bound":        {Level: RiskElevated, Reason: strings.Repeat("r", maxRiskReason+1)},
	} {
		if err := r.Validate(); err == nil {
			t.Errorf("%s: PaymentRisk.Validate accepted %+v", name, r)
		}
		risk := r
		if err := (PaymentResolution{AmountMinor: 1, Currency: "EUR", Risk: &risk}).Validate(); err == nil {
			t.Errorf("%s: a resolution carrying it passed", name)
		}
		if err := (PaymentAuthorization{AmountMinor: 1, Currency: "EUR", Risk: &risk}).Validate(); err == nil {
			t.Errorf("%s: a hold carrying it passed", name)
		}
		if _, err := pay.Resolve(ctx, "s1", PaymentResolution{AmountMinor: 1, Currency: "EUR", Risk: &risk}); err == nil {
			t.Errorf("%s: the client sent it", name)
		}
	}
	if sent != 0 {
		t.Errorf("the client sent %d refused report(s) to Core", sent)
	}
	for _, level := range []string{RiskNormal, RiskElevated, RiskHighest, RiskNotAssessed, RiskUnknown} {
		if err := (PaymentRisk{Level: level, Reason: "elevated_risk_level"}).Validate(); err != nil {
			t.Errorf("level %q was refused: %v", level, err)
		}
	}
	for _, score := range []int{0, 65, 100} {
		if err := (PaymentRisk{Level: RiskElevated, Score: riskScore(score)}).Validate(); err != nil {
			t.Errorf("score %d was refused: %v", score, err)
		}
	}
}
