package nildatest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Services is Core's service desk, in memory: providers answer contract methods, a consumer calls them, and in
// between it does what Core does — the consumer must declare the contract, an object naming one key twice is refused
// (dupkeys.go), the request is checked against the method's input schema before any provider sees it, every provider is asked at once within the method's budget, an
// answer the output schema refuses is dropped, and when nothing answered the method's fallback decides. A provider is
// asked through the SAME dispatch a plugin's Serve runs (nilda.DispatchServiceHook).
//
// A consumer's test, with a stub carrier:
//
//	desk := nildatest.NewServices()
//	desk.Provider("dhl", nildatest.StubService("shipping.carrier/v1", "rates", func(in json.RawMessage) (any, error) {
//		return map[string]any{"rates": []any{…}}, nil
//	}))
//	core, _, srv := desk.Core("shop", "shipping.carrier/v1")
//	defer srv.Close()
//	res, err := core.API().Services().Call(ctx, "shipping.carrier/v1", "rates", req, nilda.ServiceCallOptions{})
//
// A provider's test swaps the sides: desk.Provider("dhl", myCarrier), then desk.Ask — Core's own call, as the shop's
// would reach it.
type Services struct {
	mu        sync.Mutex
	providers map[string]nilda.ServiceProvider // plugin key → provider, in the order Provider was called
	order     []string
	last      map[string][]byte // provider + request → last good answer, for the "cached" fallback
	calls     []ServiceCallLog
	tiers     map[string]nilda.AgentTier // operator → what the owner set (SetAgentTier); browse when absent
	prefixes  map[string]string          // plugin key → its manifest's route_prefix (RoutePrefix)
	importers map[string]bool            // plugin keys the owner granted the import capability (ImportPlugin)
	importOff bool                       // Core's import switch is off (SetImportEnabled)
}

// SetImportEnabled is Core's import switch (PLUGIN_IMPORT_ENABLED, D-110): off, every call of an import contract from a
// plugin is refused with Core's 409 whose reason is plugin_import_off — after the grant is checked, before any provider is
// asked. The desk starts
// with it on, so an import plugin's test runs; a test of the switch turns it off. (A real Core ships it off.)
func (s *Services) SetImportEnabled(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.importOff = !on
}

// SetAgentTier sets what the site's owner lets an agent's operator do, as Core's SEO → Shopping agents list holds it —
// what a plugin's API().AgentTier answers through this desk's Core.
func (s *Services) SetAgentTier(operator string, tier nilda.AgentTier) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tiers == nil {
		s.tiers = map[string]nilda.AgentTier{}
	}
	s.tiers[strings.ToLower(operator)] = tier
}

// ServiceCallLog is one call a provider was asked, as a test reads it.
type ServiceCallLog struct {
	Provider, Contract, Method, Caller, IdempotencyKey string
	Input                                              json.RawMessage
	// ImportMarker is the marker Core opened an import call under ("plugin:<caller>:<id>", ContractMethod.Import), and
	// ImportPlanned the job's planned records; empty and 0 for any other call.
	ImportMarker  string
	ImportPlanned int
}

// NewServices makes an empty desk.
func NewServices() *Services {
	return &Services{providers: map[string]nilda.ServiceProvider{}, last: map[string][]byte{}}
}

// Provider installs p as the plugin key: every contract its methods name, it provides.
func (s *Services) Provider(key string, p nilda.ServiceProvider) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, seen := s.providers[key]; !seen {
		s.order = append(s.order, key)
	}
	s.providers[key] = p
}

// RoutePrefix gives provider key the route_prefix its manifest declares, which the desk stamps on each of its answers as
// Core does (ServiceAnswer.RoutePrefix). A provider given none answers with no prefix — a plugin with no routes.
func (s *Services) RoutePrefix(key, prefix string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prefixes == nil {
		s.prefixes = map[string]string{}
	}
	s.prefixes[key] = prefix
}

// StubService is a provider answering one method with fn — a stand-in for a carrier a consumer's test needs.
func StubService(contract, method string, fn func(in json.RawMessage) (any, error)) nilda.ServiceProvider {
	return stubService{nilda.ServiceMethod{Contract: contract, Method: method,
		Handle: func(_ context.Context, in json.RawMessage) (any, error) { return fn(in) }}}
}

