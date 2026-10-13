package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// testQuoteContract stands up a relayed contract no released SDK carries: "quote" takes {"n": integer} and answers
// {"price": integer}.
func testQuoteContract(t *testing.T, name string) {
	t.Helper()
	c, err := ParseContract([]byte(`{"name": "` + name + `", "description": "Quotes.", "methods": [
		{"name": "quote", "answered_by": "provider", "description": "A price.", "budget_ms": 2000, "retry_class": "failure",
		 "fallback": "skip", "idempotent": true, "agent": "read",
		 "input": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]},
		 "output": {"type": "object", "properties": {"price": {"type": "integer"}}, "required": ["price"]}}]}`))
	if err != nil {
		t.Fatalf("the test contract does not parse: %v", err)
	}
	t.Cleanup(RegisterContractForTest(c))
}

func TestAServiceHookSplitsIntoItsContractAndMethod(t *testing.T) {
	for _, c := range []struct{ hook, contract, method string }{
		{"svc:shipping.carrier/v1.rates", "shipping.carrier/v1", "rates"},
		{"svc:payments/v12.refund.lookup", "payments/v12", "refund.lookup"},
	} {
		if got, m, ok := SplitServiceHook(c.hook); !ok || got != c.contract || m != c.method {
			t.Errorf("SplitServiceHook(%q) = %q %q %v", c.hook, got, m, ok)
		}
		if ServiceHook(c.contract, c.method) != c.hook {
			t.Errorf("ServiceHook(%q, %q) = %q", c.contract, c.method, ServiceHook(c.contract, c.method))
		}
	}
	for _, hook := range []string{"svc:", "svc:shipping", "svc:shipping.carrier/v1", "svc:shipping.carrier/v1.", "rates"} {
		if _, _, ok := SplitServiceHook(hook); ok {
			t.Errorf("%q split", hook)
		}
	}
}

// A relayed method is answered by its provider: a contract saying otherwise describes a call Core does not make.
func TestARelayedMethodIsAnsweredByItsProvider(t *testing.T) {
	_, err := ParseContract([]byte(`{"name": "test.tell/v1", "description": "x", "methods": [
		{"name": "told", "answered_by": "consumer", "description": "x", "retry_class": "failure", "fallback": "skip",
		 "idempotent": true, "agent": "none", "agent_reason": "x", "input": {"type": "object"}, "output": {"type": "object"}}]}`))
	if err == nil || !strings.Contains(err.Error(), "answered by the provider") {
		t.Fatalf("a relayed method a consumer answers was accepted: %v", err)
	}
}

type quoteProvider struct {
	answer any
	asked  CallInfo
}

func (p *quoteProvider) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (p *quoteProvider) HandleHook(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("reached the author's HandleHook")
}
func (p *quoteProvider) HandleEvent(context.Context, string, []byte) error { return nil }
func (p *quoteProvider) ServiceMethods() []ServiceMethod {
	type in struct {
		N int `json:"n"`
	}
	return []ServiceMethod{Serves("test.quote/v1", "quote", func(ctx context.Context, q in) (any, error) {
		p.asked = CallOf(ctx)
		if p.answer != nil {
			return p.answer, nil
		}
		return map[string]int{"price": q.N * 100}, nil
	})}
}

// Serve hands a provider its call checked against the contract both ways, before the author's HandleHook is ever
// reached — and says who asked.
func TestServeAnswersAServiceProviderCheckedBothWays(t *testing.T) {
	testQuoteContract(t, "test.quote/v1")
	p := &quoteProvider{}
	s := &pluginServer{handler: p}
	ask := func(payload string) (*contract.HookResponse, error) {
		return s.HandleHook(context.Background(), &contract.HookRequest{Hook: ServiceHook("test.quote/v1", "quote"),
			Payload: []byte(payload), Contract: "test.quote/v1", Caller: "shop", IdempotencyKey: "k"})
	}
	res, err := ask(`{"n": 2}`)
	if err != nil || string(res.Payload) != `{"price":200}` {
		t.Fatalf("answer = %v %v", res, err)
	}
	if p.asked.Caller != "shop" || p.asked.IdempotencyKey != "k" {
		t.Errorf("the handler was not told who asked: %+v", p.asked)
	}
	if _, err := ask(`{"n": "two"}`); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("a request the contract refuses reached the handler: %v", err)
	}
	p.answer = map[string]string{"price": "free"}
	if _, err := ask(`{"n": 1}`); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("an answer the contract refuses left the provider: %v", err)
	}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: ServiceHook("test.quote/v1", "other"),
		Payload: []byte(`{}`)}); err == nil || !strings.Contains(err.Error(), "author's HandleHook") {
		t.Errorf("a method the provider does not answer did not fall through to HandleHook: %v", err)
	}
}

// The client drops an answer its contract refuses — Core already checks, and an older Core or a fake may not.
func TestCallRefusesAnAnswerTheSchemaRejects(t *testing.T) {
	testQuoteContract(t, "test.quote/v1")
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"contract": "test.quote/v1", "method": "quote",
			"answers": []any{
				map[string]any{"provider": "dhl", "output": map[string]any{"price": 5}},
				map[string]any{"provider": "ups", "output": map[string]any{"price": "free"}},
			}}})
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", []string{"service_consumer"}, nil, srv.URL, "t", []string{"use:test.quote/v1"})
	res, err := core.API().Services().Call(context.Background(), "test.quote/v1", "quote", map[string]int{"n": 1}, ServiceCallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/services/test.quote/v1/quote" {
		t.Errorf("the client called %s", path)
	}
	if len(res.Answers) != 1 || res.Answers[0].Provider != "dhl" || len(res.Missed) != 1 || res.Missed[0].Reason != "invalid_answer" {
		t.Fatalf("result = %+v", res)
	}
	var got struct{ Price int }
	if err := res.Answers[0].Decode(&got); err != nil || got.Price != 5 {
		t.Fatalf("decode = %+v %v", got, err)
	}
	if _, err := core.API().Services().Call(context.Background(), "test.quote/v1", "quote", map[string]any{"n": "x"}, ServiceCallOptions{}); err == nil {
		t.Fatal("a request the contract refuses was sent")
	}
	if _, err := core.API().Services().Call(context.Background(), "payments/v1", "describe", map[string]any{}, ServiceCallOptions{}); err == nil ||
		!strings.Contains(err.Error(), "rides Core's own hook") {
		t.Fatalf("a hooked method went to the desk: %v", err)
	}
}
