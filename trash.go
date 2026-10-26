package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// THE SITE'S ONE RECYCLE BIN, for your own records (`trash`; Core's D-108, the shop's DR-22).
//
// Everything the site's assistant can delete must go to a bin it cannot empty — the owner's rule. Core keeps ONE bin:
// pages, media, popups, menu rows… and, through this file, the records of every plugin. You keep your row (mark it
// deleted in your own tables, hide it from your own lists) and tell Core you moved it to the bin with Trash. Core
// lists it beside its own items, with the name you gave, for 30 days.
//
//   - Somebody puts it back (the bin's "Put back", or the assistant's trash_restore): Core calls your
//     TrashRestoreHook with its kind and id. Re-run your own checks — a discount code somebody has re-used since, a
//     slot that is taken now — and either bring the row back (return nil) or refuse in a sentence the person can act
//     on (return an error; its text reaches them, as an admin action's does). Core keeps the entry when you refuse.
//   - A person deletes it for good (only a person, on the bin's screen — no ability, no assistant tool, no MCP tool
//     reaches it), or the 30 days run out: Core calls your TrashPurgeHook. Delete the row for good then.
//
// Core never calls these hooks for another plugin's items, and you cannot report, restore or purge anybody else's: your
// key comes from your token, never from the request. While your plugin is uninstalled its items stay in the bin and
// wait — nothing is deleted for good until you are back to do it, or the owner deletes your kept data.
//
// Subscribe both hooks and route them with DispatchTrash:
//
//	return nilda.InitResult{Hooks: []string{nilda.TrashRestoreHook, nilda.TrashPurgeHook}}, nil
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchTrash(ctx, hook, payload, p.restore, p.purge); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
// What the law requires deleted at once is not binned: a privacy erasure (`personal_data`), a back-in-stock request
// withdrawn. If a record you binned is erased that way, or your own screen brings it back, tell Core with Untrash so
// the bin does not keep offering a record that is not there to restore.

// The two hooks Core calls about an item in the bin. Admitted by the `trash` grant, never by `hooks`.
const (
	TrashRestoreHook = "trash.restore"
	TrashPurgeHook   = "trash.purge"
)

// TrashRetention is how long Core keeps an item in the bin unless you ask for less (TrashItem.ExpiresAfter): the
// same 30 days every other thing in the site's bin gets, so the owner learns one number.
const TrashRetention = 30 * 24 * time.Hour

// MinTrashRetention is the shortest stay you may ask for. Shorter than an hour is a delete with extra steps.
const MinTrashRetention = time.Hour

// The shapes Core accepts. A kind is your own short name for a sort of record ("discount", "gift_card"); an id is
// that record's own identity in your tables; a name is what a person reads in the bin ("SUMMER10").
const (
	MaxTrashKind = 40
	MaxTrashID   = 128
	MaxTrashName = 200
)

var (
	trashKindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	trashIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
)

// TrashItem is one of your records you have just moved to the bin.
type TrashItem struct {
	// Kind is your name for the sort of record: lowercase letters, digits and underscores, starting with a letter, at
	// most 40. Core shows it beside your plugin's name, so "discount" reads as "Shop · discount".
	Kind string
	// ID is the record's own id in your tables — what Core hands back to your hooks. 1–128 letters, digits and
	// . _ : - (a UUID, a code).
	ID string
	// Name is what a person reads in the bin: the code, the title. 1–200 characters. Say which one, not what sort —
	// the kind already says that.
	Name string
	// ExpiresAfter is how long Core keeps it before TrashPurgeHook — zero for the bin's 30 days; otherwise between an
	// hour and 30 days. Never longer: the bin's window is the owner's promise, for your items as for Core's.
	ExpiresAfter time.Duration
}

// TrashRef names one of your items in the bin — what your two hooks receive.
type TrashRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// trashPut is the body of Core's PUT /plugin-trash/{kind}/{id}. RequestID is the call you are handling (CallOf), so
// Core can record who deleted it and whether the assistant asked — Core trusts it only for a call it is making to you
// at that moment.
type trashPut struct {
	Name                string `json:"name"`
	ExpiresAfterSeconds int64  `json:"expires_after_seconds,omitempty"`
	RequestID           string `json:"request_id,omitempty"`
}

