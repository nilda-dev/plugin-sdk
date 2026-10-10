package nilda

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// WHAT THE SITE'S PLAN ALLOWS A PLUGIN: the plan's number when it sets one, NO LIMIT when it does not — and when the
// Core is older than the route (404), so a plugin built on this release runs unchanged there.
func TestAPlanLimitIsReadAndAbsentIsNoLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/rest/v1/plugin-limits/products":
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"limited":true,"limit":100}}`))
		case "/api/rest/v1/plugin-limits/resources":
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"limited":false}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"not found"}}`))
		}
	}))
	defer srv.Close()
	api := newAPI(srv.URL+"/api/rest/v1", "tok", nil) // Core hands the REST root at Init
	ctx := context.Background()

	if n, limited, err := api.PlanLimit(ctx, "products"); err != nil || !limited || n != 100 {
		t.Fatalf("products = %d %v %v; want 100, limited", n, limited, err)
	}
	if _, limited, err := api.PlanLimit(ctx, "resources"); err != nil || limited {
		t.Fatalf("a limit the plan does not set read as limited (%v)", err)
	}
	if _, limited, err := api.PlanLimit(ctx, "older_core"); err != nil || limited {
		t.Fatalf("an older Core's 404 must read as no limit, got limited=%v err=%v", limited, err)
	}
	if _, _, err := api.PlanLimit(ctx, "Products!"); err == nil {
		t.Fatal("a name no manifest could declare was sent")
	}
}
