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
//
// # The declaration types are the manifest, typed
//
// AdminPage, SettingField, ListColumn and RowAction describe the `admin_pages` block of plugin.json. Nothing
// in Serve reads them — Core reads the manifest FILE, not your code — so declaring a page means writing it in
// plugin.json. They exist so a Go author can generate or test that block from typed values instead of
// hand-written JSON, and Core holds them equal to its own parser, field for field (its sdk_mirror_test).
// The request and result types further down (AdminAction, AdminReportRequest, …) are the ones that travel
// at run time.

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
	//
	// SETTINGS PAGES ONLY, and only on a text or textarea field. On a report page Fields are filters — never
	// stored, never masked, and carried in the page's address on every view — so Core refuses `secret` there
	// at install rather than promise protection a filter cannot have.
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
	// AlwaysAsk makes the assistant ask the person before it saves a change to this field, whatever a connector was
	// granted (v0.41.0) — for a setting that sends the site's data somewhere, such as where order alerts are mailed.
	AlwaysAsk bool `json:"always_ask,omitempty"`
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
	// Group files the page under a heading in your section of the sidebar (v0.32.0) — a shop's "Sales", "Catalog",
	// "Settings" — so a section of many pages reads in a few kinds rather than one long list. Pages with the same
	// group sit together, under the heading, in the order you declare them; a page with none sits above the first
	// heading. Your own word, translated through your translations like a label; at most 120 characters.
	Group string `json:"group,omitempty"`
	// Kind is what the page IS: "settings" — a form Core renders, validates and stores; "list" — read-only
	// rows from one of your own declared tables; "report" — read-only rows YOU compute, for anything a
	// "list" page cannot show because it is not one table's rows (a total, a GROUP BY, a number joined
	// across several of your own tables).
	//
	// A page whose kind Core does not recognise is dropped at install rather than rendered blank — and the
	// install reports it among the fields that Nilda does not know — so a plugin built against a newer Nilda
	// degrades to its other pages instead of being refused. When no page is left, the `admin_page` grant is
	// dropped (and reported) with the last one, since Core refuses the grant without a page. `nilda plugin
	// check` still refuses an unknown kind, since there the author is the one reading the error.
	Kind string `json:"kind"`
	// Fields means two different things depending on Kind. On a "settings" page they are STORED
	// configuration, read back at your next Init. On a "report" page they are FILTER INPUTS — a date
	// range, a choice of which report to show — sent to you fresh on every request and never stored at
	// all; there is nothing to persist about "which report am I looking at right now." A "calendar"
	// page's fields are filters in exactly the same way. Same vocabulary in every case, so an author
	// learns one set of controls — except Secret, which only a settings page can hold (see
	// SettingField.Secret).
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
	// Filters are drop-downs above the rows that narrow them (v0.38.0): by one value of a column — a booking's
	// status — or by a stretch of days — today, this week, next month. At most MaxListFilters. Core builds the
	// query from the declaration and binds what the owner chose; nothing they type becomes SQL.
	Filters []ListFilter `json:"filters,omitempty"`
	// Detail is the key of a TEXT column of Table, shown after the name wherever rows of this page are PICKED (a
	// form's reference field) or NAMED (a list column with Reference): "Yoga flow · yoga-flow-2". It tells apart
	// two rows whose first column reads the same — a class's slug, a product's code. A row with no value there
	// is shown by its name alone. Only a "list" page may set it; it must be a column of Table other than the
	// first of Columns, and a text column (not money, not a number). It need NOT be among the displayed Columns:
	// Core reads it for the rows it offers regardless (v0.35.0).
	Detail string `json:"detail,omitempty"`
	// Actions are buttons on each row, and they are why a list page is READ-ONLY.
	//
	// If Core let an administrator edit your orders row directly, it would set `status = 'refunded'` without
	// the refund happening, the email going out or the stock coming back — YOUR logic bypassed by YOUR admin
	// screen, with nobody able to tell. So Core shows the row and you change it: pressing an action calls
	// your `admin.action` hook with the row's primary key, and what happens next is yours.
	Actions []RowAction `json:"actions,omitempty"`
	// Form lets the owner create and edit this list's rows in a form you declare and save yourself (adminform.go).
	// Nil keeps the list read-only.
	Form *PageForm `json:"form,omitempty"`
	// Edits names one of YOUR content types (the manifest's content_types) whose items this page's Form edits — a
	// shop's products page edits its product content type, the item that gives each product its address, search
	// and sitemap entry (v0.31.0). Core then offers that type nowhere else: it is not its own row in the sidebar
	// (its taxonomies move into your section), and every way into one of its items — the content list, "Add
	// new", the search — opens this form instead, a new row for "Add new" and the item's CONTENT id as the row id
	// for an item. So your form loads a row by its own id OR by the content id it mirrors. Needs Form with Edit.
	Edits string `json:"edits,omitempty"`

	// --- kind: "report" ---

	// Metrics are a report page's headline numbers, TYPED: each names a key of your AdminReportResult.Summary
	// and says how to show it. Summary alone is a map of whatever you put in it, so a screen can only print it
	// as text — a total in cents reads "12900". Declared here, the panel and the Nilda app show money as money.
	Metrics []ReportMetric `json:"metrics,omitempty"`

	// --- kind: "calendar" ---

	// RowsPage is the key of one of YOUR "list" pages: each event's ID is a row id of that page, opening
	// an event offers that page's row actions and form, and its label is what the grid names as the rows
	// the events open. Core refuses a calendar page whose rows_page is not a list page of your manifest.
	RowsPage string `json:"rows_page,omitempty"`
	// DefaultView is the grid a calendar page opens on: "day", "week" or "month"; empty is "week".
	DefaultView string `json:"default_view,omitempty"`

	// --- any kind ---

	// Mobile says how this page appears in the Nilda app on the owner's phone. Optional: every page of every
	// installed plugin appears there without it, drawn generically from what is declared above.
	Mobile *PageMobile `json:"mobile,omitempty"`
}

