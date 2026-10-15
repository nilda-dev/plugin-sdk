package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

var searchTool = VisitorTool{
	Name: "search_products", Description: "Search this shop's products.", Effect: VisitorRead,
	InputSchema: ObjectSchema(map[string]any{"query": map[string]any{"type": "string"}}, "query"), Paths: []string{"/shop"},
}

// Init (its wire step) says what Core would refuse, so the author hears of it from their own plugin first.
func TestAVisitorToolCoreWouldRefuseStopsInit(t *testing.T) {
	bad := func(edit func(*VisitorTool)) VisitorTool { v := searchTool; edit(&v); return v }
	for what, tool := range map[string]VisitorTool{
		"a name with a capital": bad(func(v *VisitorTool) { v.Name = "Search" }),
		"no description":        bad(func(v *VisitorTool) { v.Description = " " }),
		"a write effect":        bad(func(v *VisitorTool) { v.Effect = "write" }),
		"no schema":             bad(func(v *VisitorTool) { v.InputSchema = nil }),
		"a relative path":       bad(func(v *VisitorTool) { v.Paths = []string{"shop"} }),
	} {
		s := &pluginServer{handler: &recordingHandler{}}
		if _, err := s.wire(InitResult{VisitorTools: []VisitorTool{tool}}); err == nil {
			t.Errorf("%s started", what)
		}
	}
	two := &pluginServer{handler: &recordingHandler{}}
	if _, err := two.wire(InitResult{VisitorTools: []VisitorTool{searchTool, searchTool}}); err == nil || !strings.Contains(err.Error(), "two visitor tools") {
		t.Errorf("two tools with one name: %v", err)
	}
}

// A tool with a Run is answered for the author with the call Core sent; one without reaches HandleHook.
func TestAVisitorToolIsDispatchedWithTheCallCoreSent(t *testing.T) {
	var got VisitorCall
	auto := searchTool
	auto.Run = func(_ context.Context, c VisitorCall) (any, error) {
		got = c
		return map[string]int{"found": 2}, nil
	}
	manual := searchTool
	manual.Name = "submit_review"
	h := &recordingHandler{answer: []byte(`{"by":"the author"}`)}
	s := &pluginServer{handler: h, visitorRun: visitorRunners([]VisitorTool{auto, manual})}
	payload, _ := json.Marshal(VisitorCall{Args: json.RawMessage(`{"query":"linen"}`), Path: "/shop", IP: "203.0.113.9"})
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: VisitorToolHook("search_products"), Payload: payload})
	if err != nil || string(res.Payload) != `{"found":2}` {
		t.Fatalf("the tool's answer: %s %v", res.GetPayload(), err)
	}
	if string(got.Args) != `{"query":"linen"}` || got.Path != "/shop" || got.IP != "203.0.113.9" || len(h.seen) != 0 {
		t.Errorf("the call: %+v, the author saw %v", got, h.seen)
	}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: VisitorToolHook("submit_review"), Payload: payload}); err != nil ||
		len(h.seen) != 1 || h.seen[0] != "visitor_tool:submit_review" {
		t.Errorf("a tool without a Run reaches HandleHook: %v %v", h.seen, err)
	}
}

// What Init returns is what reaches Core, every field.
func TestInitSendsTheVisitorToolsToCore(t *testing.T) {
	s := &pluginServer{handler: &recordingHandler{}}
	out, err := s.wire(InitResult{VisitorTools: []VisitorTool{searchTool}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.VisitorTools) != 1 {
		t.Fatalf("sent: %+v", out.VisitorTools)
	}
	v := out.VisitorTools[0]
	if v.Name != "search_products" || v.Effect != "read" || v.InputSchema != string(searchTool.InputSchema) ||
		len(v.Paths) != 1 || v.Paths[0] != "/shop" || v.Description == "" {
		t.Errorf("sent: %+v", v)
	}
}
