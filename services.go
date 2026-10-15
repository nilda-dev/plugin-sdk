package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// THE SERVICE DESK, FROM A PLUGIN (docs/SERVICES.md; Core's internal/servicedesk).
//
// One plugin asks another through Core in a contract this module defines as data (contracts.go): the shop asks every
// carrier for rates, a feed asks the shop for its catalogue. The two plugins never meet. The CONSUMER — declared
// `requires` or `uses` in its manifest, with `service_consumer` — calls with Services.Call. The PROVIDER — declared
// `provides`, with `service_provider` — implements ServiceProvider, and Serve hands it each call already checked
// against the contract; Core subscribes it from the manifest, so it lists no hook.
//
// Both sides check against the same file Core checks against: a request before it leaves, an answer before it is
// used. Core checks too, and is the check a plugin in another language or a hostile one cannot skip — these are so a
// Go author learns of a mistake in a test, not from a shopper.

// ServiceHookPrefix starts the hook a relayed method travels to its provider as: "svc:shipping.carrier/v1.rates".
const ServiceHookPrefix = "svc:"

// ServiceHook is the hook name one relayed method travels as.
func ServiceHook(contract, method string) string { return ServiceHookPrefix + contract + "." + method }

// SplitServiceHook splits a service hook into its contract and method. A contract's name ends in "/v<major>", so the
// first dot after that is where the method starts.
func SplitServiceHook(hook string) (contract, method string, ok bool) {
	rest, ok := strings.CutPrefix(hook, ServiceHookPrefix)
	if !ok {
		return "", "", false
	}
	slash := strings.LastIndex(rest, "/v")
	if slash < 0 {
		return "", "", false
	}
	dot := strings.IndexByte(rest[slash:], '.')
	if dot < 0 {
		return "", "", false
	}
	contract, method = rest[:slash+dot], rest[slash+dot+1:]
	return contract, method, contract != "" && method != ""
}

// ContractToolName is the name of the site assistant's tool for a relayed method its contract marks for it (agent:
// read, write or destructive): the contract with its major and the method — "shipping.carrier.v1.rates". MCP tool
// names have no "/", and a name with two dots or more can never be a plugin ability's "<plugin>.<ability>". The name
// is published the day a contract ships, so it is the contract's, written here once for Core and its guards.
func ContractToolName(contract, method string) string {
	return strings.Replace(contract, "/", ".", 1) + "." + method
}

// AssistantTools are the tool names a contract gives the site's assistant: one per relayed method not marked "none".
func (c ServiceContract) AssistantTools() []string {
	var out []string
	for _, m := range c.Methods {
		if m.Hook == "" && m.Agent != "none" {
			out = append(out, ContractToolName(c.Name, m.Name))
		}
	}
	return out
}

// ---- the consumer's side ---------------------------------------------------------------------------------

// Services is the desk's client, from API.Services. Every call needs the contract in the manifest's `requires` or
// `uses`: the token carries a use:<contract> scope for each, and Core refuses any other with a 403.
type Services struct{ api *API }

// Services is the desk's client. Nil-safe like the rest of API: on a plugin with no API access every call says so.
func (a *API) Services() *Services { return &Services{api: a} }

// ServiceCallOptions are a call's optional parts.
type ServiceCallOptions struct {
	// Provider asks one provider, by its plugin key; empty asks every active provider at once.
	Provider string
	// IdempotencyKey makes a repeated call one call: Core answers a repeat within 24 hours from the first, and hands
	// the key to each provider (CallOf) for an effect outside Core — a label bought — to happen once.
	IdempotencyKey string
}

// ServiceAnswer is one provider's answer, checked against the method's output schema.
type ServiceAnswer struct {
	Provider string          `json:"provider"`
	Output   json.RawMessage `json:"output"`
	// Cached marks the provider's last good answer to the same request, given because it did not answer now and the
	// method's fallback is "cached".
	Cached bool `json:"cached,omitempty"`
}

// Decode reads the answer into out.
func (a ServiceAnswer) Decode(out any) error { return json.Unmarshal(a.Output, out) }

// ServiceMiss is a provider asked that gave no usable answer, and why: "timeout", "error", "invalid_answer",
// "circuit_open", "busy" or "unavailable".
type ServiceMiss struct {
	Provider string `json:"provider"`
	Reason   string `json:"reason"`
}

