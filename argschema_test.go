package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// TestArgumentsAreHeldToTheInputSchema is the 2026-11-11 review's finding 10.
func TestArgumentsAreHeldToTheInputSchema(t *testing.T) {
	ran := 0
	schema := json.RawMessage(`{"type":"object","properties":{"limit":{"type":"integer","maximum":50}},"required":["limit"]}`)
	s := &pluginServer{handler: &recordingHandler{}}
	if _, err := s.wire(InitResult{
		Abilities: []Ability{{Name: "list", Label: "List", Description: "d", Class: ClassRead, InputSchema: schema,
			Run: func(context.Context, json.RawMessage) (any, error) { ran++; return nil, nil }}},
		VisitorTools: []VisitorTool{{Name: "search_things", Description: "d", Effect: VisitorRead, InputSchema: schema,
			Run: func(context.Context, VisitorCall) (any, error) { ran++; return nil, nil }}},
	}); err != nil {
		t.Fatal(err)
	}
	call := func(hook, payload string) error {
		_, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: hook, Payload: []byte(payload),
			CallerKind: CallerKindAI})
		return err
	}
	for hook, bad := range map[string][]string{
		AbilityHook("list"):              {`{"limit":5000}`, `{"limit":"5"}`, `{}`, ``},
		VisitorToolHook("search_things"): {`{"args":{"limit":5000},"path":"/"}`, `{"path":"/"}`},
	} {
		for _, p := range bad {
			err := call(hook, p)
			if err == nil {
				t.Fatalf("%s %s ran", hook, p)
			}
			if strings.Contains(err.Error(), "5000") {
				t.Fatalf("the refusal quotes the value: %v", err)
			}
		}
	}
	if ran != 0 {
		t.Fatalf("a refused call reached Run %d times", ran)
	}
	if err := call(AbilityHook("list"), `{"limit":5}`); err != nil {
		t.Fatal(err)
	}
	if err := call(VisitorToolHook("search_things"), `{"args":{"limit":5},"path":"/"}`); err != nil {
		t.Fatal(err)
	}
	if ran != 2 {
		t.Fatalf("a good call did not run (%d)", ran)
	}
}

// The arguments of an ability Core's own code builds — its importer's, a person's own press — or that a Core naming no
// door sends, are not held here: refusing them would stop an import that worked before (the review of 2026-11-11).
func TestOnlyAModelsArgumentsToAnAbilityAreHeld(t *testing.T) {
	ran := 0
	s := &pluginServer{handler: &recordingHandler{}}
	if _, err := s.wire(InitResult{Abilities: []Ability{{Name: "import_rows", Label: "Import", Description: "d",
		Class: ClassWrite, InputSchema: json.RawMessage(`{"type":"object","required":["rows"]}`),
		Run: func(context.Context, json.RawMessage) (any, error) { ran++; return nil, nil }}}}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{CallerKindImporter, CallerKindOwnerUI, ""} {
		if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("import_rows"),
			Payload: []byte(`{}`), CallerKind: kind}); err != nil {
			t.Fatalf("a %q call was refused: %v", kind, err)
		}
	}
	for _, kind := range []string{CallerKindAI, CallerKindMCP} {
		if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("import_rows"),
			Payload: []byte(`{}`), CallerKind: kind}); err == nil {
			t.Fatalf("a %q call with a missing required argument ran", kind)
		}
	}
	if ran != 3 {
		t.Fatalf("ran %d times, want 3", ran)
	}
}
