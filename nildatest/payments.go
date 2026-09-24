package nildatest

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
)

// Payments is Core's side of the payment contract, in memory: the sessions and refunds, the one state machine,
// who may touch which session, and the hooks Core sends — sent through the SAME dispatch a plugin's Serve runs
// (nilda.DispatchPaymentGatewayHook, DispatchPaymentConsumerHook), so a gateway or a consumer is exercised the
// way Core exercises it, refusals included.
//
// A gateway's test, end to end with no Core running:
//
//	pay := nildatest.NewPayments()
//	core, _, srv := pay.Core("stripe", "payment_gateway", "route")
//	defer srv.Close()
//	g := &Gateway{}
//	if _, err := g.Init(ctx, core); err != nil { t.Fatal(err) }
//	pay.Gateway("stripe", g)
//
//	s, _ := pay.CreateSession(ctx, "shop", nilda.PaymentSessionParams{Gateway: "stripe", Method: "card", …})
//	res := nildatest.Webhook(g.routes, "/stripe/webhook", body, header) // what the processor sends
//	if got, _ := pay.GetSession(s.ID); got.Status != nilda.PaymentResolved { … }
//
// A consumer's test swaps the sides: pay.Consumer("shop", shop), a StubGateway as the gateway, and Deliver to
// run the job that tells the consumer — Core tells it from a job, never inside the call that changed the
// session, and so does this.
type Payments struct {
	// SiteURL is the site's own origin. A return or cancel address anywhere else is refused, as Core refuses
	// it. Set it before the first call; the default is "https://site.test".
	SiteURL string

	mu        sync.Mutex
	hosts     map[string]*Host // plugin key → the fake Core it was given, for its grants
	gateways  []string         // registration order: the order Methods lists them in
	gateway   map[string]nilda.PaymentGateway
	consumer  map[string]nilda.PaymentConsumer
	sessions  map[string]*nilda.PaymentSession
	order     []string // session ids, oldest first
	refunds   map[string]*nilda.PaymentRefund
	refundDue map[string]bool   // refunds the delivery job still has to ask their gateway about
	reported  map[string]string // session id → the mismatched amount a processor reported, as Core keeps it
	keys      map[string]replay // caller + idempotency key → the first answer
	owed      []owed            // what the consumers have not heard yet, oldest change first
	log       []Delivery
}

// Delivery is one attempt to tell a consumer about a change.
type Delivery struct {
	Consumer string
	// Hook is nilda.HookPaymentSessionUpdated or nilda.HookPaymentRefundUpdated.
	Hook    string
	Session nilda.PaymentSession
	Refund  nilda.PaymentRefund // the zero value for a session's change
	// Err is what the consumer answered: nil means it heard it, anything else means Core owes it again.
	Err error
}

type owed struct{ session, refund string }

type replay struct {
	sum    [sha256.Size]byte
	status int
	body   []byte
}

// The budgets Core gives the payment hooks: 15 seconds for a gateway's (Core's own Stripe call has always had
// fifteen), the ordinary plugin call's 5 seconds for a consumer's.
const (
	gatewayHookBudget  = 15 * time.Second
	consumerHookBudget = 5 * time.Second
	// sessionLifetime is Core's default expiry for a session its gateway gave none: 24 hours, the default
	// Stripe Checkout gives its own sessions.
	sessionLifetime = 24 * time.Hour
	// maxProviderRef is the bound on a processor's reference, Core's and the SDK's (payment.go's, which the
	// SDK's TestTheKitsBoundsAreTheSDKs holds this copy to).
	maxProviderRef = 255
)

// NewPayments builds an empty payment service.
func NewPayments() *Payments {
	return &Payments{
		hosts:     map[string]*Host{},
		gateway:   map[string]nilda.PaymentGateway{},
		consumer:  map[string]nilda.PaymentConsumer{},
		sessions:  map[string]*nilda.PaymentSession{},
		refunds:   map[string]*nilda.PaymentRefund{},
		refundDue: map[string]bool{},
		reported:  map[string]string{},
		keys:      map[string]replay{},
	}
}

// Core builds a fake Core for the plugin key, granted exactly the capabilities named, whose API is this
// payment service answering as Core answers that plugin: its calls land here under its own identity, so a
// gateway reports only on the sessions routed to it and a consumer reads only the ones it created. Close the
// server when done.
func (p *Payments) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	p.mu.Lock()
	p.hosts[key] = h
	p.mu.Unlock()
	srv := httptest.NewServer(p.routes(key))
	return nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted)), h, srv
}

// Gateway installs g as the gateway plugin key. Core sends it the payment hooks only while that plugin holds
// payment_gateway: if key has a Core from this service, its grants decide, as they do in Core.
func (p *Payments) Gateway(key string, g nilda.PaymentGateway) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, seen := p.gateway[key]; !seen {
		p.gateways = append(p.gateways, key)
	}
	p.gateway[key] = g
}

