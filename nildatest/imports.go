package nildatest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Imports is a fake of Core's doors for an import plugin (core internal/pluginimport: S4's import map and redirects, S5's
// safety copy), answering as Core answers — a plugin without the `import` capability is refused 403, a call past its bounds
// 422 — over what an earlier import made (Map), the addresses the site serves (Serve), the redirect rules written and the
// safety copies made:
//
//	imports := nildatest.NewImports()
//	imports.Map("wordpress", "shop.example#media:68", "media", mediaID) // what the content import made
//	core, _, srv := imports.Core("site_import", "import")
//	defer srv.Close()
//	// ... the plugin's import, with core.API().LookupImported / AddImportRedirects / ImportCheckpoint ...
//	rules := imports.Redirects()
//
// Core normalises a rule's paths before it compares them (seo.NormalizePath); the fake compares them as written, so a test
// writes them as the plugin sends them.
type Imports struct {
	mu       sync.Mutex
	mapped   map[[2]string]nilda.ImportFound // source, external id → what it became
	served   map[string]bool
	rules    []ImportRule
	copies   int
	noCopy   bool
	nextRule int
	nextCopy int
	lookedUp int
}

// ImportRule is one redirect rule the door wrote.
type ImportRule struct {
	ID, From, To string
}

// NewImports builds the doors over an empty import map.
func NewImports() *Imports {
	return &Imports{mapped: map[[2]string]nilda.ImportFound{}, served: map[string]bool{}}
}

// Map says an earlier import of source made externalID into kind (content, term, media, menu, menu_item) targetID.
func (p *Imports) Map(source, externalID, kind, targetID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mapped[[2]string{source, externalID}] = nilda.ImportFound{ExternalID: externalID, Found: true, Kind: kind, TargetID: targetID}
}

// Serve says the site serves path: a redirect from it is left as it was (written false), as Core leaves one.
func (p *Imports) Serve(path string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.served[path] = true
}

// Redirects is the rules the door holds, in the order they were written.
func (p *Imports) Redirects() []ImportRule {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ImportRule(nil), p.rules...)
}

// Checkpoints is how many safety copies were made.
func (p *Imports) Checkpoints() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.copies
}

// Lookups is how many lookup calls were answered.
func (p *Imports) Lookups() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lookedUp
}

// RefuseCheckpoints makes every safety copy fail (on: Core's 503), as an install that cannot make one answers.
func (p *Imports) RefuseCheckpoints(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.noCopy = on
}

// Core builds a fake Core for the plugin key, granted exactly the capabilities named, whose API answers the import doors from
// this fake. Close the server when done.
func (p *Imports) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(p.Handler(key, h))
	return nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted)), h, srv
}

// Handler is the doors' routes for the plugin key whose grants h holds — for a test that serves them beside other fakes behind
// one address (a reverse proxy routing /plugin-import/ here).
func (p *Imports) Handler(key string, h *Host) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/plugin-import/") {
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
			return
		}
		h.mu.Lock()
		granted := h.granted[nilda.CapImport]
		h.mu.Unlock()
		if !granted {
			writeError(w, &apiError{http.StatusForbidden, "FORBIDDEN",
				fmt.Sprintf("this plugin lacks the \"import\" capability; declare it in plugin.json (%s)", key)})
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		switch strings.TrimPrefix(r.URL.Path, "/plugin-import") {
		case "/map/lookup":
			p.lookup(w, raw)
		case "/redirects":
			p.addRules(w, raw)
		case "/redirects/remove":
			p.removeRules(w, raw)
		case "/checkpoint":
			p.checkpoint(w, raw)
		default:
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
		}
	})
}

