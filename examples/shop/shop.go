// Package shop is a compilable example of a Nilda plugin, exercised by this module's own tests.
//
// It exists because of DX6, and DX6 exists because of what happened without it: docs/PLUGIN_SDK.md described
// four methods that had never existed — nilda.ServeApp, core.Datastore.DB(), core.Routes.Mount(), an
// OnContentSaved handler — and anyone following it wrote code that did not compile. A prose example is a
// claim; an example the build breaks on is a guarantee.
//
// So every non-trivial snippet in the documentation lives here, and `go test ./...` fails if the SDK changes
// underneath it.
package shop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
)

// Shop is the example plugin: it watches content, writes content, keeps a counter, and emails a receipt.
type Shop struct {
	core *nilda.Core
}

// Init receives everything Core granted, exactly once.
func (s *Shop) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
	s.core = core

	// Check for API access rather than discovering its absence as a nil dereference three calls deeper. A
	// plugin that needs to write and was not granted content.write should say so at startup, where the
	// message reaches whoever installed it.
	if !core.HasAPI() {
		return nilda.InitResult{}, fmt.Errorf("shop needs content.write — add it to plugin.json")
	}

	return nilda.InitResult{
		Hooks:  []string{"content.saved"},
		Events: []string{"order.paid"},
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

// PublishedProducts reads back the live ones, filtered server-side.
func (s *Shop) PublishedProducts(ctx context.Context) (int, error) {
	var page struct {
		Data []map[string]any `json:"data"`
	}
	err := s.core.API().Get(ctx, "/content/product", url.Values{"status": {"published"}}, &page)
	if err != nil {
		return 0, err
	}
	return len(page.Data), nil
}

// HandleHook is filter-style: return the payload, modified or not. Returning nothing DROPS it.
func (s *Shop) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	switch hook {
	case nilda.ScheduleHook("reconcile"):
		// A scheduled callback arrives as a hook named after the schedule.
		if _, err := s.core.KVIncr(ctx, "reconcile_runs"); err != nil {
			return nil, err
		}
		return payload, nil

	case "content.saved":
		var item struct {
			ID    string `json:"id"`
			Type  string `json:"type"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(payload, &item); err != nil {
			// A malformed payload is not this plugin's to fix, and swallowing it would hide a Core bug.
			return nil, err
		}
		if item.Type == "product" {
			if err := s.core.Emit(ctx, "shop.product.touched", map[string]any{"id": item.ID}); err != nil {
				return nil, err
			}
		}
		return payload, nil
	}
	return payload, nil
}

// HandleEvent is fire-and-forget: there is nothing to return but an error.
func (s *Shop) HandleEvent(ctx context.Context, eventType string, data []byte) error {
	if eventType != "order.paid" {
		return nil
	}
	var order struct {
		Email string `json:"email"`
		Total string `json:"total"`
	}
	if err := json.Unmarshal(data, &order); err != nil {
		return err
	}
	// Core fixes the sender, so a plugin can address mail but never forge who it is from.
	return s.core.SendEmail(ctx, order.Email, "Your order",
		fmt.Sprintf("Thank you — your order for %s is confirmed.", order.Total))
}
