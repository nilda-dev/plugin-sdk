package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// A PLUGIN'S OWN FIELDS ON THE SHOP'S ORDERS, LINES, PRODUCTS AND CUSTOMERS (door D2, the shop's plan 2026-11-05 §1J;
// ATTRIBUTES.md). EXPERIMENTAL.
//
// A delivery-slot plugin asks the buyer for a date at checkout; a personalisation plugin asks for the engraving on the
// product page; an e-invoicing plugin keeps a buyer's routing id; an address plugin suggests addresses while the buyer
// types. Each is a FIELD the plugin declares and the shop draws, checks, keeps and copies into the order when it is
// placed — so the order still says what was asked after the plugin is gone. Two contracts, one per direction, as the
// shop's orders and deliverables are:
//
//	shop.attributes/v1        the field-owning plugin PROVIDES, the shop USES: describe its fields, validate them when
//	                          an order is placed, suggest and resolve addresses.
//	shop.attribute.values/v1  the shop PROVIDES, the field-owning plugin USES: read its values, set one (a write command,
//	                          docs/WRITE_COMMANDS.md: keyed, attributed, undoable, announced), take one back.
//
// A field is namespaced by the plugin that owns it: the shop keeps "<plugin key>.<field key>", and the plugin key is
// Core's word for the caller (CallOf(ctx).Caller) or the provider that answered describe — never a request's.

// The contracts' names.
const (
	// ContractShopAttributes is what a plugin that owns fields — or suggests addresses — provides to the shop.
	ContractShopAttributes = "shop.attributes/v1"
	// ContractShopAttributeValues is the shop's door for a plugin's own values.
	ContractShopAttributeValues = "shop.attribute.values/v1"
)

// The methods of shop.attributes/v1, answered by the field-owning plugin.
const (
	AttributesDescribe       = "describe"
	AttributesValidate       = "validate"
	AttributesAddressSuggest = "address.suggest"
	AttributesAddressResolve = "address.resolve"
)

// The methods of shop.attribute.values/v1, answered by the shop.
const (
	AttributeValuesGet = "values.get"
	AttributeValueSet  = "value.set"
	AttributeValueUndo = "value.undo"
)

// The write commands' record kind and the events the shop emits once a write is committed. The shop emits under its
// capability's namespace (ecommerce.*), the only one Core lets it use.
const (
	AttributeValueRecord = "attribute.value"
	EventAttributeSet    = "ecommerce.attribute_set"
	EventAttributeUndone = "ecommerce.attribute_undone"
)

// What a field is ON (AttributeField.Target, AttributeTarget.Kind).
const (
	AttributeOrder    = "order"    // one value per order
	AttributeLine     = "line"     // one value per order line (a basket line until the order is placed)
	AttributeProduct  = "product"  // one value per product, set by the owner; copied onto each line at placement
	AttributeCustomer = "customer" // one value per buyer account; copied into the order at placement
)

// Where a field is shown and collected (AttributeField.Shown).
const (
	// AttributeShownCheckout: the shop's checkout form asks for it — an order, line or customer field.
	AttributeShownCheckout = "checkout"
	// AttributeShownProductPage: the product page asks for it before the basket button — a line field.
	AttributeShownProductPage = "product_page"
	// AttributeShownProductForm: the owner fills it in the shop's product form — a product field.
	AttributeShownProductForm = "product_form"
	// AttributeShownOrder: the order shows it wherever the shop shows the order — its order screen, the buyer's account,
	// the order mails, the packing slip. A field shown only here is set by its plugin (value.set), never by the buyer.
	AttributeShownOrder = "order"
)

// What the shop does when a plugin that validates did not answer in time (AttributeDescription.WhenUnanswered) — Shopify's
// blockOnFailure, said by the plugin that knows whether an unchecked value is harmless.
const (
	AttributeBlock = "block" // the order is not placed; the buyer reads that it cannot be checked now
	AttributeAllow = "allow" // the order is placed, and marked unchecked for that plugin
)

// The field types: a subset of the form vocabulary every Nilda screen and the owner's app already draw (FormFieldTypes),
// and nothing the mobile app cannot draw. A value's JSON is the type's: a string, a number, true/false, a choice's value,
// a date "2026-11-05".
const (
	AttributeText     = FieldText
	AttributeTextarea = FieldTextarea
	AttributeEmail    = FieldEmail
	AttributePhone    = FieldPhone
	AttributeURL      = FieldURL
	AttributeNumber   = FieldNumber
	AttributeBoolean  = FieldBoolean
	AttributeSelect   = FieldSelect
	AttributeRadio    = FieldRadio
	AttributeDate     = FieldDate
)

// AttributeFieldTypes returns every type a field may declare, sorted.
func AttributeFieldTypes() []string {
	out := []string{AttributeText, AttributeTextarea, AttributeEmail, AttributePhone, AttributeURL, AttributeNumber,
		AttributeBoolean, AttributeSelect, AttributeRadio, AttributeDate}
	sort.Strings(out)
	return out
}

// The contract's bounds.
const (
	MaxAttributeFields    = 20  // fields one plugin declares
	MaxAttributeChoices   = 100 // choices of one select or radio field
	MaxAttributeLanguages = 20  // languages one label is written in
	MaxAttributeLabel     = 100 // characters of a label or a choice's label
	MaxAttributeHelp      = 300 // characters of a help text
	MaxAttributeMessage   = 300 // characters of a validation message
	MaxAttributeSKUs      = 500 // SKUs a line field is limited to
	MaxAttributeValues    = 500 // values one validate request or one values.get answer carries
	maxAttributeKey       = 40
	maxAttributeID        = 64

	// Text bounds per type, in characters: a declared max_length may lower them, never raise them.
	MaxAttributeText     = 255
	MaxAttributeTextarea = 2000
	maxAttributeEmail    = 254
	maxAttributeURL      = 2048
	maxAttributePhone    = 32
	maxAttributeDateDays = 3650

	// Address suggestions: what a visitor may ask and how often (AddressSuggestLimiter).
	MinAddressQuery         = 3 // characters typed before the shop asks
	MaxAddressQuery         = 200
	MaxAddressSuggestions   = 5  // shown at once (Shopify's address autocomplete shows the first five)
	AddressSuggestPerMinute = 30 // suggest and resolve calls one visitor may make in a minute
	maxAddressSession       = 64
)