func (p *Imports) lookup(w http.ResponseWriter, raw []byte) {
	var in struct {
		Source      string   `json:"source"`
		ExternalIDs []string `json:"external_ids"`
	}
	if json.Unmarshal(raw, &in) != nil || in.Source == "" || len(in.Source) > 200 || len(in.ExternalIDs) == 0 ||
		len(in.ExternalIDs) > nilda.MaxImportBatch {
		writeError(w, invalid("a lookup names a source and 1 to 200 ids"))
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lookedUp++
	out := make([]nilda.ImportFound, 0, len(in.ExternalIDs))
	for _, id := range in.ExternalIDs {
		f, ok := p.mapped[[2]string{in.Source, id}]
		if !ok || len(id) > 300 {
			f = nilda.ImportFound{ExternalID: id}
		}
		out = append(out, f)
	}
	answer(w, http.StatusOK, out, nil)
}

// localPath is Core's rule for an address a plugin may name: a path on this site.
func localPath(s string) bool {
	if s == "" || len(s) > 2000 || s[0] != '/' || strings.HasPrefix(s, "//") || strings.Contains(s, "\\") {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func (p *Imports) addRules(w http.ResponseWriter, raw []byte) {
	var in struct {
		Rules []nilda.ImportRedirect `json:"rules"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Rules) == 0 || len(in.Rules) > nilda.MaxImportBatch {
		writeError(w, invalid("a call carries 1 to 200 rules"))
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]nilda.ImportRedirectResult, 0, len(in.Rules))
	for _, r := range in.Rules {
		switch {
		case !localPath(r.From):
			out = append(out, nilda.ImportRedirectResult{Refused: "from_not_local"})
		case !localPath(r.To):
			out = append(out, nilda.ImportRedirectResult{Refused: "to_not_local"})
		case r.From == "/" || r.From == r.To || p.served[r.From] || p.ruleFrom(r.From):
			out = append(out, nilda.ImportRedirectResult{}) // left as it was
		default:
			p.nextRule++
			id := fmt.Sprintf("00000000-0000-4000-8000-%012d", p.nextRule)
			p.rules = append(p.rules, ImportRule{ID: id, From: r.From, To: r.To})
			out = append(out, nilda.ImportRedirectResult{ID: id, Written: true})
		}
	}
	answer(w, http.StatusOK, out, nil)
}

func (p *Imports) ruleFrom(from string) bool {
	for _, r := range p.rules {
		if r.From == from {
			return true
		}
	}
	return false
}

func (p *Imports) removeRules(w http.ResponseWriter, raw []byte) {
	var in struct {
		Rules []nilda.ImportRedirectRemoval `json:"rules"`
	}
	if json.Unmarshal(raw, &in) != nil || len(in.Rules) == 0 || len(in.Rules) > nilda.MaxImportBatch {
		writeError(w, invalid("a call carries 1 to 200 rules"))
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	type removed struct {
		Removed bool `json:"removed"`
	}
	out := make([]removed, 0, len(in.Rules))
	for _, r := range in.Rules {
		gone := false
		for i, have := range p.rules {
			if have.ID == r.ID && have.From == r.From && have.To == r.To { // only while it is still exactly that rule
				p.rules = append(p.rules[:i], p.rules[i+1:]...)
				gone = true
				break
			}
		}
		out = append(out, removed{Removed: gone})
	}
	answer(w, http.StatusOK, out, nil)
}

func (p *Imports) checkpoint(w http.ResponseWriter, raw []byte) {
	var in struct {
		Reason string `json:"reason"`
	}
	reason := ""
	if json.Unmarshal(raw, &in) == nil {
		reason = strings.TrimSpace(in.Reason)
	}
	if reason == "" || utf8.RuneCountInString(reason) > nilda.MaxImportReason {
		writeError(w, invalid("a safety copy names why it is made, in 1 to 200 characters"))
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.noCopy {
		writeError(w, &apiError{http.StatusServiceUnavailable, "UNAVAILABLE", "no safety copy could be made"})
		return
	}
	p.copies++
	p.nextCopy++
	answer(w, http.StatusOK, nilda.ImportSafetyCopy{ID: "ckpt-" + strconv.Itoa(p.nextCopy)}, nil)
}
