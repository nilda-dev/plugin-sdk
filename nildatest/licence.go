package nildatest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Licence is a fake of the plugin's own licence as Core answers it (EXPERIMENTAL, like nilda.Licence): Core's
// GET /plugin-licence and POST /plugin-licence/bind. It holds none until a test sets one; a bind is refused
// none_free until the test says otherwise; and it counts the binds.
//
//	fake := nildatest.NewLicence()
//	core, _, srv := fake.Core("shop", "admin_page")
//	defer srv.Close()
//	fake.Set(nilda.Licence{State: nilda.LicenceGrace, PaidUntil: paid, LiveUntil: live})
//	// ... your gate reads core.API().Licence(ctx) ...
//	fake.BindGrants(nilda.Licence{State: nilda.LicenceTrial, Source: "trial", Trial: true})
//	// ... the owner presses "go live", your code calls core.API().BindLicence(ctx) ...
//	if fake.Binds() != 1 { ... }
type Licence struct {
	mu      sync.Mutex
	licence nilda.Licence
	grant   *nilda.Licence // what a bind binds; nil: refused with reason
	reason  string
	fails   bool
	binds   int
}

// NewLicence builds a fake holding no licence, whose binds are refused none_free.
func NewLicence() *Licence {
	return &Licence{licence: nilda.Licence{State: nilda.LicenceNone}, reason: nilda.BindReasonNoneFree}
}

// Core builds a fake Core for the plugin key, granted exactly the capabilities named (the licence routes ask for none,
// but Core hands a plugin an API only when a capability grants one), whose API answers the licence routes from this
// fake. Close the server when done.
func (l *Licence) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(http.HandlerFunc(l.serve))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

// Set is the licence the site holds for the plugin from now on, as Core would judge it. A licence with no state is none.
func (l *Licence) Set(lic nilda.Licence) {
	if lic.State == "" {
		lic.State = nilda.LicenceNone
	}
	l.mu.Lock()
	l.licence = lic
	l.mu.Unlock()
}

// Licence is what the site holds for the plugin now.
func (l *Licence) Licence() nilda.Licence {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.licence
}

// BindRefuses makes every bind from now on answer not bound, with reason (one of nilda.BindReasons), the licence
// unchanged.
func (l *Licence) BindRefuses(reason string) {
	l.mu.Lock()
	l.grant, l.reason, l.fails = nil, reason, false
	l.mu.Unlock()
}

// BindGrants makes every bind from now on bind: the site holds lic and the answer carries it.
func (l *Licence) BindGrants(lic nilda.Licence) {
	if lic.State == "" {
		lic.State = nilda.LicenceNone
	}
	l.mu.Lock()
	l.grant, l.reason, l.fails = &lic, "", false
	l.mu.Unlock()
}

// BindFails makes every bind from now on a call that did not complete — nilda.dev did not answer, Core's 503 — which
// the plugin reads as an error, never as bound.
func (l *Licence) BindFails() {
	l.mu.Lock()
	l.grant, l.fails = nil, true
	l.mu.Unlock()
}

// Binds is how many bind requests reached this Core — a failed one counts each attempt the client made, its retries
// of the 503 included.
func (l *Licence) Binds() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.binds
}

func (l *Licence) serve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && path == "/plugin-licence":
		answer(w, http.StatusOK, l.licence, nil)
	case r.Method == http.MethodPost && path == "/plugin-licence/bind":
		l.binds++
		switch {
		case l.fails:
			writeError(w, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE", "nilda.dev did not answer"})
		case l.grant != nil:
			l.licence = *l.grant
			answer(w, http.StatusOK, nilda.BindResult{Bound: true, Licence: l.licence}, nil)
		default:
			answer(w, http.StatusOK, nilda.BindResult{Reason: l.reason, Licence: l.licence}, nil)
		}
	default:
		writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
	}
}
