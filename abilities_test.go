package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestObjectSchemaBuildsAValidJSONSchemaObject(t *testing.T) {
	raw := ObjectSchema(map[string]any{
		"title": map[string]any{"type": "string"},
		"price": map[string]any{"type": "number"},
	}, "title")

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	// Core REFUSES an ability whose schema is not an object, so this is the shape that decides whether a
	// plugin's ability appears at all.
	if got["type"] != "object" {
		t.Fatalf(`type = %v, want "object"`, got["type"])
	}
	props, ok := got["properties"].(map[string]any)
	if !ok || len(props) != 2 {
		t.Fatalf("properties lost: %v", got["properties"])
	}
	req, ok := got["required"].([]any)
	if !ok || len(req) != 1 || req[0] != "title" {
		t.Fatalf("required lost: %v", got["required"])
	}
}

func TestObjectSchemaWithNoPropertiesIsStillAnObject(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(ObjectSchema(nil), &got); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if got["type"] != "object" {
		t.Fatal("an argument-less ability must still declare an object schema")
	}
	if _, ok := got["properties"]; !ok {
		t.Fatal("properties must be present, even when empty")
	}
}

// The point of Ability.Run: declaring an ability and then forgetting to route its hook would be a plugin
// that advertises an action and fails every time an agent tries it.
func TestDeclaredRunIsDispatchedWithoutTheAuthorRoutingIt(t *testing.T) {
	var gotArgs string
	runners := abilityRunners([]Ability{{
		Name: "create_product",
		Run: func(_ context.Context, args json.RawMessage) (any, error) {
			gotArgs = string(args)
			return map[string]any{"id": "p1"}, nil
		},
	}})

	out, handled, err := dispatchAbility(context.Background(), runners,
		AbilityHook("create_product"), []byte(`{"title":"Mug"}`))
	if !handled {
		t.Fatal("an ability hook must be handled by the SDK, not passed through")
	}
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if gotArgs != `{"title":"Mug"}` {
		t.Fatalf("arguments were not passed through: %q", gotArgs)
	}
	if !strings.Contains(string(out), `"id":"p1"`) {
		t.Fatalf("result was not returned: %s", out)
	}
}

// Everything that is NOT an ability must still reach the author's own HandleHook.
func TestOrdinaryHooksArePassedThrough(t *testing.T) {
	_, handled, err := dispatchAbility(context.Background(), abilityRunners(nil), "content.saved", []byte(`{}`))
	if handled || err != nil {
		t.Fatalf("an ordinary hook must pass through untouched (handled=%v err=%v)", handled, err)
	}
}

// "ability:" is a reserved namespace. A hook in it that names an ability with no runner must NOT fall
// An `ability:` hook the author did not give a Run to falls THROUGH to their HandleHook. That is the
// documented way to handle abilities yourself, it is the only reason `AbilityHook` is exported, and it is
// what makes `Ability.Run` genuinely optional.
//
// This asserted the opposite until 2026-08-06 — handled=true with "no runner for ability" — which made the
// documented pattern fail at runtime with an error naming the author's own ability. The reserved-namespace
// worry it was written for cannot happen: Core dispatches `ability:<name>` only for abilities this plugin
// declared at Init, so the only names that arrive are the author's.
func TestAnAbilityWithNoRunReachesTheAuthorsHandleHook(t *testing.T) {
	out, handled, err := dispatchAbility(context.Background(), abilityRunners(nil), AbilityHook("manual"), nil)
	if handled {
		t.Fatal("an ability declared without a Run must reach the author's HandleHook")
	}
	if err != nil || out != nil {
		t.Fatalf("a fall-through answers nothing: out=%s err=%v", out, err)
	}
}

// An ability that returns nothing is a success, not an empty response the agent has to guess about.
func TestNilResultBecomesAnExplicitOK(t *testing.T) {
	runners := abilityRunners([]Ability{{
		Name: "reindex",
		Run:  func(context.Context, json.RawMessage) (any, error) { return nil, nil },
	}})
	out, handled, err := dispatchAbility(context.Background(), runners, AbilityHook("reindex"), nil)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	if string(out) != `{"ok":true}` {
		t.Fatalf("got %s, want an explicit ok", out)
	}
}

// An ability with no Run is a legitimate choice — the author handles it in HandleHook — so it must not be
// indexed as a runner.
func TestAbilityWithoutRunIsNotIndexed(t *testing.T) {
	runners := abilityRunners([]Ability{{Name: "manual"}})
	if _, ok := runners["manual"]; ok {
		t.Fatal("an ability with no Run must not be registered as a runner")
	}
}