// THE NILDA APP. The owner's phone shows every installed plugin by itself — a plugin needs no app code and
// waits for no app release (mobile/docs/BUILD_PLAN_V1.md, Unit A). Core resolves what you declare into the
// app's own blocks, so the hints below only make that drawing better: a money column shown as money, a row
// laid out as a title and a line under it, a status shown as a coloured badge. Words that may grow with
// Nilda — a column type, a metric format, a badge tone, an icon — are never refused at install: one this
// Nilda does not know is drawn as plain text or the generic icon, so a plugin built for a newer Nilda still
// installs on an older one, and `nilda plugin check` names the unknown word to you.

// Column types a list page or a report may declare. A type Core does not know is drawn as text.
const (
	ColumnText     = "text"
	ColumnNumber   = "number"
	ColumnDate     = "date"
	ColumnDatetime = "datetime"
	ColumnBoolean  = "boolean"
	// ColumnMoney is an integer in the currency's MINOR units — 1290 is €12.90, 500 is ¥500. Name the currency
	// on the column (ListColumn.Currency or ListColumn.CurrencyColumn).
	ColumnMoney = "money"
	// ColumnBadge is a short status the screen draws as a coloured badge; ListColumn.Badges gives each value
	// its tone.
	ColumnBadge = "badge"
	// ColumnPercent is the number itself, not a fraction: 20 is 20 % (v0.32.0).
	ColumnPercent = "percent"
)

// ColumnTypes returns every column type Core draws, sorted.
func ColumnTypes() []string {
	out := []string{ColumnText, ColumnNumber, ColumnDate, ColumnDatetime, ColumnBoolean, ColumnMoney, ColumnBadge, ColumnPercent}
	sort.Strings(out)
	return out
}

// Badge tones. A tone is a meaning, not a colour — the app picks the colour for light and dark — and a value
// a badge column holds with no tone declared is drawn grey.
const (
	BadgeGreen = "green" // done, paid, active
	BadgeGold  = "gold"  // waiting on the owner: to pack, to answer
	BadgeBlue  = "blue"  // under way: shipped, in progress
	BadgeWarn  = "warn"  // needs attention: failed, disputed
	BadgeGrey  = "grey"  // over: refunded, cancelled, archived
)

// BadgeTones returns every badge tone, sorted.
func BadgeTones() []string {
	out := []string{BadgeGreen, BadgeGold, BadgeBlue, BadgeWarn, BadgeGrey}
	sort.Strings(out)
	return out
}

