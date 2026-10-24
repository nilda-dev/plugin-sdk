package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// EDIT FORMS: a list page that can also create and change its rows.
//
// A "list" page is read-only on purpose (see AdminPage.Actions): Core never writes your table, because a row
// written behind your back is your logic bypassed. A FORM keeps that rule and adds the half that was missing —
// the owner can open a row, change it and press Save — by sending what they typed to YOU. Core draws the form
// from what you declare, checks the plain things itself (a required field left empty, a number that is not one, a
// choice that is not offered), and calls your `admin.form.save` hook; you check the rest, save it your own way and
// answer with what you saved, or with what is wrong, field by field. Nothing is written until you say so.
//
// The same declaration is drawn in the admin panel, in the Nilda app on the owner's phone, and given to the site's
// assistant (Core's plugin_form and plugin_form_save tools, over MCP too): one form, three doors, and your one
// save hook behind all of them.
//
// A form hangs off a list page (AdminPage.Form) rather than being a page of its own: the list is where its rows
// are found, searched and opened, and its New button is where a new one starts.

// Form field types. A form takes every settings type (SettingFieldTypes), the date and media types of the widget
// vocabulary (one set of names to learn), and four of its own.
const (
	FormDate     = FieldDate     // "2026-10-24"
	FormDateTime = FieldDateTime // RFC 3339, in the site's time zone on screen
	FormImage    = FieldImage    // one Core media id
	FormGallery  = FieldGallery  // Core media ids, in order; the first is the main one
	// FormRepeater is a group of rows the owner adds, reorders and removes — the sizes of a product, the tiers of a
	// price. Its sub-fields are FormField.Fields, one level deep. The value is a list of objects.
	FormRepeater = FieldRepeater
	// FormMoney is an amount in the shop's currency, in MINOR units on the wire (cents): "20.00 €" arrives as 2000.
	// Set FormField.Currency to fix the currency; empty means the site's own.
	FormMoney = "money"
	// FormTags is several short texts — free words, or only Choices when there are any. The value is a list.
	FormTags = "tags"
	// FormReference picks rows of ANOTHER of your list pages, by id: a discount's products, a group's members. The
	// page is FormField.Reference; Multiple allows several. The value is an id, or a list of ids.
	FormReference = "reference"
	// FormStat is a figure you fill and nobody types — "sold in 30 days: 146". It is shown, never sent back.
	FormStat = "stat"
)

// FormFieldTypes returns every type a form field may declare, sorted. Core holds its own list equal to it.
func FormFieldTypes() []string {
	out := append(SettingFieldTypes(), FormDate, FormDateTime, FormImage, FormGallery, FormRepeater,
		FormMoney, FormTags, FormReference, FormStat)
	sort.Strings(out)
	return out
}

// PageForm is a list page's form: whether rows can be created and edited here, how the form is laid out, and its
// fields.
type PageForm struct {
	// Create adds a New button to the list; Edit makes each row open in the form. A form with neither is refused.
	Create bool `json:"create,omitempty"`
	Edit   bool `json:"edit,omitempty"`
	// CreateLabel is the New button's words — "New product". Empty reads "New".
	CreateLabel string `json:"create_label,omitempty"`
	// Sections group the fields, in order. A form with none is one section.
	Sections []FormSection `json:"sections,omitempty"`
	Fields   []FormField   `json:"fields"`
	// AlwaysAsk makes the assistant ask the person before every save of this form, whatever a connector was granted
	// (v0.41.0) — for a form that moves money, such as one that issues a gift card.
	AlwaysAsk bool `json:"always_ask,omitempty"`
}

// FormSection is one card of the form.
type FormSection struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Help  string `json:"help,omitempty"`
	// Side puts the card in the narrow column beside the form on a wide screen (status, categories, figures); on a
	// phone, and in the panel at a narrow width, the side cards come after the main ones, as Shopify stacks them.
	Side bool `json:"side,omitempty"`
	// Folded starts the card closed, showing its label and a one-line summary — for what is rarely changed.
	Folded bool `json:"folded,omitempty"`
}

// FormField is one control of a form.
type FormField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"` // one of FormFieldTypes
	// Section is the FormSection.Key the field sits in; empty is the first section.
	Section     string `json:"section,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Help        string `json:"help,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	// Default is what a NEW row starts with. Its type must match Type.
	Default any `json:"default,omitempty"`
	// Choices are the options of a select, radio or tags field.
	Choices []string `json:"choices,omitempty"`
	// Unit is shown after a number — "kg", "%".
	Unit string `json:"unit,omitempty"`
	// Min and Max bound a number or a money amount (minor units for money); MaxLength bounds text.
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
	// Currency fixes a money field's currency (ISO 4217). Empty is the form's own `currency` field — declare one,
	// read-only, and answer the row's currency in it (a shop whose currency the owner chooses) — else the amount is
	// shown with two decimals and no sign.
	Currency string `json:"currency,omitempty"`
	// Reference is the key of another of your list pages, for a reference field; Multiple allows several picks.
	Reference string `json:"reference,omitempty"`
	Multiple  bool   `json:"multiple,omitempty"`
	// Fields are a repeater's sub-fields, one level deep.
	Fields []FormField `json:"fields,omitempty"`
	// ShownWhen shows the field only while another field holds a value; RequiredWhen makes it required then.
	ShownWhen    *FieldCondition `json:"shown_when,omitempty"`
	RequiredWhen *FieldCondition `json:"required_when,omitempty"`
	// ReadOnly shows the value without letting it be changed here (an order number, a created date).
	ReadOnly bool `json:"read_only,omitempty"`
	// Secret is a text or long-text value that is written and never shown back — licence keys pasted in, a token.
	// Load it empty; the assistant's confirmation card says how many lines will be sent, never what.
	Secret bool `json:"secret,omitempty"`
}

