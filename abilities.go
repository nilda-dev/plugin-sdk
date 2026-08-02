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

// Class is an ability's risk class. Core uses it to decide which connectors may call the ability at all
// and whether a confirmation step is required, exactly as it does for its own tools.
//
// Declare the TRUEST class, not the most convenient one: a lower class does not make an action safer, it
// only makes it reachable by connectors the owner meant to keep on a short leash. A class Core does not
// recognise is treated as the most restricted one.
type Class string

const (
	ClassRead        Class = "read"        // returns information, changes nothing
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

// abilityRunners indexes the abilities that brought their own Run, so Serve can dispatch them.
func abilityRunners(list []Ability) map[string]func(context.Context, json.RawMessage) (any, error) {
	out := map[string]func(context.Context, json.RawMessage) (any, error){}
	for _, a := range list {
		if a.Run != nil {
			out[a.Name] = a.Run
		}
	}
	return out
}

// dispatchAbility runs a declared ability if the hook names one. Returns handled=false for any other
// hook, so the author's own HandleHook still sees everything that is not an ability.
func dispatchAbility(ctx context.Context, runners map[string]func(context.Context, json.RawMessage) (any, error),
	hook string, payload []byte) (out []byte, handled bool, err error) {
	name, ok := strings.CutPrefix(hook, "ability:")
	if !ok {
		return nil, false, nil
	}
	run, ok := runners[name]
	if !ok {
		// The hook names an ability this plugin did not declare a runner for. Handled=true regardless:
		// falling through would hand a reserved hook name to the author's HandleHook, which is not what
		// "ability:" means.
		return nil, true, fmt.Errorf("no runner for ability %q", name)
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