// Metric formats. A format Core does not know is drawn as text.
const (
	MetricNumber  = "number"  // the default
	MetricMoney   = "money"   // an integer in minor units, like ColumnMoney; name the currency
	MetricPercent = "percent" // the number itself, not a fraction: 12.5 is 12.5 %
	MetricText    = "text"
)

// MetricFormats returns every metric format, sorted.
func MetricFormats() []string {
	out := []string{MetricNumber, MetricMoney, MetricPercent, MetricText}
	sort.Strings(out)
	return out
}

// ReportMetric is one headline number of a report page.
type ReportMetric struct {
	// Key is a key of your AdminReportResult.Summary.
	Key   string `json:"key"`
	Label string `json:"label"`
	// Format is one of the Metric* constants; empty is MetricNumber.
	Format string `json:"format,omitempty"`
	// A MetricMoney names its currency once (Currency: "EUR") or names the Summary key that holds its ISO 4217
	// code (CurrencyKey). Exactly one.
	Currency    string `json:"currency,omitempty"`
	CurrencyKey string `json:"currency_key,omitempty"`
}

// PageMobile is how one page appears in the Nilda app.
type PageMobile struct {
	// Hide keeps the page off the phone — a page that only makes sense beside a large screen. Prefer leaving
	// it shown: every page Core knows is drawn on the phone, and one an older app cannot draw becomes a card
	// saying it needs a newer app.
	Hide bool `json:"hide,omitempty"`
	// Row lays out each row of a list page as the phone shows it. Without it the app shows the first columns.
	Row *MobileRow `json:"row,omitempty"`
	// Pin lets the owner pin a row of this list page to a message in the site's chat ("Ask in Chat about this
	// order"); the assistant then reads that row, as the owner may see it. Needs a table with a primary key.
	Pin bool `json:"pin,omitempty"`
	// Pick makes this pin page a level of the owner app's step picker: its groups, the photo its rows show,
	// the tools a pin of it brings the assistant, and the action chips offered after a pick. Needs Pin.
	Pick *PagePick `json:"pick,omitempty"`
}

// MobileRow names, by column key, what a list row shows on the phone. Every name is one of the page's
// declared columns.
type MobileRow struct {
	Title string `json:"title"`
	// Subtitle are at most two columns, shown under the title joined by " · ".
	Subtitle []string `json:"subtitle,omitempty"`
	// Trailing is shown at the row's end — usually a money column.
	Trailing string `json:"trailing,omitempty"`
	// Badge is a column of type ColumnBadge, shown under Trailing.
	Badge string `json:"badge,omitempty"`
}

// PluginMobile is the plugin-level `mobile` block of plugin.json.
type PluginMobile struct {
	// Icon is one of MobileIcons(): the app ships the icons, so a plugin names one rather than sending a picture.
	Icon string `json:"icon,omitempty"`
	// Headline are at most three numbers the app shows on the plugin's row and on top of its screen — "14
	// orders today" — each a metric of one of your report pages.
	Headline []HeadlineMetric `json:"headline,omitempty"`
	// Label is what the plugin DOES in two or three plain words — "Shop", "Card payments", "Bookings" — the row
	// the owner app's step picker shows at its first level. Translated through your translations. Optional:
	// without it the picker shows your Name.
	Label string `json:"label,omitempty"`
}

// PagePick is how one pin page appears in the owner app's step picker (v0.35.0).
type PagePick struct {
	// GroupBy splits the page's rows into groups the picker lists under the page.
	GroupBy *PickGroupBy `json:"group_by,omitempty"`
	// Photo is a column of the page's table holding a Core media id: the row's photo in the picker.
	Photo string `json:"photo,omitempty"`
	// Tools are at most MaxPickTools of YOUR abilities (their names, without your plugin's prefix) that a pin of
	// this page brings the assistant before it searches. Core adds its own generic ones.
	Tools []string `json:"tools,omitempty"`
	// Actions are at most MaxPickActions chips offered after a pick.
	Actions []PickAction `json:"actions,omitempty"`
}

// PickGroupBy names ONE way to group: a column, or a taxonomy through the column holding each row's content id.
type PickGroupBy struct {
	Column        string `json:"column,omitempty"`
	Taxonomy      string `json:"taxonomy,omitempty"`
	ContentColumn string `json:"content_column,omitempty"`
}

