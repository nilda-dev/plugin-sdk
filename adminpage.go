package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// A PLUGIN'S OWN CORNER OF THE ADMIN.
//
// Until this existed a plugin could serve the public site and contribute page-builder widgets, and had
// nowhere at all in the panel. Installing one added a row to the Plugins list and nothing else: no screen,
// no settings, no way to ask the site owner a question. That is why the moment a plugin needs an API key —
// which is most of the integrations a marketplace exists for — it could not be written. There was no place
// for the owner to type it.
//
// # It is DECLARED, like everything else a plugin contributes
//
// A plugin says WHAT its pages contain; Core draws them with the panel's own controls. It ships no
// JavaScript into the admin, which is the same rule the editor commands and the declared tables follow, and
// for the same three reasons: the plugin is a separate process with no route to the browser, the admin's CSP
// refuses third-party script, and the panel holds an administrator's session. Every other CMS extends its
// admin by injecting code — a WordPress plugin enqueues a script, a Strapi plugin ships React — and every
// one of them pays for it with a panel that a bad plugin can break.
//
// The cost is honest: you can express what the vocabulary below expresses, and no more. If your plugin
// needs a canvas, serve it from your own `route` and link to it.
//
// # Where it lands
//
// One section in the sidebar, named after your plugin, below Core's own rows. A plugin cannot choose to sit
// above Media — WordPress lets it, and the result is a rail nobody arranged where an owner cannot tell
// which rows came from what they installed. A person who uses your plugin daily can drag it up themselves.

// Setting field types an admin page may declare. They are the SAME vocabulary the page builder uses for
// widget configuration (widgets.go) — deliberately, so an author learns one set of type names — minus the
// structural and page-only ones, which a settings form has no meaning for.
const (
	SettingText     = FieldText
	SettingTextarea = FieldTextarea
	SettingURL      = FieldURL
	SettingEmail    = FieldEmail
	SettingPhone    = FieldPhone
	SettingNumber   = FieldNumber
	SettingBoolean  = FieldBoolean
	SettingSelect   = FieldSelect
	SettingRadio    = FieldRadio
	SettingColor    = FieldColor
	SettingMessage  = FieldMessage
)

// SettingFieldTypes returns every type an admin-page field may declare, sorted.
//
// Exported so the contract can be CHECKED rather than described: Core walks this list in a test and fails if
// it names a type Core would drop. A field with an unknown type is dropped on Core's side, silently, which
// an author has no way to distinguish from a typo.
func SettingFieldTypes() []string {
	out := []string{
		SettingText, SettingTextarea, SettingURL, SettingEmail, SettingPhone,
		SettingNumber, SettingBoolean, SettingSelect, SettingRadio, SettingColor, SettingMessage,
	}
	sort.Strings(out)
	return out
}

// SettingField is one control on a declared admin page.
type SettingField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"` // one of the Setting* constants

	// Secret marks a credential: an API key, a webhook signing secret, a gateway password.
	//
	// Core encrypts it at rest and NEVER sends it back — the screen shows that a value is configured, not
	// what it is. This is the one thing a plugin must not do for itself. An author storing a Zarinpal key in
	// their own table is not being careless; encryption simply is not their job, and if two hundred authors
	// each solve it, some number of them store it in plain text on somebody else's site.
	//
	// It also means YOUR plugin receives the real value at Init and the admin screen does not, which is the
	// right way round: the process that must call the gateway needs the key, and the browser never does.
	Secret bool `json:"secret,omitempty"`

	Required bool `json:"required,omitempty"`
	// Default is what the field holds until the owner sets it. Its type must match Type.
	Default any `json:"default,omitempty"`
	// Choices are the options for SettingSelect and SettingRadio. A choice field with none is dropped: an
	// empty dropdown is a control nobody can answer.
	Choices []string `json:"choices,omitempty"`
	// Placeholder and Help are affordances on the form. Help is where you explain a field you could not name
	// clearly enough — "find this under Account → API in your Kavenegar panel" belongs there, not in a README
	// the owner is not reading while looking at the box.
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// AdminPage is one page inside your plugin's section of the sidebar.
type AdminPage struct {
	// Key is stable and yours alone — Core namespaces the route with your plugin key, so two plugins may
	// both have a "settings" page.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Icon is a name from Core's bundled icon set; unknown names fall back to a generic one rather than
	// rendering nothing.
	Icon string `json:"icon,omitempty"`
	// Kind is what the page IS. Today: "settings" — a form Core renders, validates and stores.
	//
	// A page whose kind Core does not recognise is dropped rather than rendered blank, so a plugin built
	// against a newer Nilda degrades to its other pages instead of showing an owner an empty screen.
	Kind   string         `json:"kind"`
	Fields []SettingField `json:"fields,omitempty"`
	// Help is shown at the top of the page — one or two sentences on what this page is for.
	Help string `json:"help,omitempty"`

	// --- kind: "list" ---

	// Table is one of YOUR OWN declared tables. Core created it from your manifest and owns it, so a list
	// page can only ever show storage the site owner already approved at install.
	Table   string       `json:"table,omitempty"`
	Columns []ListColumn `json:"columns,omitempty"`
	// OrderBy is one of that table's columns; Order is "asc" or "desc".
	OrderBy string `json:"order_by,omitempty"`
	Order   string `json:"order,omitempty"`
	// Search are the TEXT columns the search box looks in. Empty means no search box, which is better than
	// one that silently matches nothing.
	Search []string `json:"search,omitempty"`
	// Actions are buttons on each row, and they are why a list page is READ-ONLY.
	//
	// If Core let an administrator edit your orders row directly, it would set `status = 'refunded'` without
	// the refund happening, the email going out or the stock coming back — YOUR logic bypassed by YOUR admin
	// screen, with nobody able to tell. So Core shows the row and you change it: pressing an action calls
	// your `admin.action` hook with the row's primary key, and what happens next is yours.
	Actions []RowAction `json:"actions,omitempty"`
}