// The codes a value is refused with (AttributeValueError.Code). The shop writes the sentence in the buyer's language.
const (
	AttributeRequired   = "required"
	AttributeWrongType  = "type"
	AttributeMalformed  = "malformed"
	AttributeTooShort   = "too_short"
	AttributeTooLong    = "too_long"
	AttributeTooSmall   = "too_small"
	AttributeTooLarge   = "too_large"
	AttributeNotAChoice = "not_a_choice"
	AttributeTooEarly   = "too_early"
	AttributeTooLate    = "too_late"
	// AttributeNotOffered: a value for a field the shop did not offer there — another plugin's, an unknown key, a line
	// field on a line of another SKU, a field the buyer is never asked for.
	AttributeNotOffered = "not_offered"
)

// The codes a write is refused with besides a value's (AttributeWriteAnswer.Refused).
const (
	AttributeNoRecord      = "no_record"      // the order, its line, the product or the customer is not there
	AttributeUnknownWrite  = "unknown_write"  // an undo of a write this plugin did not make
	AttributeUndoneAlready = "undone_already" // an undo of a write taken back already
	AttributeReplaced      = "replaced"       // an undo of a write a later write replaced: take that one back first
)

// ---- shop.attributes/v1: what the field-owning plugin answers ----------------------------------------------------------

// AttributeChoice is one option of a select or radio field: the value kept, and its label per language.
type AttributeChoice struct {
	Value string            `json:"value"`
	Label map[string]string `json:"label"`
}

// AttributeField is one field a plugin declares.
type AttributeField struct {
	// Key is the field's key within the plugin, lower-case ("delivery_date"); the shop keeps it as "<plugin>.<key>".
	Key string `json:"key"`
	// Target is what the field is on: AttributeOrder, AttributeLine, AttributeProduct or AttributeCustomer.
	Target string `json:"target"`
	// Type is one of AttributeFieldTypes.
	Type string `json:"type"`
	// Label is the field's words per language tag ("en", "de", "pt-BR"); Help an optional sentence under it, likewise.
	// The shop picks the buyer's language, then its base language, then "en", then the first tag in order (LabelIn).
	Label map[string]string `json:"label"`
	Help  map[string]string `json:"help,omitempty"`
	// Shown are the places the field is collected and shown (AttributeShown*), at least one.
	Shown []string `json:"shown"`
	// Required refuses an order (or a product form's save) without a value; a required boolean must be true — a tick.
	Required bool `json:"required,omitempty"`
	// Choices are a select's or a radio's options, and only theirs.
	Choices []AttributeChoice `json:"choices,omitempty"`
	// MinLength and MaxLength bound a text's characters; Min and Max a number; MinDate and MaxDate a date — each an
	// ISO date ("2026-12-24") or a number of days from the shop's today ("P1D", "P0D"), as WooCommerce's date field takes.
	MinLength int      `json:"min_length,omitempty"`
	MaxLength int      `json:"max_length,omitempty"`
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MinDate   string   `json:"min_date,omitempty"`
	MaxDate   string   `json:"max_date,omitempty"`
	// SKUs limits a line field to the lines of these SKUs (a product's or a variant's); empty is every line.
	SKUs []string `json:"skus,omitempty"`
}

// AttributeDescription is a plugin's answer to describe: its fields, whether it validates them when an order is placed
// and what the shop does when it does not answer, and whether it suggests addresses.
type AttributeDescription struct {
	Fields []AttributeField `json:"fields"`
	// Validates asks the shop to call validate before it places an order holding any of these fields; WhenUnanswered
	// (AttributeBlock or AttributeAllow) is then required.
	Validates      bool   `json:"validates,omitempty"`
	WhenUnanswered string `json:"when_unanswered,omitempty"`
	// AddressSuggestions says the plugin answers address.suggest and address.resolve.
	AddressSuggestions bool `json:"address_suggestions,omitempty"`
}

// AttributeValue is one value of a field.
type AttributeValue struct {
	// Plugin is whose field it is — set by the shop (Core's caller, or the provider that described it), never trusted
	// from a writer.
	Plugin string `json:"plugin,omitempty"`
	Field  string `json:"field"`
	// Kind is the field's target; on an order it says where a copied value came from (customer, product).
	Kind string `json:"kind,omitempty"`
	// Line is the order or basket line a line or product value belongs to.
	Line  string          `json:"line,omitempty"`
	Value json.RawMessage `json:"value"`
}

// AttributeBasketLine is one line of the basket an order is placed from.
type AttributeBasketLine struct {
	ID       string `json:"id"`
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
}

// AttributeValidateRequest asks the plugin whether its values let this order be placed. Values are the plugin's own
// fields only, each already checked against its declaration (AttributeField.CheckValue).
type AttributeValidateRequest struct {
	Currency string                `json:"currency"`
	Country  string                `json:"country,omitempty"` // where it goes; the billing country when nothing ships
	Customer string                `json:"customer,omitempty"`
	Locale   string                `json:"locale,omitempty"` // the buyer's language, for the messages
	Lines    []AttributeBasketLine `json:"lines"`
	Values   []AttributeValue      `json:"values"`
}

// AttributeError is one reason the order may not be placed: the field (and line) it is about, and the sentence the
// buyer reads beside it, in the request's language.
type AttributeError struct {
	Field   string `json:"field"`
	Line    string `json:"line,omitempty"`
	Message string `json:"message"`
}

// AttributeValidateAnswer is the plugin's verdict: no errors lets the order be placed.
type AttributeValidateAnswer struct {
	Errors []AttributeError `json:"errors"`
}

// AddressSuggestRequest is what a visitor typed, proxied by the shop: the provider's key never reaches the browser.
// Session is the shop's opaque token for one address entry (several keystrokes, one choice), which providers that bill
// per session (Google Places' session token) group by.
type AddressSuggestRequest struct {
	Query   string `json:"query"`
	Country string `json:"country,omitempty"`
	Locale  string `json:"locale,omitempty"`
	Session string `json:"session,omitempty"`
	Limit   int    `json:"limit,omitempty"` // 1 to MaxAddressSuggestions; 0 is MaxAddressSuggestions
}

// AddressSuggestion is one suggestion: the line the visitor reads and, when the provider has it already, the address.
// Without Address the shop asks address.resolve with ID when the visitor picks it.
type AddressSuggestion struct {
	ID      string       `json:"id"`
	Label   string       `json:"label"`
	Address *ShipAddress `json:"address,omitempty"`
}

// AddressSuggestAnswer lists the suggestions, best first.
type AddressSuggestAnswer struct {
	Suggestions []AddressSuggestion `json:"suggestions"`
}

// AddressResolveRequest asks for the whole address of a picked suggestion.
type AddressResolveRequest struct {
	ID      string `json:"id"`
	Session string `json:"session,omitempty"`
	Locale  string `json:"locale,omitempty"`
}