// PickAction is one chip. It does at most one of: change Field, press RowAction, run Ability. With none of
// them it only writes its Sentence for the owner to finish.
type PickAction struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Field is a field of the page's form (with "edit": true) that Core changes across a pick.
	Field string `json:"field,omitempty"`
	// SetTo is the value the chip always sets ("Draft" for Hide); without it the owner types the value.
	SetTo any `json:"set_to,omitempty"`
	// Ops are how a typed value may change a money or number field: PickOpSet, PickOpAdd, PickOpPercent.
	// Default [PickOpSet].
	Ops []string `json:"ops,omitempty"`
	// CurrencyField is the form field holding a money field's ISO 4217 code, unless the field declares its currency.
	CurrencyField string `json:"currency_field,omitempty"`
	RowAction     string `json:"row_action,omitempty"`
	Ability       string `json:"ability,omitempty"`
	// Sentence is what the chip writes in the chat box: {title}, {now} (only inside one "( … )"), {blank}.
	Sentence string `json:"sentence"`
	// Tone PickToneWarning draws the chip in the warning colour and never first.
	Tone string `json:"tone,omitempty"`
	// Effect says what visitors will see change, on the confirm card ("Visitors no longer see it in the shop").
	Effect string `json:"effect,omitempty"`
}

const (
	PickOpSet       = "set"
	PickOpAdd       = "add"
	PickOpPercent   = "percent"
	PickToneWarning = "warning"
	MaxPickActions  = 4
	MaxPickTools    = 12
)

// HeadlineMetric points at one declared ReportMetric.
type HeadlineMetric struct {
	Page   string `json:"page"`   // a report page's key
	Metric string `json:"metric"` // one of that page's Metrics keys
}

// MobileIcons returns the icons the Nilda app ships for a plugin, sorted.
func MobileIcons() []string {
	out := []string{"bell", "box", "calendar", "card", "chart", "chat", "file", "form", "gift", "globe", "image",
		"lock", "mail", "music", "people", "puzzle", "shop", "star", "tag", "ticket", "truck", "video"}
	sort.Strings(out)
	return out
}

// AdminPageKind values.
const (
	// AdminPageSettings is a form: Core renders the fields, validates what is typed, stores it, and hands
	// the values to your plugin at Init.
	AdminPageSettings = "settings"
	// AdminPageList shows rows from one of your own declared tables.
	AdminPageList = "list"
	// AdminPageReport shows rows YOU compute and hand back fresh on every open — see AdminReportHook.
	AdminPageReport = "report"
	// AdminPageCalendar shows dated rows YOU compute (v0.33.0), drawn on a day/week/month grid; each event
	// opens a row of RowsPage, with that page's row actions — see AdminCalendarHook.
	AdminPageCalendar = "calendar"
)

// ListColumn is one column of a list page. Key names a column of the page's table; Type is how to RENDER it
// (one of the Column* constants; empty is text) and never changes what is fetched.
type ListColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"`
	// A ColumnMoney names its currency once (Currency: "EUR") or names the column holding each row's ISO 4217
	// code (CurrencyColumn: "currency"; on a list page, a column of the page's table). Exactly one.
	Currency       string `json:"currency,omitempty"`
	CurrencyColumn string `json:"currency_column,omitempty"`
	// Badges belong to a ColumnBadge: each value the column may hold → its tone (Badge* constants).
	Badges map[string]string `json:"badges,omitempty"`
	// Labels say what each value the column holds READS as (v0.32.0): a code you store ("fixed_amount", "shipment")
	// → the words the owner reads ("Fixed amount", "When it ships"), translated through your translations like any
	// label. A value with none is shown as stored. For a text or badge column.
	Labels map[string]string `json:"labels,omitempty"`
	// TypeBy and Types draw the column per ROW (v0.32.0): TypeBy names another column of the row, and Types maps
	// its value to the type this cell is drawn as — a discount's value is money when its kind is "fixed_amount" and
	// a percentage when it is "percentage". A value Types does not name draws as Type.
	TypeBy string            `json:"type_by,omitempty"`
	Types  map[string]string `json:"types,omitempty"`
	// Reference names another of your list pages whose row this column holds the id of (v0.32.0): a shipment's
	// order_id is drawn as that order's reference — the other page's first column — and opens it, never a bare id.
	Reference string `json:"reference,omitempty"`
}

