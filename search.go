package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// CONTRIBUTING A SEARCH ENGINE.
//
// Nilda ships two search backends: Postgres full-text, which every install has, and Meilisearch for
// sites that outgrow it. Your plugin can be a third — Typesense, Elasticsearch, Algolia, a vector index,
// whatever the site needs — and once the owner grants the capability, the site's search box, the theme's
// search API and the admin's own search all run through you.
//
// # What you are handed, and what you must never do with it
//
// Core sends you DOCUMENTS to index and QUERIES to answer. A document is already reduced to text: title,
// body text, excerpt, type, locale, author, terms, and a published-at date. You never see draft bodies you
// were not sent, and you are never asked who is logged in.
//
// Visibility is NOT yours to decide. Core tells you, on every query, exactly who is asking —
// Query.Statuses, Query.PublicOnly, Query.AuthorID — and your job is to filter on it.
//
// Core also RESOLVES what you return, and you should know the shape of that because it decides what your
// engine can and cannot affect. The ids you hand back are looked up in Core's own database under that same
// visibility: an id the viewer may not see is dropped, and every field the site DISPLAYS — title, excerpt,
// type, author, date — comes from Core's row, not from your answer. What survives from you is what only
// you know: which items matched, in what order, and a highlight snippet (rendered as text, never markup).
//
// So filter properly, because a page of results Core drops is a page the visitor does not get. And do not
// bother returning titles: nobody will see them.
//
// # Nothing you can break stays broken
//
// Postgres full-text remains wired as the FALLBACK on every install. If your Query returns an error, or
// your process is down, Core answers the same search from Postgres and marks the result `degraded`. So a
// search plugin that fails makes the site's search worse for a moment, never absent — and you can deploy,
// restart and crash without taking a page down.
//
// # The shape
//
//	type engine struct{ /* your client */ }
//
//	func (e *engine) Configure(ctx context.Context) error { … }               // create/settle the index
//	func (e *engine) Index(ctx context.Context, docs []SearchDoc) error { … } // upsert
//	func (e *engine) Remove(ctx context.Context, ids []string) error { … }    // delete by id
//	func (e *engine) Truncate(ctx context.Context) error { … }                // before a full reindex
//	func (e *engine) Query(ctx context.Context, q SearchQuery) (SearchResults, error) { … }
//	func (e *engine) Healthy(ctx context.Context) bool { … }
//
//	func main() { nilda.ServeSearchProvider(&engine{}) }
//
// Declare it in the manifest:
//
//	"capabilities": ["search_provider"],
//	"search": { "name": "Typesense" }
//
// At most ONE plugin on an install may provide search. Two would each hold half an index and neither
// would know it, so Core refuses the second at install time rather than at the first empty result page.

// SearchDoc is one document to index. Core builds it; you store what you need and ignore the rest.
type SearchDoc struct {
	// ID is the content item's UUID, and the id you must return from Query.
	ID string `json:"id"`
	// Type is the content-type key ("post", "product").
	Type    string `json:"type"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
	// Status is "published", "draft", … — index it, because a query names which statuses may match.
	Status string `json:"status"`
	// Lang is the item's language code.
	Lang string `json:"lang,omitempty"`
	// AuthorID is the author's UUID, Author their display name, Terms their taxonomy terms — index them
	// if your engine can filter or facet on them.
	AuthorID string   `json:"author_id,omitempty"`
	Author   string   `json:"author,omitempty"`
	Terms    []string `json:"terms,omitempty"`
	// Unlisted and EmbargoUntil are what makes a PUBLISHED item still invisible: unlisted means "reachable
	// by link, absent from listings", and an embargo is a publish date in the future. Filter them out on a
	// PublicOnly query. Nilda drops them again on the way out either way — see the note on Query.
	Unlisted     bool  `json:"unlisted,omitempty"`
	EmbargoUntil int64 `json:"embargo_until,omitempty"`
	// PublishedAt is unix SECONDS, 0 when unpublished — the same unit Nilda stores.
	PublishedAt int64 `json:"published_at,omitempty"`
}

// SearchQuery is one search to answer.
type SearchQuery struct {
	Q string `json:"q"`
	// Type, Lang and Author narrow the search when the caller asked to; empty means "any".
	Type   string `json:"type,omitempty"`
	Lang   string `json:"lang,omitempty"`
	Author string `json:"author,omitempty"`
	// Limit and Offset page the result set.
	Limit  int `json:"limit"`
	Offset int `json:"offset"`

	// VISIBILITY — set by Nilda, never by the caller. Return only documents whose Status is in Statuses.
	// An anonymous visitor arrives here with Statuses = ["published"] and PublicOnly = true.
	Statuses []string `json:"statuses"`
	// PublicOnly means the viewer is anonymous: also exclude unlisted and still-embargoed items.
	PublicOnly bool `json:"public_only,omitempty"`
	// AuthorID, when set, scopes the search to one author's own content.
	AuthorID string `json:"author_id,omitempty"`
}

// SearchHit is one result. Only ID and Score are load-bearing: Core reads the row it already has, so a
// title you return is never shown in place of the real one. Return them in the order you want them shown.
type SearchHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score,omitempty"`
	// Snippet is an optional highlighted fragment. Core sanitises it before it reaches a browser.
	Snippet string `json:"snippet,omitempty"`
}

