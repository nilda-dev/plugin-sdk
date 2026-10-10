package nildatest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Core's limits on a plugin's secrets (internal/pluginsecrets): 32 names, a value of 1–4096 bytes.
const (
	maxSecretNames = 32
	maxSecretValue = 4096
)

// Secrets is a fake of Core's /plugin-secrets routes — the place a plugin keeps a credential that has to survive a
// restart (the `secrets` capability) — answering as Core answers: each plugin sees only its own, a plugin that
// never declared `secrets` is refused 403, a bad name or value is 422, a name nobody set is 404, and a plugin
// already at 32 names may replace one but not add a 33rd.
//
//	sec := nildatest.NewSecrets()
//	core, _, srv := sec.Core("higgsfield", "secrets", "route")
//	defer srv.Close()
//	// ... run the code under test with core ...
//	got, _ := sec.Get("higgsfield", "refresh_token") // what Core would be holding
type Secrets struct {
	mu   sync.Mutex
	data map[string]map[string]string
}

// NewSecrets builds an empty secret store.
func NewSecrets() *Secrets { return &Secrets{data: map[string]map[string]string{}} }

// Core builds a fake Core for the plugin key, granted exactly the capabilities named, whose API answers the secret
// routes from this store under that plugin's own identity. Close the server when done.
func (s *Secrets) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(s.routes(key, h))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

// Get returns the secret Core would hold for plugin under name — what the plugin wrote, as the plugin sees it.
func (s *Secrets) Get(plugin, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[plugin][name]
	return v, ok
}

// Names returns the names plugin keeps, sorted. A secret's value is not in the list: no screen shows one.
func (s *Secrets) Names(plugin string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data[plugin]))
	for n := range s.data[plugin] {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Uninstall does what Core does when the owner removes the plugin: every secret it kept goes with it.
func (s *Secrets) Uninstall(plugin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, plugin)
}

func (s *Secrets) routes(caller string, h *Host) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) != 2 || parts[0] != "plugin-secrets" {
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
			return
		}
		h.mu.Lock()
		granted := h.granted["secrets"]
		h.mu.Unlock()
		if !granted {
			writeError(w, &apiError{http.StatusForbidden, "FORBIDDEN",
				fmt.Sprintf("plugin %q lacks the \"secrets\" capability — declare it in plugin.json", caller)})
			return
		}
		name := parts[1]
		if !validSecretName(name) {
			writeError(w, invalid("a secret's name is 1-64 lowercase letters, digits or underscores"))
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxSecretValue+512))
		s.mu.Lock()
		defer s.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			v, ok := s.data[caller][name]
			if !ok {
				writeError(w, notFound("no secret by that name"))
				return
			}
			answer(w, http.StatusOK, map[string]string{"value": v}, nil)
		case http.MethodPut:
			var in struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(body, &in); err != nil || in.Value == "" || len(in.Value) > maxSecretValue {
				writeError(w, invalid("a secret's value is 1-4096 bytes"))
				return
			}
			if _, replacing := s.data[caller][name]; !replacing && len(s.data[caller]) >= maxSecretNames {
				writeError(w, invalid("a plugin may keep at most 32 secrets"))
				return
			}
			if s.data[caller] == nil {
				s.data[caller] = map[string]string{}
			}
			s.data[caller][name] = in.Value
			answer(w, http.StatusOK, struct{}{}, nil)
		case http.MethodDelete:
			if _, ok := s.data[caller][name]; !ok {
				writeError(w, notFound("no secret by that name"))
				return
			}
			delete(s.data[caller], name)
			answer(w, http.StatusOK, struct{}{}, nil)
		default:
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
		}
	})
}
