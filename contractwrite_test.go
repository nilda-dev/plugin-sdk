package nilda

import (
	"encoding/json"
	"strings"
	"testing"
)

// writeContract is a small write contract, as docs/WRITE_COMMANDS.md draws it, with edit applied to its decoded tree.
func writeContract(t *testing.T, edit func(c map[string]any)) []byte {
	t.Helper()
	answer := map[string]any{"type": "object", "properties": map[string]any{
		"record_id": map[string]any{"type": "string"}, "already": map[string]any{"type": "boolean"}},
		"required": []any{"record_id", "already"}}
	c := map[string]any{
		"name": "shop.example/v1", "description": "An example of write commands.", "status": "experimental",
		"methods": []any{
			map[string]any{"name": "note.set", "answered_by": "provider", "description": "Sets a note.",
				"retry_class": "answer", "fallback": "block", "idempotent": true, "agent": "write",
				"input":  map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
				"output": answer,
				"write":  map[string]any{"record": "order.note", "undo": "note.undo", "emits": "shop.note.set"}},
			map[string]any{"name": "note.undo", "answered_by": "provider", "description": "Takes a note back.",
				"retry_class": "answer", "fallback": "block", "idempotent": true, "agent": "write",
				"input": map[string]any{"type": "object", "properties": map[string]any{"record_id": map[string]any{"type": "string"}},
					"required": []any{"record_id"}},
				"output": answer,
				"write":  map[string]any{"record": "order.note", "emits": "shop.note.undone"}},
		},
	}
	if edit != nil {
		edit(c)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func method(c map[string]any, i int) map[string]any { return c["methods"].([]any)[i].(map[string]any) }

// A WRITE COMMAND KEEPS ITS PROMISES OR THE CONTRACT IS REFUSED (D0, docs/WRITE_COMMANDS.md §2), and an experimental
// contract says so.
func TestAWriteContractIsHeldToTheWriteRules(t *testing.T) {
	c, err := ParseContract(writeContract(t, nil))
	if err != nil {
		t.Fatalf("the example is refused: %v", err)
	}
	if !c.Experimental() {
		t.Fatal("status experimental does not read as experimental")
	}
	if m, _ := c.Method("note.set"); m.Write == nil || m.Write.Undo != "note.undo" || m.Write.Emits != "shop.note.set" {
		t.Fatalf("the write part was not read: %+v", m.Write)
	}
	cases := map[string]struct {
		edit func(c map[string]any)
		want string
	}{
		"unknown status":    {func(c map[string]any) { c["status"] = "beta" }, "status"},
		"not idempotent":    {func(c map[string]any) { method(c, 0)["idempotent"] = false }, "not idempotent"},
		"skipped":           {func(c map[string]any) { method(c, 0)["fallback"] = "skip" }, "never skipped"},
		"cached":            {func(c map[string]any) { method(c, 0)["fallback"] = "cached" }, "never skipped"},
		"a read tool":       {func(c map[string]any) { method(c, 0)["agent"] = "read" }, "read tool"},
		"no undo":           {func(c map[string]any) { delete(method(c, 0)["write"].(map[string]any), "undo") }, "names no undo"},
		"undo missing":      {func(c map[string]any) { method(c, 0)["write"].(map[string]any)["undo"] = "note.gone" }, "not a write command of the same record"},
		"undo other record": {func(c map[string]any) { method(c, 1)["write"].(map[string]any)["record"] = "order.tag" }, "same record"},
		"no record_id":      {func(c map[string]any) { method(c, 0)["output"].(map[string]any)["required"] = []any{"already"} }, "record_id and already"},
		"undo without id":   {func(c map[string]any) { delete(method(c, 1)["input"].(map[string]any), "required") }, "must require record_id"},
		"undo undoes":       {func(c map[string]any) { method(c, 1)["write"].(map[string]any)["undo"] = "note.set" }, ""},
		"no event":          {func(c map[string]any) { method(c, 0)["write"].(map[string]any)["emits"] = "" }, "emits"},
		"record not a name": {func(c map[string]any) { method(c, 0)["write"].(map[string]any)["record"] = "Order Note" }, "record"},
		"rides a core hook": {func(c map[string]any) { method(c, 0)["hook"] = "payment_start"; delete(method(c, 0), "fallback") }, ""},
	}
	for name, tc := range cases {
		raw := writeContract(t, tc.edit)
		_, err := ParseContract(raw)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refused for another reason: %v", name, err)
		}
	}
}

// A contract without a status is stable, and every contract this SDK ships keeps the write rules (it loads).
func TestShippedContractsAreStableUnlessTheySayOtherwise(t *testing.T) {
	all, err := Contracts()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range all {
		if c.Status == "" && c.Experimental() {
			t.Errorf("%s: no status reads as experimental", c.Name)
		}
	}
}
