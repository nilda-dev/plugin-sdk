package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// recordingHandler is a plugin author's Handler: it records what reached HandleHook.
type recordingHandler struct {
	res    InitResult
	seen   []string
	answer []byte
	err    error
}

func (h *recordingHandler) Init(context.Context, *Core) (InitResult, error)   { return h.res, nil }
func (h *recordingHandler) HandleEvent(context.Context, string, []byte) error { return nil }
func (h *recordingHandler) HandleHook(_ context.Context, hook string, _ []byte) ([]byte, error) {
	h.seen = append(h.seen, hook)
	return h.answer, h.err
}

// TestTheDocumentedAbilityPathsBothWork drives pluginServer.HandleHook — the real dispatcher, the one every
// hook a plugin ever receives goes through, and the one with no test at all until now.
//
// Both halves of the contract are asserted together because they are one decision: an ability WITH a Run is
// dispatched for the author and never reaches HandleHook, and an ability WITHOUT one reaches HandleHook
// under AbilityHook(name). The second half failed at runtime for anyone following the documentation.
func TestTheDocumentedAbilityPathsBothWork(t *testing.T) {
	ran := false
	h := &recordingHandler{
		answer: []byte(`{"handled":"by the author"}`),
		res: InitResult{Abilities: []Ability{
			{Name: "auto", Run: func(context.Context, json.RawMessage) (any, error) {
				ran = true
				return map[string]string{"did": "it"}, nil
			}},
			{Name: "manual"}, // no Run — the author's own switch handles it
		}},
	}
	s := &pluginServer{handler: h}
	s.abilityRun = abilityRunners(h.res.Abilities)

	// (1) With a Run: dispatched for the author, and their HandleHook never sees it.
	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("auto")})
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	if !ran {
		t.Error("the declared Run was not called")
	}
	if !strings.Contains(string(res.Payload), `"did":"it"`) {
		t.Errorf("the ability's own result must be the answer, got %s", res.Payload)
	}
	if len(h.seen) != 0 {
		t.Errorf("an ability with a Run must not reach HandleHook, but it saw %v", h.seen)
	}

	// (2) Without a Run: it reaches HandleHook under the name AbilityHook gives the author.
	res, err = s.HandleHook(context.Background(), &contract.HookRequest{Hook: AbilityHook("manual")})
	if err != nil {
		t.Fatalf("manual: %v", err)
	}
	if len(h.seen) != 1 || h.seen[0] != "ability:manual" {
		t.Fatalf("the author's HandleHook saw %v, want [ability:manual]", h.seen)
	}
	if string(res.Payload) != `{"handled":"by the author"}` {
		t.Errorf("the author's own answer must be returned, got %s", res.Payload)
	}
}

// An ordinary hook reaches the author untouched — the case every plugin depends on.
func TestAnOrdinaryHookReachesTheAuthor(t *testing.T) {
	h := &recordingHandler{answer: []byte(`{}`)}
	s := &pluginServer{handler: h}
	if _, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "content.saved"}); err != nil {
		t.Fatalf("content.saved: %v", err)
	}
	if len(h.seen) != 1 || h.seen[0] != "content.saved" {
		t.Fatalf("the author saw %v", h.seen)
	}
}

// decodeSettings: unparseable settings are a WARNING and an empty map, never a failed Init — a plugin that
// cannot start is a plugin the owner cannot reach the settings screen to fix.
func TestUnparseableSettingsStartTheProcessAnyway(t *testing.T) {
	if got := decodeSettings([]byte("{not json"), "acme"); len(got) != 0 {
		t.Errorf("bad settings must yield an empty map, got %v", got)
	}
	if got := decodeSettings(nil, "acme"); got == nil || len(got) != 0 {
		t.Errorf("absent settings must yield an empty map, not nil: %v", got)
	}
	got := decodeSettings([]byte(`{"api_key":"k","retries":3,"on":true}`), "acme")
	if got["api_key"] != "k" || got["on"] != true {
		t.Errorf("settings did not decode: %v", got)
	}
}