// Validate reports the first thing Core would refuse about the item.
func (it TrashItem) Validate() error {
	if err := validTrashRef(it.Kind, it.ID); err != nil {
		return err
	}
	name := strings.TrimSpace(it.Name)
	if name == "" || utf8.RuneCountInString(name) > MaxTrashName {
		return fmt.Errorf("nilda: a bin item's name is 1-%d characters, got %d", MaxTrashName, utf8.RuneCountInString(name))
	}
	if it.ExpiresAfter != 0 && (it.ExpiresAfter < MinTrashRetention || it.ExpiresAfter > TrashRetention) {
		return fmt.Errorf("nilda: a bin item stays between an hour and 30 days, got %s", it.ExpiresAfter)
	}
	return nil
}

func validTrashRef(kind, id string) error {
	if len(kind) == 0 || len(kind) > MaxTrashKind || !trashKindRe.MatchString(kind) {
		return fmt.Errorf("nilda: a bin item's kind is 1-%d lowercase letters, digits or underscores, starting with a letter, got %q",
			MaxTrashKind, kind)
	}
	if len(id) == 0 || len(id) > MaxTrashID || !trashIDRe.MatchString(id) {
		return fmt.Errorf("nilda: a bin item's id is 1-%d letters, digits or . _ : -, got %q", MaxTrashID, id)
	}
	return nil
}

func trashPath(kind, id string) string {
	return "/plugin-trash/" + kind + "/" + url.PathEscape(id)
}

// Trash tells Core you moved one of your records to the site's recycle bin (requires `trash`). Call it AFTER your own
// write marked the row deleted: the bin then offers something you can really bring back.
//
// Reporting the same kind and id again refreshes its entry — its name, its 30 days — rather than listing it twice.
// A refusal is an *APIError: 403 for a manifest that does not declare `trash`, 422 for an item Validate refuses.
func (c *Core) Trash(ctx context.Context, item TrashItem) error {
	if err := item.Validate(); err != nil {
		return err
	}
	body := trashPut{Name: strings.TrimSpace(item.Name), RequestID: CallOf(ctx).RequestID}
	if item.ExpiresAfter > 0 {
		body.ExpiresAfterSeconds = int64(item.ExpiresAfter / time.Second)
	}
	return c.API().JSON(ctx, http.MethodPut, trashPath(item.Kind, item.ID), body, nil)
}

// Untrash takes your item out of the bin's list WITHOUT asking you to restore or purge it — for a record that left the
// bin by your own act: your own screen put it back, or the law made you erase it (a privacy erasure). It never deletes
// anything; deleting for good is the purge hook's, and only a person or the 30 days start it. An item that is not in
// the bin is not an error.
func (c *Core) Untrash(ctx context.Context, kind, id string) error {
	if err := validTrashRef(kind, id); err != nil {
		return err
	}
	err := c.API().JSON(ctx, http.MethodDelete, trashPath(kind, id), nil, nil)
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.NotFound() {
		return nil
	}
	return err
}

// DispatchTrash routes Core's two bin hooks to your handlers, and shapes the reply Core expects. handled is false for
// any other hook, so a plugin with hooks of its own passes it on.
//
// An error your restore returns reaches the person who pressed "Put back" (or the assistant that asked) as the reason
// it did not come back — its first 300 characters — so make it a sentence they can act on ("The code SUMMER10 is in
// use by another discount now"). It is your answer, not a failure: it never counts toward switching the plugin off.
// An error your purge returns keeps the entry in the bin, and Core asks again on the next sweep; delete idempotently,
// so a record already gone answers nil.
func DispatchTrash(ctx context.Context, hook string, payload []byte,
	restore, purge func(context.Context, TrashRef) error) (out []byte, handled bool, err error) {
	var fn func(context.Context, TrashRef) error
	switch hook {
	case TrashRestoreHook:
		fn = restore
	case TrashPurgeHook:
		fn = purge
	default:
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: Core called %s and this plugin registered no handler for it", hook)
	}
	var ref TrashRef
	if err := json.Unmarshal(payload, &ref); err != nil {
		return nil, true, fmt.Errorf("nilda: %s payload: %w", hook, err)
	}
	if err := fn(ctx, ref); err != nil {
		return nil, true, err
	}
	return []byte("{}"), true, nil
}
