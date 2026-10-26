package nildatest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Trash is a fake of the site's recycle bin as a plugin meets it (the `trash` capability): Core's /plugin-trash
// routes, answering as Core answers — each plugin reports and takes back only its own items, a plugin that never
// declared `trash` is refused 403, an item Core would refuse is 422 — and the two hooks Core calls, driven the way
// Core drives them.
//
//	bin := nildatest.NewTrash()
//	core, _, srv := bin.Core("shop", "trash", "admin_page")
//	defer srv.Close()
//	// ... delete a discount through your own code, with core ...
//	items := bin.Items("shop")                                 // what the site's bin would list
//	err := bin.Restore(ctx, "shop", plugin.HandleHook, items[0]) // a person pressed "Put back"
//
// Restore and Purge call your HandleHook with nilda.TrashRestoreHook / nilda.TrashPurgeHook and the item's kind and
// id, and settle the entry as Core does: it leaves the bin when your hook answers nil and stays when it refuses.
type Trash struct {
	mu    sync.Mutex
	items map[string]map[nilda.TrashRef]TrashEntry
}

// TrashEntry is one item a plugin reported, as Core keeps it.
type TrashEntry struct {
	nilda.TrashRef
	Name string
	// ExpiresAfter is the stay Core gives it: the plugin's ask, or nilda.TrashRetention.
	ExpiresAfter time.Duration
	// RequestID is the hook call the plugin said it was handling (nilda.CallOf), "" outside one.
	RequestID string
}

// HookFunc is a plugin's HandleHook — what Core calls.
type HookFunc func(ctx context.Context, hook string, payload []byte) ([]byte, error)

// NewTrash builds an empty bin.
func NewTrash() *Trash { return &Trash{items: map[string]map[nilda.TrashRef]TrashEntry{}} }

// Core builds a fake Core for the plugin key, granted exactly the capabilities named, whose API answers the bin's
// routes from this fake under that plugin's own identity. Close the server when done.
func (b *Trash) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(b.routes(key, h))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

// Items returns what the bin holds for plugin, sorted by kind and id.
func (b *Trash) Items(plugin string) []TrashEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]TrashEntry, 0, len(b.items[plugin]))
	for _, e := range b.items[plugin] {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Restore asks the plugin to bring one of its items back, as the bin's "Put back" does. A refusal is returned as the
// plugin's own error — the sentence the person would read — and the entry stays.
func (b *Trash) Restore(ctx context.Context, plugin string, hook HookFunc, e TrashEntry) error {
	return b.drive(ctx, plugin, hook, nilda.TrashRestoreHook, e.TrashRef)
}

// Purge tells the plugin one of its items is gone for good, as a person's "Delete for good" or the 30 days do. An
// error keeps the entry, as Core keeps it for the next sweep.
func (b *Trash) Purge(ctx context.Context, plugin string, hook HookFunc, e TrashEntry) error {
	return b.drive(ctx, plugin, hook, nilda.TrashPurgeHook, e.TrashRef)
}

func (b *Trash) drive(ctx context.Context, plugin string, hook HookFunc, name string, ref nilda.TrashRef) error {
	b.mu.Lock()
	_, ok := b.items[plugin][ref]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("nildatest: %s has no %s %q in the bin", plugin, ref.Kind, ref.ID)
	}
	payload, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	ctx = nilda.WithCallInfo(ctx, nilda.CallInfo{RequestID: "nildatest-trash"})
	if _, err := hook(ctx, name, payload); err != nil {
		return err
	}
	b.mu.Lock()
	delete(b.items[plugin], ref)
	b.mu.Unlock()
	return nil
}

func (b *Trash) routes(caller string, h *Host) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/")
		if len(parts) != 3 || parts[0] != "plugin-trash" {
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
			return
		}
		h.mu.Lock()
		granted := h.granted["trash"]
		h.mu.Unlock()
		if !granted {
			writeError(w, &apiError{http.StatusForbidden, "FORBIDDEN",
				fmt.Sprintf("plugin %q lacks the \"trash\" capability — declare it in plugin.json", caller)})
			return
		}
		id, err := url.PathUnescape(parts[2])
		if err != nil {
			writeError(w, invalid("the item's id is not a path segment"))
			return
		}
		ref := nilda.TrashRef{Kind: parts[1], ID: id}
		b.mu.Lock()
		defer b.mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			var in struct {
				Name                string `json:"name"`
				ExpiresAfterSeconds int64  `json:"expires_after_seconds"`
				RequestID           string `json:"request_id"`
			}
			body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
			if err := json.Unmarshal(body, &in); err != nil {
				writeError(w, invalid("the body is not JSON"))
				return
			}
			item := nilda.TrashItem{Kind: ref.Kind, ID: ref.ID, Name: in.Name,
				ExpiresAfter: time.Duration(in.ExpiresAfterSeconds) * time.Second}
			if err := item.Validate(); err != nil {
				writeError(w, invalid("%s", err.Error()))
				return
			}
			stay := item.ExpiresAfter
			if stay == 0 {
				stay = nilda.TrashRetention
			}
			if b.items[caller] == nil {
				b.items[caller] = map[nilda.TrashRef]TrashEntry{}
			}
			b.items[caller][ref] = TrashEntry{TrashRef: ref, Name: strings.TrimSpace(in.Name), ExpiresAfter: stay,
				RequestID: in.RequestID}
			answer(w, http.StatusOK, map[string]any{"kind": ref.Kind, "id": ref.ID}, nil)
		case http.MethodDelete:
			if _, ok := b.items[caller][ref]; !ok {
				writeError(w, notFound("that item is not in the bin"))
				return
			}
			delete(b.items[caller], ref)
			answer(w, http.StatusOK, struct{}{}, nil)
		default:
			writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
		}
	})
}