// RowAction is one button on a row of a list page.
//
// Pressing it calls your `admin.action` hook with the row's primary key. Core never writes your table
// itself — see the note on AdminPage.Actions.
type RowAction struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Confirm is the question asked first. Set it for anything the owner cannot undo. At most 300 characters:
	// Core asks it in its own sheet, which names your plugin and quotes you.
	Confirm string `json:"confirm,omitempty"`
	// ShownWhen draws the button only on the rows that fit it (v0.38.0): "Approve" on a request, not on a
	// booking that is already confirmed. Without it the button shows on every row, and pressing it where it does
	// not fit is the owner's only way to find out. It is a convenience of the screen, not a lock: the hook still
	// checks the row, because a press can arrive from a row that changed a second ago, or from the assistant.
	ShownWhen *RowCondition `json:"shown_when,omitempty"`
	// AlwaysAsk makes the assistant ask the person before every press of this button, whatever a connector was
	// granted (v0.41.0) — for a button that moves money or lets data out, as the ability that does the same is
	// declared (Ability.AlwaysAsk). A person's own press in the panel is not asked twice: Confirm already asks them.
	AlwaysAsk bool `json:"always_ask,omitempty"`
}

// RowCondition is a test on one cell of a row. Column is a column of the page's table; a null cell reads as the
// empty string, so In: [""] means "nothing there" and NotIn: [""] means "something there". Exactly one of In and
// NotIn is set.
type RowCondition struct {
	Column string   `json:"column"`
	In     []string `json:"in,omitempty"`
	NotIn  []string `json:"not_in,omitempty"`
}

// Filter kinds a list page may declare.
const (
	// FilterChoice narrows the rows to one value of Column; the owner picks from Choices.
	FilterChoice = "choice"
	// FilterPeriod narrows the rows to those whose Column (a date or a timestamp) falls in a stretch of days the
	// owner picks from Periods, counted in the site's own time zone.
	FilterPeriod = "period"
)

// The stretches of days a FilterPeriod offers, in the site's time zone. A week runs Monday to Sunday.
const (
	PeriodToday     = "today"
	PeriodTomorrow  = "tomorrow"
	PeriodYesterday = "yesterday"
	PeriodThisWeek  = "this_week"
	PeriodNextWeek  = "next_week"
	PeriodLastWeek  = "last_week"
	PeriodThisMonth = "this_month"
	PeriodNextMonth = "next_month"
	PeriodLastMonth = "last_month"
	PeriodNext30    = "next_30_days"
	PeriodLast30    = "last_30_days"
)

// FilterPeriods returns every period a FilterPeriod may offer, in the order a screen lists them.
func FilterPeriods() []string {
	return []string{PeriodToday, PeriodTomorrow, PeriodYesterday, PeriodThisWeek, PeriodNextWeek, PeriodLastWeek,
		PeriodThisMonth, PeriodNextMonth, PeriodLastMonth, PeriodNext30, PeriodLast30}
}

// MaxListFilters is how many filters one list page may declare.
const MaxListFilters = 4

// ListFilter is one drop-down above a list page's rows (v0.38.0).
type ListFilter struct {
	// Key names the filter in the screen's address and in Core's request; lowercase, stable, unique on the page.
	Key string `json:"key"`
	// Label is what the drop-down is called ("Status", "Day"); translated through your translations.
	Label string `json:"label"`
	// Column is a column of the page's table: for FilterChoice the one whose value is matched, for FilterPeriod a
	// date or timestamp column.
	Column string `json:"column"`
	// Kind is FilterChoice (the default) or FilterPeriod.
	Kind string `json:"kind,omitempty"`
	// Choices are the values a FilterChoice offers, as the column stores them; the owner reads each as the
	// column's Labels say. At least one, at most 20.
	Choices []string `json:"choices,omitempty"`
	// AlsoColumn is a FilterPeriod's second date column: a row matches when EITHER falls in the period — a
	// booking's start time, or a stay's arrival day.
	AlsoColumn string `json:"also_column,omitempty"`
	// Periods are the periods a FilterPeriod offers, in this order; empty offers them all.
	Periods []string `json:"periods,omitempty"`
}