type stubService struct{ m nilda.ServiceMethod }

func (s stubService) ServiceMethods() []nilda.ServiceMethod { return []nilda.ServiceMethod{s.m} }

// Calls is every call a provider was asked, oldest first.
func (s *Services) Calls() []ServiceCallLog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ServiceCallLog(nil), s.calls...)
}

// Core gives a consumer plugin key a Core whose API reaches this desk, holding service_consumer and a use: scope
// for each contract named — what a manifest's `requires` and `uses` give it on a real site.
func (s *Services) Core(key string, contracts ...string) (*nilda.Core, *Host, *httptest.Server) {
	granted := []string{"service_consumer"}
	h := newHost(key, granted)
	scopes := make([]string, 0, len(contracts))
	for _, c := range contracts {
		scopes = append(scopes, "use:"+c)
	}
	srv := httptest.NewServer(s.routes(key, scopes))
	return nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopes), h, srv
}

// ImportPlugin is Core for an import plugin: like Core, and holding the `import` capability the owner granted, which an
// import call (ContractMethod.Import, shop.import/v1's records.put) needs. A plugin given Core instead is refused an import
// call with a 403 whose reason is import_not_granted, as Core refuses it, before any provider is asked.
func (s *Services) ImportPlugin(key string, contracts ...string) (*nilda.Core, *Host, *httptest.Server) {
	s.mu.Lock()
	if s.importers == nil {
		s.importers = map[string]bool{}
	}
	s.importers[key] = true
	s.mu.Unlock()
	granted := []string{nilda.CapServiceConsumer, nilda.CapImport}
	h := newHost(key, granted)
	scopes := make([]string, 0, len(contracts))
	for _, c := range contracts {
		scopes = append(scopes, "use:"+c)
	}
	srv := httptest.NewServer(s.routes(key, scopes))
	return nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopes), h, srv
}

// The desk's refusals of an import contract's call (ServiceContract.CallerCapability import), as Core answers them:
// ErrImportNotGranted from a plugin the owner did not grant `import` — Core's 403, reason import_not_granted; ErrImportOff
// while the import switch is off — Core's 409, reason plugin_import_off (not retried: the switch is the owner's);
// ErrProviderNotNamed for an import call with two providers and none named — Core's 422, reason provider_not_named.
// Core's own call (the assistant's) reaches only a method whose agent is read and that is no import call, whatever the
// switch says: the switch closes the import plugin's doors, not the owner's reading of his own shop.
var (
	ErrImportNotGranted = errors.New("an import call needs the import capability, granted by the site's owner")
	ErrImportOff        = errors.New("imports are switched off on this site")
	ErrProviderNotNamed = errors.New("more than one plugin provides the contract: an import call names the one it writes into")
)

// callerMay is the grant and the switch of a contract whose every call needs a capability, checked before the request
// is read, as Core checks them.
func (s *Services) callerMay(caller, contract, method string) error {
	c, ok := nilda.ContractNamed(contract)
	if !ok || c.CallerCapability != nilda.CapImport {
		return nil
	}
	if caller == "" {
		if m, ok := c.Method(method); ok && m.Agent == "read" && !m.Import {
			return nil // the owner's own read, through Core's assistant
		}
		return ErrImportNotGranted
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.importers[caller] {
		return ErrImportNotGranted
	}
	if s.importOff {
		return ErrImportOff
	}
	return nil
}

// Ask calls as Core's own assistant would — no caller plugin, no scope — for a provider's test.
func (s *Services) Ask(ctx context.Context, contract, method string, input any, opts nilda.ServiceCallOptions) (nilda.ServiceResult, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return nilda.ServiceResult{}, err
	}
	return s.call(ctx, "", contract, method, raw, opts)
}

// ErrServiceBlocked is the desk's answer when nothing answered a method whose fallback is "block" — Core's 503.
var ErrServiceBlocked = errors.New("no provider answered, and the method cannot be answered without one")

