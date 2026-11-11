package nilda

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// AN ABILITY'S OR A VISITOR TOOL'S ARGUMENTS ARE CHECKED AGAINST ITS InputSchema BEFORE THEY REACH YOU.
//
// The schema was the declaration and nothing held a call to it: whatever a model wrote — or, for a visitor tool, any
// anonymous stranger's agent — reached Run or HandleHook as raw JSON, wrong types, values past a declared maximum and
// missing required properties included (the 2026-11-11 review of the SDK, finding 10). Service contracts were checked
// on both sides; these were not. Serve now compiles each declared schema at Init and refuses a call whose arguments do
// not match it, before Run or your HandleHook sees them, as the plugin's answer (the refusal names where and which
// rule, never the value). Core may check too; this is the check a plugin can rely on whatever Core it runs under.
//
// WHO IS HELD. A visitor tool's call always: only a stranger's agent makes one. An ability's call when a MODEL wrote the
// arguments — CallerKind ai or mcp. Not Core's importer or a person's own press, whose arguments Core's own code builds:
// those bodies were never held to the plugin's schema, and refusing them here would stop an import that worked
// yesterday (the separate review of this change, 2026-11-11). Not a call from a Core that names no door either: such a
// Core marks its importer as nothing, and checks the assistant's calls itself.
//
// A schema this SDK cannot compile (one that names an external $ref, say) is LOGGED at Init and its calls go through
// unchecked, as they did before: a plugin that loaded yesterday keeps loading. ObjectSchema's always compile.

// argSchemas are the compiled input schemas, by hook name ("ability:<name>", "visitor_tool:<name>").
type argSchemas map[string]*jsonschema.Resolved

// compileArgSchemas compiles every declared input schema, logging the ones it cannot.
func compileArgSchemas(abilities []Ability, tools []VisitorTool) argSchemas {
	out := argSchemas{}
	add := func(hook string, raw json.RawMessage) {
		if len(raw) == 0 {
			return
		}
		var s jsonschema.Schema
		err := json.Unmarshal(raw, &s)
		var r *jsonschema.Resolved
		if err == nil {
			r, err = s.Resolve(nil)
		}
		if err != nil {
			defaultLogger().Warn("an input schema did not compile; its calls are not checked against it", "hook", hook, "error", err)
			return
		}
		out[hook] = r
	}
	for _, a := range abilities {
		if _, seen := out[AbilityHook(a.Name)]; !seen {
			add(AbilityHook(a.Name), a.InputSchema)
		}
	}
	for _, t := range tools {
		add(VisitorToolHook(t.Name), t.InputSchema)
	}
	return out
}

// check refuses a call whose arguments do not match its declared schema. A hook with no compiled schema passes, and so
// does an ability's call whose arguments no model wrote (kind is CallOf's CallerKind).
func (s argSchemas) check(hook, kind string, payload []byte) error {
	r, ok := s[hook]
	if !ok {
		return nil
	}
	if strings.HasPrefix(hook, "ability:") && kind != CallerKindAI && kind != CallerKindMCP {
		return nil
	}
	args := json.RawMessage(payload)
	if strings.HasPrefix(hook, "visitor_tool:") {
		var call VisitorCall
		if err := json.Unmarshal(payload, &call); err != nil {
			return fmt.Errorf("visitor tool %q: the call did not parse: %w", strings.TrimPrefix(hook, "visitor_tool:"), err)
		}
		args = call.Args
	}
	// No arguments is an empty object: a tool with nothing to ask is called with nothing.
	if t := strings.TrimSpace(string(args)); t == "" || t == "null" {
		args = json.RawMessage(`{}`)
	}
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return fmt.Errorf("%s: the arguments are not JSON", hook)
	}
	if err := r.Validate(v); err != nil {
		return fmt.Errorf("%s: the arguments do not match its input schema: %s", hook, schemaFailure(err))
	}
	return nil
}