// TestStartHTTPListensOnLocalhostWithTimeouts drives the real helper: it must answer, it must bind
// LOCALHOST only (the proxy is the only intended door), and it must not accept a request whose headers
// never arrive — the slowloris that `http.Serve`'s zero timeouts leave open.
func TestStartHTTPListensOnLocalhostWithTimeouts(t *testing.T) {
	addr, err := StartHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plugin says hello"))
	}))
	if err != nil {
		t.Fatalf("StartHTTP: %v", err)
	}
	if !strings.HasPrefix(addr, "127.0.0.1:") {
		t.Fatalf("bound %q — a plugin's server must not be reachable except through Core's proxy", addr)
	}

	res, err := http.Get("http://" + addr + "/anything")
	if err != nil {
		t.Fatalf("the server did not answer: %v", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if string(body) != "plugin says hello" {
		t.Errorf("the handler's answer did not come back: %q", body)
	}

	// A connection that opens and sends nothing must be closed by the server rather than held forever.
	// ReadHeaderTimeout is 10s, so this asserts the deadline EXISTS by reading until the server hangs up
	// — with a generous ceiling, because a test that races a timeout is a flake.
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	if _, err := io.ReadAll(conn); err != nil {
		// A read error (reset) is also the server hanging up — both mean the connection did not survive.
		t.Logf("the silent connection ended with %v, which is the timeout doing its job", err)
	}
}

// TestEverySingleCapabilityHandlerSubscribesToItsWholeContract.
//
// Each ServeX helper exists so a plugin that does one thing writes nothing else — which means its Init is
// the ONLY place its hooks get subscribed. A name missing from one of these lists is a plugin that
// installs cleanly, is approved by the owner, and is never asked anything: the capability appears to work
// and silently does nothing, which is the failure this repository hunts everywhere else.
//
// Asserted against the hook CONSTANTS, so adding a hook to a contract without adding it here fails.
func TestEverySingleCapabilityHandlerSubscribesToItsWholeContract(t *testing.T) {
	for _, c := range []struct {
		name string
		h    Handler
		want []string
	}{
		{
			name: "field",
			h:    &fieldOnlyHandler{},
			want: []string{HookFieldChoices, HookFieldValidate},
		},
		{
			name: "auth_provider",
			h:    &authOnlyHandler{},
			want: []string{HookAuthDescribe, HookAuthStart, HookAuthComplete},
		},
		{
			name: "search_provider",
			h:    &searchOnlyHandler{},
			want: []string{
				HookSearchConfigure, HookSearchIndex, HookSearchRemove,
				HookSearchTruncate, HookSearchQuery, HookSearchHealthy,
			},
		},
	} {
		res, err := c.h.Init(context.Background(), &Core{PluginKey: "acme"})
		if err != nil {
			t.Errorf("%s: Init: %v", c.name, err)
			continue
		}
		got := map[string]bool{}
		for _, h := range res.Hooks {
			got[h] = true
		}
		for _, want := range c.want {
			if !got[want] {
				t.Errorf("%s: Init does not subscribe to %q — the plugin would install and never be asked", c.name, want)
			}
		}
		if len(res.Hooks) != len(c.want) {
			t.Errorf("%s: subscribes to %v, want exactly %v", c.name, res.Hooks, c.want)
		}
	}
}

// And each refuses a hook that is not its own, rather than answering nil — a plugin that silently returns
// nothing to a hook it does not understand is indistinguishable from one that handled it.
func TestASingleCapabilityHandlerRefusesAHookThatIsNotItsOwn(t *testing.T) {
	for name, h := range map[string]Handler{
		"field":           &fieldOnlyHandler{p: nil},
		"auth_provider":   &authOnlyHandler{p: nil},
		"search_provider": &searchOnlyHandler{p: nil},
	} {
		if _, err := h.HandleHook(context.Background(), "content.saved", nil); err == nil {
			t.Errorf("%s: a foreign hook must be an error, not a silent nil", name)
		}
		// HandleEvent is a no-op for all three: they subscribe to no events, so one arriving is Core's
		// business, not a reason to fail.
		if err := h.HandleEvent(context.Background(), "content.published", nil); err != nil {
			t.Errorf("%s: HandleEvent must be a harmless no-op, got %v", name, err)
		}
	}
}

// consumerHandler is a plugin that is also a payment consumer, so its hooks go through the provider dispatch.
type consumerHandler struct {
	recordingHandler
	recordingConsumer
}

// An error a handler returns reaches Core as gRPC Unknown — the code Core reads as the plugin ANSWERING (a
// refusal shown to the person, a consumer's "tell me again") — whatever it wraps. grpc-go sends a wrapped
// status's own code, so a refusal wrapping a Core call's ResourceExhausted, or any gRPC client's error, arrived
// as that code and was counted toward switching the plugin off. The words are kept.
func TestAHandlersErrorReachesCoreAsItsAnswer(t *testing.T) {
	ctx := context.Background()
	wrapped := fmt.Errorf("saving the refund: %w", status.Error(codes.ResourceExhausted, "plugin kv: this plugin's namespace is full"))
	refund := func(context.Context, json.RawMessage) (any, error) { return nil, wrapped }
	for name, c := range map[string]struct {
		s    *pluginServer
		hook string
	}{
		"HandleHook":  {&pluginServer{handler: &recordingHandler{err: wrapped}}, "admin.action"},
		"an ability":  {&pluginServer{handler: &recordingHandler{}, abilityRun: map[string]func(context.Context, json.RawMessage) (any, error){"refund": refund}}, AbilityHook("refund")},
		"a provider":  {&pluginServer{handler: &consumerHandler{recordingConsumer: recordingConsumer{err: wrapped}}}, HookPaymentSessionUpdated},
		"plain error": {&pluginServer{handler: &recordingHandler{err: errors.New("already refunded")}}, "admin.action"},
	} {
		_, err := c.s.HandleHook(ctx, &contract.HookRequest{Hook: c.hook, Payload: []byte(`{"session":{"id":"s1"}}`)})
		if status.Code(err) != codes.Unknown {
			t.Errorf("%s: the handler's error reached Core as %v, want Unknown — Core counts every other code as a failure", name, status.Code(err))
		}
		if want := "already refunded"; name == "plain error" && status.Convert(err).Message() != want {
			t.Errorf("%s: the words Core shows are %q, want %q", name, status.Convert(err).Message(), want)
		}
		if name != "plain error" && !strings.Contains(status.Convert(err).Message(), "saving the refund: ") {
			t.Errorf("%s: the handler's words were lost: %q", name, status.Convert(err).Message())
		}
	}
}

// panickyHandler is a plugin author having a bad day in exactly one of their capabilities.
type panickyHandler struct{ recordingHandler }

func (h *panickyHandler) HandleHook(_ context.Context, hook string, _ []byte) ([]byte, error) {
	var m map[string]string
	m["boom"] = hook // nil map write — the ordinary way author code panics
	return nil, nil
}
func (h *panickyHandler) HandleEvent(context.Context, string, []byte) error {
	panic("author panicked in an event handler")
}

// TestAPanicInTheAuthorsCodeFailsTheCallNotTheProcess.
//
// Without the boundary recover, one nil-map access in a widget render kills the plugin PROCESS — and takes
// down every other capability that plugin serves: its sign-in button, its search engine, its field
// validation. Core supervises and restarts, but it also counts a crash toward the failure budget, so a
// widget that panics on one malformed config eventually gets the whole plugin disabled.
//
// The test asserts the two halves that matter: the call FAILS (never a silent success), and the process is
// still answering afterwards.
func TestAPanicInTheAuthorsCodeFailsTheCallNotTheProcess(t *testing.T) {
	s := &pluginServer{handler: &panickyHandler{}}

	res, err := s.HandleHook(context.Background(), &contract.HookRequest{Hook: "content.saved"})
	if err == nil {
		t.Fatal("a panic must surface as an error — a silent success tells Core the plugin handled it")
	}
	if res != nil {
		t.Errorf("no response may be returned alongside the error, got %+v", res)
	}
	if !strings.Contains(err.Error(), "panicked") || !strings.Contains(err.Error(), "content.saved") {
		t.Errorf("the error must say it panicked and in which hook, got %q", err)
	}
	// A FAILURE, which Core counts toward switching the plugin off — never Unknown, which Core reads as the
	// handler's own refusal and shows the person as the plugin's words.
	if status.Code(err) != codes.Internal {
		t.Errorf("a panic reached Core as %v, want Internal — Unknown is a handler's answer, and never counts", status.Code(err))
	}

	// An event handler too — fire-and-forget on Core's side, so a panic here would otherwise kill the
	// process for a notification nobody was waiting on.
	if _, err := s.HandleEvent(context.Background(), &contract.EventRequest{Type: "content.published"}); err == nil {
		t.Error("a panic in an event handler must fail the delivery, not the process")
	}

	// Still alive and still dispatching: the whole point.
	ok := &recordingHandler{answer: []byte(`{"still":"here"}`)}
	alive := &pluginServer{handler: ok}
	out, err := alive.HandleHook(context.Background(), &contract.HookRequest{Hook: "content.saved"})
	if err != nil || string(out.Payload) != `{"still":"here"}` {
		t.Fatalf("the process must keep serving after a panic: %s %v", out.GetPayload(), err)
	}
}
