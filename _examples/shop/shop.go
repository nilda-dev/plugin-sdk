// Package shop is a compilable example of a Nilda plugin, exercised by this module's own tests.
//
// It exists because of DX6, and DX6 exists because of what happened without it: docs/PLUGIN_SDK.md described
// four methods that had never existed — nilda.ServeApp, core.Datastore.DB(), core.Routes.Mount(), an
// OnContentSaved handler — and anyone following it wrote code that did not compile. A prose example is a
// claim; an example the build breaks on is a guarantee.
//
// So every non-trivial snippet in the documentation lives here, and CI fails if the SDK changes underneath it:
// it runs `cd _examples && go test ./...`. From the module root `go test ./...` never reaches this directory —
// the Go tool skips a directory whose name starts with an underscore — so run it from here.
package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Shop is the example plugin: it watches content, writes content, keeps a counter, and mails its owner when
// the site's shop reports an order paid.
type Shop struct {
	core *nilda.Core
}

// OrderPaid is the event this example listens for. Nilda's own shop plugin (`commerce`) emits it when an
// order is paid, with the order's id among its fields; Core lets only the plugin holding the `commerce`
// capability emit an `ecommerce.*` event (core's internal/plugin/eventprovenance.go), so no other plugin can
// announce an order nobody paid.
const OrderPaid = "ecommerce.order_paid"

// Init receives everything Core granted, exactly once.
func (s *Shop) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
	s.core = core

	// Check for the capability this plugin needs rather than discovering its absence as a 403 three calls
	// deeper. HasAPI alone is not that check: ANY API capability — content.read, media.read — makes the
	// client exist, and a shop holding only content.read would start and then fail its first write. A plugin
	// that needs to write should say so at startup, where the message reaches whoever installed it.
	if !core.HasAPI() || !core.HasCapability("content.write") {
		return nilda.InitResult{}, fmt.Errorf("shop needs content.write — add it to plugin.json")
	}

	return nilda.InitResult{
		Hooks:  []string{"content.saved"},
		Events: []string{OrderPaid},
		Schedules: []nilda.Schedule{
			{Name: "reconcile", Cron: "0 3 * * *"},
		},
	}, nil
}

// Product is the shape this example writes.
type Product struct {
	Title  string         `json:"title"`
	Status string         `json:"status"`
	Values map[string]any `json:"values,omitempty"`
}

// created is what Core answers with — a single resource arrives inside a `data` envelope (SPEC_74).
type created struct {
	Data struct {
		ID       string `json:"id"`
		AuthorID string `json:"author_id"`
	} `json:"data"`
}

// CreateProduct writes a product through Core's API.
func (s *Shop) CreateProduct(ctx context.Context, title, sku string) (string, error) {
	var out created
	err := s.core.API().Post(ctx, "/content/product", Product{
		Title: title, Status: "draft", Values: map[string]any{"sku": sku},
	}, &out)
	if err != nil {
		return "", err
	}
	return out.Data.ID, nil
}

// PublishedProducts counts the live ones, filtered server-side.
//
// It reads meta.total, not the length of the page: a list answer is ONE page, so counting its rows undercounts
// every catalogue larger than a page — forever, and with no error. Core reports the whole count in meta.total
// for published content on an OFFSET page (`page=` — the default cursor pages carry no total), so one request
// for page 1 with a size of 1 is all it takes.
func (s *Shop) PublishedProducts(ctx context.Context) (int, error) {
	var page struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	q := url.Values{"status": {"published"}, "page": {"1"}, "size": {"1"}}
	err := s.core.API().Get(ctx, "/content/product", q, &page)
	if err != nil {
		return 0, err
	}
	return page.Meta.Total, nil
}

// HandleHook answers one hook. For a FILTER hook the answer is the payload, modified or not, and returning
// nothing drops it; content.saved is an ACTION — Core ignores what comes back — so echoing it is simply
// harmless.
func (s *Shop) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	switch hook {
	case nilda.ScheduleHook("reconcile"):
		// A scheduled callback arrives as a hook named after the schedule.
		if _, err := s.core.KVIncr(ctx, "reconcile_runs"); err != nil {
			return nil, err
		}
		return payload, nil

	case "content.saved":
		// Core sends the saved item's id and its content type — nothing else (core's cmd/server/main.go,
		// SetOnLifecycle). Read the rest through the API if you need it.
		var item struct {
			ContentID string `json:"content_id"`
			Type      string `json:"type"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			// A malformed payload is not this plugin's to fix, and swallowing it would hide a Core bug.
			return nil, err
		}
		if item.Type == "product" {
			if err := s.core.Emit(ctx, "shop.product.touched", map[string]any{"id": item.ContentID}); err != nil {
				return nil, err
			}
		}
		return payload, nil
	}
	return payload, nil
}

// HandleEvent is fire-and-forget: there is nothing to return but an error.
//
// The event carries the order's id and total, not the buyer — the shop keeps its customers — so this mails
// the address the site owner typed on the plugin's settings page ("notify_email"), and nothing while none is
// set: not configured is a state, not an error.
func (s *Shop) HandleEvent(ctx context.Context, eventType string, data []byte) error {
	if eventType != OrderPaid {
		return nil
	}
	var order struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(data, &order); err != nil {
		return err
	}
	if !s.core.HasSetting("notify_email") {
		return nil
	}
	// Core fixes the sender, so a plugin can address mail but never forge who it is from.
	return s.core.SendEmail(ctx, s.core.Setting("notify_email"), "Order paid",
		fmt.Sprintf("Order %s is paid — time to ship it.", order.OrderID))
}
