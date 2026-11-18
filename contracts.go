package nilda

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
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
	// Status is "experimental" for a contract whose shape may still change: it is published and Core relays it, but
	// it is outside the v1 no-breaking promise until its first plugin has shipped green on it (DR-24, docs/
	// WRITE_COMMANDS.md §5). Empty or "stable" is a contract inside the promise. From v0.58.0.
	Status string `json:"status,omitempty"`
	// CallerCapability is a capability EVERY call of the contract needs its caller to hold, granted by the site's owner —
	// before the request is read, before any provider is asked, whatever the method: "import" (CapImport) for
	// shop.import/v1, whose every method is the import's (a plugin that may not import may not plan, publish or undo an
	// import either). Empty for a contract any consumer naming it under `uses` may call. A method marked Import needs
	// the contract to say "import" here. From v0.70.0.
	CallerCapability string `json:"caller_capability,omitempty"`
	// Closed is a contract whose every object is CLOSED (additionalProperties: false): a member neither side declared is
	// refused, so a request cannot carry "KIND" beside "kind" — encoding/json matches a key without regard to case, and
	// would decode the one the schema never checked. Such a contract grows by a new major version, not within one. For a
	// contract that carries an outside party's data (an old shop's records). From v0.70.0.
	Closed bool `json:"closed,omitempty"`
}

// The capabilities a contract may require of every caller (ServiceContract.CallerCapability).
var contractCallerCapabilities = []string{CapImport}

// Contract statuses (ServiceContract.Status).
const (
	ContractStable       = "stable"
	ContractExperimental = "experimental"
)

// Experimental reports whether the contract is outside the v1 no-breaking promise.
func (c ServiceContract) Experimental() bool { return c.Status == ContractExperimental }

// ContractMethod is one operation of a contract.
type ContractMethod struct {
	// Name is the method's name within the contract, e.g. "rates".
	Name string `json:"name"`
	// Hook is the hook name the method already travels as, for a contract that describes hooks Core sends itself
	// (payments). Empty for a method Core's service desk relays, which travels as "svc:<contract>.<method>".
	Hook string `json:"hook,omitempty"`
	// AnsweredBy is who answers it: "provider" (the plugin that provides the contract) or, for a method riding one of
	// Core's own hooks, "consumer" (the plugin that requires it, told something by Core). A relayed method is always
	// the provider's.
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
	// Write marks a WRITE COMMAND: a method by which a plugin changes a record the provider keeps (docs/
	// WRITE_COMMANDS.md). Nil for a read or a method of one of Core's own hooks. From v0.58.0.
	Write *ContractWrite `json:"write,omitempty"`
	// Import marks an IMPORT CALL: one call of a job an import plugin runs, writing an old site's records into the
	// provider (shop.import/v1's records.put; docs/SHOP_IMPORT.md §1.1). Core's service desk relays it only for a caller
	// holding the `import` capability the owner granted — before any provider is asked; reads the job's id and planned
	// record count from the request's `import` object (ImportCall); opens the call under a marker of Core's own,
	// "plugin:<caller key>:<id>", so that what the provider writes back through Core's API while it serves is quiet (no
	// mail, webhook or notice) and counted on the job's own rate bucket, sized from the planned count; asks ONE provider
	// (the one named, or the only one); and never answers it from a cache. Core takes its request up to
	// MaxImportInputBytes. ParseContract holds it to: answered by the provider, fallback block, idempotent, agent none,
	// and an input that requires an `import` object requiring id and planned. From v0.70.0.
	Import bool `json:"import,omitempty"`

	in, out *jsonschema.Resolved
}

// ImportCall is the part every import call's request carries, under "import" (ContractMethod.Import).
type ImportCall struct {
	// ID is the import plugin's own id for the job, 1 to MaxImportCallID characters of letters, digits and . _ : -; Core
	// namespaces it with the caller's key, so two plugins' jobs never share a marker.
	ID string `json:"id" contract:"pattern=importid"`
	// Planned is how many records the whole job plans to write, 0 to MaxImportPlanned: Core sizes the job's own hourly
	// allowance of API requests from it.
	Planned int `json:"planned" contract:"min=0,max=1000000"`
}