// AdminActionHook is the hook Core calls when somebody presses a row action.
//
// SUBSCRIBE IT. There is no interface to imply it — DispatchAdminAction takes a function, not a provider
// — so `admin.action` has to be in your InitResult.Hooks or Core answers "plugin is not subscribed to
// hook admin.action" and every button on your list does nothing:
//
//	return nilda.InitResult{Hooks: []string{nilda.AdminActionHook}}, nil
//
// The grant is `admin_page`, the same one that buys the page. It is not `hooks`.
const AdminActionHook = "admin.action"

// AdminAction is one press of a row action: which page, which button, and which row.
//
// The id is the row's PRIMARY KEY, as text, from your own table. Nilda read it out of the row it showed;
// it never invents one and never sends you a row you did not declare a page for.
//
// What you are NOT told is who pressed it: the press carries the page, the button and the row, and nothing
// about the person — so a refund's own record cannot say who refunded it (a limit Core records in its known
// issues, as it does for abilities). Who MAY press it is Core's to decide: a person holding `plugin.manage` or
// your plugin's own `plugin.<key>.configure`.
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
//
// The admin resolves Message through your manifest's `translations`, exactly as it does your page labels —
// so answer with a FIXED sentence ("Refunded") and translate it there, and the owner reads it in their own
// language. A sentence built per call ("Refunded order ord_42") matches no translation and is shown as you
// wrote it: keep the variable part out of it — the row the owner pressed the button on already says which.
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
//			// A fixed sentence, translated in plugin.json — see AdminActionResult.
//			return nilda.AdminActionResult{Message: "Refunded"}, p.refund(ctx, a.ID)
//		}
//		return nilda.AdminActionResult{}, errors.New("This action is not available")
//	}
//
// An error you return reaches the owner as the reason the action did not run — its first 300 characters,
// through plugin.json's translations like Message — so make it a fixed sentence they can act on. It is your
// answer, not a failure: refusing an action, however often, never counts toward switching the plugin off.
// Serve sends every error your handler returns to Core as that answer (gRPC Unknown), whatever it wraps — a
// Core call's error included. Running out of time, crashing, or panicking is not an answer, and does count.
// handled=false means the hook was not a row action, so a plugin with hooks of its own passes it on rather
// than failing it.
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

// AdminReportHook is the hook Core calls to fetch a "report" page's data, fresh, every time the page is
// opened or its filters change — the read equivalent of AdminActionHook.
//
// SUBSCRIBE IT, the same way and for the same reason:
//
//	return nilda.InitResult{Hooks: []string{nilda.AdminReportHook}}, nil
//
// The grant is `admin_page`, not `hooks`.
const AdminReportHook = "admin.report"

// AdminReportRequest is one open of a report page. Params is the CURRENT value of every field the page
// declared — its Fields work as filter inputs on a report page (see AdminPage.Fields), never stored
// settings, so there is nothing to read back from Init and everything arrives here instead. A field the
// owner left blank is simply absent from Params; default what an absent one means yourself, the same way
// you would default a missing argument to an ability.
type AdminReportRequest struct {
	Page   string            `json:"page"`
	Params map[string]string `json:"params,omitempty"`
}

