package nilda

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
)

// SERVICE CONTRACTS, AS DATA (Core's plugin communication plan, PC3; owner decision D9, 2026-10-12).
//
// A contract is what one kind of plugin promises another through Core — a payment gateway to the shop, a carrier to
// the shop, the shop's catalogue to a feed — written as a file under contracts/<name>/v<major>/contract.json: its
// methods, who answers each, how long one may take, what an error from it means, what happens when it fails, how the
// site's assistant may use it, and the JSON Schema (2020-12) of each method's input and answer.
//
// The file is the published copy: Core embeds it from the SDK version it builds against and checks every request
// and every answer against it (D9 — the check sits in Core, so a plugin not written in Go, or a hostile one, cannot
// skip it), a plugin in another language reads the same schema, and a Go plugin gets the same check here. Its
// schemas are generated from this module's Go types, and a test fails the moment the two differ.
//
// A contract's name carries its major version ("payments/v1"); within it, changes are additive only.

//go:embed contracts
var contractFiles embed.FS

// ServiceContract is one contract file.
type ServiceContract struct {
	// Name is the contract and its major version, e.g. "shipping.carrier/v1" — also its directory under contracts/.
	Name string `json:"name"`
	// Description says what the contract is for, in a sentence an owner and a model can both read.
	Description string `json:"description"`
	// Methods are its operations, in the file's order.
	Methods []ContractMethod `json:"methods"`
}

// ContractMethod is one operation of a contract.
type ContractMethod struct {
	// Name is the method's name within the contract, e.g. "rates".
	Name string `json:"name"`
	// Hook is the hook name the method already travels as, for a contract that describes hooks Core sends itself
	// (payments). Empty for a method Core's service desk relays, which travels as "svc:<contract>.<method>".
	Hook string `json:"hook,omitempty"`
	// AnsweredBy is who answers it: "provider" (the plugin that provides the contract) or "consumer" (the plugin
	// that requires it, told something by Core).
	AnsweredBy string `json:"answered_by"`
	// Description says what the method does.
	Description string `json:"description"`
	// BudgetMS is the least time one call may take, in milliseconds; 0 is the ordinary per-call budget (5 seconds
	// by default). At most 15000.
	BudgetMS int `json:"budget_ms,omitempty"`
	// RetryClass says what an error from the plugin's handler is: "failure" (the plugin failing — it counts toward
	// switching the plugin off) or "answer" (the method's documented way to say "not now" or "no").
	RetryClass string `json:"retry_class"`
	// Fallback is what Core's service desk does when a provider fails or runs out of time: "skip" (answer without
	// it), "block" (the call fails) or "cached" (the provider's last good answer). Required for a relayed method;
	// empty for one that rides a hook of Core's own, whose owner handles failure itself.
	Fallback string `json:"fallback,omitempty"`
	// Idempotent says a call repeated with the same input — or the same idempotency key — has the effect of one.
	Idempotent bool `json:"idempotent"`
	// Agent is how the site's assistant may use the method: "read", "write" or "destructive" makes it a tool of
	// that risk class; "none" keeps it from the assistant and needs AgentReason (nothing without a door, D8).
	Agent string `json:"agent"`
	// AgentReason says why a method is not a tool — which door the owner has instead.
	AgentReason string `json:"agent_reason,omitempty"`
	// Input and Output are the JSON Schemas (2020-12) of the method's request and answer, each an object.
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`

	in, out *jsonschema.Resolved
}

// The values each field may take.
var (
	contractNameRe   = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*/v[1-9][0-9]*$`)
	contractMethodRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)
	answeredBy       = map[string]bool{"provider": true, "consumer": true}
	retryClasses     = map[string]bool{"failure": true, "answer": true}
	fallbacks        = map[string]bool{"skip": true, "block": true, "cached": true}
	agentClasses     = map[string]bool{"none": true, "read": true, "write": true, "destructive": true}
)

// maxContractBudgetMS is the longest a method may ask for: a processor's or a carrier's API, as payments has.
const maxContractBudgetMS = 15000

var (
	contractsOnce sync.Once
	contractsAll  []ServiceContract
	contractsErr  error
)

// Contracts returns every service contract this SDK version defines, each checked and its schemas compiled, sorted
// by name. An error names the file and what is wrong with it; a released SDK never has one.
func Contracts() ([]ServiceContract, error) {
	contractsOnce.Do(func() { contractsAll, contractsErr = loadContracts(contractFiles) })
	return contractsAll, contractsErr
}

// ContractNamed returns one contract by its name, e.g. "payments/v1".
func ContractNamed(name string) (ServiceContract, bool) {
	all, err := Contracts()
	if err != nil {
		return ServiceContract{}, false
	}
	for _, c := range all {
		if c.Name == name {
			return c, true
		}
	}
	return ServiceContract{}, false
}

// Method returns one of the contract's methods by name.
func (c ServiceContract) Method(name string) (ContractMethod, bool) {
	for _, m := range c.Methods {
		if m.Name == name {
			return m, true
		}
	}
	return ContractMethod{}, false
}

// ValidateInput checks a request for the method against its input schema.
func (m ContractMethod) ValidateInput(raw []byte) error {
	return validateAgainst(m.in, raw, m.Name, "input")
}