func (s *Services) call(ctx context.Context, caller, contract, method string, input []byte, opts nilda.ServiceCallOptions) (nilda.ServiceResult, error) {
	c, ok := nilda.ContractNamed(contract) // the SDK's files, or one a test registered (nilda.RegisterContractForTest)
	if !ok {
		return nilda.ServiceResult{}, errors.New("Core relays no contract " + contract)
	}
	m, ok := c.Method(method)
	if !ok || m.Hook != "" {
		return nilda.ServiceResult{}, errors.New("Core relays no method " + method + " of " + contract)
	}
	// AN IMPORT CONTRACT'S CALL, as Core's desk relays it: only for a caller the owner granted import (Core itself holds no
	// grant), while the switch is on — both before the request is read.
	if err := s.callerMay(caller, contract, method); err != nil {
		return nilda.ServiceResult{}, err
	}
	if len(input) == 0 || string(input) == "null" {
		input = []byte(`{}`)
	}
	// ONE KEY, ONE SPELLING (dupkeys.go): after the grant, before the schema.
	if err := refuseKeyGivenTwice(input, m.Input); err != nil {
		return nilda.ServiceResult{}, err
	}
	if err := m.ValidateInput(input); err != nil {
		return nilda.ServiceResult{}, err
	}
	// An import call is opened under a marker namespaced by the caller's key, to ONE provider.
	var marker string
	var planned int
	if m.Import {
		ic, err := nilda.ReadImportCall(input)
		if err != nil {
			return nilda.ServiceResult{}, err
		}
		marker, planned = "plugin:"+caller+":"+ic.ID, ic.Planned
	}
	s.mu.Lock()
	var asked []string
	for _, k := range s.order {
		if provides(s.providers[k], contract, method) && (opts.Provider == "" || opts.Provider == k) {
			asked = append(asked, k)
		}
	}
	s.mu.Unlock()
	if opts.Provider != "" && len(asked) == 0 {
		return nilda.ServiceResult{}, errors.New("the plugin " + opts.Provider + " is not an active provider of " + contract)
	}
	if m.Import && len(asked) > 1 {
		return nilda.ServiceResult{}, ErrProviderNotNamed
	}
	budget := time.Duration(m.BudgetMS) * time.Millisecond
	if budget == 0 {
		budget = 5 * time.Second
	}
	res := nilda.ServiceResult{Contract: contract, Method: method, Answers: []nilda.ServiceAnswer{}}
	type got struct {
		out  []byte
		miss string
	}
	results := make([]got, len(asked))
	var wg sync.WaitGroup
	for i, k := range asked {
		wg.Add(1)
		go func(i int, k string) {
			defer wg.Done()
			s.mu.Lock()
			p := s.providers[k]
			s.calls = append(s.calls, ServiceCallLog{Provider: k, Contract: contract, Method: method, Caller: caller,
				IdempotencyKey: opts.IdempotencyKey, Input: append(json.RawMessage(nil), input...),
				ImportMarker: marker, ImportPlanned: planned})
			s.mu.Unlock()
			cctx, cancel := context.WithTimeout(ctx, budget)
			defer cancel()
			// What Core tells a provider about the call: the contract, the asking plugin, its key (CallOf).
			cctx = nilda.WithCallInfo(cctx, nilda.CallInfo{Contract: contract, Caller: caller, IdempotencyKey: opts.IdempotencyKey})
			done := make(chan got, 1)
			go func() {
				out, _, err := nilda.DispatchServiceHook(cctx, p, nilda.ServiceHook(contract, method), input)
				switch {
				case err != nil:
					done <- got{miss: "error"}
				case m.ValidateOutput(out) != nil:
					done <- got{miss: "invalid_answer"}
				default:
					done <- got{out: out}
				}
			}()
			select {
			case g := <-done:
				results[i] = g
			case <-cctx.Done():
				results[i] = got{miss: "timeout"}
			}
		}(i, k)
	}
	wg.Wait()
	key := contract + "\x00" + method + "\x00" + canonical(input)
	s.mu.Lock()
	for i, g := range results {
		if g.miss != "" {
			res.Missed = append(res.Missed, nilda.ServiceMiss{Provider: asked[i], Reason: g.miss})
			continue
		}
		res.Answers = append(res.Answers, nilda.ServiceAnswer{Provider: asked[i], Output: g.out, RoutePrefix: s.prefixes[asked[i]]})
		if !m.Import { // an import call's answer is never kept to answer another
			s.last[asked[i]+"\x00"+key] = g.out
		}
	}
	if len(res.Answers) == 0 {
		switch m.Fallback {
		case "cached":
			for _, miss := range res.Missed {
				if out, ok := s.last[miss.Provider+"\x00"+key]; ok {
					res.Answers = append(res.Answers, nilda.ServiceAnswer{Provider: miss.Provider, Output: out, Cached: true, RoutePrefix: s.prefixes[miss.Provider]})
				}
			}
			res.Fallback = "cached"
		case "skip":
			res.Fallback = "skip"
		}
	}
	s.mu.Unlock()
	if len(res.Answers) == 0 && m.Fallback == "block" {
		return res, ErrServiceBlocked
	}
	return res, nil
}