// AddressResolveAnswer is the address, or Found false. The shop fills its own fields with it and checks them by its own
// rules (its countries, its postcode rule) — a suggestion is never trusted past them.
type AddressResolveAnswer struct {
	Found   bool         `json:"found"`
	Address *ShipAddress `json:"address,omitempty"`
}

// ---- shop.attribute.values/v1: what the shop answers ------------------------------------------------------------------

// AttributeTarget names the record a value is on: an order, one line of it (Line), a product or a customer.
type AttributeTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Line string `json:"line,omitempty"`
}

// AttributeValuesRequest asks for the caller's own values on one record.
type AttributeValuesRequest struct {
	Target AttributeTarget `json:"target"`
}

// AttributeValuesAnswer is the caller's values on the record — on an order, its order and line values and the copies
// of its customer and product values taken when the order was placed (Kind says which).
type AttributeValuesAnswer struct {
	Values []AttributeValue `json:"values"`
}

// AttributeSetRequest sets one of the CALLER's fields on one record. A null value clears it (refused for a required
// field). The idempotency key is the call's (ServiceCallOptions.IdempotencyKey): derive it from what the value is for.
type AttributeSetRequest struct {
	Target AttributeTarget `json:"target"`
	Field  string          `json:"field"`
	Value  json.RawMessage `json:"value"`
}

// AttributeWriteAnswer is the answer to value.set and value.undo: the record (WriteAnswer), or Refused — a code saying why
// nothing was written (a value's code, AttributeNotOffered for a field that is not the caller's or not on that kind of
// record, AttributeNoRecord, AttributeUnknownWrite, AttributeUndoneAlready, AttributeReplaced). A refusal is a RESULT,
// never an error: Core's desk relays a provider's error as the provider failing, which blocks the call and is retried,
// so a plugin told "no" by an error would wait out the retries and read "unavailable". A refusal is not kept under its
// key: the same key may succeed once the record is there.
type AttributeWriteAnswer struct {
	WriteAnswer
	Refused string `json:"refused,omitempty"`
}

// Validate refuses an answer that is neither a record nor a known refusal, or both.
func (a AttributeWriteAnswer) Validate() error {
	switch {
	case a.Refused == "" && a.RecordID == "":
		return errors.New("nilda: a write's answer names its record, or refused says why nothing was written")
	case a.Refused != "" && (a.RecordID != "" || a.Already):
		return errors.New("nilda: a refused write names no record")
	case a.Refused != "" && !slices.Contains(attributeRefusals, a.Refused):
		return fmt.Errorf("nilda: refused %q is not a code the contract knows (%s)", a.Refused, strings.Join(attributeRefusals, ", "))
	}
	return attributeText("record_id", a.RecordID, maxAttributeID, false)
}

// attributeRefusals is every code a write may be refused with.
var attributeRefusals = []string{AttributeRequired, AttributeWrongType, AttributeMalformed, AttributeTooShort,
	AttributeTooLong, AttributeTooSmall, AttributeTooLarge, AttributeNotAChoice, AttributeTooEarly, AttributeTooLate,
	AttributeNotOffered, AttributeNoRecord, AttributeUnknownWrite, AttributeUndoneAlready, AttributeReplaced}

// ---- the rules ------------------------------------------------------------------------------------------------------

var (
	attributeKeyRe  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	attributeDaysRe = regexp.MustCompile(`^P([0-9]{1,4})D$`)
	attributePhone  = regexp.MustCompile(`^\+?[0-9 ()./-]+$`)
)

// AttributeValueError is a value its declaration refuses: which field, and a code (AttributeRequired …) the shop says in
// the buyer's language.
type AttributeValueError struct {
	Field string
	Line  string
	Code  string
}

func (e *AttributeValueError) Error() string {
	if e.Line != "" {
		return fmt.Sprintf("nilda: field %s on line %s: %s", e.Field, e.Line, e.Code)
	}
	return fmt.Sprintf("nilda: field %s: %s", e.Field, e.Code)
}

// The places each target may be shown: a buyer fills an order, line or customer field at checkout, a line field on the
// product page, the owner a product field in the product form; every field may be shown on the order.
var attributePlaces = map[string]map[string]bool{
	AttributeOrder:    {AttributeShownCheckout: true, AttributeShownOrder: true},
	AttributeLine:     {AttributeShownCheckout: true, AttributeShownProductPage: true, AttributeShownOrder: true},
	AttributeProduct:  {AttributeShownProductForm: true, AttributeShownOrder: true},
	AttributeCustomer: {AttributeShownCheckout: true, AttributeShownOrder: true},
}

// attributeTextMax is the most characters a value of each text type holds.
var attributeTextMax = map[string]int{AttributeText: MaxAttributeText, AttributeTextarea: MaxAttributeTextarea,
	AttributeEmail: maxAttributeEmail, AttributeURL: maxAttributeURL, AttributePhone: maxAttributePhone}

// Validate refuses a description the shop could not draw or keep.
func (d AttributeDescription) Validate() error {
	if len(d.Fields) == 0 && !d.AddressSuggestions {
		return errors.New("nilda: a description offers nothing — no field and no address suggestions")
	}
	if len(d.Fields) > MaxAttributeFields {
		return fmt.Errorf("nilda: a plugin declares at most %d fields, got %d", MaxAttributeFields, len(d.Fields))
	}
	switch {
	case d.Validates && d.WhenUnanswered != AttributeBlock && d.WhenUnanswered != AttributeAllow:
		return fmt.Errorf("nilda: a plugin that validates says when_unanswered — %q or %q — and says %q",
			AttributeBlock, AttributeAllow, d.WhenUnanswered)
	case !d.Validates && d.WhenUnanswered != "":
		return errors.New("nilda: when_unanswered is for a plugin that validates")
	}
	seen := map[string]bool{}
	for _, f := range d.Fields {
		if err := f.Validate(); err != nil {
			return err
		}
		if seen[f.Key] {
			return fmt.Errorf("nilda: the field %q is declared twice", f.Key)
		}
		seen[f.Key] = true
	}
	return nil
}

