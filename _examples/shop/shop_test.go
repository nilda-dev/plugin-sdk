package shop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/nildatest"
)

// This file is the point of DX6. The example next door is the code the documentation shows, and these tests
// run it — so an SDK change that would have quietly made the docs wrong breaks the build instead.
//
// It is also the worked demonstration of nildatest: everything here runs with `go test` and nothing else.

// fakeCore stands in for Core's API. Recording the requests is what lets an author assert the thing they
// actually care about — "did it create the product, at the right path, with the right body" — rather than
// only that no error came back.
type fakeCore struct {
	requests []string
	bodies   []string
}

func (f *fakeCore) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.requests = append(f.requests, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		f.bodies = append(f.bodies, string(body))

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"data":{"id":"c1","author_id":"plugin-account"}}`))
		default:
			// Core's offset page: one page of rows, the whole count in meta.total (api/rest/crud.go).
			_, _ = w.Write([]byte(`{"data":[{"id":"c1"}],"meta":{"page":1,"size":1,"total":37}}`))
		}
	})
}

func TestInitRefusesWithoutAPIAccess(t *testing.T) {
	// hooks only: no capability implying API access, so core.API() is nil.
	core, _ := nildatest.New("shop", "hooks")
	_, err := (&Shop{}).Init(context.Background(), core)
	if err == nil {
		t.Fatal("Init succeeded without API access — the failure would surface later, on the first write")
	}
	// The message must name the capability to add, since the person reading it is whoever installed the
	// plugin, not its author.
	if !strings.Contains(err.Error(), "content.write") {
		t.Fatalf("the error does not say what to add: %v", err)
	}
}

// manifest is the example's plugin.json, as far as these tests read it.
type manifest struct {
	Key          string   `json:"key"`
	Capabilities []string `json:"capabilities"`
	AdminPages   []struct {
		Kind   string `json:"kind"`
		Fields []struct {
			Key string `json:"key"`
		} `json:"fields"`
	} `json:"admin_pages"`
}

func readManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("plugin.json: %v", err)
	}
	return m
}

// The manifest declares what the example uses, and nothing it does not (the 2026-09-24 review of the SDK).
// The example read `notify_email` with no settings page declaring it — and Core delivers only the fields a
// settings page declares, so the example, copied as it was, never mailed anyone and said nothing; its test
// passed only because SetSettings put in a key Core would never send.
func TestTheManifestDeclaresWhatTheExampleUses(t *testing.T) {
	m := readManifest(t)
	raw, err := os.ReadFile("shop.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	settings := map[string]bool{}
	for _, pg := range m.AdminPages {
		if pg.Kind == "settings" {
			for _, f := range pg.Fields {
				settings[f.Key] = true
			}
		}
	}
	read := regexp.MustCompile(`Setting\("([a-z0-9_]+)"\)`).FindAllStringSubmatch(src, -1)
	if len(read) == 0 {
		t.Fatal("shop.go reads no setting — repoint this test")
	}
	for _, s := range read {
		if !settings[s[1]] {
			t.Errorf("shop.go reads the setting %q, and no settings page in plugin.json declares it — Core never delivers it", s[1])
		}
	}

	// Each capability the code reaches for, by the call that needs it — and each declared one used.
	uses := map[string]string{
		"hooks":         `Hooks:  []string{"content.saved"}`,
		"events":        ".Emit(ctx,",
		"schedule":      "Schedules: []nilda.Schedule{",
		"kv":            ".KVIncr(ctx,",
		"email":         ".SendEmail(ctx,",
		"content.write": `API().Post(ctx, "/content/product"`,
		"admin_page":    `.Setting("notify_email")`,
	}
	declared := map[string]bool{}
	for _, c := range m.Capabilities {
		declared[c] = true
		if _, known := uses[c]; !known {
			t.Errorf("plugin.json declares %q, which the example never uses — an owner approves every capability listed", c)
		}
	}
	for c, call := range uses {
		if !strings.Contains(src, call) {
			t.Errorf("shop.go no longer has %q — repoint this test's use of %q", call, c)
		}
		if !declared[c] {
			t.Errorf("shop.go uses %q (%s) and plugin.json does not declare it — Core refuses the call", c, call)
		}
	}
	if m.Key != "shop" {
		t.Errorf("plugin.json's key is %q; the tests build the plugin as \"shop\"", m.Key)
	}
}

func TestInitSubscribesToHooksEventsAndSchedules(t *testing.T) {
	api := &fakeCore{}
	// Granted exactly what the manifest declares: what a real install would hand it.
	core, _, srv := nildatest.NewWithAPI("shop", api.handler(), readManifest(t).Capabilities...)
	defer srv.Close()

	res, err := (&Shop{}).Init(context.Background(), core)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if len(res.Hooks) == 0 || len(res.Events) == 0 || len(res.Schedules) == 0 {
		t.Fatalf("init wired nothing: %+v", res)
	}
	if res.Schedules[0].Cron == "" {
		t.Fatal("a schedule with no cron will never fire")
	}
}

// Creating content is what no plugin could do before contract v2, so it is the example's centrepiece.
func TestCreateProductPostsToTheRightPlace(t *testing.T) {
	api := &fakeCore{}
	core, _, srv := nildatest.NewWithAPI("shop", api.handler(), "content.write")
	defer srv.Close()
	s := &Shop{core: core}

	id, err := s.CreateProduct(context.Background(), "Blue Widget", "BW-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != "c1" {
		t.Fatalf("id = %q — the `data` envelope was not unwrapped", id)
	}
	if len(api.requests) != 1 || !strings.HasPrefix(api.requests[0], "POST /content/product") {
		t.Fatalf("requests = %v", api.requests)
	}
	// The content TYPE is part of the path, not a body field — a detail worth pinning, because getting it
	// wrong produces a 404 that reads like a missing route.
	if !strings.Contains(api.bodies[0], "BW-1") {
		t.Fatalf("the sku never reached Core: %s", api.bodies[0])
	}
}

func TestPublishedProductsFiltersServerSide(t *testing.T) {
	api := &fakeCore{}
	core, _, srv := nildatest.NewWithAPI("shop", api.handler(), "content.read")
	defer srv.Close()

	n, err := (&Shop{core: core}).PublishedProducts(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 37, not 1: the page holds one row and the catalogue holds 37. Counting the rows of a page undercounts
	// every catalogue larger than a page (the 2026-09-23 plugin hunt's M21).
	if n != 37 {
		t.Fatalf("count = %d, want the whole catalogue's 37", n)
	}
	// Filtering belongs in the query, not in a loop after fetching everything — and the total is only on an
	// offset page, so the request has to ask for one.
	if !strings.Contains(api.requests[0], "status=published") || !strings.Contains(api.requests[0], "page=1") {
		t.Fatalf("the filter or the offset page was not asked for: %v", api.requests)
	}
}

// M21: API access is not write access. A shop granted only content.read has an API client — HasAPI is true —
// and would start, then fail its first write with a 403 nobody reads.
func TestInitRefusesReadOnlyAPIAccess(t *testing.T) {
	core, _, srv := nildatest.NewWithAPI("shop", (&fakeCore{}).handler(), "hooks", "content.read")
	defer srv.Close()
	_, err := (&Shop{}).Init(context.Background(), core)
	if err == nil || !strings.Contains(err.Error(), "content.write") {
		t.Fatalf("Init with only content.read answered %v — it must refuse and name content.write", err)
	}
}

// A scheduled callback arrives as a hook named after the schedule. The kit records the KV write, so the
// assertion is about what the plugin DID rather than that it returned nil.
func TestScheduledHookIncrementsItsCounter(t *testing.T) {
	core, host := nildatest.New("shop", "kv")
	s := &Shop{core: core}

	for i := 0; i < 3; i++ {
		if _, err := s.HandleHook(context.Background(), "schedule:reconcile", []byte("{}")); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := host.KV()["reconcile_runs"]; got != "3" {
		t.Fatalf("counter = %q, want 3", got)
	}
}

// savedPayload is content.saved as Core sends it: the item's id and its type, nothing else (core's
// cmd/server/main.go, SetOnLifecycle). A fixture in any other shape tests a hook Core never calls.
func savedPayload(id, typ string) []byte {
	b, _ := json.Marshal(map[string]string{"content_id": id, "type": typ})
	return b
}

func TestContentHookEmitsOnlyForProducts(t *testing.T) {
	core, host := nildatest.New("shop", "hooks", "events")
	s := &Shop{core: core}
	ctx := context.Background()

	payload := savedPayload("p1", "product")
	out, err := s.HandleHook(ctx, "content.saved", payload)
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	// content.saved is an action, so Core ignores the answer; the example echoes it anyway, which is what
	// a filter hook would need.
	if string(out) != string(payload) {
		t.Fatalf("the payload was altered: %s", out)
	}
	if len(host.Events()) != 1 || host.Events()[0].Type != "shop.product.touched" {
		t.Fatalf("events = %+v", host.Events())
	}
	// The id travels from Core's content_id: a hook reading any other name emits an event about nothing.
	var touched map[string]string
	if err := json.Unmarshal(host.Events()[0].Data, &touched); err != nil || touched["id"] != "p1" {
		t.Fatalf("the emitted event does not name the saved item: %s (%v)", host.Events()[0].Data, err)
	}

	// A post is not a product: nothing more should be emitted.
	other := savedPayload("b1", "post")
	if _, err := s.HandleHook(ctx, "content.saved", other); err != nil {
		t.Fatalf("hook: %v", err)
	}
	if len(host.Events()) != 1 {
		t.Fatalf("an unrelated type emitted an event: %+v", host.Events())
	}
}

// The kit enforces capabilities the same deny-by-default way Core does. A plugin that quietly relies on a
// capability its manifest never declared works in a permissive test and fails on a real install — the worst
// possible place to find out.
func TestAnUndeclaredCapabilityFailsInTheTest(t *testing.T) {
	core, host := nildatest.New("shop", "hooks") // no `events`
	s := &Shop{core: core}

	payload := savedPayload("p1", "product")
	_, err := s.HandleHook(context.Background(), "content.saved", payload)
	if err == nil {
		t.Fatal("emitting an event without the `events` capability succeeded")
	}
	if !strings.Contains(err.Error(), "events") {
		t.Fatalf("the denial does not name the capability: %v", err)
	}
	if len(host.Events()) != 0 {
		t.Fatal("a denied emit was recorded anyway")
	}

	// Granting it mid-test proves the before and after of the same call.
	host.Grant("events")
	if _, err := s.HandleHook(context.Background(), "content.saved", payload); err != nil {
		t.Fatalf("after granting `events`: %v", err)
	}
	if len(host.Events()) != 1 {
		t.Fatalf("events = %+v", host.Events())
	}
}

// paidPayload is ecommerce.order_paid as Nilda's own shop plugin emits it (commerce's onlinepay.go).
func paidPayload(orderID string) []byte {
	b, _ := json.Marshal(map[string]any{"order_id": orderID, "total_minor": 4900})
	return b
}

func TestOrderPaidSendsOneNotice(t *testing.T) {
	core, host := nildatest.New("shop", "events", "email")
	nildatest.SetSettings(core, map[string]any{"notify_email": "warehouse@example.com"})
	s := &Shop{core: core}

	if err := s.HandleEvent(context.Background(), OrderPaid, paidPayload("ord_42")); err != nil {
		t.Fatalf("event: %v", err)
	}
	mails := host.Emails()
	if len(mails) != 1 {
		t.Fatalf("emails = %+v", mails)
	}
	if mails[0].To != "warehouse@example.com" || !strings.Contains(mails[0].Body, "ord_42") {
		t.Fatalf("the notice is wrong: %+v", mails[0])
	}

	// An unrelated event must not send mail. A plugin that mails on everything is how a warehouse gets six
	// notices for one order.
	if err := s.HandleEvent(context.Background(), "content.published", []byte("{}")); err != nil {
		t.Fatalf("unrelated event: %v", err)
	}
	if len(host.Emails()) != 1 {
		t.Fatalf("an unrelated event sent mail: %+v", host.Emails())
	}
}

// Not configured is the first state every install is in: no address, no mail, and no error either.
func TestOrderPaidWithNoAddressSendsNothing(t *testing.T) {
	core, host := nildatest.New("shop", "events", "email")
	s := &Shop{core: core}

	if err := s.HandleEvent(context.Background(), OrderPaid, paidPayload("ord_42")); err != nil {
		t.Fatalf("an unconfigured plugin failed the event: %v", err)
	}
	if len(host.Emails()) != 0 {
		t.Fatalf("mail went out with no address configured: %+v", host.Emails())
	}
}

// What a plugin does when Core is having a bad day. A plugin that ignores a failed SendEmail loses the
// notice with nothing recorded anywhere.
func TestAFailingHostSurfacesAsAnError(t *testing.T) {
	core, host := nildatest.New("shop", "events", "email")
	nildatest.SetSettings(core, map[string]any{"notify_email": "warehouse@example.com"})
	host.Fail = errAPIDown
	s := &Shop{core: core}

	if err := s.HandleEvent(context.Background(), OrderPaid, paidPayload("ord_42")); err == nil {
		t.Fatal("a failed send was swallowed — the notice is lost and nothing says so")
	}
}

var errAPIDown = &hostDown{}

type hostDown struct{}

func (*hostDown) Error() string { return "core is unavailable" }
