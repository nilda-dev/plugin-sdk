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
}

// HasCapability reports whether a capability was granted (a convenience mirror of the server-side
// enforcement — the server is the authority).
func (c *Core) HasCapability(key string) bool { return slices.Contains(c.Granted, key) }

// ---- content.read (Stable API v1: published-only) ----

type Site struct {
	Title, Description, BaseURL, Locale string
}

type Page struct {
	ID, Type, Title, Slug, Excerpt string
	Body                           json.RawMessage
	PublishedAt                    time.Time
	AuthorID                       string
	Values                         json.RawMessage
}

type ListItem struct {
	ID, Type, Title, Slug, Excerpt string
	PublishedAt                    time.Time
	AuthorID                       string
}

type ContentList struct {
	Items      []ListItem
	Page, Size int
	Total      int64
}

func (c *Core) Site(ctx context.Context) (Site, error) {
	res, err := c.host.ContentSite(ctx, &contract.SiteRequest{})
	if err != nil {
		return Site{}, err
	}
	return Site{Title: res.Title, Description: res.Description, BaseURL: res.BaseUrl, Locale: res.Locale}, nil
}

func (c *Core) PageBySlug(ctx context.Context, slug string) (Page, bool, error) {
	res, err := c.host.ContentPageBySlug(ctx, &contract.PageBySlugRequest{Slug: slug})
	if err != nil {
		return Page{}, false, err
	}
	if !res.Found || res.Page == nil {
		return Page{}, false, nil
	}
	p := res.Page
	return Page{
		ID: p.Id, Type: p.Type, Title: p.Title, Slug: p.Slug, Excerpt: p.Excerpt,
		Body: p.BodyJson, PublishedAt: time.Unix(p.PublishedAtUnix, 0).UTC(),
		AuthorID: p.AuthorId, Values: p.ValuesJson,
	}, true, nil
}

func (c *Core) ContentList(ctx context.Context, typeKey string, page, size int) (ContentList, error) {
	res, err := c.host.ContentList(ctx, &contract.ContentListRequest{TypeKey: typeKey, Page: int32(page), Size: int32(size)})
	if err != nil {
		return ContentList{}, err
	}
	out := ContentList{Page: int(res.Page), Size: int(res.Size), Total: res.Total, Items: make([]ListItem, 0, len(res.Items))}
	for _, it := range res.Items {
		out.Items = append(out.Items, ListItem{
			ID: it.Id, Type: it.Type, Title: it.Title, Slug: it.Slug, Excerpt: it.Excerpt,
			PublishedAt: time.Unix(it.PublishedAtUnix, 0).UTC(), AuthorID: it.AuthorId,
		})
	}
	return out, nil
}

// ---- users.read / media.read (minimal, privacy-lean DTOs) ----

type User struct{ ID, DisplayName string }

type Media struct{ ID, URL, MIME string }

func (c *Core) UserByID(ctx context.Context, id string) (User, error) {
	res, err := c.host.UserByID(ctx, &contract.UserByIDRequest{Id: id})
	if err != nil {
		return User{}, err
	}
	return User{ID: res.Id, DisplayName: res.DisplayName}, nil
}

func (c *Core) MediaByID(ctx context.Context, id string) (Media, error) {
	res, err := c.host.MediaByID(ctx, &contract.MediaByIDRequest{Id: id})
	if err != nil {
		return Media{}, err
	}
	return Media{ID: res.Id, URL: res.Url, MIME: res.Mime}, nil
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
func NewCoreForTest(pluginKey string, granted []string, host contract.HostServiceClient) *Core {
	return &Core{PluginKey: pluginKey, Granted: granted, host: host}
}
