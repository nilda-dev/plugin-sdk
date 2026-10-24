package nilda

import (
	"context"
	"encoding/json"
	"testing"
)

// setup.status is answered in the shape Core reads, and an answer that cannot be true is refused at the source.
func TestSetupStatusDispatch(t *testing.T) {
	out, handled, err := DispatchSetupStatus(context.Background(), SetupStatusHook, []byte(`{}`),
		func(context.Context) (SetupStatus, error) { return SetupStatus{Done: 7, Total: 11}, nil })
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("not JSON: %s", out)
	}
	if wire["done"] != float64(7) || wire["total"] != float64(11) || wire["completed"] != false {
		t.Errorf("setup.status went on the wire as %s", out)
	}
	if _, handled, _ := DispatchSetupStatus(context.Background(), AdminReportHook, nil, nil); handled {
		t.Error("another hook was taken for setup.status")
	}
	for name, s := range map[string]SetupStatus{
		"done past total":  {Done: 3, Total: 2},
		"negative":         {Done: -1, Total: 2},
		"completed undone": {Done: 1, Total: 2, Completed: true},
		"too many steps":   {Done: 0, Total: MaxSetupSteps + 1},
	} {
		if _, _, err := DispatchSetupStatus(context.Background(), SetupStatusHook, nil,
			func(context.Context) (SetupStatus, error) { return s, nil }); err == nil {
			t.Errorf("%s: %+v was answered", name, s)
		}
	}
	// All steps done is completed whether or not the plugin said so: the onboarding row goes either way.
	out, _, err = DispatchSetupStatus(context.Background(), SetupStatusHook, nil,
		func(context.Context) (SetupStatus, error) { return SetupStatus{Done: 2, Total: 2}, nil })
	if err != nil || string(out) != `{"done":2,"total":2,"completed":true}` {
		t.Errorf("every step done answered %s (%v)", out, err)
	}
}

// A recommendation names exactly one of a capability or a contract, and a manifest holds at most eight.
func TestRecommendsNamesOneOfCapabilityOrContract(t *testing.T) {
	good := []Recommendation{
		{Capability: "payment_gateway", Why: "Take card payments"},
		{Contract: "shipping.carrier/v1", Why: "Live carrier rates"},
	}
	if err := ValidateRecommendations(good); err != nil {
		t.Fatalf("two good recommendations were refused: %v", err)
	}
	b, _ := json.Marshal(good[1])
	if string(b) != `{"contract":"shipping.carrier/v1","why":"Live carrier rates"}` {
		t.Errorf("a recommendation went on the wire as %s", b)
	}
	for name, bad := range map[string][]Recommendation{
		"neither":         {{Why: "x"}},
		"both":            {{Capability: "payment_gateway", Contract: "shipping.carrier/v1"}},
		"no major":        {{Contract: "shipping.carrier"}},
		"bad capability":  {{Capability: "Payment Gateway"}},
		"twice":           {{Capability: "payment_gateway"}, {Capability: "payment_gateway"}},
		"why too long":    {{Capability: "payment_gateway", Why: string(make([]byte, MaxRecommendationWhy+1))}},
		"more than eight": make([]Recommendation, MaxRecommendations+1),
	} {
		if name == "more than eight" {
			for i := range bad {
				bad[i] = Recommendation{Contract: "c" + string(rune('a'+i)) + ".x/v1"}
			}
		}
		if err := ValidateRecommendations(bad); err == nil {
			t.Errorf("%s: %+v was accepted", name, bad)
		}
	}
}