// ServiceResult is the desk's answer. A provider that failed is a Miss, never an error: the others' answers stand,
// and a deadline is a result — the shop shows the rates it has.
type ServiceResult struct {
	Contract string          `json:"contract"`
	Method   string          `json:"method"`
	Answers  []ServiceAnswer `json:"answers"`
	Missed   []ServiceMiss   `json:"missed,omitempty"`
	// Fallback names the method's fallback when it decided the answer: nothing answered, and "skip" answered without
	// them or "cached" gave last answers.
	Fallback  string `json:"fallback,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// Call asks contract's method of every active provider (or the one opts names). input is checked against the
// method's input schema before it leaves, and each answer against its output schema before it is returned — an
// answer that fails is moved to Missed as "invalid_answer". An error is a request the contract refuses, a contract
// this plugin did not declare, or — for a method whose fallback is "block" — no answer at all.
func (s *Services) Call(ctx context.Context, contract, method string, input any, opts ServiceCallOptions) (ServiceResult, error) {
	m, err := relayedMethodOf(contract, method)
	if err != nil {
		return ServiceResult{}, err
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return ServiceResult{}, fmt.Errorf("nilda: the input of %s %s does not encode: %w", contract, method, err)
	}
	if err := m.ValidateInput(raw); err != nil {
		return ServiceResult{}, err
	}
	path, err := servicePath(contract, method)
	if err != nil {
		return ServiceResult{}, err
	}
	body := map[string]any{"input": json.RawMessage(raw)}
	if opts.Provider != "" {
		body["provider"] = opts.Provider
	}
	api := s.api
	if opts.IdempotencyKey != "" {
		if err := validIdempotencyKey(opts.IdempotencyKey); err != nil {
			return ServiceResult{}, err
		}
		api = api.WithIdempotencyKey(opts.IdempotencyKey)
	}
	var out struct {
		Data ServiceResult `json:"data"`
	}
	if err := api.Post(ctx, path, body, &out); err != nil {
		return ServiceResult{}, err
	}
	res := out.Data
	kept := make([]ServiceAnswer, 0, len(res.Answers))
	for _, a := range res.Answers {
		if m.ValidateOutput(a.Output) != nil {
			res.Missed = append(res.Missed, ServiceMiss{Provider: a.Provider, Reason: "invalid_answer"})
			continue
		}
		kept = append(kept, a)
	}
	res.Answers = kept
	return res, nil
}

// servicePath is the route of one method: /services/<name>/v<major>/<method>, each part escaped.
func servicePath(contract, method string) (string, error) {
	name, major, ok := strings.Cut(contract, "/")
	if !ok || name == "" || major == "" {
		return "", fmt.Errorf("nilda: %q is not a contract name with its major, e.g. shipping.carrier/v1", contract)
	}
	return "/services/" + url.PathEscape(name) + "/" + url.PathEscape(major) + "/" + url.PathEscape(method), nil
}

// relayedMethodOf is a method Core relays, from this module's contract files.
func relayedMethodOf(contract, method string) (ContractMethod, error) {
	c, ok := ContractNamed(contract)
	if !ok {
		return ContractMethod{}, fmt.Errorf("nilda: this SDK has no contract %s — a newer plugin-sdk may", contract)
	}
	m, ok := c.Method(method)
	if !ok {
		return ContractMethod{}, fmt.Errorf("nilda: %s has no method %s", contract, method)
	}
	if m.Hook != "" {
		return ContractMethod{}, fmt.Errorf("nilda: %s %s rides Core's own hook %s; it is not called through the service desk", contract, method, m.Hook)
	}
	return m, nil
}

// ---- the provider's side ---------------------------------------------------------------------------------

// ServiceProvider is a plugin that answers service contracts. Implement it and declare each contract in the
// manifest's `provides` (with `service_provider`): Serve routes each call to the method's handler, the request
// checked against the contract first and the answer after — an answer the contract refuses is returned as an error,
// so the provider's own test fails on it before Core drops it.
type ServiceProvider interface {
	ServiceMethods() []ServiceMethod
}

// ServiceMethod is one method a provider answers.
type ServiceMethod struct {
	Contract string // "shipping.carrier/v1"
	Method   string // "rates"
	// Handle answers one call. CallOf(ctx) says who asked, with which idempotency key. An error is the provider
	// failing, or saying no where the method's retry class is "answer".
	Handle func(ctx context.Context, input json.RawMessage) (any, error)
}

// Serves is a ServiceMethod with typed input and output.
func Serves[In, Out any](contract, method string, fn func(ctx context.Context, in In) (Out, error)) ServiceMethod {
	return ServiceMethod{Contract: contract, Method: method, Handle: func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in In
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("nilda: the request of %s %s does not decode: %w", contract, method, err)
		}
		return fn(ctx, in)
	}}
}

// serviceProviderOf is what answers a plugin's service hooks: its own ServiceMethods, or — for a plugin that
// implements Carrier or CatalogProvider and wrote no ServiceMethods — their methods (CarrierMethods, CatalogMethods),
// so a provider that forgot the one line is still answered rather than failing every call. A plugin may be both.
func serviceProviderOf(h Handler) ServiceProvider {
	if sp, is := h.(ServiceProvider); is {
		return sp
	}
	var methods []ServiceMethod
	if c, is := h.(Carrier); is {
		methods = append(methods, CarrierMethods(c)...)
	}
	if c, is := h.(CatalogProvider); is {
		methods = append(methods, CatalogMethods(c)...)
	}
	if methods == nil {
		return nil
	}
	return methodsProvider(methods)
}

// methodsProvider answers from a fixed list of methods.
type methodsProvider []ServiceMethod

func (m methodsProvider) ServiceMethods() []ServiceMethod { return m }

// DispatchServiceHook answers one service hook from a provider's methods. ok is false for a hook that is not a
// service hook, or names a method p does not answer — it then reaches the plugin's own HandleHook.
func DispatchServiceHook(ctx context.Context, p ServiceProvider, hook string, payload []byte) (out []byte, ok bool, err error) {
	contract, method, isService := SplitServiceHook(hook)
	if !isService {
		return nil, false, nil
	}
	for _, sm := range p.ServiceMethods() {
		if sm.Contract != contract || sm.Method != method || sm.Handle == nil {
			continue
		}
		m, err := relayedMethodOf(contract, method)
		if err != nil {
			return nil, true, err
		}
		if err := m.ValidateInput(payload); err != nil {
			return nil, true, err
		}
		answer, err := sm.Handle(ctx, payload)
		if err != nil {
			return nil, true, err
		}
		raw, err := json.Marshal(answer)
		if err != nil {
			return nil, true, fmt.Errorf("nilda: the answer of %s %s does not encode: %w", contract, method, err)
		}
		if err := m.ValidateOutput(raw); err != nil {
			return nil, true, err
		}
		return raw, true, nil
	}
	return nil, false, nil
}
