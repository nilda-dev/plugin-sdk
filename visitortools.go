package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Visitor tools: what a VISITOR's own AI agent may do on the public site through your plugin.
//
// An Ability acts for the site's OWNER, through an authenticated connector, under the owner's risk classes. A
// visitor tool acts for whoever is looking at the page — an anonymous stranger, or the assistant that stranger
// pointed at the site: "find me a linen shirt in M under €60 on this shop". Core publishes it beside its own public
// tools, in the page (WebMCP, navigator.modelContext) and on the site's public MCP, as "<plugin-key>.<name>", under
// the owner's one switch for the whole surface and a per-address rate limit.
//
//	func (p *Shop) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
//	    return nilda.InitResult{
//	        VisitorTools: []nilda.VisitorTool{{
//	            Name:        "search_products",
//	            Description: "Search this shop's products by words, and get each one's price, sizes and page.",
//	            Effect:      nilda.VisitorRead,
//	            InputSchema: nilda.ObjectSchema(map[string]any{"query": map[string]any{"type": "string"}}, "query"),
//	            Paths:       []string{"/shop"},
//	            Run:         p.searchForAVisitor,
//	        }},
//	    }, nil
//	}
//
// Requires the `visitor_tools` capability, and each tool declared in plugin.json's "visitor_tools" exactly as Init
// returns it (nildatest.ManifestVisitorTools writes and holds that list): the owner who grants it reads what a
// stranger's agent will be able to do.
//
// # The ceiling, and it is yours to keep
//
// A visitor tool may do ONLY what the same visitor could already do through your public pages, which is why there
// are two effects and no third: read what the page shows, or submit what a form on it submits. Core cannot see
// inside Run, so the way to keep the ceiling is REUSE — call the same code your public route calls, behind the same
// checks (stock, prices, the cart's own limits), and return what that page would show. A tool that reaches anything
// else is a second public door beside the first, and the narrower of two doors stops being true.
//
// Run gets no identity, on purpose: it acts for nobody, and a tool that answered a signed-in caller differently
// would grow a private surface on a public endpoint. VisitorCall carries the page, the caller's address and agent
// string for your anti-abuse checks only.

// VisitorEffect is what a visitor tool does: read or submit. There is no third.
type VisitorEffect string

const (
	// VisitorRead answers from what the public pages show; it changes nothing.
	VisitorRead VisitorEffect = "read"
	// VisitorSubmit hands the site what the visitor typed — what pressing a form's button on the page would.
	VisitorSubmit VisitorEffect = "submit"
)

// VisitorTool is one action a visitor's agent may take on the public site.
type VisitorTool struct {
	// Name is the id within your plugin, lowercase letters, digits and underscores in verb_noun form:
	// "search_products". Core publishes it as "<plugin-key>.<name>".
	Name string
	// Description is what a STRANGER's model reads, knowing nothing of this site beyond the page it is on: say what
	// the tool is for and what it returns.
	Description string
	// Effect is VisitorRead or VisitorSubmit.
	Effect VisitorEffect
	// InputSchema is the JSON Schema object of the arguments, required — it is the only thing between a stranger's
	// model and Run. Build it with ObjectSchema.
	InputSchema json.RawMessage
	// Paths are the site paths the tool is offered on, as prefixes ("/shop" covers /shop and /shop/product/mug);
	// empty offers it on every page. A tool offered where it means nothing is a tool a model calls by mistake.
	Paths []string
	// Run executes the tool for an anonymous caller, with Core's per-call budget. Optional — leave it nil to answer
	// VisitorToolHook(Name) in HandleHook yourself, with the VisitorCall JSON as the payload.
	Run func(ctx context.Context, call VisitorCall) (any, error)
}

// VisitorCall is what a visitor tool receives: the arguments, and what the request itself knew.
type VisitorCall struct {
	// Args is the raw JSON arguments the visitor's agent sent, already checked by Core to be a JSON object.
	Args json.RawMessage `json:"args,omitempty"`
	// Path is the page the agent was on, with its leading slash: "/shop/product/mug".
	Path string `json:"path"`
	// IP and UserAgent are the caller's, for your anti-abuse checks — never to vary the answer by who asked.
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
}

// VisitorToolHook is the hook name Core dispatches a visitor tool under. Only needed if you handle a tool in
// HandleHook yourself instead of setting VisitorTool.Run.
func VisitorToolHook(name string) string { return "visitor_tool:" + name }

// visitorToolName is the shape a tool's own name must have: verb_noun, lowercase.
var visitorToolName = regexp.MustCompile(`^[a-z][a-z0-9_]{1,62}$`)

// Validate says what is wrong with a tool's declaration, or nil — the checks Core makes again before it publishes
// one, run at Init so the author hears of them first.
func (t VisitorTool) Validate() error {
	if !visitorToolName.MatchString(t.Name) {
		return fmt.Errorf("visitor tool %q: a name is lowercase letters, digits and underscores, starting with a letter", t.Name)
	}
	if strings.TrimSpace(t.Description) == "" {
		return fmt.Errorf("visitor tool %q has no description — a stranger's model has nothing else to go on", t.Name)
	}
	if t.Effect != VisitorRead && t.Effect != VisitorSubmit {
		return fmt.Errorf("visitor tool %q: effect %q is neither %q nor %q", t.Name, t.Effect, VisitorRead, VisitorSubmit)
	}
	var schema map[string]any
	if json.Unmarshal(t.InputSchema, &schema) != nil || schema["type"] != "object" {
		return fmt.Errorf("visitor tool %q: the input schema must be a JSON Schema object (build it with ObjectSchema)", t.Name)
	}
	for _, p := range t.Paths {
		if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "?#") {
			return fmt.Errorf("visitor tool %q: path %q must start with \"/\" and carry no query or fragment", t.Name, p)
		}
	}
	return nil
}

// checkVisitorTools refuses a declaration Core would refuse, and two tools with one name, at Init.
func checkVisitorTools(list []VisitorTool) error {
	seen := map[string]bool{}
	for _, t := range list {
		if err := t.Validate(); err != nil {
			return err
		}
		if seen[t.Name] {
			return fmt.Errorf("two visitor tools are named %q — each needs its own name", t.Name)
		}
		seen[t.Name] = true
	}
	return nil
}

// visitorRunners indexes the tools that brought their own Run.
func visitorRunners(list []VisitorTool) map[string]func(context.Context, VisitorCall) (any, error) {
	out := map[string]func(context.Context, VisitorCall) (any, error){}
	for _, t := range list {
		if t.Run != nil {
			out[t.Name] = t.Run
		}
	}
	return out
}

// dispatchVisitorTool runs a declared tool if the hook names one with a Run; handled=false otherwise, so a tool
// declared without Run reaches HandleHook as VisitorToolHook(name).
func dispatchVisitorTool(ctx context.Context, runners map[string]func(context.Context, VisitorCall) (any, error),
	hook string, payload []byte) (out []byte, handled bool, err error) {
	name, ok := strings.CutPrefix(hook, "visitor_tool:")
	if !ok {
		return nil, false, nil
	}
	run, ok := runners[name]
	if !ok {
		return nil, false, nil
	}
	var call VisitorCall
	if err := json.Unmarshal(payload, &call); err != nil {
		return nil, true, fmt.Errorf("visitor tool %q: the call did not parse: %w", name, err)
	}
	res, err := run(ctx, call)
	if err != nil {
		return nil, true, err
	}
	if res == nil {
		return []byte(`{"ok":true}`), true, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, true, fmt.Errorf("visitor tool %q returned a result that is not JSON: %w", name, err)
	}
	return b, true, nil
}