func provides(p nilda.ServiceProvider, contract, method string) bool {
	if p == nil {
		return false
	}
	for _, m := range p.ServiceMethods() {
		if m.Contract == contract && m.Method == method {
			return true
		}
	}
	return false
}

func canonical(raw []byte) string {
	var v any
	if json.Unmarshal(raw, &v) == nil {
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
	}
	return string(raw)
}

// routes is the consumer's door: POST /services/<name>/<major>/<method>, as Core serves it.
func (s *Services) routes(caller string, scopes []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/agents/tier" {
			op := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("operator")))
			s.mu.Lock()
			tier, ok := s.tiers[op]
			s.mu.Unlock()
			if !ok {
				tier = nilda.AgentBrowse
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"operator": op, "tier": tier}})
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/services/"), "/")
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/services/") || len(parts) != 3 {
			writeServiceError(w, http.StatusNotFound, "NOT_FOUND", "no such route", nil)
			return
		}
		contract, method := parts[0]+"/"+parts[1], parts[2]
		allowed := false
		for _, sc := range scopes {
			allowed = allowed || sc == "use:"+contract
		}
		if !allowed {
			writeServiceError(w, http.StatusForbidden, "FORBIDDEN", "this plugin does not require or use "+contract,
				map[string]string{"reason": "contract_not_declared", "contract": contract})
			return
		}
		if err := s.callerMay(caller, contract, method); err != nil {
			writeDeskRefusal(w, err, contract)
			return
		}
		var body struct {
			Input    json.RawMessage `json:"input"`
			Provider string          `json:"provider"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeServiceError(w, http.StatusBadRequest, "VALIDATION", "the body must be JSON", nil)
			return
		}
		res, err := s.call(r.Context(), caller, contract, method, body.Input,
			nilda.ServiceCallOptions{Provider: body.Provider, IdempotencyKey: r.Header.Get("Idempotency-Key")})
		switch {
		case errors.Is(err, ErrServiceBlocked):
			writeServiceError(w, http.StatusServiceUnavailable, "UNAVAILABLE", err.Error(), nil)
		case errors.Is(err, ErrImportNotGranted), errors.Is(err, ErrImportOff), errors.Is(err, ErrProviderNotNamed):
			writeDeskRefusal(w, err, contract)
		case errors.Is(err, ErrKeyGivenTwice):
			var twice *KeyGivenTwice
			errors.As(err, &twice)
			writeServiceError(w, http.StatusUnprocessableEntity, "VALIDATION", err.Error(),
				map[string]string{"reason": "key_given_twice", "at": twice.At})
		case err != nil:
			writeServiceError(w, http.StatusUnprocessableEntity, "VALIDATION", err.Error(), nil)
		default:
			sort.SliceStable(res.Missed, func(i, j int) bool { return res.Missed[i].Provider < res.Missed[j].Provider })
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": res})
		}
	})
}

// writeDeskRefusal answers one of an import contract's refusals as Core does.
func writeDeskRefusal(w http.ResponseWriter, err error, contract string) {
	switch {
	case errors.Is(err, ErrImportNotGranted):
		writeServiceError(w, http.StatusForbidden, "FORBIDDEN", err.Error(),
			map[string]string{"reason": "import_not_granted", "contract": contract})
	case errors.Is(err, ErrImportOff):
		writeServiceError(w, http.StatusConflict, "CONFLICT", err.Error(),
			map[string]string{"reason": "plugin_import_off", "contract": contract})
	default:
		writeServiceError(w, http.StatusUnprocessableEntity, "VALIDATION", err.Error(),
			map[string]string{"reason": "provider_not_named", "contract": contract})
	}
}

func writeServiceError(w http.ResponseWriter, status int, code, msg string, details map[string]string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": msg, "details": details}})
}
