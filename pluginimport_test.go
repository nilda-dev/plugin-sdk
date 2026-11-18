package nilda

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An import door's answer is used only when it answers what was asked: one result per id or rule, in order, a found id with
// its target, a written rule with its id, a safety copy with its id (else ErrImportAnswer) — a short answer would otherwise
// read as "not found" and an import would go on believing nothing was there (lesson 10).
func TestAnImportDoorsAnswerIsUsedOnlyWhenItAnswersWhatWasAsked(t *testing.T) {
	answers := map[string]string{
		"/plugin-import/map/lookup":       `{"data":[{"external_id":"a","found":true,"kind":"media"}]}`,
		"/plugin-import/redirects":        `{"data":[{"written":true}]}`,
		"/plugin-import/redirects/remove": `{"data":[]}`,
		"/plugin-import/checkpoint":       `{"data":{"id":""}}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(answers[r.URL.Path]))
	}))
	defer srv.Close()
	api := newAPI(srv.URL, "tok", []string{"manage:import"})
	ctx := context.Background()
	if _, err := api.LookupImported(ctx, "wordpress", []string{"a"}); !errors.Is(err, ErrImportAnswer) {
		t.Errorf("a found id with no target: %v", err)
	}
	if _, err := api.AddImportRedirects(ctx, []ImportRedirect{{From: "/a/", To: "/b/"}}); !errors.Is(err, ErrImportAnswer) {
		t.Errorf("a written rule with no id: %v", err)
	}
	if _, err := api.RemoveImportRedirects(ctx, []ImportRedirectRemoval{{ID: "x", From: "/a/", To: "/b/"}}); !errors.Is(err, ErrImportAnswer) {
		t.Errorf("no result for the rule: %v", err)
	}
	if _, err := api.ImportCheckpoint(ctx, "why"); !errors.Is(err, ErrImportAnswer) {
		t.Errorf("a safety copy with no id: %v", err)
	}
	answers["/plugin-import/map/lookup"] = `{"data":[{"external_id":"b","found":false}]}`
	if _, err := api.LookupImported(ctx, "wordpress", []string{"a"}); !errors.Is(err, ErrImportAnswer) {
		t.Errorf("an answer about another id: %v", err)
	}
	var none *API
	if _, err := none.ImportCheckpoint(ctx, "why"); err == nil {
		t.Error("a nil API asked")
	}
}