// The bounds of an import call.
const (
	MaxImportCallID  = 200
	MaxImportPlanned = 1_000_000
	// MaxImportInputBytes is the largest request of an import call Core's service desk takes, as the wire carries it (an
	// ordinary method's is 256 KiB): one batch of an old shop, measured at 0.94 MiB decoding in 6 to 8 ms.
	MaxImportInputBytes = 1 << 20
)

// importCallID is what an import call's id may be.
var importCallID = regexp.MustCompile(contractPatterns["importid"])

// contractPatterns are the patterns a contract type's field names in its `contract` tag, which the contract file's schema is
// generated from (contracts_test.go): `contract:"len=1-300,pattern=ref"` bounds a string's characters and its pattern,
// `items=0-800` an array's length (len, pattern and enum then hold each item), `enum=|a|b` its values ("" first when
// empty is one), `min=0,max=5` a number, `props=100` a map's keys (its pattern or enum holding each value). A bound a
// tag states is checked by Core before any provider is asked, and by this module on both sides.
var contractPatterns = map[string]string{
	"currency":         `^[A-Z]{3}$`,
	"currency_or_none": `^([A-Z]{3})?$`,
	"date":             `^[0-9]{4}-[0-9]{2}-[0-9]{2}$`,
	"date_or_none":     `^([0-9]{4}-[0-9]{2}-[0-9]{2})?$`,
	"importid":         `^[A-Za-z0-9._:-]{1,200}$`,
	"importid_or_none": `^[A-Za-z0-9._:-]{0,200}$`,
	"line":             `^[^\x00-\x1F\x7F]*$`, // one line: no control character
	"locale":           `^[A-Za-z0-9-]*$`,
	"measure":          `^([0-9]{1,9}(\.[0-9]{1,6})?)?$`,
	"media":            `^[A-Za-z0-9-]*$`, // a Core id
	"money":            `^(-?[0-9]{1,15}(\.[0-9]{1,6})?)?$`,
	"percent":          `^[0-9]{1,3}(\.[0-9]{1,4})?$`,
	"rate":             `^[0-9]{1,18}(\.[0-9]{1,18})?$`,
	"rate_or_none":     `^([0-9]{1,18}(\.[0-9]{1,18})?)?$`,
	"ref":              `^[^|\x00-\x1F\x7F]*$`,   // a record's id at the old shop: one line, no "|" (the shop's key joins on it)
	"sitepath":         `^/[^\s\x00-\x1F\x7F]*$`, // a public path on the site: from its root, no space or control character
	"source":           `^(woocommerce|shopify|wix|squarespace|csv):[^|\s]+$`,
	"source_or_none":   `^((woocommerce|shopify|wix|squarespace|csv):[^|\s]+)?$`,
	"time":             `^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z)?$`, // RFC 3339 UTC, or none
}

// Validate refuses an id or a planned count outside the contract.
func (c ImportCall) Validate() error {
	switch {
	case !importCallID.MatchString(c.ID):
		return fmt.Errorf("nilda: an import call's id is 1 to %d characters of letters, digits and . _ : -", MaxImportCallID)
	case c.Planned < 0 || c.Planned > MaxImportPlanned:
		return fmt.Errorf("nilda: an import call plans 0 to %d records", MaxImportPlanned)
	}
	return nil
}