// AdminReportResult is a report's answer: a table, in the SAME column vocabulary a "list" page's Columns
// already use (so Core draws both with the one component it already has), plus optional headline numbers
// shown above it — a total, a count, an average. Columns describes the shape; Rows is the data, one
// map per row keyed by each column's Key exactly the way a "list" page's own rows already are.
type AdminReportResult struct {
	Columns []ListColumn     `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Summary map[string]any   `json:"summary,omitempty"`
}

// DispatchAdminReport routes a report-page open to your handler, the same shape DispatchAdminAction uses:
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchAdminReport(ctx, hook, payload, p.onReport); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
//	func (p *Plugin) onReport(ctx context.Context, req nilda.AdminReportRequest) (nilda.AdminReportResult, error) {
//		from, to := req.Params["from"], req.Params["to"]
//		rows, total := p.salesBetween(ctx, from, to)
//		return nilda.AdminReportResult{
//			Columns: []nilda.ListColumn{{Key: "day", Label: "Day"}, {Key: "total", Label: "Revenue", Type: "number"}},
//			Rows:    rows,
//			Summary: map[string]any{"total_revenue": total},
//		}, nil
//	}
//
// handled=false means the hook was not a report open, so a plugin with hooks of its own passes it on.
func DispatchAdminReport(ctx context.Context, hook string, payload []byte, fn func(context.Context, AdminReportRequest) (AdminReportResult, error)) (out []byte, handled bool, err error) {
	if hook != AdminReportHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a report page was opened and this plugin registered no handler")
	}
	var req AdminReportRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: admin.report payload: %w", err)
	}
	res, err := fn(ctx, req)
	if err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}

// AdminCalendarHook is the hook Core calls to fetch a "calendar" page's events for the range it is
// drawing, fresh, every time the page is opened or its filters change — the dated counterpart of
// AdminReportHook.
//
// SUBSCRIBE IT, the same way and for the same reason:
//
//	return nilda.InitResult{Hooks: []string{nilda.AdminCalendarHook}}, nil
//
// The grant is `admin_page`, not `hooks`.
const AdminCalendarHook = "admin.calendar"

// AdminCalendarRequest is one open of a calendar page: the range Core is drawing and the CURRENT value of
// every field the page declared. From is inclusive and To exclusive, both RFC 3339 instants; Core asks at
// most 42 days at a time. Params works exactly as on a report page (see AdminPage.Fields): a field the
// owner left blank is simply absent from it, and default what an absent one means yourself.
type AdminCalendarRequest struct {
	Page   string            `json:"page"`
	From   string            `json:"from"`             // RFC 3339 instant, inclusive
	To     string            `json:"to"`               // RFC 3339 instant, exclusive; Core asks at most 42 calendar days
	Params map[string]string `json:"params,omitempty"` // the page's Fields as filters, as on a report page
}

// AdminCalendarResult is a calendar's answer: the events to draw in the range, optional shades behind
// them, and whether you had to leave events out. Core caps a reply at 2000 events, 500 shades and
// 512 KiB, and strings pass through PlainText like every other label you send.
type AdminCalendarResult struct {
	Events    []CalendarEvent `json:"events"`
	Shaded    []CalendarShade `json:"shaded,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
}

// CalendarEvent is one event on the grid — timed (Start and End) or a bar over whole days (StartDate and
// EndDate, exclusive); an event with neither is dropped. ID is a row id of the page's RowsPage: opening
// the event shows that row's sheet, with that page's row actions and its form when the page has one.
type CalendarEvent struct {
	ID        string `json:"id"` // a row id of RowsPage
	Title     string `json:"title"`
	Subtitle  string `json:"subtitle,omitempty"`
	Start     string `json:"start,omitempty"` // RFC 3339: a timed event …
	End       string `json:"end,omitempty"`
	StartDate string `json:"start_date,omitempty"` // … or "2006-01-02" days, EndDate exclusive: a bar over days
	EndDate   string `json:"end_date,omitempty"`
	Lane      string `json:"lane,omitempty"` // the day view's column: "Table 4"
	Tone      string `json:"tone,omitempty"` // a Badge* tone
}

// CalendarShade is a stretch shaded behind the events — the hours you take bookings, a closed day.
type CalendarShade struct {
	Start     string `json:"start,omitempty"`
	End       string `json:"end,omitempty"`
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	Label     string `json:"label,omitempty"`
	Lane      string `json:"lane,omitempty"`
}

// DispatchAdminCalendar routes a calendar-page open to your handler, the same shape DispatchAdminReport
// uses:
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchAdminCalendar(ctx, hook, payload, p.onCalendar); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
//	func (p *Plugin) onCalendar(ctx context.Context, req nilda.AdminCalendarRequest) (nilda.AdminCalendarResult, error) {
//		events, shades, more := p.between(ctx, req.From, req.To)
//		return nilda.AdminCalendarResult{Events: events, Shaded: shades, Truncated: more}, nil
//	}
//
// handled=false means the hook was not a calendar open, so a plugin with hooks of its own passes it on.
func DispatchAdminCalendar(ctx context.Context, hook string, payload []byte,
	fn func(context.Context, AdminCalendarRequest) (AdminCalendarResult, error)) (out []byte, handled bool, err error) {
	if hook != AdminCalendarHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a calendar page was opened and this plugin registered no handler")
	}
	var req AdminCalendarRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: admin.calendar payload: %w", err)
	}
	res, err := fn(ctx, req)
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
