package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeEngine struct {
	configured int
	indexed    []SearchDoc
	removed    []string
	truncated  int
	lastQuery  SearchQuery
	queryErr   error
	healthy    bool
}

func (e *fakeEngine) Configure(context.Context) error { e.configured++; return nil }
func (e *fakeEngine) Index(_ context.Context, docs []SearchDoc) error {
	e.indexed = append(e.indexed, docs...)
	return nil
}
func (e *fakeEngine) Remove(_ context.Context, ids []string) error {
	e.removed = append(e.removed, ids...)
	return nil
}
func (e *fakeEngine) Truncate(context.Context) error { e.truncated++; return nil }
func (e *fakeEngine) Query(_ context.Context, q SearchQuery) (SearchResults, error) {
	e.lastQuery = q
	if e.queryErr != nil {
		return SearchResults{}, e.queryErr
	}
	return SearchResults{Hits: []SearchHit{{ID: "c1", Score: 1.5, Snippet: "hello"}}, Total: 1}, nil
}
func (e *fakeEngine) Healthy(context.Context) bool { return e.healthy }

// TestEverySearchHookReachesTheEngine walks the whole contract, because a hook that is declared and not
// routed is a plugin that installs, indexes nothing, and reports no error while doing it.
func TestEverySearchHookReachesTheEngine(t *testing.T) {
	e := &fakeEngine{healthy: true}
	ctx := context.Background()

	if _, handled, err := DispatchSearchHook(ctx, nil, e, HookSearchConfigure, nil); !handled || err != nil {
		t.Fatalf("configure: handled=%v err=%v", handled, err)
	}
	if e.configured != 1 {
		t.Errorf("configure reached the engine %d times", e.configured)
	}

	docs := `{"docs":[{"id":"c1","type":"post","title":"Hello","status":"published","lang":"en","published_at":1735689600}]}`
	if _, handled, err := DispatchSearchHook(ctx, nil, e, HookSearchIndex, []byte(docs)); !handled || err != nil {
		t.Fatalf("index: handled=%v err=%v", handled, err)
	}
	if len(e.indexed) != 1 || e.indexed[0].Title != "Hello" || e.indexed[0].Status != "published" || e.indexed[0].PublishedAt != 1735689600 {
		t.Errorf("index delivered %+v", e.indexed)
	}

	if _, handled, err := DispatchSearchHook(ctx, nil, e, HookSearchRemove, []byte(`{"ids":["c1","c2"]}`)); !handled || err != nil {
		t.Fatalf("remove: handled=%v err=%v", handled, err)
	}
	if len(e.removed) != 2 {
		t.Errorf("remove delivered %v", e.removed)
	}

	if _, handled, err := DispatchSearchHook(ctx, nil, e, HookSearchTruncate, nil); !handled || err != nil {
		t.Fatalf("truncate: handled=%v err=%v", handled, err)
	}
	if e.truncated != 1 {
		t.Errorf("truncate reached the engine %d times", e.truncated)
	}

	out, handled, err := DispatchSearchHook(ctx, nil, e, HookSearchHealthy, nil)
	if !handled || err != nil {
		t.Fatalf("healthy: handled=%v err=%v", handled, err)
	}
	if !strings.Contains(string(out), `"healthy":true`) {
		t.Errorf("healthy answered %s", out)
	}
}

// TestAQueryCarriesTheVisibilityCoreDecided is the one that matters for a leak: the statuses a viewer may
// see are set by CORE and must arrive at the engine intact. An engine that never receives them cannot
// filter on them, and every draft on the site is one search away.
func TestAQueryCarriesTheVisibilityCoreDecided(t *testing.T) {
	e := &fakeEngine{}
	payload := `{"q":"hello","limit":10,"offset":0,"statuses":["published"],"public_only":true}`

	out, handled, err := DispatchSearchHook(context.Background(), nil, e, HookSearchQuery, []byte(payload))
	if !handled || err != nil {
		t.Fatalf("query: handled=%v err=%v", handled, err)
	}
	if got := e.lastQuery.Statuses; len(got) != 1 || got[0] != "published" {
		t.Errorf("the engine was asked with statuses %v — an anonymous search must carry exactly published", got)
	}
	if !e.lastQuery.PublicOnly {
		t.Error("public_only did not reach the engine — an anonymous search would match unlisted items")
	}

	var res SearchResults
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("the answer must be a SearchResults: %v (%s)", err, out)
	}
	if res.Total != 1 || len(res.Hits) != 1 || res.Hits[0].ID != "c1" {
		t.Errorf("results round-tripped wrong: %+v", res)
	}
}

// TestAnEmptyResultIsAListNotNull: `null` hits crash whatever reads `.hits[0]` next, and an engine that
// legitimately matched nothing is the most ordinary case there is.
func TestAnEmptyResultIsAListNotNull(t *testing.T) {
	empty := &stubEngine{}
	out, _, err := DispatchSearchHook(context.Background(), nil, empty, HookSearchQuery, []byte(`{"q":"zzz","statuses":["published"]}`))
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !strings.Contains(string(out), `"hits":[]`) {
		t.Errorf("an empty result must serialise as an empty list, got %s", out)
	}
}

// TestAnEngineErrorReachesCoreRatherThanBeingSwallowed — Core degrades to Postgres on an error, so an
// error that never arrives is a search that silently returns nothing instead of falling back.
func TestAnEngineErrorReachesCoreRatherThanBeingSwallowed(t *testing.T) {
	e := &fakeEngine{queryErr: errors.New("index unreachable")}
	_, handled, err := DispatchSearchHook(context.Background(), nil, e, HookSearchQuery, []byte(`{"q":"x","statuses":["published"]}`))
	if !handled {
		t.Fatal("the hook must be claimed even when the engine fails")
	}
	if err == nil || !strings.Contains(err.Error(), "index unreachable") {
		t.Errorf("the engine's error must reach Core, got %v", err)
	}
}

// TestAnUnknownHookIsNotClaimed lets a plugin that ALSO does other things pass its own hooks through.
func TestAnUnknownHookIsNotClaimedBySearch(t *testing.T) {
	if _, handled, _ := DispatchSearchHook(context.Background(), nil, &fakeEngine{}, "content.saved", nil); handled {
		t.Error("a non-search hook must not be claimed")
	}
}

// TestMalformedPayloadIsAnErrorNotAPanic — the payload crosses a process boundary, so it is untrusted.
func TestAMalformedSearchPayloadIsAnErrorNotAPanic(t *testing.T) {
	for _, hook := range []string{HookSearchIndex, HookSearchRemove, HookSearchQuery} {
		if _, handled, err := DispatchSearchHook(context.Background(), nil, &fakeEngine{}, hook, []byte("{")); !handled || err == nil {
			t.Errorf("%s: malformed payload → handled=%v err=%v, want a claimed error", hook, handled, err)
		}
	}
}

// stubEngine answers everything with nothing — the empty-index case.
type stubEngine struct{}

func (stubEngine) Configure(context.Context) error          { return nil }
func (stubEngine) Index(context.Context, []SearchDoc) error { return nil }
func (stubEngine) Remove(context.Context, []string) error   { return nil }
func (stubEngine) Truncate(context.Context) error           { return nil }
func (stubEngine) Healthy(context.Context) bool             { return true }
func (stubEngine) Query(context.Context, SearchQuery) (SearchResults, error) {
	return SearchResults{}, nil
}
