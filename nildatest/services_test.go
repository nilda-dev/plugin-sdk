package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func useQuoteContract(t *testing.T, budgetMS int, fallback string) {
	t.Helper()
	c, err := nilda.ParseContract([]byte(`{"name": "test.rates/v1", "description": "Rates.", "methods": [
		{"name": "quote", "answered_by": "provider", "description": "A price.", "budget_ms": ` + itoa(budgetMS) + `,
		 "retry_class": "failure", "fallback": "` + fallback + `", "idempotent": true, "agent": "read",
		 "input": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
		 "output": {"type": "object", "properties": {"price": {"type": "integer"}}, "required": ["price"]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nilda.RegisterContractForTest(c))
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

type carrier struct{ wait time.Duration }

func (c carrier) ServiceMethods() []nilda.ServiceMethod {
	type in struct {
		N int `json:"n"`
	}
	return []nilda.ServiceMethod{nilda.Serves("test.rates/v1", "quote", func(ctx context.Context, q in) (map[string]int, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.wait):
		}
		return map[string]int{"price": q.N * 10}, nil
	})}
}

// A consumer and a provider, with the desk between them and no Core running.
func TestServeServiceAnswersThroughTheFakeHost(t *testing.T) {
	useQuoteContract(t, 2000, "skip")
	desk := NewServices()
	desk.Provider("dhl", carrier{})
	core, _, srv := desk.Core("shop", "test.rates/v1")
	defer srv.Close()
	res, err := core.API().Services().Call(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 3},
		nilda.ServiceCallOptions{IdempotencyKey: "order-1-rates"})
	if err != nil || len(res.Answers) != 1 || string(res.Answers[0].Output) != `{"price":30}` {
		t.Fatalf("result = %+v, err %v", res, err)
	}
	calls := desk.Calls()
	if len(calls) != 1 || calls[0].Caller != "shop" || calls[0].IdempotencyKey != "order-1-rates" {
		t.Fatalf("the provider was asked %+v", calls)
	}

	stranger, _, srv2 := desk.Core("blog")
	defer srv2.Close()
	if _, err := stranger.API().Services().Call(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1},
		nilda.ServiceCallOptions{}); err == nil || !strings.Contains(err.Error(), "does not require or use") {
		t.Fatalf("a plugin that did not declare the contract was answered: %v", err)
	}
}

// A provider that runs out of the method's budget is a Miss in the result, not the call failing: the shop shows the
// rates it has.
func TestADeadlineIsAResultNotAnError(t *testing.T) {
	useQuoteContract(t, 100, "skip")
	desk := NewServices()
	desk.Provider("dhl", carrier{})
	desk.Provider("slow", carrier{wait: time.Second})
	res, err := desk.Ask(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1}, nilda.ServiceCallOptions{})
	if err != nil {
		t.Fatalf("a slow provider failed the call: %v", err)
	}
	if len(res.Answers) != 1 || res.Answers[0].Provider != "dhl" || len(res.Missed) != 1 || res.Missed[0].Reason != "timeout" {
		t.Fatalf("result = %+v", res)
	}

	useQuoteContract(t, 100, "block")
	only := NewServices()
	only.Provider("slow", carrier{wait: time.Second})
	if _, err := only.Ask(context.Background(), "test.rates/v1", "quote", map[string]int{"n": 1}, nilda.ServiceCallOptions{}); !errors.Is(err, ErrServiceBlocked) {
		t.Fatalf("a blocking method with nothing answered: %v", err)
	}
}