// AdminPageKind values.
const (
	// AdminPageSettings is a form: Core renders the fields, validates what is typed, stores it, and hands
	// the values to your plugin at Init.
	AdminPageSettings = "settings"
	// AdminPageList shows rows from one of your own declared tables.
	AdminPageList = "list"
)

// ListColumn is one column of a list page. Key names a column of the page's table; Type is how to RENDER it
// (text, number, datetime, boolean) and never changes what is fetched.
type ListColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
}

// RowAction is one button on a row of a list page.
//
// Pressing it calls your `admin.action` hook with the row's primary key. Core never writes your table
// itself — see the note on AdminPage.Actions.
type RowAction struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Confirm is the question asked first. Set it for anything the owner cannot undo.
	Confirm string `json:"confirm,omitempty"`
}

// AdminActionHook is the hook Core calls when somebody presses a row action.
const AdminActionHook = "admin.action"

// AdminAction is one press of a row action: which page, which button, and which row.
//
// The id is the row's PRIMARY KEY, as text, from your own table. Nilda read it out of the row it showed;
// it never invents one and never sends you a row you did not declare a page for.
type AdminAction struct {
	Page   string `json:"page"`
	Action string `json:"action"`
	ID     string `json:"id"`
}

// AdminActionResult is what the owner is told. `Message` is the field the admin reads — anything else you
// return is ignored, and an empty message shows "Done".
//
// Worth being exact about, because the failure is silent: an action that returns `{"result":"refunded"}`
// runs perfectly and the person who pressed the button sees "Done", with no way to learn what happened.
// Say what happened.
type AdminActionResult struct {
	Message string `json:"message,omitempty"`
}

// DispatchAdminAction routes a row-action press to your handler and shapes the reply Nilda expects.
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchAdminAction(ctx, hook, payload, p.onAction); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
//	func (p *Plugin) onAction(ctx context.Context, a nilda.AdminAction) (nilda.AdminActionResult, error) {
//		switch a.Action {
//		case "refund":
//			return nilda.AdminActionResult{Message: "Refunded order " + a.ID}, p.refund(ctx, a.ID)
//		}
//		return nilda.AdminActionResult{}, fmt.Errorf("unknown action %q", a.Action)
//	}
//
// An error you return reaches the owner as the reason the action did not run, so make it a sentence they
// can act on. handled=false means the hook was not a row action, so a plugin with hooks of its own passes
// it on rather than failing it.
func DispatchAdminAction(ctx context.Context, hook string, payload []byte, fn func(context.Context, AdminAction) (AdminActionResult, error)) (out []byte, handled bool, err error) {
	if hook != AdminActionHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a row action was pressed and this plugin registered no handler")
	}
	var a AdminAction
	if err := json.Unmarshal(payload, &a); err != nil {
		return nil, true, fmt.Errorf("nilda: admin.action payload: %w", err)
	}
	res, err := fn(ctx, a)
	if err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}

// Setting returns one of this plugin's stored settings, as a string.
//
// Values arrive at Init and do not change while the process runs: Core RESTARTS a plugin when its settings
// are saved, so a running plugin never holds a key the owner has already replaced. That is why there is no
// watch API here — there is nothing to watch.
func (c *Core) Setting(key string) string {
	if c == nil {
		return ""
	}
	v, _ := c.settings[key].(string)
	return v
}

// SettingBool returns a boolean setting.
func (c *Core) SettingBool(key string) bool {
	if c == nil {
		return false
	}
	switch v := c.settings[key].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1"
	}
	return false
}

// SettingNumber returns a numeric setting. JSON numbers arrive as float64, so this is the one accessor that
// has to look at more than one type.
func (c *Core) SettingNumber(key string) float64 {
	if c == nil {
		return 0
	}
	switch v := c.settings[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	}
	return 0
}

// HasSetting reports whether a setting holds a non-empty value. Use it before doing work that cannot
// succeed without one — "no API key configured yet" is a state to handle, not an error to log every minute.
func (c *Core) HasSetting(key string) bool {
	if c == nil {
		return false
	}
	switch v := c.settings[key].(type) {
	case nil:
		return false
	case string:
		return v != ""
	default:
		return true
	}
}
