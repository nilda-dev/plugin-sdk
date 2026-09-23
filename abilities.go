package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Abilities: telling an AI agent what your plugin can do.
//
// A hook is the site calling you. An ability is the reverse — you telling the site's agent that an action
// exists, so when the owner says "add these fifty products" the model has something to call. Without one,
// the only actions in the world are the ones Core itself ships, however much your plugin knows how to do.
//
// Declare them from Init and Core turns each into an agent tool named "<your-plugin-key>.<name>":
//
//	func (p *Shop) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
//	    return nilda.InitResult{
//	        Abilities: []nilda.Ability{{
//	            Name:        "create_product",
//	            Label:       "Create a product",
//	            Description: "Add a product to the shop. Use when the owner asks to list something for sale.",
//	            Class:       nilda.ClassWrite,
//	            InputSchema: nilda.ObjectSchema(map[string]any{
//	                "title": map[string]any{"type": "string"},
//	                "price": map[string]any{"type": "number"},
//	            }, "title", "price"),
//	            Run: p.createProduct,
//	        }},
//	    }, nil
//	}
//
// Requires the `abilities` capability in plugin.json — its own, separate from `hooks`, because an ability
// is an action a model can take with no human in the loop and the owner granting it should be agreeing to
// exactly that.
//
// Run is called through the ordinary hook path, so nothing new has to be wired: if you return abilities
// with a Run function, Serve dispatches them for you and your HandleHook never sees them.

// Class is an ability's risk class. Core uses it the way it does for its own tools to decide which
// connectors may call the ability at all, and to apply the owner's destructive-action policy (a `block`
// refuses the class outright; a `dryrun` stops it from applying).
//
// What Core does NOT give a plugin's ability yet is its own tools' preview-then-confirm step — a staged
// change the owner approves before it applies. There is no Preview in this contract, so a destructive
// ability runs when it is called. Design one for that: take an exact identifier (an id, a code), never a
// name a model could mismatch, and make a repeated call change nothing the first did not.
//
// Declare the TRUEST class, not the most convenient one: a lower class does not make an action safer, it
// only makes it reachable by connectors the owner meant to keep on a short leash. A class Core does not
// recognise is treated as the most restricted one.
type Class string

// The classes, in Core's order from least to most restricted. A guard in this package holds the list equal
// to Core's own (core's internal/agent/catalog.go).
const (
	ClassRead        Class = "read"        // returns information, changes nothing
	ClassAnalyze     Class = "analyze"     // audits the owner's own site — grades it, lists its problems; changes nothing
	ClassAdditive    Class = "additive"    // produces a draft or a suggestion; nothing goes live
	ClassWrite       Class = "write"       // edits live data
	ClassPublish     Class = "publish"     // makes something public, or takes it down
	ClassStructural  Class = "structural"  // changes the shape of the data model
	ClassDestructive Class = "destructive" // deletes, or acts in bulk
	ClassAccess      Class = "access"      // touches roles, permissions or credentials
	ClassInfra       Class = "infra"       // maintenance, caches, backups, the install itself
)

// Ability is one action offered to an AI agent.
type Ability struct {
	// Name is the id within your plugin: lowercase letters, digits and underscores, e.g. "create_product".
	// The agent calls it as "<plugin-key>.<name>".
	Name string
	// Label is the human-readable name shown in the owner's tool list.
	Label string
	// Description is what the MODEL reads to decide whether this is the right tool. Write it for a reader
	// who knows nothing about your plugin: say what it does and when to use it. A vague description is an
	// ability that never gets called.
	Description string
	// Class is the risk class — see Class.
	Class Class
	// InputSchema is a JSON Schema object describing the arguments. Required: an agent that cannot see the
	// shape of your input will call you with the wrong thing. Build one with ObjectSchema.
	InputSchema json.RawMessage
	// ReadOnly is an advisory hint to MCP clients. It changes no gate; Class does that.
	ReadOnly bool
	// Run executes the ability. Optional — leave it nil to handle the dispatch yourself in HandleHook,
	// where the hook name is AbilityHook(Name).
	//
	// IT HAS CORE'S PER-CALL BUDGET — 5 seconds by default (the site's PLUGIN_CALL_TIMEOUT) — and ctx is
	// cancelled past it; a call that overruns counts toward the failures that switch a plugin off. "Add these
	// fifty products" does not fit, and half of it is worse than none. Work that long ANSWERS FAST AND FINISHES
	// IN THE BACKGROUND: start it on a context of your own, return what was started ("importing 50 products"),
	// and make the result visible where the owner looks — your admin list page, an event, a row in your table.
	Run func(ctx context.Context, args json.RawMessage) (any, error)
}