// Validate refuses a field the shop could not draw or keep.
func (f AttributeField) Validate() error {
	if !attributeKeyRe.MatchString(f.Key) {
		return fmt.Errorf("nilda: a field's key is lower-case letters, digits and _, at most %d, starting with a letter; got %q",
			maxAttributeKey, f.Key)
	}
	wrap := func(err error) error { return fmt.Errorf("nilda: field %s: %w", f.Key, err) }
	places, ok := attributePlaces[f.Target]
	if !ok {
		return wrap(fmt.Errorf("target %q is not order, line, product or customer", f.Target))
	}
	if !slices.Contains(AttributeFieldTypes(), f.Type) {
		return wrap(fmt.Errorf("type %q is not one of %s", f.Type, strings.Join(AttributeFieldTypes(), ", ")))
	}
	if len(f.Shown) == 0 {
		return wrap(errors.New("it is shown nowhere; shown names at least one place"))
	}
	for _, s := range f.Shown {
		if !places[s] {
			return wrap(fmt.Errorf("a %s field is not shown at %q", f.Target, s))
		}
	}
	if f.Required && !f.Collected() {
		return wrap(errors.New("it is required and nobody is asked for it — a required field is collected somewhere"))
	}
	if err := attributeWords("label", f.Label, MaxAttributeLabel, true); err != nil {
		return wrap(err)
	}
	if err := attributeWords("help", f.Help, MaxAttributeHelp, false); err != nil {
		return wrap(err)
	}
	choosing := f.Type == AttributeSelect || f.Type == AttributeRadio
	switch {
	case choosing && len(f.Choices) == 0:
		return wrap(errors.New("a select or radio field lists its choices"))
	case !choosing && len(f.Choices) > 0:
		return wrap(errors.New("choices are a select's or a radio's"))
	case len(f.Choices) > MaxAttributeChoices:
		return wrap(fmt.Errorf("at most %d choices", MaxAttributeChoices))
	}
	values := map[string]bool{}
	for _, c := range f.Choices {
		if strings.TrimSpace(c.Value) == "" || utf8.RuneCountInString(c.Value) > MaxAttributeLabel || !plainLine(c.Value) {
			return wrap(fmt.Errorf("a choice's value is one plain line of 1 to %d characters", MaxAttributeLabel))
		}
		if values[c.Value] {
			return wrap(fmt.Errorf("the choice %q is listed twice", c.Value))
		}
		values[c.Value] = true
		if err := attributeWords("choice label", c.Label, MaxAttributeLabel, true); err != nil {
			return wrap(err)
		}
	}
	if err := f.validBounds(); err != nil {
		return wrap(err)
	}
	if len(f.SKUs) > 0 && f.Target != AttributeLine {
		return wrap(errors.New("skus limit a line field to some products; this field is not on a line"))
	}
	if len(f.SKUs) > MaxAttributeSKUs {
		return wrap(fmt.Errorf("skus lists at most %d", MaxAttributeSKUs))
	}
	for _, s := range f.SKUs {
		if strings.TrimSpace(s) == "" || len(s) > maxAttributeID || !plainLine(s) {
			return wrap(fmt.Errorf("skus holds an SKU that is empty or past %d bytes", maxAttributeID))
		}
	}
	return nil
}

// validBounds holds each bound to the type it bounds.
func (f AttributeField) validBounds() error {
	textMax, isText := attributeTextMax[f.Type]
	switch {
	case !isText && (f.MinLength != 0 || f.MaxLength != 0):
		return errors.New("min_length and max_length bound a text")
	case f.MinLength < 0 || f.MaxLength < 0 || f.MaxLength > textMax:
		return fmt.Errorf("max_length is at most %d for a %s", textMax, f.Type)
	case f.MaxLength != 0 && f.MinLength > f.MaxLength:
		return errors.New("min_length is past max_length")
	case f.Type != AttributeNumber && (f.Min != nil || f.Max != nil):
		return errors.New("min and max bound a number")
	case f.Min != nil && (math.IsNaN(*f.Min) || math.IsInf(*f.Min, 0)), f.Max != nil && (math.IsNaN(*f.Max) || math.IsInf(*f.Max, 0)):
		return errors.New("min and max are finite numbers")
	case f.Min != nil && f.Max != nil && *f.Min > *f.Max:
		return errors.New("min is past max")
	case f.Type != AttributeDate && (f.MinDate != "" || f.MaxDate != ""):
		return errors.New("min_date and max_date bound a date")
	}
	// Both bounds read against one day: a relative and a fixed bound compare only on a given day, so the order is
	// checked where both are of one kind.
	day := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	lo, loOK, err := dateBound(f.MinDate, day)
	if err != nil {
		return fmt.Errorf("min_date: %w", err)
	}
	hi, hiOK, err := dateBound(f.MaxDate, day)
	if err != nil {
		return fmt.Errorf("max_date: %w", err)
	}
	sameKind := attributeDaysRe.MatchString(f.MinDate) == attributeDaysRe.MatchString(f.MaxDate)
	if loOK && hiOK && sameKind && lo.After(hi) {
		return errors.New("min_date is past max_date")
	}
	return nil
}

// dateBound reads a date bound against today: "" is none, "P<n>D" today plus n days, else an ISO date.
func dateBound(b string, today time.Time) (time.Time, bool, error) {
	if b == "" {
		return time.Time{}, false, nil
	}
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if m := attributeDaysRe.FindStringSubmatch(b); m != nil {
		n, _ := strconv.Atoi(m[1])
		if n > maxAttributeDateDays {
			return time.Time{}, false, fmt.Errorf("%q is past %d days", b, maxAttributeDateDays)
		}
		return day.AddDate(0, 0, n), true, nil
	}
	t, err := time.Parse(time.DateOnly, b)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%q is neither a date (2026-12-24) nor days from today (P1D)", b)
	}
	return t, true, nil
}

// attributeWords holds a per-language text: language tags, one plain line each, within max characters.
func attributeWords(what string, words map[string]string, max int, required bool) error {
	if len(words) == 0 {
		if required {
			return fmt.Errorf("its %s is written in at least one language", what)
		}
		return nil
	}
	if len(words) > MaxAttributeLanguages {
		return fmt.Errorf("its %s is written in at most %d languages", what, MaxAttributeLanguages)
	}
	for tag, w := range words {
		if tag == "" || !validLocale(tag) {
			return fmt.Errorf("its %s names %q, which is not a language tag (\"de\", \"pt-BR\")", what, tag)
		}
		if strings.TrimSpace(w) == "" || utf8.RuneCountInString(w) > max || !plainLine(w) {
			return fmt.Errorf("its %s in %s is one plain line of 1 to %d characters", what, tag, max)
		}
	}
	return nil
}

