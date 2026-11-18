package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// BULK ACTIONS (v0.69.0, EXPERIMENTAL). A list page may offer actions on MANY rows at once: the owner ticks rows (or
// "all rows matching this view" — the search and the filters as they stand), presses one of the page's BulkActions,
// answers its Fields if it has any, and Core hands the chosen rows to your `admin.bulk_action` hook in batches of at
// most MaxBulkBatch ids, showing the owner the progress and every row you said failed.
//
// Core resolves the selection itself, from your declared table, with the page's own search and filters: an id you
// receive is always a primary key of that table — never one the browser typed — and "all matching" is the rows the
// owner was looking at, read in primary-key order so a row that changes while the run goes is neither skipped nor
// sent twice. At most MaxBulkSelection rows a run.
//
// Like a row action, a bulk action is a request, never a write Core performs: your hook decides what each row means,
// and checks each row as it would a single press, because a row can change between the owner's tick and its batch.
// The assistant reaches the same door through Core's `plugin_bulk_action` tool, so a bulk action needs no ability of
// its own — but when you already have an ability that does the same work over many rows, make both call ONE function
// (a shop's price change previews through the same code from the list and from the chat).

// MaxPageBulkActions bounds how many bulk actions one list page may declare.
const MaxPageBulkActions = 6

// MaxBulkBatch is the most row ids one `admin.bulk_action` call carries.
const MaxBulkBatch = 50

// MaxBulkSelection is the most rows one run may cover — ticked one by one or "all matching".
const MaxBulkSelection = 5000

// MaxBulkActionFields bounds a bulk action's Fields.
const MaxBulkActionFields = 12

// BulkAction is one action on the rows the owner selected on a list page.
type BulkAction struct {
	// Key is stable and unique among the page's bulk actions (it may repeat a row action's key: they are apart).
	Key string `json:"key"`
	// Label is the button, translated through your translations ("Change prices", "Mark shipped").
	Label string `json:"label"`
	// Confirm is the question Core asks ONCE before the run starts, naming how many rows it covers. REQUIRED: Core
	// refuses a bulk action without one at install and does not run a stored one — one click over thousands of rows
	// with nobody asked is not a run it starts. A fixed sentence, translated through your translations; at most 300
	// characters.
	Confirm string `json:"confirm,omitempty"`
	// Fields are what the owner answers before the run — the new price, the carrier. The form vocabulary
	// (FormField): Core checks every value against its field exactly as it checks a form's, so your hook receives
	// typed values (money in minor units, a date as YYYY-MM-DD). At most MaxBulkActionFields; no repeaters, files or
	// galleries. Sent to every batch of the run unchanged.
	Fields []FormField `json:"fields,omitempty"`
	// AlwaysAsk makes the assistant ask the person before it starts a run of this action, whatever a connector was
	// granted — a run that moves money or lets data out. Core's bulk tool asks before every run anyway; this is the
	// declaration that the action is one of those.
	AlwaysAsk bool `json:"always_ask,omitempty"`
	// Permission is the key of one of your manifest's `permissions` a person needs to start this action (see
	// PermissionDecl). Empty: only those who may configure the whole plugin.
	Permission string `json:"permission,omitempty"`
}

// AdminBulkActionHook is the hook Core calls with each batch of a bulk action's run. SUBSCRIBE IT, as you do
// AdminActionHook; the grant is `admin_page`.
const AdminBulkActionHook = "admin.bulk_action"

// AdminBulkAction is one batch of a run.
//
// Run is "" on a run's first batch; whatever you answer as AdminBulkActionResult.Run comes back on every later
// batch of the same run, so a plugin that gathers the rows into one record of its own (a preview) knows which.
// Last is true on the run's final batch: the place to close that record and say where the owner finds it. Values
// are the bulk action's Fields, checked. Who pressed is CallOf(ctx).ActorUserID, as for a row action.
type AdminBulkAction struct {
	Page   string         `json:"page"`
	Action string         `json:"action"`
	IDs    []string       `json:"ids"`
	Values map[string]any `json:"values,omitempty"`
	Run    string         `json:"run,omitempty"`
	// Batch is the batch's number in its run, from 1.
	Batch int  `json:"batch"`
	Last  bool `json:"last,omitempty"`
	// Total is how many rows the whole run covers.
	Total int `json:"total"`
}

// AdminBulkActionResult answers one batch.
type AdminBulkActionResult struct {
	// Rows says what happened to each id of the batch you have something to say about. An id you leave out counts as
	// done. A row with Failed set is shown to the owner with its Message as the reason; Skipped is "not this one, and
	// that is not a failure" (an order already shipped), with its reason too. Messages are fixed sentences, translated
	// through your translations; at most 300 characters each.
	Rows []BulkRowResult `json:"rows,omitempty"`
	// Run is your own name for the run (a preview's id); Core sends it back on the run's later batches. At most 128
	// characters.
	Run string `json:"run,omitempty"`
	// Message is shown when the run ends — your last batch's, a fixed sentence translated like a label.
	Message string `json:"message,omitempty"`
	// Open names a row of one of your own list pages the owner should open when the run ends — the preview a run
	// gathered, with its Apply button. Core draws it as a link.
	Open *BulkOpen `json:"open,omitempty"`
}

// BulkRowResult is one row's outcome in a batch.
type BulkRowResult struct {
	ID      string `json:"id"`
	Failed  bool   `json:"failed,omitempty"`
	Skipped bool   `json:"skipped,omitempty"`
	Message string `json:"message,omitempty"`
}

// BulkOpen names one row of one of your list pages: Page is the page's key, Row the row's primary key.
type BulkOpen struct {
	Page string `json:"page"`
	Row  string `json:"row"`
}

// DispatchAdminBulkAction routes a batch to your handler, as DispatchAdminAction routes a press. An error you return
// fails the WHOLE batch with that sentence as each row's reason, and the run stops there: the owner reads it and the
// rows before it keep what they did.
func DispatchAdminBulkAction(ctx context.Context, hook string, payload []byte,
	fn func(context.Context, AdminBulkAction) (AdminBulkActionResult, error)) (out []byte, handled bool, err error) {
	if hook != AdminBulkActionHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a bulk action was pressed and this plugin registered no handler")
	}
	var a AdminBulkAction
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, true, fmt.Errorf("nilda: admin.bulk_action payload: %w", err)
	}
	res, err := fn(ctx, a)
	if err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}