// ValidateOutput checks an answer from the method against its output schema.
func (m ContractMethod) ValidateOutput(raw []byte) error {
	return validateAgainst(m.out, raw, m.Name, "answer")
}

func validateAgainst(r *jsonschema.Resolved, raw []byte, method, what string) error {
	if r == nil {
		return fmt.Errorf("nilda: the %s of %s has no compiled schema — read the method from Contracts()", what, method)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("nilda: the %s of %s is not JSON: %w", what, method, err)
	}
	if err := r.Validate(v); err != nil {
		return fmt.Errorf("nilda: the %s of %s does not match its contract: %w", what, method, err)
	}
	return nil
}

// loadContracts reads and checks every contracts/**/contract.json in fsys.
func loadContracts(fsys fs.FS) ([]ServiceContract, error) {
	var out []ServiceContract
	err := fs.WalkDir(fsys, "contracts", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) != "contract.json" {
			return err
		}
		raw, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		c, err := parseContract(raw)
		if err != nil {
			return fmt.Errorf("nilda: %s: %w", p, err)
		}
		if want := strings.TrimSuffix(strings.TrimPrefix(p, "contracts/"), "/contract.json"); c.Name != want {
			return fmt.Errorf("nilda: %s names itself %q, and its directory says %q", p, c.Name, want)
		}
		out = append(out, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// parseContract reads one contract file and checks every rule a contract must keep.
func parseContract(raw []byte) (ServiceContract, error) {
	var c ServiceContract
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	if !contractNameRe.MatchString(c.Name) {
		return c, fmt.Errorf("the name %q is not <name>/v<major>, e.g. shipping.carrier/v1", c.Name)
	}
	if strings.TrimSpace(c.Description) == "" {
		return c, fmt.Errorf("%s has no description", c.Name)
	}
	if len(c.Methods) == 0 {
		return c, fmt.Errorf("%s has no methods", c.Name)
	}
	seen := map[string]bool{}
	for i := range c.Methods {
		m := &c.Methods[i]
		if err := checkMethod(m); err != nil {
			return c, fmt.Errorf("%s method %q: %w", c.Name, m.Name, err)
		}
		if seen[m.Name] {
			return c, fmt.Errorf("%s declares the method %q twice", c.Name, m.Name)
		}
		seen[m.Name] = true
	}
	return c, nil
}

func checkMethod(m *ContractMethod) error {
	switch {
	case !contractMethodRe.MatchString(m.Name):
		return fmt.Errorf("the name is not lower-case words joined by dots or underscores")
	case strings.TrimSpace(m.Description) == "":
		return fmt.Errorf("no description")
	case !answeredBy[m.AnsweredBy]:
		return fmt.Errorf("answered_by %q is not provider or consumer", m.AnsweredBy)
	case m.BudgetMS < 0 || m.BudgetMS > maxContractBudgetMS:
		return fmt.Errorf("budget_ms %d is not between 0 (the ordinary budget) and %d", m.BudgetMS, maxContractBudgetMS)
	case !retryClasses[m.RetryClass]:
		return fmt.Errorf("retry_class %q is not failure or answer", m.RetryClass)
	case m.Hook == "" && !fallbacks[m.Fallback]:
		return fmt.Errorf("a relayed method needs fallback skip, block or cached; it has %q", m.Fallback)
	case m.Hook != "" && m.Fallback != "":
		return fmt.Errorf("it rides the hook %s, whose owner handles failure — a fallback here would say what nothing does", m.Hook)
	case !agentClasses[m.Agent]:
		return fmt.Errorf("agent %q is not none, read, write or destructive — every method says how the assistant may use it", m.Agent)
	case m.Agent == "none" && strings.TrimSpace(m.AgentReason) == "":
		return fmt.Errorf("agent is none and agent_reason does not say which door the owner has instead")
	case m.Agent != "none" && m.AgentReason != "":
		return fmt.Errorf("agent_reason is for a method kept from the assistant; this one is a %s tool", m.Agent)
	}
	var err error
	if m.in, err = compileObjectSchema(m.Input); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if m.out, err = compileObjectSchema(m.Output); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	return nil
}

// compileObjectSchema compiles a method's schema; each must describe a JSON object.
func compileObjectSchema(raw json.RawMessage) (*jsonschema.Resolved, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("no schema")
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	if s.Type != "object" {
		return nil, fmt.Errorf("the schema's type is %q, and every request and answer is a JSON object", s.Type)
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, err
	}
	if closesAnObject(tree) {
		return nil, fmt.Errorf("it closes an object (additionalProperties: false): within a major version a " +
			"contract only grows, so a field added on one side must not be refused by the other")
	}
	return s.Resolve(nil)
}

// closesAnObject reports whether a schema tree anywhere says "additionalProperties": false.
func closesAnObject(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if b, ok := t["additionalProperties"].(bool); ok && !b {
			return true
		}
		for _, c := range t {
			if closesAnObject(c) {
				return true
			}
		}
	case []any:
		for _, c := range t {
			if closesAnObject(c) {
				return true
			}
		}
	}
	return false
}