// CheckValue is the ONE rule for a value of f — the shop runs it at checkout, on the product page, in the product form
// and on every value.set, and the kit runs the same. today is the shop's date (its time zone), for a date's bounds.
// A missing value (absent, null, "") is refused only for a required field.
func (f AttributeField) CheckValue(raw json.RawMessage, today time.Time) error {
	refuse := func(code string) error { return &AttributeValueError{Field: f.Key, Code: code} }
	var v any
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &v); err != nil {
			return refuse(AttributeWrongType)
		}
	}
	if v == nil || v == "" || (f.Type == AttributeBoolean && v == false && f.Required) {
		if f.Required {
			return refuse(AttributeRequired)
		}
		return nil
	}
	switch f.Type {
	case AttributeNumber:
		n, ok := v.(float64)
		switch {
		case !ok:
			return refuse(AttributeWrongType)
		case f.Min != nil && n < *f.Min:
			return refuse(AttributeTooSmall)
		case f.Max != nil && n > *f.Max:
			return refuse(AttributeTooLarge)
		}
		return nil
	case AttributeBoolean:
		if _, ok := v.(bool); !ok {
			return refuse(AttributeWrongType)
		}
		return nil
	}
	s, ok := v.(string)
	if !ok {
		return refuse(AttributeWrongType)
	}
	switch f.Type {
	case AttributeSelect, AttributeRadio:
		for _, c := range f.Choices {
			if c.Value == s {
				return nil
			}
		}
		return refuse(AttributeNotAChoice)
	case AttributeDate:
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			return refuse(AttributeMalformed)
		}
		if lo, ok, _ := dateBound(f.MinDate, today); ok && d.Before(lo) {
			return refuse(AttributeTooEarly)
		}
		if hi, ok, _ := dateBound(f.MaxDate, today); ok && d.After(hi) {
			return refuse(AttributeTooLate)
		}
		return nil
	}
	// The text types.
	text := s
	if f.Type == AttributeTextarea {
		text = strings.NewReplacer("\r\n", "", "\n", "").Replace(s)
	}
	if !plainLine(text) {
		return refuse(AttributeMalformed)
	}
	n := utf8.RuneCountInString(s)
	max := attributeTextMax[f.Type]
	if f.MaxLength > 0 {
		max = f.MaxLength
	}
	switch {
	case n > max:
		return refuse(AttributeTooLong)
	case n < f.MinLength:
		return refuse(AttributeTooShort)
	}
	switch f.Type {
	case AttributeEmail:
		if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
			return refuse(AttributeMalformed)
		}
	case AttributeURL:
		if u, err := url.Parse(s); err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return refuse(AttributeMalformed)
		}
	case AttributePhone:
		digits := strings.Count(s, "0") + strings.Count(s, "1") + strings.Count(s, "2") + strings.Count(s, "3") +
			strings.Count(s, "4") + strings.Count(s, "5") + strings.Count(s, "6") + strings.Count(s, "7") +
			strings.Count(s, "8") + strings.Count(s, "9")
		if !attributePhone.MatchString(s) || digits < 3 {
			return refuse(AttributeMalformed)
		}
	}
	return nil
}

// Collected reports whether a buyer or the owner fills the field somewhere (checkout, product page, product form), as
// against a field only its plugin sets.
func (f AttributeField) Collected() bool {
	for _, s := range f.Shown {
		if s == AttributeShownCheckout || s == AttributeShownProductPage || s == AttributeShownProductForm {
			return true
		}
	}
	return false
}

// Shows reports whether the field is shown at place.
func (f AttributeField) Shows(place string) bool { return slices.Contains(f.Shown, place) }

// ForSKU reports whether a line field applies to a line of sku.
func (f AttributeField) ForSKU(sku string) bool {
	return len(f.SKUs) == 0 || slices.Contains(f.SKUs, sku)
}

// LabelIn picks a label for locale: the tag itself, its base language, "en", then the first tag in order.
func LabelIn(labels map[string]string, locale string) string {
	if len(labels) == 0 {
		return ""
	}
	byLower := map[string]string{}
	tags := make([]string, 0, len(labels))
	for tag, w := range labels {
		byLower[strings.ToLower(tag)] = w
		tags = append(tags, tag)
	}
	l := strings.ToLower(locale)
	base, _, _ := strings.Cut(l, "-")
	for _, try := range []string{l, base, "en"} {
		if w, ok := byLower[try]; ok && try != "" {
			return w
		}
	}
	sort.Strings(tags)
	return labels[tags[0]]
}

// FormField is f in the admin form vocabulary (PageForm): how the shop's product form and the owner's app draw it.
func (f AttributeField) FormField(locale string) FormField {
	ff := FormField{Key: f.Key, Label: LabelIn(f.Label, locale), Type: f.Type, Required: f.Required,
		Help: LabelIn(f.Help, locale), Min: f.Min, Max: f.Max, MaxLength: f.MaxLength}
	for _, c := range f.Choices {
		ff.Choices = append(ff.Choices, c.Value)
	}
	return ff
}

// AttributeInputName is the name of the input a field is collected under on the shop's pages: attr[<plugin>.<key>],
// and attr[<plugin>.<key>][<line>] for a line's.
func AttributeInputName(plugin, key, line string) string {
	name := "attr[" + plugin + "." + key + "]"
	if line != "" {
		name += "[" + line + "]"
	}
	return name
}

// attributeText holds one short identifier: present, within max bytes, one plain line.
func attributeText(what, v string, max int, required bool) error {
	switch {
	case v == "" && !required:
		return nil
	case strings.TrimSpace(v) == "" || len(v) > max || !plainLine(v):
		return fmt.Errorf("nilda: %s is one plain line of 1 to %d bytes", what, max)
	}
	return nil
}

// Validate refuses a validate request the plugin could not read.
func (r AttributeValidateRequest) Validate() error {
	if err := validCurrency("the order's currency", r.Currency); err != nil {
		return err
	}
	if r.Country != "" {
		if err := validCountry("the order's country", r.Country); err != nil {
			return err
		}
	}
	if r.Locale != "" && !validLocale(r.Locale) {
		return fmt.Errorf("nilda: %q is not a language tag", r.Locale)
	}
	if err := attributeText("the customer", r.Customer, maxAttributeID, false); err != nil {
		return err
	}
	if len(r.Values) > MaxAttributeValues || len(r.Lines) > MaxAttributeValues {
		return fmt.Errorf("nilda: a validate request carries at most %d lines and %d values", MaxAttributeValues, MaxAttributeValues)
	}
	for _, l := range r.Lines {
		if err := attributeText("a line's id", l.ID, maxAttributeID, true); err != nil {
			return err
		}
		if err := attributeText("a line's SKU", l.SKU, maxAttributeID, true); err != nil {
			return err
		}
		if l.Quantity <= 0 {
			return fmt.Errorf("nilda: line %s has quantity %d", l.ID, l.Quantity)
		}
	}
	return validValues(r.Values)
}