// AbilityHook is the hook name Core dispatches an ability under. Only needed if you handle abilities in
// HandleHook yourself instead of setting Ability.Run.
func AbilityHook(name string) string { return "ability:" + name }

// ObjectSchema builds the JSON Schema object an ability needs, from a property map and the names of the
// required ones. It exists so declaring an ability does not start with hand-writing JSON:
//
//	nilda.ObjectSchema(map[string]any{
//	    "title": map[string]any{"type": "string", "description": "The product name."},
//	}, "title")
func ObjectSchema(properties map[string]any, required ...string) json.RawMessage {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	b, err := json.Marshal(schema)
	if err != nil {
		// Unreachable for map[string]any of JSON-able values; a broken schema is better empty than
		// silently half-formed, and Core refuses an ability whose schema is not an object.
		return nil
	}
	return b
}

// abilityRunners indexes the abilities that brought their own Run, so Serve can dispatch them. The FIRST
// declaration of a name wins, as it does in Core's tool list — Serve refuses a duplicate before this runs
// (duplicateAbility), so this only keeps the two sides agreeing if that check is ever bypassed.
func abilityRunners(list []Ability) map[string]func(context.Context, json.RawMessage) (any, error) {
	out := map[string]func(context.Context, json.RawMessage) (any, error){}
	seen := map[string]bool{}
	for _, a := range list {
		if seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		if a.Run != nil {
			out[a.Name] = a.Run
		}
	}
	return out
}

// duplicateAbility refuses two abilities with one Name (the 2026-09-23 plugin hunt's M5). Core offers the
// agent the FIRST declaration — its description, its schema, its class — while the runner map kept the LAST
// one's Run, so the model read one action and a different one ran. A name is the whole identity of an
// ability, so this stops Init: a plugin that cannot start names its mistake; one that starts runs the wrong
// code with no error anywhere.
func duplicateAbility(list []Ability) error {
	seen := map[string]bool{}
	for _, a := range list {
		if seen[a.Name] {
			return fmt.Errorf("two abilities are named %q — each needs its own name, or the agent reaches only one of them", a.Name)
		}
		seen[a.Name] = true
	}
	return nil
}

// dispatchAbility runs a declared ability if the hook names one AND the author gave it a Run.
//
// Returns handled=false for any other hook — and for an `ability:` hook with NO runner, which is the
// documented way to handle abilities yourself:
//
//	Abilities: []nilda.Ability{{Name: "reindex", …}},   // no Run
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if hook == nilda.AbilityHook("reindex") { … }
//	}
//
// It used to answer handled=true with "no runner for ability", on the reasoning that a reserved hook name
// must never reach the author. That reasoning made `AbilityHook` — a function exported for exactly this
// path — unreachable, made `Run` "optional" only in the sense that omitting it broke the ability, and
// contradicted three separate doc comments including this file's own on `Ability.Run`. An author following
// the documentation declared an ability, handled it in HandleHook, and watched every agent call fail with
// an error naming their own ability.
//
// The confusion it guarded against cannot happen: Core dispatches `ability:<name>` only for abilities THIS
// plugin declared at Init, so the only names that arrive are the author's own.
func dispatchAbility(ctx context.Context, runners map[string]func(context.Context, json.RawMessage) (any, error),
	hook string, payload []byte) (out []byte, handled bool, err error) {
	name, ok := strings.CutPrefix(hook, "ability:")
	if !ok {
		return nil, false, nil
	}
	run, ok := runners[name]
	if !ok {
		// Declared without a Run — the author handles it themselves. Fall through.
		return nil, false, nil
	}
	res, err := run(ctx, payload)
	if err != nil {
		return nil, true, err
	}
	if res == nil {
		return []byte(`{"ok":true}`), true, nil
	}
	b, err := json.Marshal(res)
	if err != nil {
		return nil, true, fmt.Errorf("ability %q returned a result that is not JSON: %w", name, err)
	}
	return b, true, nil
}
