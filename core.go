package nilda

import (
	"context"
	"encoding/json"
	"log/slog"
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

	// PreviousVersion is the version this plugin last COMPLETED an Init at, or "" on a fresh install.
	// Read it with IsFirstRun / UpgradedFrom rather than comparing strings by hand.
	PreviousVersion string

	// logger is this plugin's own, named after its key so a line in the operator's log says which plugin
	// wrote it. Built at Init; Log() falls back to the unnamed process logger before that. See log.go.
	logger *slog.Logger

	host contract.HostServiceClient
	// settings are the values the site owner typed on this plugin's declared admin pages (adminpage.go).
	// Read them with Setting/SettingBool/SettingNumber/HasSetting — never by reaching in here, so a nil
	// Core stays safe.
	settings map[string]any

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

// ---- email (requires `email`) ----

// SendEmail sends mail through Core's mailer. Core fixes the sender identity, so a plugin cannot forge the
// site's address — and a booking plugin sends its own confirmations without needing SMTP credentials of its
// own, or the owner configuring mail a second time.
func (c *Core) SendEmail(ctx context.Context, to, subject, body string) error {
	_, err := c.host.SendEmail(ctx, &contract.SendEmailRequest{To: to, Subject: subject, Body: body})
	return err
}

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
//
// Prefer the nildatest package, which supplies a working fake host: passing nil here means any host call —
// KVGet, Emit, SendEmail — dereferences a nil client and panics, and the panic names gRPC internals rather
// than the missing dependency. That is a trap for exactly the author this helper exists for.
func NewCoreForTest(pluginKey string, granted []string, host contract.HostServiceClient) *Core {
	return &Core{PluginKey: pluginKey, Granted: granted, host: host}
}

// NewCoreForTestWithAPI is NewCoreForTest plus an API client pointed at a test server.
//
// Exists because a plugin's most interesting code is what it does with core.API(), and a Core built for a
// test had none — so the half an author most wants to test was the half they could not. baseURL is usually
// an httptest.Server's URL; nildatest wires it for you.
func NewCoreForTestWithAPI(pluginKey string, granted []string, host contract.HostServiceClient,
	baseURL, token string, scopes []string) *Core {
	c := NewCoreForTest(pluginKey, granted, host)
	c.api = newAPI(baseURL, token, scopes)
	return c
}