func validValues(vs []AttributeValue) error {
	for _, v := range vs {
		if !attributeKeyRe.MatchString(v.Field) {
			return fmt.Errorf("nilda: a value names the field %q, which is no field's key", v.Field)
		}
		if err := attributeText("a value's line", v.Line, maxAttributeID, false); err != nil {
			return err
		}
		if v.Kind != "" && attributePlaces[v.Kind] == nil {
			return fmt.Errorf("nilda: a value's kind %q is not order, line, product or customer", v.Kind)
		}
	}
	return nil
}

// ValidateFor refuses an answer naming a field the plugin did not declare, or a message past its bound.
func (a AttributeValidateAnswer) ValidateFor(d AttributeDescription) error {
	declared := map[string]bool{}
	for _, f := range d.Fields {
		declared[f.Key] = true
	}
	for _, e := range a.Errors {
		if !declared[e.Field] {
			return fmt.Errorf("nilda: a verdict about %q, which this plugin does not declare", e.Field)
		}
		if strings.TrimSpace(e.Message) == "" || utf8.RuneCountInString(e.Message) > MaxAttributeMessage || !plainLine(e.Message) {
			return fmt.Errorf("nilda: the message about %s is one plain line of 1 to %d characters", e.Field, MaxAttributeMessage)
		}
		if err := attributeText("a verdict's line", e.Line, maxAttributeID, false); err != nil {
			return err
		}
	}
	return nil
}

// limit is the most suggestions r asks for.
func (r AddressSuggestRequest) limit() int {
	if r.Limit == 0 {
		return MaxAddressSuggestions
	}
	return r.Limit
}

// Validate refuses a query too short or too long, or a limit past MaxAddressSuggestions.
func (r AddressSuggestRequest) Validate() error {
	n := utf8.RuneCountInString(strings.TrimSpace(r.Query))
	switch {
	case n < MinAddressQuery || utf8.RuneCountInString(r.Query) > MaxAddressQuery || !plainLine(r.Query):
		return fmt.Errorf("nilda: an address query is one plain line of %d to %d characters", MinAddressQuery, MaxAddressQuery)
	case r.Limit < 0 || r.Limit > MaxAddressSuggestions:
		return fmt.Errorf("nilda: a limit is 1 to %d, or 0 for %d", MaxAddressSuggestions, MaxAddressSuggestions)
	case r.Locale != "" && !validLocale(r.Locale):
		return fmt.Errorf("nilda: %q is not a language tag", r.Locale)
	}
	if r.Country != "" {
		if err := validCountry("an address query's country", r.Country); err != nil {
			return err
		}
	}
	return attributeText("a suggestion session", r.Session, maxAddressSession, false)
}

