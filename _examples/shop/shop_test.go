package shop

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"gitlab.com/nildalabs/nilda-sdk/plugin-sdk/nildatest"
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
			_, _ = w.Write([]byte(`{"data":[{"id":"c1"},{"id":"c2"}]}`))
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

func TestInitSubscribesToHooksEventsAndSchedules(t *testing.T) {
	api := &fakeCore{}
	core, _, srv := nildatest.NewWithAPI("shop", api.handler(), "hooks", "events", "content.write", "email", "kv")
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
	if n != 2 {
		t.Fatalf("count = %d", n)
	}
	// Filtering belongs in the query, not in a loop after fetching everything.
	if !strings.Contains(api.requests[0], "status=published") {
		t.Fatalf("the filter was not sent: %v", api.requests)
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

func TestContentHookEmitsOnlyForProducts(t *testing.T) {
	core, host := nildatest.New("shop", "hooks", "events")
	s := &Shop{core: core}
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"id": "p1", "type": "product", "title": "Widget"})
	out, err := s.HandleHook(ctx, "content.saved", payload)
	if err != nil {
		t.Fatalf("hook: %v", err)
	}
	// A filter hook must echo what it does not change. Returning nothing would DROP the payload for every
	// plugin downstream.
	if string(out) != string(payload) {
		t.Fatalf("the payload was altered: %s", out)
	}
	if len(host.Events()) != 1 || host.Events()[0].Type != "shop.product.touched" {
		t.Fatalf("events = %+v", host.Events())
	}

	// A post is not a product: nothing more should be emitted.
	other, _ := json.Marshal(map[string]string{"id": "b1", "type": "post"})
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

	payload, _ := json.Marshal(map[string]string{"id": "p1", "type": "product"})
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

func TestOrderPaidSendsOneReceipt(t *testing.T) {
	core, host := nildatest.New("shop", "events", "email")
	s := &Shop{core: core}

	data, _ := json.Marshal(map[string]string{"email": "buyer@example.com", "total": "£49"})
	if err := s.HandleEvent(context.Background(), "order.paid", data); err != nil {
		t.Fatalf("event: %v", err)
	}
	mails := host.Emails()
	if len(mails) != 1 {
		t.Fatalf("emails = %+v", mails)
	}
	if mails[0].To != "buyer@example.com" || !strings.Contains(mails[0].Body, "£49") {
		t.Fatalf("the receipt is wrong: %+v", mails[0])
	}

	// An unrelated event must not send mail. A plugin that mails on everything is how a customer gets six
	// receipts for one order.
	if err := s.HandleEvent(context.Background(), "content.published", []byte("{}")); err != nil {
		t.Fatalf("unrelated event: %v", err)
	}
	if len(host.Emails()) != 1 {
		t.Fatalf("an unrelated event sent mail: %+v", host.Emails())
	}
}

// What a plugin does when Core is having a bad day. A plugin that ignores a failed SendEmail loses a
// customer's receipt with nothing recorded anywhere.
func TestAFailingHostSurfacesAsAnError(t *testing.T) {
	core, host := nildatest.New("shop", "events", "email")
	host.Fail = errAPIDown
	s := &Shop{core: core}

	data, _ := json.Marshal(map[string]string{"email": "buyer@example.com", "total": "£49"})
	if err := s.HandleEvent(context.Background(), "order.paid", data); err == nil {
		t.Fatal("a failed send was swallowed — the receipt is lost and nothing says so")
	}
}

var errAPIDown = &hostDown{}

type hostDown struct{}

func (*hostDown) Error() string { return "core is unavailable" }