// Consumer installs c as the consumer plugin key: the one Core asks to confirm, and tells about its sessions.
func (p *Payments) Consumer(key string, c nilda.PaymentConsumer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.consumer[key] = c
}

// ---- what a test reads -----------------------------------------------------------------------------------

// GetSession returns a session as Core holds it now.
func (p *Payments) GetSession(id string) (nilda.PaymentSession, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	if !ok {
		return nilda.PaymentSession{}, false
	}
	return *s, true
}

// GetRefund returns a refund as Core holds it now.
func (p *Payments) GetRefund(id string) (nilda.PaymentRefund, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.refunds[id]
	if !ok {
		return nilda.PaymentRefund{}, false
	}
	return *r, true
}

// Sessions returns every session, oldest first.
func (p *Payments) Sessions() []nilda.PaymentSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]nilda.PaymentSession, 0, len(p.order))
	for _, id := range p.order {
		out = append(out, *p.sessions[id])
	}
	return out
}

// Deliveries returns every attempt to tell a consumer something, in order — the ones it refused included.
func (p *Payments) Deliveries() []Delivery {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Delivery(nil), p.log...)
}

// ---- Core's own jobs, run when a test says --------------------------------------------------------------

// Deliver runs Core's delivery job once: every consumer that has not heard a change is told the session's —
// or the refund's — state as it is NOW, oldest change first, and a refund whose payment.refund went
// unanswered is asked of its gateway again. It returns how many deliveries are still owed: a consumer that
// answered with an error is owed until it answers without one, which is the promise Core makes about "the
// customer paid".
func (p *Payments) Deliver(ctx context.Context) int {
	p.mu.Lock()
	due := make([]string, 0, len(p.refundDue))
	for id := range p.refundDue {
		due = append(due, id)
	}
	p.mu.Unlock()
	slices.Sort(due)
	for _, id := range due {
		p.askRefund(ctx, id)
	}

	p.mu.Lock()
	queue := p.owed
	p.owed = nil
	p.mu.Unlock()
	for _, o := range queue {
		p.deliver(ctx, o)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.owed)
}

// Expire runs Core's expiry for one session, as if its ExpiresAt had passed: a session nothing decided —
// created or redirected — becomes expired, and its consumer is owed the news. A session the processor has
// (pending) is not expired: the processor decides it. Anything else is refused, as the job would leave it.
func (p *Payments) Expire(id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	if !ok {
		return fmt.Errorf("nildatest: no payment session %q", id)
	}
	if s.Status != nilda.PaymentCreated && s.Status != nilda.PaymentRedirected {
		return fmt.Errorf("nildatest: a %s session does not expire; only one nothing has decided does", s.Status)
	}
	p.move(s, nilda.PaymentExpired)
	return nil
}

// ---- the consumer's side, callable directly --------------------------------------------------------------

// CreateSession creates a payment as the consumer plugin would, through the same checks, and asks the gateway
// to start it — for a gateway's test, which has no consumer of its own. It is not keyed: every call is a new
// attempt. (A consumer's own calls, through its Core's API, go through the idempotency layer as in Core.)
func (p *Payments) CreateSession(ctx context.Context, consumer string, params nilda.PaymentSessionParams) (nilda.PaymentSession, error) {
	return p.createSession(ctx, consumer, params)
}

// ---- the HTTP surface, as Core serves it under /api/rest/v1 ---------------------------------------------

// routes answers one plugin's calls. The server is bound to the plugin, so the caller is never taken from the
// request: there is no header a test could set to be somebody else.
func (p *Payments) routes(caller string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] != "payments" {
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		ctx := r.Context()
		switch {
		case r.Method == http.MethodGet && len(parts) == 2 && parts[1] == "methods":
			if err := p.may(caller, "payment_session"); err != nil {
				writeError(w, err)
				return
			}
			opts, err := p.methods(ctx, r.URL.Query().Get("currency"))
			answer(w, http.StatusOK, opts, err)

		case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "sessions":
			p.keyed(w, r, caller, body, func() (int, any, error) {
				var params nilda.PaymentSessionParams
				if err := json.Unmarshal(body, &params); err != nil {
					return 0, nil, invalid("the body is not a payment session: %v", err)
				}
				s, err := p.createSession(ctx, caller, params)
				return http.StatusCreated, s, err
			})

		case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "sessions":
			s, err := p.readSession(caller, parts[2])
			answer(w, http.StatusOK, s, err)

		case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "sessions" && parts[3] == "refunds":
			p.keyed(w, r, caller, body, func() (int, any, error) {
				var params nilda.PaymentRefundParams
				if err := json.Unmarshal(body, &params); err != nil {
					return 0, nil, invalid("the body is not a refund: %v", err)
				}
				rf, err := p.createRefund(ctx, caller, parts[2], params)
				return http.StatusCreated, rf, err
			})

		case r.Method == http.MethodGet && len(parts) == 3 && parts[1] == "refunds":
			rf, err := p.readRefund(caller, parts[2])
			answer(w, http.StatusOK, rf, err)

		case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "sessions":
			s, err := p.report(ctx, caller, parts[2], parts[3], body)
			answer(w, http.StatusOK, s, err)

		case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "refunds":
			rf, err := p.reportRefund(caller, parts[2], parts[3], body)
			answer(w, http.StatusOK, rf, err)

		default:
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
		}
	})
}