// ValidateFor refuses more suggestions than asked, or one without an id or a label.
func (a AddressSuggestAnswer) ValidateFor(r AddressSuggestRequest) error {
	if len(a.Suggestions) > r.limit() {
		return fmt.Errorf("nilda: %d suggestions for a limit of %d", len(a.Suggestions), r.limit())
	}
	for _, s := range a.Suggestions {
		if err := attributeText("a suggestion's id", s.ID, maxAddressLine, true); err != nil {
			return err
		}
		if strings.TrimSpace(s.Label) == "" || utf8.RuneCountInString(s.Label) > maxAddressLine || !plainLine(s.Label) {
			return fmt.Errorf("nilda: a suggestion's label is one plain line of 1 to %d characters", maxAddressLine)
		}
		if s.Address != nil {
			if err := s.Address.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate refuses a resolve with no id.
func (r AddressResolveRequest) Validate() error {
	if err := attributeText("a suggestion's id", r.ID, maxAddressLine, true); err != nil {
		return err
	}
	if r.Locale != "" && !validLocale(r.Locale) {
		return fmt.Errorf("nilda: %q is not a language tag", r.Locale)
	}
	return attributeText("a suggestion session", r.Session, maxAddressSession, false)
}

// Validate refuses a found answer without an address, or an address past its bounds.
func (a AddressResolveAnswer) Validate() error {
	if a.Found != (a.Address != nil) {
		return errors.New("nilda: a found address carries the address, and only then")
	}
	if a.Address != nil {
		return a.Address.Validate()
	}
	return nil
}

// Validate refuses a target that is not one record.
func (t AttributeTarget) Validate() error {
	if attributePlaces[t.Kind] == nil {
		return fmt.Errorf("nilda: a target's kind %q is not order, line, product or customer", t.Kind)
	}
	if err := attributeText("a target's id", t.ID, maxAttributeID, true); err != nil {
		return err
	}
	if (t.Kind == AttributeLine) != (t.Line != "") {
		return errors.New("nilda: a line target names the order (id) and its line (line); no other target names a line")
	}
	return attributeText("a target's line", t.Line, maxAttributeID, false)
}

// Validate refuses a set without a target or a field.
func (r AttributeSetRequest) Validate() error {
	if err := r.Target.Validate(); err != nil {
		return err
	}
	if !attributeKeyRe.MatchString(r.Field) {
		return fmt.Errorf("nilda: %q is no field's key", r.Field)
	}
	return nil
}

// ---- the providers -------------------------------------------------------------------------------------------------

// AttributeProvider is a plugin that owns fields or suggests addresses. Serve finds it by itself; declare "provides":
// ["shop.attributes/v1"] with service_provider.
type AttributeProvider interface {
	DescribeAttributes(ctx context.Context) (AttributeDescription, error)
}

// AttributeValidator is an AttributeProvider whose description says Validates.
type AttributeValidator interface {
	ValidateAttributes(ctx context.Context, req AttributeValidateRequest) (AttributeValidateAnswer, error)
}

// AddressSuggester is an AttributeProvider whose description says AddressSuggestions.
type AddressSuggester interface {
	SuggestAddresses(ctx context.Context, req AddressSuggestRequest) (AddressSuggestAnswer, error)
	ResolveAddress(ctx context.Context, req AddressResolveRequest) (AddressResolveAnswer, error)
}

// AttributeMethods are a field-owning plugin's methods, each checking the request before the plugin sees it and the
// answer before it leaves: a description the shop cannot keep, or one promising a validator or suggestions the plugin
// does not implement, fails the plugin's own test rather than every checkout.
func AttributeMethods(p AttributeProvider) []ServiceMethod {
	describe := func(ctx context.Context) (AttributeDescription, error) {
		d, err := p.DescribeAttributes(ctx)
		if err != nil {
			return d, err
		}
		if d.Fields == nil {
			d.Fields = []AttributeField{} // none is an empty list on the wire, never null
		}
		if err := d.Validate(); err != nil {
			return AttributeDescription{}, err
		}
		if _, ok := p.(AttributeValidator); d.Validates && !ok {
			return AttributeDescription{}, errors.New("nilda: the description says validates and the plugin implements no AttributeValidator")
		}
		if _, ok := p.(AddressSuggester); d.AddressSuggestions && !ok {
			return AttributeDescription{}, errors.New("nilda: the description offers address suggestions and the plugin implements no AddressSuggester")
		}
		return d, nil
	}
	methods := []ServiceMethod{
		Serves(ContractShopAttributes, AttributesDescribe, func(ctx context.Context, _ struct{}) (AttributeDescription, error) {
			return describe(ctx)
		}),
	}
	if v, ok := p.(AttributeValidator); ok {
		methods = append(methods, Serves(ContractShopAttributes, AttributesValidate,
			func(ctx context.Context, req AttributeValidateRequest) (AttributeValidateAnswer, error) {
				if err := req.Validate(); err != nil {
					return AttributeValidateAnswer{}, err
				}
				d, err := describe(ctx)
				if err != nil {
					return AttributeValidateAnswer{}, err
				}
				a, err := v.ValidateAttributes(ctx, req)
				if err != nil {
					return a, err
				}
				if a.Errors == nil {
					a.Errors = []AttributeError{}
				}
				return a, a.ValidateFor(d)
			}))
	}
	if s, ok := p.(AddressSuggester); ok {
		methods = append(methods,
			Serves(ContractShopAttributes, AttributesAddressSuggest, func(ctx context.Context, req AddressSuggestRequest) (AddressSuggestAnswer, error) {
				if err := req.Validate(); err != nil {
					return AddressSuggestAnswer{}, err
				}
				a, err := s.SuggestAddresses(ctx, req)
				if err != nil {
					return a, err
				}
				if a.Suggestions == nil {
					a.Suggestions = []AddressSuggestion{}
				}
				return a, a.ValidateFor(req)
			}),
			Serves(ContractShopAttributes, AttributesAddressResolve, func(ctx context.Context, req AddressResolveRequest) (AddressResolveAnswer, error) {
				if err := req.Validate(); err != nil {
					return AddressResolveAnswer{}, err
				}
				return checked(s.ResolveAddress(ctx, req))
			}))
	}
	return methods
}

// AttributeValuesProvider is the shop. Each write is answered through the shop's one write-command helper
// (WRITE_COMMANDS.md §3): keyed, attributed to CallOf(ctx).Caller, undone by value.undo, announced after the commit.
type AttributeValuesProvider interface {
	AttributeValues(ctx context.Context, req AttributeValuesRequest) (AttributeValuesAnswer, error)
	SetAttribute(ctx context.Context, req AttributeSetRequest) (AttributeWriteAnswer, error)
	UndoAttribute(ctx context.Context, req UndoInput) (AttributeWriteAnswer, error)
}

// AttributeValuesMethods are the shop's value methods, each checking the request before the shop sees it.
func AttributeValuesMethods(p AttributeValuesProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopAttributeValues, AttributeValuesGet, func(ctx context.Context, req AttributeValuesRequest) (AttributeValuesAnswer, error) {
			if err := req.Target.Validate(); err != nil {
				return AttributeValuesAnswer{}, err
			}
			a, err := p.AttributeValues(ctx, req)
			if err != nil {
				return a, err
			}
			if a.Values == nil {
				a.Values = []AttributeValue{}
			}
			if len(a.Values) > MaxAttributeValues {
				return AttributeValuesAnswer{}, fmt.Errorf("nilda: at most %d values in one answer", MaxAttributeValues)
			}
			return a, validValues(a.Values)
		}),
		Serves(ContractShopAttributeValues, AttributeValueSet, func(ctx context.Context, req AttributeSetRequest) (AttributeWriteAnswer, error) {
			if err := req.Validate(); err != nil {
				return AttributeWriteAnswer{}, err
			}
			return checked(p.SetAttribute(ctx, req))
		}),
		Serves(ContractShopAttributeValues, AttributeValueUndo, func(ctx context.Context, req UndoInput) (AttributeWriteAnswer, error) {
			if err := attributeText("record_id", req.RecordID, maxAttributeID, true); err != nil {
				return AttributeWriteAnswer{}, err
			}
			return checked(p.UndoAttribute(ctx, req))
		}),
	}
}

// ---- the shop's side: asking the field-owning plugins ----------------------------------------------------------------

// AttributeFieldsAnswer is one plugin's description, as the shop reads it.
type AttributeFieldsAnswer struct {
	Provider    string
	Description AttributeDescription
	Cached      bool
}

// AttributeFields asks every field-owning plugin for its fields. A plugin that did not answer is in the misses; its
// last good description (a day, Core's cache) stands in, marked Cached. An answer the rules refuse is a miss too.
func (s *Services) AttributeFields(ctx context.Context) ([]AttributeFieldsAnswer, []ServiceMiss, error) {
	res, err := s.Call(ctx, ContractShopAttributes, AttributesDescribe, struct{}{}, ServiceCallOptions{})
	if err != nil {
		return nil, nil, err
	}
	out := make([]AttributeFieldsAnswer, 0, len(res.Answers))
	missed := res.Missed
	for _, a := range res.Answers {
		var d AttributeDescription
		if err := a.Decode(&d); err != nil || d.Validate() != nil {
			missed = append(missed, ServiceMiss{Provider: a.Provider, Reason: "invalid_answer"})
			continue
		}
		out = append(out, AttributeFieldsAnswer{Provider: a.Provider, Description: d, Cached: a.Cached})
	}
	return out, missed, nil
}

// errNoAttributeAnswer is a call to one plugin that gave no usable answer.
var errNoAttributeAnswer = errors.New("nilda: the plugin gave no answer")

// attributeCallOne asks one plugin (or the shop) one method and decodes its answer into out.
func (s *Services) attributeCallOne(ctx context.Context, contract, provider, method string, req any, key string, out any) error {
	res, err := s.Call(ctx, contract, method, req, ServiceCallOptions{Provider: provider, IdempotencyKey: key})
	if err != nil {
		return err
	}
	if len(res.Answers) == 0 {
		reason := "no answer"
		if len(res.Missed) > 0 {
			reason = res.Missed[0].Reason
		}
		return fmt.Errorf("%w (%s %s: %s)", errNoAttributeAnswer, provider, method, reason)
	}
	if err := json.Unmarshal(res.Answers[0].Output, out); err != nil {
		return fmt.Errorf("nilda: the answer of %s %s does not decode: %w", contract, method, err)
	}
	return nil
}

// ValidateAttributes asks one plugin — d is its description — to check its values before the order is placed. An
// error is the plugin not answering, or answering about a field it never declared: the shop then does what
// d.WhenUnanswered says.
func (s *Services) ValidateAttributes(ctx context.Context, provider string, d AttributeDescription, req AttributeValidateRequest) (AttributeValidateAnswer, error) {
	if provider == "" {
		return AttributeValidateAnswer{}, errors.New("nilda: name the plugin whose fields are checked")
	}
	if err := req.Validate(); err != nil {
		return AttributeValidateAnswer{}, err
	}
	var a AttributeValidateAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributes, provider, AttributesValidate, req, "", &a); err != nil {
		return AttributeValidateAnswer{}, err
	}
	return a, a.ValidateFor(d)
}

