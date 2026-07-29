package nilda

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"gitlab.com/nilda-sdk/plugin-sdk/contract"
)

// Core is the plugin's typed door into Nilda: plain-Go wrappers over HostService. Only the granted
// capabilities are usable — Core (the server side) enforces deny-by-default on EVERY call; an
// undeclared capability fails with a PermissionDenied gRPC error, never silently.
type Core struct {
	PluginKey    string
	NildaVersion string
	Granted      []string // the granted capability keys
	DatastoreDSN string   // scoped least-privilege DSN (own schema only); empty unless `datastore`
	KVNamespace  string   // informational; KV ops go through the host

	host contract.HostServiceClient
	// api is how the plugin READS AND WRITES real data — Core's own /api/v1, with a scoped token
	// (api.go). nil when no granted capability implies API access. See HasAPI/API.
	api *API
}

// HasCapability reports whether a capability was granted (a convenience mirror of the server-side
// enforcement — the server is the authority).
func (c *Core) HasCapability(key string) bool { return slices.Contains(c.Granted, key) }

// Reading and writing Nilda's data is Core.API() — see api.go.
//
// This file used to carry typed wrappers over five gRPC read methods: Site, PageBySlug, ContentList,
// UserByID, MediaByID. They are gone, and their removal is the point of contract v2. Between them they
// could fetch one page by slug and page through one content type, and they could not write at all — so a
// plugin author who needed anything else (filter by a field, create a product, upload an image, delete a
// row) had nowhere to go, and no application-class plugin could exist.
//
// A plugin now calls Core's own API with a scoped token, which is the same surface every first-party
// feature and external integration uses. What replaced each of them:
//
//	Site()          -> api.Get(ctx, "/site", nil, &out)
//	PageBySlug(s)   -> api.Get(ctx, "/content", url.Values{"slug": {s}}, &out)
//	ContentList(t)  -> api.Get(ctx, "/content", url.Values{"type": {t}, "page": {"1"}}, &out)
//	UserByID(id)    -> api.Get(ctx, "/users/"+id, nil, &out)
//	MediaByID(id)   -> api.Get(ctx, "/media/"+id, nil, &out)
//
// and, newly possible: api.Post(ctx, "/content", item, &created), api.Patch, api.Delete, media upload,
// bulk import, GraphQL. A plugin holding `datastore` can also query Core's published data in SQL through
// the read-only views, joining it against its own tables in one statement.

// ---- kv (scoped Dragonfly namespace) ----

func (c *Core) KVGet(ctx context.Context, key string) (string, bool, error) {
	res, err := c.host.KVGet(ctx, &contract.KVGetRequest{Key: key})
	if err != nil {
		return "", false, err
	}
	return res.Value, res.Found, nil
}

func (c *Core) KVSet(ctx context.Context, key, value string, ttl time.Duration) error {
	_, err := c.host.KVSet(ctx, &contract.KVSetRequest{Key: key, Value: value, TtlSeconds: int64(ttl / time.Second)})
	return err
}

func (c *Core) KVDel(ctx context.Context, key string) error {
	_, err := c.host.KVDel(ctx, &contract.KVDelRequest{Key: key})
	return err
}

func (c *Core) KVIncr(ctx context.Context, key string) (int64, error) {
	res, err := c.host.KVIncr(ctx, &contract.KVIncrRequest{Key: key})
	if err != nil {
		return 0, err
	}
	return res.Value, nil
}

// ---- events ----

// Emit publishes a plugin-originated event into Core's event pipeline (requires `events`).
func (c *Core) Emit(ctx context.Context, eventType string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = c.host.EmitEvent(ctx, &contract.EmitEventRequest{Type: eventType, DataJson: raw})
	return err
}

// NewCoreForTest builds a Core over an existing HostService client — for tests only.
func NewCoreForTest(pluginKey string, granted []string, host contract.HostServiceClient) *Core {
	return &Core{PluginKey: pluginKey, Granted: granted, host: host}
}