// ReadImportCall reads the import object of an import call's request and checks it — what Core's service desk does
// with an Import method's request once its schema passed, and what a provider may do to learn the job it serves.
func ReadImportCall(input []byte) (ImportCall, error) {
	var in struct {
		Import *ImportCall `json:"import"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return ImportCall{}, fmt.Errorf("nilda: an import call's request does not decode: %w", err)
	}
	if in.Import == nil {
		return ImportCall{}, errors.New("nilda: an import call's request carries an import object {id, planned}")
	}
	return *in.Import, in.Import.Validate()
}

// ContractWrite is what a write command promises beyond an ordinary method (docs/WRITE_COMMANDS.md §2):
//
//   - it is idempotent by the call's idempotency key (ServiceCallOptions.IdempotencyKey, CallOf on the provider's
//     side), which the provider keeps under a unique index — a second call with the key answers the first one's
//     result with already: true, for as long as the record exists, not Core's 24-hour window;
//   - it never runs without a key (the provider refuses), and its fallback is "block": a write is never skipped;
//   - its answer names the record (record_id) and says whether this call made it (already);
//   - it can be taken back by its Undo method, which takes the record_id, and is itself a write command;
//   - the provider keeps which plugin wrote it (CallOf(ctx).Caller) on the record, and announces it with Emits.
type ContractWrite struct {
	// Record is the kind of record written, e.g. "order.attribute".
	Record string `json:"record"`
	// Undo names the contract's method that takes this write back. Required, except on an undo method itself.
	Undo string `json:"undo,omitempty"`
	// Emits is the event type the provider emits once the write is committed, e.g. "shop.attribute.set".
	Emits string `json:"emits"`
}

// WriteAnswer is the part every write command's answer carries (ContractWrite).
type WriteAnswer struct {
	// RecordID is the record written, the one the undo method takes.
	RecordID string `json:"record_id"`
	// Already is true when the idempotency key was used before: nothing was written now, and RecordID is the first call's.
	Already bool `json:"already"`
}

// UndoInput is the part every undo method's request carries.
type UndoInput struct {
	RecordID string `json:"record_id"`
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

// ContractNamed returns one contract by its name, e.g. "payments/v1" — one of this SDK's files, or one a test
// registered (RegisterContractForTest).
func ContractNamed(name string) (ServiceContract, bool) {
	testContractsMu.RLock()
	c, ok := testContracts[name]
	testContractsMu.RUnlock()
	if ok {
		return c, true
	}
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

var (
	testContractsMu sync.RWMutex
	testContracts   = map[string]ServiceContract{}
)

// RegisterContractForTest makes a contract no released SDK carries — one being written, from ParseContract — known
// to this process's ContractNamed, Services.Call and DispatchServiceHook until remove is called. For tests: Core
// reads only the files of the SDK it is built with and refuses a manifest naming any other contract, so a contract
// registered here reaches nothing on a real site. Like NewCoreForTest, it is plumbing and not part of the v1 promise.
func RegisterContractForTest(c ServiceContract) (remove func()) {
	testContractsMu.Lock()
	testContracts[c.Name] = c
	testContractsMu.Unlock()
	return func() {
		testContractsMu.Lock()
		delete(testContracts, c.Name)
		testContractsMu.Unlock()
	}
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
		return fmt.Errorf("nilda: the %s of %s does not match its contract: %s", what, method, schemaFailure(err))
	}
	return nil
}

// schemaRefusal reads the validator's own sentence: the schema paths it walked, the keyword that failed, its words.
var schemaRefusal = regexp.MustCompile(`validating (/\S*|root): `)

// schemaKeyword is a JSON Schema keyword, the first word of the validator's last clause.
var schemaKeyword = regexp.MustCompile(`^([a-zA-Z]+): `)

// schemaFailure says WHERE a value broke its schema and WHICH rule, and never the value. The validator's own error
// quotes the instance ("type: hunter2 has type "string", want "object""), and this error travels as the call's
// answer to Core and into logs — with an owner's application password in site.reader's start, a buyer's address in a
// carrier's rates (the 2026-11-11 review of the SDK, finding 5). The path and the keyword are the schema's, not the
// caller's; "required" keeps its words, which name the schema's own properties.
func schemaFailure(err error) string {
	msg := err.Error()
	path := "/"
	rest := msg
	if all := schemaRefusal.FindAllStringSubmatchIndex(msg, -1); len(all) > 0 {
		last := all[len(all)-1]
		if p := msg[last[2]:last[3]]; p != "root" {
			path = p
		}
		rest = msg[last[1]:]
	}
	m := schemaKeyword.FindStringSubmatch(rest)
	if m == nil {
		return "at " + path
	}
	if m[1] == "required" {
		return "at " + path + ": " + strings.TrimSpace(rest)
	}
	return "at " + path + ": it breaks the schema's \"" + m[1] + "\" rule"
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
		c, err := ParseContract(raw)
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

// ParseContract reads one contract file and checks every rule a contract must keep, compiling its schemas — what
// Contracts does for each file this SDK carries, for a file it does not: a contract being written, or one a test
// needs before any released SDK defines it.
func ParseContract(raw []byte) (ServiceContract, error) {
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
	if c.Status != "" && c.Status != ContractStable && c.Status != ContractExperimental {
		return c, fmt.Errorf("%s has status %q; it is stable (or empty) or experimental", c.Name, c.Status)
	}
	if c.CallerCapability != "" && !slices.Contains(contractCallerCapabilities, c.CallerCapability) {
		return c, fmt.Errorf("%s requires the caller capability %q; a contract may require %v", c.Name, c.CallerCapability,
			contractCallerCapabilities)
	}
	seen := map[string]bool{}
	for i := range c.Methods {
		m := &c.Methods[i]
		if err := checkMethod(m, c.Closed); err != nil {
			return c, fmt.Errorf("%s method %q: %w", c.Name, m.Name, err)
		}
		if seen[m.Name] {
			return c, fmt.Errorf("%s declares the method %q twice", c.Name, m.Name)
		}
		seen[m.Name] = true
	}
	if err := checkWrites(c); err != nil {
		return c, fmt.Errorf("%s: %w", c.Name, err)
	}
	for _, m := range c.Methods {
		if err := checkImport(m); err != nil {
			return c, fmt.Errorf("%s method %q: %w", c.Name, m.Name, err)
		}
		if m.Import && c.CallerCapability != CapImport {
			return c, fmt.Errorf("%s method %q is an import call, and the contract does not require caller_capability %q of every "+
				"caller: a plugin that may not import would plan, publish and undo the import", c.Name, m.Name, CapImport)
		}
	}
	return c, nil
}

// checkImport holds an import call to what Core's service desk relies on (ContractMethod.Import).
func checkImport(m ContractMethod) error {
	if !m.Import {
		return nil
	}
	switch {
	case m.Hook != "" || m.AnsweredBy != "provider":
		return errors.New("an import call is answered by the provider, relayed by the service desk")
	case m.Fallback != "block":
		return fmt.Errorf("an import call has fallback %q; a batch is never skipped or answered from a cache, so it blocks", m.Fallback)
	case !m.Idempotent:
		return errors.New("an import call is not idempotent; a batch sent again must change nothing")
	case m.Agent != "none":
		return fmt.Errorf("an import call is a %s tool for the assistant; it is none — the import plugin's own abilities are its door", m.Agent)
	}
	var in struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(m.Input, &in)
	var importSchema struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(in.Properties["import"], &importSchema)
	if !slices.Contains(in.Required, "import") || importSchema.Type != "object" ||
		!requiresProps(in.Properties["import"], "id", "planned") {
		return errors.New("an import call's request must require an import object requiring id and planned (nilda.ImportCall)")
	}
	return nil
}

// checkWrites holds every write command of c to docs/WRITE_COMMANDS.md §2.
func checkWrites(c ServiceContract) error {
	undoes := map[string]bool{}
	for _, m := range c.Methods {
		if m.Write != nil && m.Write.Undo != "" {
			undoes[m.Write.Undo] = true
		}
	}
	for _, m := range c.Methods {
		w := m.Write
		if w == nil {
			continue
		}
		switch {
		case !contractMethodRe.MatchString(w.Record):
			return fmt.Errorf("write command %q: record %q is not lower-case words joined by dots", m.Name, w.Record)
		case m.Hook != "":
			return fmt.Errorf("write command %q rides a hook of Core's; a write command is relayed by the service desk", m.Name)
		case !m.Idempotent:
			return fmt.Errorf("write command %q is not idempotent; a write is one write per idempotency key", m.Name)
		case m.Fallback != "block":
			return fmt.Errorf("write command %q has fallback %q; a write is never skipped or answered from a cache, so it blocks", m.Name, m.Fallback)
		case m.Agent == "read":
			return fmt.Errorf("write command %q is a read tool for the assistant; it is write, destructive or none", m.Name)
		case !contractMethodRe.MatchString(w.Emits):
			return fmt.Errorf("write command %q emits %q, which is not an event type of lower-case words joined by dots", m.Name, w.Emits)
		}
		if !requiresProps(m.Output, "record_id", "already") {
			return fmt.Errorf("write command %q: its answer must require record_id and already (nilda.WriteAnswer)", m.Name)
		}
		if undoes[m.Name] {
			if w.Undo != "" {
				return fmt.Errorf("write command %q is an undo and names an undo of its own", m.Name)
			}
			if !requiresProps(m.Input, "record_id") {
				return fmt.Errorf("undo %q: its request must require record_id (nilda.UndoInput)", m.Name)
			}
			continue
		}
		if w.Undo == "" {
			return fmt.Errorf("write command %q names no undo method; every write a plugin makes can be taken back", m.Name)
		}
		u, ok := c.Method(w.Undo)
		if !ok || u.Write == nil || u.Write.Record != w.Record {
			return fmt.Errorf("write command %q: its undo %q is not a write command of the same record in this contract", m.Name, w.Undo)
		}
	}
	return nil
}

// requiresProps reports whether an object schema lists every name in its required properties.
func requiresProps(raw json.RawMessage, names ...string) bool {
	var s struct {
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	for _, n := range names {
		found := false
		for _, r := range s.Required {
			found = found || r == n
		}
		if !found {
			return false
		}
	}
	return true
}

func checkMethod(m *ContractMethod, closed bool) error {
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
	case m.Hook == "" && m.AnsweredBy != "provider":
		return fmt.Errorf("a relayed method is answered by the provider — Core's service desk asks providers on a consumer's behalf, and tells a consumer something only through an event")
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
	if m.in, err = compileObjectSchema(m.Input, closed); err != nil {
		return fmt.Errorf("input: %w", err)
	}
	if m.out, err = compileObjectSchema(m.Output, closed); err != nil {
		return fmt.Errorf("output: %w", err)
	}
	return nil
}

// compileObjectSchema compiles a method's schema; each must describe a JSON object — every object open, or for a closed
// contract every object closed.
func compileObjectSchema(raw json.RawMessage, closed bool) (*jsonschema.Resolved, error) {
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
	switch {
	case closed:
		if path := openObjectIn(tree, "$"); path != "" {
			return nil, fmt.Errorf("the contract is closed and the object at %s is open: give it additionalProperties: false", path)
		}
	case closesAnObject(tree):
		return nil, fmt.Errorf("it closes an object (additionalProperties: false): within a major version a " +
			"contract only grows, so a field added on one side must not be refused by the other")
	}
	return s.Resolve(nil)
}

// openObjectIn is the path of the first object schema (type object, with properties) in tree that does not close its
// members, "" when every one does. A map — an object whose additionalProperties is its values' schema — is not one.
func openObjectIn(v any, path string) string {
	switch t := v.(type) {
	case map[string]any:
		_, hasProps := t["properties"]
		if hasProps && isObjectType(t["type"]) {
			if b, ok := t["additionalProperties"].(bool); !ok || b {
				return path
			}
		}
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if p := openObjectIn(t[k], path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, c := range t {
			if p := openObjectIn(c, fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	}
	return ""
}

// isObjectType is a schema's type naming an object: "object", or a list holding it.
func isObjectType(v any) bool {
	switch t := v.(type) {
	case string:
		return t == "object"
	case []any:
		for _, x := range t {
			if x == "object" {
				return true
			}
		}
	}
	return false
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