// keyed runs a write that must name its attempt, and answers a repeat the way Core's idempotency layer does:
// the same key and the same request get the first answer back, marked Idempotent-Replayed; the same key on a
// different request is refused; a repeat while the first is still running is a 409; a refused request (4xx)
// and an UNAVAILABLE one (503: nothing changed) keep nothing, so a retry runs.
func (p *Payments) keyed(w http.ResponseWriter, r *http.Request, caller string, body []byte, run func() (int, any, error)) {
	key := r.Header.Get("Idempotency-Key")
	if strings.TrimSpace(key) == "" {
		writeError(w, invalid("a payment write needs an Idempotency-Key naming this attempt"))
		return
	}
	if len(key) > 255 {
		writeError(w, invalid("Idempotency-Key is longer than 255 characters"))
		return
	}
	slot := caller + "\x00" + key
	sum := sha256.Sum256(append([]byte(r.Method+" "+r.URL.Path+"\n"), body...))
	p.mu.Lock()
	prev, seen := p.keys[slot]
	if !seen {
		p.keys[slot] = replay{sum: sum} // claimed: status 0 until the first answer is in
	}
	p.mu.Unlock()
	if seen {
		if prev.sum != sum {
			writeError(w, invalid("this Idempotency-Key was used for a different request"))
			return
		}
		if prev.status == 0 {
			writeError(w, &apiError{http.StatusConflict, "CONFLICT",
				"a request with this Idempotency-Key is still being processed; ask again in a moment"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Idempotent-Replayed", "true")
		w.WriteHeader(prev.status)
		_, _ = w.Write(prev.body)
		return
	}
	status, v, err := run()
	rec := httptest.NewRecorder()
	answer(rec, status, v, err)
	p.mu.Lock()
	if (rec.Code >= 400 && rec.Code < 500) || rec.Code == http.StatusServiceUnavailable {
		delete(p.keys, slot) // it changed nothing: the key is given back
	} else {
		p.keys[slot] = replay{sum: sum, status: rec.Code, body: rec.Body.Bytes()}
	}
	p.mu.Unlock()
	for k, vs := range rec.Header() {
		w.Header()[k] = vs
	}
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

// ---- the rules -------------------------------------------------------------------------------------------

func (p *Payments) may(caller, capability string) error {
	p.mu.Lock()
	h := p.hosts[caller]
	p.mu.Unlock()
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.granted[capability] {
		return &apiError{http.StatusForbidden, "FORBIDDEN",
			fmt.Sprintf("plugin %q lacks the %q capability — declare it in plugin.json", caller, capability)}
	}
	return nil
}

// installedGateway returns the gateway plugin key, if it is installed and holds payment_gateway.
func (p *Payments) installedGateway(key string) (nilda.PaymentGateway, bool) {
	p.mu.Lock()
	g, ok := p.gateway[key]
	p.mu.Unlock()
	if !ok || p.may(key, "payment_gateway") != nil {
		return nil, false
	}
	return g, true
}

// installedConsumer returns the consumer plugin key, if it is installed and holds payment_session: Core sends
// the consumer hooks to nobody else.
func (p *Payments) installedConsumer(key string) (nilda.PaymentConsumer, bool) {
	p.mu.Lock()
	c, ok := p.consumer[key]
	p.mu.Unlock()
	if !ok || p.may(key, "payment_session") != nil {
		return nil, false
	}
	return c, true
}

func (p *Payments) describe(ctx context.Context, key string) ([]nilda.PaymentMethod, bool) {
	g, ok := p.installedGateway(key)
	if !ok {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	defer cancel()
	out, _, err := nilda.DispatchPaymentGatewayHook(ctx, nil, g, nilda.HookPaymentDescribe, []byte(`{}`))
	if err != nil {
		return nil, false // Core logs it and lists the gateway's methods as none
	}
	var res nilda.PaymentDescribeResponse
	if json.Unmarshal(out, &res) != nil {
		return nil, false
	}
	return res.Methods, true
}

func (p *Payments) methods(ctx context.Context, currency string) ([]nilda.PaymentOption, error) {
	code := strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := nilda.MinorUnits(code); !ok {
		return nil, invalid("%q is not a currency ISO 4217 gives a minor unit to", currency)
	}
	p.mu.Lock()
	keys := append([]string(nil), p.gateways...)
	p.mu.Unlock()
	opts := []nilda.PaymentOption{}
	for _, key := range keys {
		methods, ok := p.describe(ctx, key)
		if !ok {
			continue
		}
		for _, m := range methods {
			if takes(m, code) {
				opts = append(opts, nilda.PaymentOption{Gateway: key, Method: m.Key, Label: m.Label,
					Description: m.Description, Refunds: m.Refunds, Test: m.Test})
			}
		}
	}
	return opts, nil
}

func takes(m nilda.PaymentMethod, currency string) bool {
	if len(m.Currencies) == 0 {
		return true
	}
	for _, c := range m.Currencies {
		if strings.EqualFold(c, currency) {
			return true
		}
	}
	return false
}

func (p *Payments) method(ctx context.Context, gateway, key, currency string) (nilda.PaymentMethod, error) {
	methods, ok := p.describe(ctx, gateway)
	if !ok {
		return nilda.PaymentMethod{}, invalid("no gateway %q is installed and taking payments", gateway)
	}
	for _, m := range methods {
		if m.Key == key {
			if !takes(m, currency) {
				return nilda.PaymentMethod{}, invalid("gateway %q's %q does not take %s", gateway, key, currency)
			}
			return m, nil
		}
	}
	return nilda.PaymentMethod{}, invalid("gateway %q offers no method %q", gateway, key)
}

func (p *Payments) onSite(field, raw string) error {
	if raw == "" {
		return nil
	}
	site := p.SiteURL
	if site == "" {
		site = "https://site.test"
	}
	want, _ := url.Parse(site)
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, want.Scheme) || !strings.EqualFold(u.Host, want.Host) {
		return invalid("%s must be on this site (%s), got %q", field, site, raw)
	}
	return nil
}

func (p *Payments) createSession(ctx context.Context, consumer string, params nilda.PaymentSessionParams) (nilda.PaymentSession, error) {
	if err := p.may(consumer, "payment_session"); err != nil {
		return nilda.PaymentSession{}, err
	}
	if err := params.Validate(); err != nil {
		return nilda.PaymentSession{}, invalid("%v", err)
	}
	if err := p.onSite("return_url", params.ReturnURL); err != nil {
		return nilda.PaymentSession{}, err
	}
	if err := p.onSite("cancel_url", params.CancelURL); err != nil {
		return nilda.PaymentSession{}, err
	}
	currency := strings.ToUpper(strings.TrimSpace(params.Currency))
	m, err := p.method(ctx, params.Gateway, params.Method, currency)
	if err != nil {
		return nilda.PaymentSession{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(sessionLifetime)
	s := &nilda.PaymentSession{
		ID: newID(), Consumer: consumer, Gateway: params.Gateway, Method: params.Method,
		Reference: params.Reference, AmountMinor: params.AmountMinor, Currency: currency,
		Status: nilda.PaymentCreated, Test: m.Test, Description: params.Description,
		ReturnURL: params.ReturnURL, CancelURL: params.CancelURL, Email: params.Email, Locale: params.Locale,
		ExpiresAt: &expires, CreatedAt: now, UpdatedAt: now,
	}
	if s.CancelURL == "" {
		s.CancelURL = s.ReturnURL
	}
	p.mu.Lock()
	p.sessions[s.ID] = s
	p.order = append(p.order, s.ID)
	snapshot := *s
	p.mu.Unlock()

	res, err := p.start(ctx, snapshot)
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.Status != nilda.PaymentCreated {
		// The gateway reported an outcome before its start answer arrived — its processor settled at once.
		// That report is the truth; the start answer describes a moment already past.
		return *s, nil
	}
	if err != nil {
		// The gateway did not answer. No payer has seen a processor's page, so no money can have moved:
		// the session is closed, and the consumer can offer the payer another try.
		s.FailureCode = nilda.PaymentFailProviderUnavailable
		s.FailureMessage = "the payment could not be started"
		p.move(s, nilda.PaymentRejected)
		return *s, nil
	}
	s.ProviderRef = res.ProviderRef
	if res.ExpiresAt != nil && res.ExpiresAt.After(now) {
		t := res.ExpiresAt.UTC()
		s.ExpiresAt = &t
	}
	switch res.Status {
	case nilda.PaymentRedirected:
		s.RedirectURL = res.RedirectURL
	case nilda.PaymentRejected:
		s.FailureCode, s.FailureMessage = res.FailureCode, res.FailureMessage
	}
	p.move(s, res.Status)
	return *s, nil
}

func (p *Payments) start(ctx context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	g, ok := p.installedGateway(s.Gateway)
	if !ok {
		return nilda.PaymentStartResult{}, fmt.Errorf("gateway %q is not installed", s.Gateway)
	}
	payload, _ := json.Marshal(nilda.PaymentStartRequest{Session: s})
	ctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
	defer cancel()
	out, _, err := nilda.DispatchPaymentGatewayHook(ctx, nil, g, nilda.HookPaymentStart, payload)
	if err != nil {
		return nilda.PaymentStartResult{}, err
	}
	var res nilda.PaymentStartResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nilda.PaymentStartResult{}, err
	}
	return res, nil
}

// move changes a session's status by the state machine and owes its consumer the news. Callers hold p.mu.
func (p *Payments) move(s *nilda.PaymentSession, to string) bool {
	if s.Status == to || !nilda.PaymentCanMove(s.Status, to) {
		return false
	}
	s.Status = to
	s.UpdatedAt = time.Now().UTC()
	p.owe(owed{session: s.ID})
	return true
}

// owe queues a delivery once: a consumer is told the latest state, not every state it missed.
func (p *Payments) owe(o owed) {
	if !slices.Contains(p.owed, o) {
		p.owed = append(p.owed, o)
	}
}

func (p *Payments) deliver(ctx context.Context, o owed) {
	p.mu.Lock()
	s := *p.sessions[o.session]
	var rf nilda.PaymentRefund
	hook := nilda.HookPaymentSessionUpdated
	payload, _ := json.Marshal(nilda.PaymentSessionUpdate{Session: s})
	if o.refund != "" {
		rf = *p.refunds[o.refund]
		hook = nilda.HookPaymentRefundUpdated
		payload, _ = json.Marshal(nilda.PaymentRefundUpdate{Refund: rf, Session: s})
	}
	p.mu.Unlock()
	c, ok := p.installedConsumer(s.Consumer)

	var err error
	if !ok {
		err = fmt.Errorf("consumer %q is not installed", s.Consumer)
	} else {
		cctx, cancel := context.WithTimeout(ctx, consumerHookBudget)
		_, _, err = nilda.DispatchPaymentConsumerHook(cctx, nil, c, hook, payload)
		cancel()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, Delivery{Consumer: s.Consumer, Hook: hook, Session: s, Refund: rf, Err: err})
	if err != nil {
		p.owe(o)
	}
}

// readSession is the session, for its consumer or its gateway; anyone else is told it does not exist.
func (p *Payments) readSession(caller, id string) (nilda.PaymentSession, error) {
	if p.may(caller, "payment_session") != nil && p.may(caller, "payment_gateway") != nil {
		return nilda.PaymentSession{}, p.may(caller, "payment_session")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.sessions[id]
	if !ok || (s.Consumer != caller && s.Gateway != caller) {
		return nilda.PaymentSession{}, notFound("no payment session %q", id)
	}
	return *s, nil
}

func (p *Payments) readRefund(caller, id string) (nilda.PaymentRefund, error) {
	if p.may(caller, "payment_session") != nil && p.may(caller, "payment_gateway") != nil {
		return nilda.PaymentRefund{}, p.may(caller, "payment_session")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rf, ok := p.refunds[id]
	if !ok {
		return nilda.PaymentRefund{}, notFound("no refund %q", id)
	}
	s := p.sessions[rf.Session]
	if s.Consumer != caller && s.Gateway != caller {
		return nilda.PaymentRefund{}, notFound("no refund %q", id)
	}
	return *rf, nil
}

// report applies a gateway's resolve, reject, pending or confirm to a session routed to it.
func (p *Payments) report(ctx context.Context, caller, id, verb string, body []byte) (any, error) {
	if err := p.may(caller, "payment_gateway"); err != nil {
		return nil, err
	}
	p.mu.Lock()
	s, ok := p.sessions[id]
	if !ok || s.Gateway != caller {
		p.mu.Unlock()
		return nil, notFound("no payment session %q", id)
	}
	if verb == "confirm" {
		snapshot := *s
		p.mu.Unlock()
		return p.confirm(ctx, snapshot)
	}
	defer p.mu.Unlock()

	switch verb {
	case "resolve":
		var r nilda.PaymentResolution
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, invalid("the body is not a resolution: %v", err)
		}
		if err := r.Validate(); err != nil {
			return nil, invalid("%v", err)
		}
		// Nothing below writes to the session until the report is known to apply: a refused report changes
		// nothing, not even the processor's reference.
		if r.AmountMinor != s.AmountMinor || !strings.EqualFold(r.Currency, s.Currency) {
			// The processor took something other than what was asked. Neither paid nor unpaid: a person
			// decides, so the session is held where the consumer ships nothing.
			if s.Status != nilda.PaymentPending && !nilda.PaymentCanMove(s.Status, nilda.PaymentPending) {
				return nil, conflict(s)
			}
			// The same mismatch again — the processor's redelivery — changes nothing, as in Core. What the
			// processor reported is the owner's to read (Settings → Payments), not the wire's: no failure message.
			seen := fmt.Sprintf("%d %s", r.AmountMinor, strings.ToUpper(r.Currency))
			if s.Status == nilda.PaymentPending && s.FailureCode == nilda.PaymentFailAmountMismatch && p.reported[s.ID] == seen {
				return *s, nil
			}
			p.reported[s.ID] = seen
			if r.ProviderRef != "" {
				s.ProviderRef = r.ProviderRef
			}
			s.FailureCode, s.FailureMessage = nilda.PaymentFailAmountMismatch, ""
			if !p.move(s, nilda.PaymentPending) {
				// Already pending: the status is the same, the reason is not — the consumer hears it.
				s.UpdatedAt = time.Now().UTC()
				p.owe(owed{session: s.ID})
			}
			return *s, nil
		}
		if s.Status != nilda.PaymentResolved && !nilda.PaymentCanMove(s.Status, nilda.PaymentResolved) {
			return nil, conflict(s)
		}
		if r.ProviderRef != "" && s.Status != nilda.PaymentResolved {
			s.ProviderRef = r.ProviderRef
		}
		return p.apply(s, nilda.PaymentResolved)

	case "reject":
		var r nilda.PaymentRejection
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, invalid("the body is not a rejection: %v", err)
		}
		if err := r.Validate(); err != nil {
			return nil, invalid("%v", err)
		}
		if s.Status == nilda.PaymentRejected {
			return *s, nil // the same verdict again: nothing to change
		}
		if !nilda.PaymentCanMove(s.Status, nilda.PaymentRejected) {
			return nil, conflict(s)
		}
		if r.ProviderRef != "" {
			s.ProviderRef = r.ProviderRef
		}
		s.FailureCode, s.FailureMessage = r.FailureCode, r.FailureMessage
		return p.apply(s, nilda.PaymentRejected)

	case "pending":
		var r struct {
			ProviderRef string `json:"provider_ref"`
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &r); err != nil {
				return nil, invalid("the body is not a pending report: %v", err)
			}
		}
		if len(r.ProviderRef) > maxProviderRef {
			return nil, invalid("a processor's reference is at most %d bytes", maxProviderRef)
		}
		if s.Status == nilda.PaymentPending {
			return *s, nil // already pending: nothing changes, the reference included (Core's MarkPending)
		}
		if !nilda.PaymentCanMove(s.Status, nilda.PaymentPending) {
			return nil, conflict(s)
		}
		if r.ProviderRef != "" {
			s.ProviderRef = r.ProviderRef
		}
		return p.apply(s, nilda.PaymentPending)
	}
	return nil, notFound("no such report %q", verb)
}

// apply moves s to the status a report names: the same status is a no-op, a move the machine refuses is a
// 409 carrying where the session is. Callers hold p.mu.
func (p *Payments) apply(s *nilda.PaymentSession, to string) (any, error) {
	if s.Status == to {
		return *s, nil
	}
	if !p.move(s, to) {
		return nil, conflict(s)
	}
	if to == nilda.PaymentResolved && s.FailureCode == nilda.PaymentFailAmountMismatch {
		s.FailureCode, s.FailureMessage = "", ""
	}
	return *s, nil
}

func (p *Payments) confirm(ctx context.Context, s nilda.PaymentSession) (any, error) {
	if s.Status != nilda.PaymentCreated && s.Status != nilda.PaymentRedirected && s.Status != nilda.PaymentPending {
		return nil, conflict(&s)
	}
	c, ok := p.installedConsumer(s.Consumer)
	if !ok {
		return nil, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE",
			fmt.Sprintf("the consumer %q could not be asked; ask again later", s.Consumer)}
	}
	payload, _ := json.Marshal(nilda.PaymentConfirmRequest{Session: s})
	cctx, cancel := context.WithTimeout(ctx, consumerHookBudget)
	defer cancel()
	out, _, err := nilda.DispatchPaymentConsumerHook(cctx, nil, c, nilda.HookPaymentConfirm, payload)
	if err != nil {
		return nil, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE",
			fmt.Sprintf("the consumer %q could not decide: %v; ask again later", s.Consumer, err)}
	}
	var res nilda.PaymentConfirmResult
	if err := json.Unmarshal(out, &res); err != nil {
		return nil, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE", "the consumer's answer did not parse"}
	}
	if !res.Proceed {
		p.mu.Lock()
		live := p.sessions[s.ID]
		live.FailureCode, live.FailureMessage = nilda.PaymentFailConsumerRefused, res.Reason
		p.move(live, nilda.PaymentRejected)
		p.mu.Unlock()
	}
	return res, nil
}

func (p *Payments) createRefund(ctx context.Context, caller, sessionID string, params nilda.PaymentRefundParams) (nilda.PaymentRefund, error) {
	if err := p.may(caller, "payment_session"); err != nil {
		return nilda.PaymentRefund{}, err
	}
	if err := params.Validate(); err != nil {
		return nilda.PaymentRefund{}, invalid("%v", err)
	}
	p.mu.Lock()
	s, ok := p.sessions[sessionID]
	if !ok || s.Consumer != caller {
		p.mu.Unlock()
		return nilda.PaymentRefund{}, notFound("no payment session %q", sessionID)
	}
	if err := p.refundFits(s, params.AmountMinor); err != nil {
		p.mu.Unlock()
		return nilda.PaymentRefund{}, err
	}
	gateway, method, currency := s.Gateway, s.Method, s.Currency
	p.mu.Unlock()

	// As Core's refundable: a gateway that is not running, or cannot be asked what it offers, is UNAVAILABLE —
	// ask again later — and the method must say it gives money back. The currency is not asked again: the
	// payment was taken, whatever the method lists today.
	methods, ok := p.describe(ctx, gateway)
	if !ok {
		return nilda.PaymentRefund{}, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE",
			fmt.Sprintf("the gateway %q that took this payment is not running or could not be asked; ask again later", gateway)}
	}
	refunds := false
	for _, m := range methods {
		if m.Key == method && m.Refunds {
			refunds = true
		}
	}
	if !refunds {
		return nilda.PaymentRefund{}, invalid("gateway %q's %q cannot give money back through Nilda", gateway, method)
	}
	now := time.Now().UTC()
	rf := &nilda.PaymentRefund{ID: newID(), Session: sessionID, AmountMinor: params.AmountMinor,
		Currency: currency, Status: nilda.RefundRequested, Reason: params.Reason, CreatedAt: now, UpdatedAt: now}
	p.mu.Lock()
	// Asked again at the moment of writing: a second refund may have been written while the gateway was
	// asked about the method, and two refunds that each fit alone must not both be written.
	if err := p.refundFits(s, params.AmountMinor); err != nil {
		p.mu.Unlock()
		return nilda.PaymentRefund{}, err
	}
	p.refunds[rf.ID] = rf
	// Asked by the delivery job (Deliver), as Core asks it: CreateRefund answers REQUESTED and never waits on
	// the processor inside the consumer's call — an owner's row action has five seconds, a processor fifteen.
	p.refundDue[rf.ID] = true
	got := *rf
	p.mu.Unlock()
	return got, nil
}

// refundFits refuses a refund of an unpaid session, or one that would take the session's refunds — resolved
// and still in flight — past what was paid. Callers hold p.mu.
func (p *Payments) refundFits(s *nilda.PaymentSession, amount int64) error {
	if s.Status != nilda.PaymentResolved {
		return &apiError{http.StatusConflict, "CONFLICT",
			fmt.Sprintf("only a resolved payment can be refunded; this one is %s", s.Status)}
	}
	var committed int64
	for _, rf := range p.refunds {
		if rf.Session == s.ID && rf.Status != nilda.RefundRejected {
			committed += rf.AmountMinor
		}
	}
	if committed+amount > s.AmountMinor {
		return invalid("a refund of %d would take this payment's refunds to %d, past the %d paid",
			amount, committed+amount, s.AmountMinor)
	}
	return nil
}

// askRefund sends payment.refund for a requested refund. An unanswered one stays requested and is asked
// again by the delivery job — the processor may have given the money back before the answer was lost, so
// calling it failed would invite a second refund. The refund's id is the gateway's idempotency key.
func (p *Payments) askRefund(ctx context.Context, id string) {
	p.mu.Lock()
	rf := p.refunds[id]
	if rf.Status != nilda.RefundRequested {
		delete(p.refundDue, id)
		p.mu.Unlock()
		return
	}
	req := nilda.PaymentRefundRequest{Refund: *rf, Session: *p.sessions[rf.Session]}
	p.mu.Unlock()

	var res nilda.PaymentRefundResult
	g, ok := p.installedGateway(req.Session.Gateway)
	err := fmt.Errorf("gateway %q is not installed", req.Session.Gateway)
	if ok {
		payload, _ := json.Marshal(req)
		gctx, cancel := context.WithTimeout(ctx, gatewayHookBudget)
		var out []byte
		out, _, err = nilda.DispatchPaymentGatewayHook(gctx, nil, g, nilda.HookPaymentRefund, payload)
		cancel()
		if err == nil {
			err = json.Unmarshal(out, &res)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil {
		p.refundDue[id] = true
		return
	}
	delete(p.refundDue, id)
	// The answer may arrive after a webhook already settled the refund; then it describes a moment past.
	if !nilda.RefundCanMove(rf.Status, res.Status) {
		return
	}
	if res.ProviderRef != "" {
		rf.ProviderRef = res.ProviderRef
	}
	if res.Status == nilda.RefundRejected {
		rf.FailureCode, rf.FailureMessage = res.FailureCode, res.FailureMessage
	}
	p.moveRefund(rf, res.Status)
}

// moveRefund changes a refund's status by the machine, keeps the session's refunded total, and owes the
// consumer the news. Callers hold p.mu.
func (p *Payments) moveRefund(rf *nilda.PaymentRefund, to string) bool {
	if rf.Status == to || !nilda.RefundCanMove(rf.Status, to) {
		return false
	}
	rf.Status = to
	rf.UpdatedAt = time.Now().UTC()
	if to == nilda.RefundResolved {
		p.sessions[rf.Session].RefundedMinor += rf.AmountMinor
	}
	p.owe(owed{session: rf.Session, refund: rf.ID})
	return true
}

// reportRefund applies a gateway's resolve or reject to a refund of a session routed to it.
func (p *Payments) reportRefund(caller, id, verb string, body []byte) (any, error) {
	if err := p.may(caller, "payment_gateway"); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	rf, ok := p.refunds[id]
	if !ok || p.sessions[rf.Session].Gateway != caller {
		return nil, notFound("no refund %q", id)
	}
	var to string
	switch verb {
	case "resolve":
		var r struct {
			ProviderRef string `json:"provider_ref"`
		}
		if len(body) > 0 {
			if err := json.Unmarshal(body, &r); err != nil {
				return nil, invalid("the body is not a refund resolution: %v", err)
			}
		}
		if len(r.ProviderRef) > maxProviderRef {
			return nil, invalid("a processor's reference is at most %d bytes", maxProviderRef)
		}
		if r.ProviderRef != "" && rf.Status != nilda.RefundResolved && rf.Status != nilda.RefundRejected {
			rf.ProviderRef = r.ProviderRef
		}
		to = nilda.RefundResolved
	case "reject":
		var r nilda.PaymentRejection
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, invalid("the body is not a rejection: %v", err)
		}
		if err := r.Validate(); err != nil {
			return nil, invalid("%v", err)
		}
		if rf.Status != nilda.RefundRejected && nilda.RefundCanMove(rf.Status, nilda.RefundRejected) {
			rf.FailureCode, rf.FailureMessage = r.FailureCode, r.FailureMessage
			if r.ProviderRef != "" {
				rf.ProviderRef = r.ProviderRef
			}
		}
		to = nilda.RefundRejected
	default:
		return nil, notFound("no such report %q", verb)
	}
	if rf.Status == to {
		return *rf, nil
	}
	if !p.moveRefund(rf, to) {
		return nil, &apiError{http.StatusConflict, "CONFLICT",
			fmt.Sprintf("this refund is already %s; that report cannot change it", rf.Status)}
	}
	delete(p.refundDue, id)
	return *rf, nil
}

// ---- answers, in Core's envelope -------------------------------------------------------------------------

// apiError is one refusal, with the status and the code Core's error envelope carries.
type apiError struct {
	status  int
	code    string
	message string
}

func (e *apiError) Error() string { return e.message }

func invalid(format string, a ...any) error {
	return &apiError{http.StatusUnprocessableEntity, "VALIDATION", fmt.Sprintf(format, a...)}
}

func notFound(format string, a ...any) error {
	return &apiError{http.StatusNotFound, "NOT_FOUND", fmt.Sprintf(format, a...)}
}

func conflict(s *nilda.PaymentSession) error {
	return &apiError{http.StatusConflict, "CONFLICT",
		fmt.Sprintf("this payment is already %s; that report cannot change it", s.Status)}
}

func answer(w http.ResponseWriter, status int, v any, err error) {
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": v})
}

func writeError(w http.ResponseWriter, err error) {
	ae, ok := err.(*apiError)
	if !ok {
		ae = &apiError{http.StatusInternalServerError, "INTERNAL", "internal error"}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(ae.status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": ae.code, "message": ae.message}})
}

// newID is a random UUID, the shape Core's ids have: a plugin that stores one somewhere with a length limit
// meets the real length here.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---- webhooks and a gateway to test a consumer with -------------------------------------------------------

// Webhook delivers one POST to your plugin's HTTP handler the way Core's proxy delivers a path your manifest
// declares in webhook_paths: the body byte for byte — a processor's signature is over those exact bytes — the
// caller's headers as sent, and no X-Nilda-* header at all, since Core strips any a caller sends and adds no
// identity on a webhook path. path is the whole path your server sees, route prefix included
// ("/stripe/webhook").
func Webhook(h http.Handler, path string, body []byte, header http.Header) *http.Response {
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	for k, vs := range header {
		if strings.HasPrefix(strings.ToLower(k), "x-nilda-") {
			continue
		}
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

// StubGateway is a gateway whose answers a test sets, for testing a CONSUMER without writing one.
type StubGateway struct {
	// Methods is what it offers. Nil offers one: "card", in any currency, able to refund.
	Methods []nilda.PaymentMethod
	// Start answers every payment.start. The zero value sends the payer to https://pay.test/<session id>.
	Start nilda.PaymentStartResult
	// Refund answers every payment.refund. The zero value says pending.
	Refund nilda.PaymentRefundResult
}

// DescribePayments answers with Methods.
func (g *StubGateway) DescribePayments(context.Context) ([]nilda.PaymentMethod, error) {
	if g.Methods == nil {
		return []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true}}, nil
	}
	return g.Methods, nil
}

// StartPayment answers with Start.
func (g *StubGateway) StartPayment(_ context.Context, s nilda.PaymentSession) (nilda.PaymentStartResult, error) {
	if g.Start.Status == "" {
		return nilda.PaymentStartResult{Status: nilda.PaymentRedirected, RedirectURL: "https://pay.test/" + s.ID,
			ProviderRef: "stub_" + s.ID}, nil
	}
	return g.Start, nil
}

// RefundPayment answers with Refund.
func (g *StubGateway) RefundPayment(context.Context, nilda.PaymentRefund, nilda.PaymentSession) (nilda.PaymentRefundResult, error) {
	if g.Refund.Status == "" {
		return nilda.PaymentRefundResult{Status: nilda.RefundPending}, nil
	}
	return g.Refund, nil
}