// SearchResults is a page of hits plus the total the engine matched.
type SearchResults struct {
	Hits  []SearchHit `json:"hits"`
	Total int64       `json:"total"`
}

// SearchProvider is the engine your plugin implements.
type SearchProvider interface {
	// Configure creates or settles the index. Core calls it at boot and after an upgrade, so it must be
	// idempotent — it will run against an index that already exists, many times.
	Configure(ctx context.Context) error
	// Index upserts documents. Called in batches; an id you already hold must be REPLACED, not duplicated.
	Index(ctx context.Context, docs []SearchDoc) error
	// Remove deletes by id. Removing an id you do not have is not an error.
	Remove(ctx context.Context, ids []string) error
	// Truncate empties the index. Core calls it before a full reindex.
	Truncate(ctx context.Context) error
	// Query answers a search, filtered by the visibility the query carries.
	Query(ctx context.Context, q SearchQuery) (SearchResults, error)
	// Healthy reports whether your engine is reachable. Returning false makes Core serve from Postgres
	// and say so, which is the honest answer while your engine is restarting.
	Healthy(ctx context.Context) bool
}

// Search hook names — Core's side of the same contract.
const (
	HookSearchConfigure = "search.configure"
	HookSearchIndex     = "search.index"
	HookSearchRemove    = "search.remove"
	HookSearchTruncate  = "search.truncate"
	HookSearchQuery     = "search.query"
	HookSearchHealthy   = "search.healthy"
)

// ServeSearchProvider runs a plugin whose whole job is search. Use Serve with your own Handler and
// DispatchSearchHook when your plugin does other things too.
func ServeSearchProvider(p SearchProvider) {
	Serve(&searchOnlyHandler{p: p})
}

// searchOnlyHandler adapts a SearchProvider onto the full Handler interface, so a plugin that does
// nothing else writes nothing else.
type searchOnlyHandler struct {
	p    SearchProvider
	core *Core
}

func (h *searchOnlyHandler) Init(ctx context.Context, core *Core) (InitResult, error) {
	h.core = core
	// Subscribed unconditionally, like the auth hooks: Core delivers them only to a plugin granted
	// `search_provider`, so asking without the capability is harmless — and not asking WITH it would be a
	// plugin that installs cleanly and is then never asked to index anything.
	return InitResult{Hooks: []string{
		HookSearchConfigure, HookSearchIndex, HookSearchRemove,
		HookSearchTruncate, HookSearchQuery, HookSearchHealthy,
	}}, nil
}

func (h *searchOnlyHandler) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, handled, err := DispatchSearchHook(ctx, h.core, h.p, hook, payload)
	if !handled {
		return nil, fmt.Errorf("unknown hook %q", hook)
	}
	return out, err
}

func (h *searchOnlyHandler) HandleEvent(context.Context, string, []byte) error { return nil }

// DispatchSearchHook routes one search hook to p. handled=false means the hook was not a search hook, so
// a plugin that does several things can pass it on rather than failing it.
func DispatchSearchHook(ctx context.Context, _ *Core, p SearchProvider, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookSearchConfigure:
		return okOrErr(p.Configure(ctx))

	case HookSearchIndex:
		var in struct {
			Docs []SearchDoc `json:"docs"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: search.index payload: %w", err)
		}
		return okOrErr(p.Index(ctx, in.Docs))

	case HookSearchRemove:
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: search.remove payload: %w", err)
		}
		return okOrErr(p.Remove(ctx, in.IDs))

	case HookSearchTruncate:
		return okOrErr(p.Truncate(ctx))

	case HookSearchQuery:
		var q SearchQuery
		if err := json.Unmarshal(payload, &q); err != nil {
			return nil, true, fmt.Errorf("nilda: search.query payload: %w", err)
		}
		res, err := p.Query(ctx, q)
		if err != nil {
			return nil, true, err
		}
		// A nil Hits marshals as null, and a caller reading `.hits[0]` on null is a crash in whatever
		// language reads it next. An empty search result is a list with nothing in it.
		if res.Hits == nil {
			res.Hits = []SearchHit{}
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookSearchHealthy:
		b, err := json.Marshal(map[string]any{"healthy": p.Healthy(ctx)})
		return b, true, err
	}
	return nil, false, nil
}

// okOrErr is the answer shape for the hooks that either worked or did not.
func okOrErr(err error) ([]byte, bool, error) {
	if err != nil {
		return nil, true, err
	}
	return []byte(`{"ok":true}`), true, nil
}