// SuggestAddresses asks one plugin for suggestions. A plugin that did not answer is no suggestions, never an error:
// the visitor types the address. The shop bounds each visitor first (AddressSuggestLimiter).
func (s *Services) SuggestAddresses(ctx context.Context, provider string, req AddressSuggestRequest) (AddressSuggestAnswer, error) {
	if provider == "" {
		return AddressSuggestAnswer{}, errors.New("nilda: name the plugin that suggests addresses")
	}
	if err := req.Validate(); err != nil {
		return AddressSuggestAnswer{}, err
	}
	var a AddressSuggestAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributes, provider, AttributesAddressSuggest, req, "", &a); err != nil {
		if errors.Is(err, errNoAttributeAnswer) {
			return AddressSuggestAnswer{Suggestions: []AddressSuggestion{}}, nil
		}
		return AddressSuggestAnswer{}, err
	}
	return a, a.ValidateFor(req)
}

// ResolveAddress asks one plugin for the address of a picked suggestion; none answering is Found false.
func (s *Services) ResolveAddress(ctx context.Context, provider string, req AddressResolveRequest) (AddressResolveAnswer, error) {
	if provider == "" {
		return AddressResolveAnswer{}, errors.New("nilda: name the plugin that suggests addresses")
	}
	if err := req.Validate(); err != nil {
		return AddressResolveAnswer{}, err
	}
	var a AddressResolveAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributes, provider, AttributesAddressResolve, req, "", &a); err != nil {
		if errors.Is(err, errNoAttributeAnswer) {
			return AddressResolveAnswer{}, nil
		}
		return AddressResolveAnswer{}, err
	}
	return a, a.Validate()
}

// ---- the field-owning plugin's side: its values in the shop ----------------------------------------------------------

// AttributeValuesOf reads the caller's own values on one record.
func (s *Services) AttributeValuesOf(ctx context.Context, target AttributeTarget) ([]AttributeValue, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	var a AttributeValuesAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributeValues, "", AttributeValuesGet, AttributeValuesRequest{Target: target}, "", &a); err != nil {
		return nil, err
	}
	return a.Values, validValues(a.Values)
}

// SetAttribute sets one of the caller's fields; key is the write's idempotency key, required — derive it from what the
// value is for (the order and the field), never a fresh random one per attempt. A refusal is the answer's Refused, not
// an error; an error is the shop not answering.
func (s *Services) SetAttribute(ctx context.Context, key string, req AttributeSetRequest) (AttributeWriteAnswer, error) {
	if strings.TrimSpace(key) == "" {
		return AttributeWriteAnswer{}, errors.New("nilda: a write into the shop needs an idempotency key — derive it from what the write is for")
	}
	if err := req.Validate(); err != nil {
		return AttributeWriteAnswer{}, err
	}
	var a AttributeWriteAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributeValues, "", AttributeValueSet, req, key, &a); err != nil {
		return AttributeWriteAnswer{}, err
	}
	return a, a.Validate()
}

// UndoAttribute takes one of the caller's writes back; key is the undo's own idempotency key, required.
func (s *Services) UndoAttribute(ctx context.Context, key, recordID string) (AttributeWriteAnswer, error) {
	if strings.TrimSpace(key) == "" {
		return AttributeWriteAnswer{}, errors.New("nilda: an undo needs an idempotency key of its own")
	}
	if err := attributeText("record_id", recordID, maxAttributeID, true); err != nil {
		return AttributeWriteAnswer{}, err
	}
	var a AttributeWriteAnswer
	if err := s.attributeCallOne(ctx, ContractShopAttributeValues, "", AttributeValueUndo, UndoInput{RecordID: recordID}, key, &a); err != nil {
		return AttributeWriteAnswer{}, err
	}
	return a, a.Validate()
}

// ---- the rate limit of the proxy ------------------------------------------------------------------------------------

// AddressSuggestLimiter is the shop's bound on its address proxy: at most AddressSuggestPerMinute suggest and resolve
// calls per visitor in any minute, so a page that loops cannot spend the merchant's provider budget. Safe for
// concurrent use; it forgets a visitor a minute after their last call.
type AddressSuggestLimiter struct {
	mu     sync.Mutex
	seen   map[string][]time.Time
	pruned time.Time
}

// NewAddressSuggestLimiter makes an empty limiter.
func NewAddressSuggestLimiter() *AddressSuggestLimiter {
	return &AddressSuggestLimiter{seen: map[string][]time.Time{}}
}

// Allow records one call by visitor at now and reports whether it is within the bound. A refused call is not counted;
// a call naming no visitor is refused.
//
// Key it by VisitorKey(r) — Core's hash of the visitor's address, the same all day — and not by a basket or session
// id alone: a cookie is the caller's to drop, so a script that sends none is a new visitor on every call and spends
// the merchant's provider budget without bound. With both, ask twice — once per key — and refuse when either says no.
func (l *AddressSuggestLimiter) Allow(visitor string, now time.Time) bool {
	if visitor == "" {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	from := now.Add(-time.Minute)
	if now.Sub(l.pruned) >= time.Minute { // at most once a minute, so a call costs the visitors of one minute
		l.pruned = now
		for v, times := range l.seen {
			if len(times) == 0 || !times[len(times)-1].After(from) {
				delete(l.seen, v)
			}
		}
	}
	recent := l.seen[visitor][:0:0]
	for _, t := range l.seen[visitor] {
		if t.After(from) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= AddressSuggestPerMinute {
		l.seen[visitor] = recent
		return false
	}
	l.seen[visitor] = append(recent, now)
	return true
}

// tracked is how many visitors the limiter remembers (its test).
func (l *AddressSuggestLimiter) tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}

// ErrAddressSuggestLimited is the proxy's refusal of a visitor past the bound (HTTP 429).
var ErrAddressSuggestLimited = errors.New("nilda: too many address suggestions asked; wait a moment")