// FieldCondition names another field of the same form (or of the same repeater row) and the value it must hold.
// Equals compares with the field's value; NotEmpty asks only that it has one.
type FieldCondition struct {
	Field    string `json:"field"`
	Equals   any    `json:"equals,omitempty"`
	NotEmpty bool   `json:"not_empty,omitempty"`
}

// AdminFormLoadHook is called to open a form: an existing row (its values to edit) or a new one (its starting
// values). AdminFormSaveHook is called when the owner — or the assistant, after the owner's yes — saves it.
//
// SUBSCRIBE BOTH, as the other admin hooks; the grant is `admin_page`:
//
//	return nilda.InitResult{Hooks: []string{nilda.AdminFormLoadHook, nilda.AdminFormSaveHook}}, nil
const (
	AdminFormLoadHook = "admin.form.load"
	AdminFormSaveHook = "admin.form.save"
)

// AdminFormRequest is one open or one save of a form. ID is the row's primary key as text, "" for a new row.
// On a save, Values are every writable field the form declares, as the owner left them (money in minor units, a
// gallery a list of media ids, a repeater a list of objects) — a field the assistant did not send arrives with the
// value your load answered — and Version is the one your load answered with, so a save started before somebody
// else's can be told apart from one started after.
type AdminFormRequest struct {
	Page    string         `json:"page"`
	ID      string         `json:"id,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
	Version string         `json:"version,omitempty"`
}

// AdminFormResult answers a load or a save.
//
// A load: ID, Title (the form's heading — the product's name), Values and Version. A save that worked: the same,
// as saved (ID is the new row's for a create). A save that did not: Errors, field by field, and nothing saved —
// Core shows them where the owner can fix them and the assistant reads them as the reason. Warnings are said and
// do not stop the save ("one photo has no description").
//
// A save whose Version is older than the row's is a CONFLICT: answer with Conflict — the row as it is now and who
// changed it — and save nothing, so the owner chooses instead of somebody's change being lost.
type AdminFormResult struct {
	ID       string         `json:"id,omitempty"`
	Title    string         `json:"title,omitempty"`
	Values   map[string]any `json:"values,omitempty"`
	Version  string         `json:"version,omitempty"`
	Errors   []FormError    `json:"errors,omitempty"`
	Warnings []FormError    `json:"warnings,omitempty"`
	Conflict *FormConflict  `json:"conflict,omitempty"`
}

// FormError is one thing wrong (or worth saying) about a field. Field is a path: "price", or "variants.1.price"
// for a repeater row's field; "" is the form as a whole. Message is a fixed sentence the owner can act on, resolved
// through your manifest's translations like an action's message.
type FormError struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

// FormConflict is the row as it is now, after a change made since the owner opened it: who made it ("the
// assistant", a person's name), when (RFC 3339), and the row's current values and version.
type FormConflict struct {
	By      string         `json:"by,omitempty"`
	At      string         `json:"at,omitempty"`
	Values  map[string]any `json:"values,omitempty"`
	Version string         `json:"version,omitempty"`
}

// DispatchAdminForm routes a form's load and save to your two handlers, as DispatchAdminAction does a row action:
//
//	if out, handled, err := nilda.DispatchAdminForm(ctx, hook, payload, p.loadForm, p.saveForm); handled {
//		return out, err
//	}
//
// An error you return is the reason the form could not be opened or saved, shown to the owner — for what is wrong
// with a field, answer Errors instead, so it is shown at the field. handled=false means the hook was not a form's.
func DispatchAdminForm(ctx context.Context, hook string, payload []byte,
	load, save func(context.Context, AdminFormRequest) (AdminFormResult, error)) (out []byte, handled bool, err error) {
	var fn func(context.Context, AdminFormRequest) (AdminFormResult, error)
	switch hook {
	case AdminFormLoadHook:
		fn = load
	case AdminFormSaveHook:
		fn = save
	default:
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a form was %s and this plugin registered no handler", map[string]string{
			AdminFormLoadHook: "opened", AdminFormSaveHook: "saved"}[hook])
	}
	var req AdminFormRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: %s payload: %w", hook, err)
	}
	res, err := fn(ctx, req)
	if err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}
