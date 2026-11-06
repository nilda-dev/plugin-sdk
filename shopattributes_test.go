package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A delivery-slot plugin's description, as docs/ATTRIBUTES.md draws it.
func slotDescription() AttributeDescription {
	one, nine := 1.0, 9.0
	return AttributeDescription{
		Validates: true, WhenUnanswered: AttributeBlock,
		Fields: []AttributeField{
			{Key: "delivery_date", Target: AttributeOrder, Type: AttributeDate, Required: true,
				Label: map[string]string{"en": "Delivery date", "de": "Liefertag"}, Shown: []string{AttributeShownCheckout, AttributeShownOrder},
				MinDate: "P1D", MaxDate: "P60D"},
			{Key: "slot", Target: AttributeOrder, Type: AttributeSelect, Label: map[string]string{"en": "Time"},
				Shown:   []string{AttributeShownCheckout, AttributeShownOrder},
				Choices: []AttributeChoice{{Value: "am", Label: map[string]string{"en": "Morning"}}, {Value: "pm", Label: map[string]string{"en": "Afternoon"}}}},
			{Key: "engraving", Target: AttributeLine, Type: AttributeText, MaxLength: 20, Label: map[string]string{"en": "Engraving"},
				Shown: []string{AttributeShownProductPage, AttributeShownOrder}, SKUs: []string{"RING-1"}},
			{Key: "bags", Target: AttributeOrder, Type: AttributeNumber, Min: &one, Max: &nine, Label: map[string]string{"en": "Bags"},
				Shown: []string{AttributeShownCheckout}},
			{Key: "peppol_id", Target: AttributeCustomer, Type: AttributeText, Label: map[string]string{"en": "Peppol id"},
				Shown: []string{AttributeShownCheckout, AttributeShownOrder}},
			{Key: "fragile", Target: AttributeProduct, Type: AttributeBoolean, Label: map[string]string{"en": "Fragile"},
				Shown: []string{AttributeShownProductForm, AttributeShownOrder}},
			{Key: "slot_ref", Target: AttributeOrder, Type: AttributeText, Label: map[string]string{"en": "Slot reference"},
				Shown: []string{AttributeShownOrder}},
		},
	}
}

func field(d AttributeDescription, key string) AttributeField {
	for _, f := range d.Fields {
		if f.Key == key {
			return f
		}
	}
	panic("no field " + key)
}

// A PLUGIN'S FIELDS ARE HELD TO WHAT THE SHOP CAN DRAW AND KEEP (D2): a description that breaks any rule is refused
// before the shop sees it, each for its own reason.
func TestAnAttributeDescriptionIsHeldToItsRules(t *testing.T) {
	if err := slotDescription().Validate(); err != nil {
		t.Fatalf("the example is refused: %v", err)
	}
	two := 2.0
	cases := map[string]struct {
		edit func(d *AttributeDescription)
		want string
	}{
		"a key not lower-case":       {func(d *AttributeDescription) { d.Fields[0].Key = "Delivery" }, "key"},
		"a key twice":                {func(d *AttributeDescription) { d.Fields[1].Key = "delivery_date" }, "twice"},
		"too many fields":            {func(d *AttributeDescription) { d.Fields = append(d.Fields, manyFields(MaxAttributeFields)...) }, "at most"},
		"an unknown type":            {func(d *AttributeDescription) { d.Fields[0].Type = "file" }, "type"},
		"an unknown target":          {func(d *AttributeDescription) { d.Fields[0].Target = "basket" }, "target"},
		"shown nowhere":              {func(d *AttributeDescription) { d.Fields[0].Shown = nil }, "shown"},
		"an unknown place":           {func(d *AttributeDescription) { d.Fields[0].Shown = []string{"sidebar"} }, "shown"},
		"a product page order field": {func(d *AttributeDescription) { d.Fields[0].Shown = []string{AttributeShownProductPage} }, "product_page"},
		"a checkout product field":   {func(d *AttributeDescription) { d.Fields[5].Shown = []string{AttributeShownCheckout} }, "checkout"},
		"a product form order field": {func(d *AttributeDescription) { d.Fields[0].Shown = []string{AttributeShownProductForm} }, "product_form"},
		"required and never asked":   {func(d *AttributeDescription) { d.Fields[6].Required = true }, "required"},
		"choices on a text":          {func(d *AttributeDescription) { d.Fields[2].Choices = d.Fields[1].Choices }, "choices"},
		"a select without choices":   {func(d *AttributeDescription) { d.Fields[1].Choices = nil }, "choices"},
		"a choice twice":             {func(d *AttributeDescription) { d.Fields[1].Choices[1].Value = "am" }, "twice"},
		"a choice without a label":   {func(d *AttributeDescription) { d.Fields[1].Choices[0].Label = nil }, "label"},
		"no label":                   {func(d *AttributeDescription) { d.Fields[0].Label = nil }, "label"},
		"a label in no language":     {func(d *AttributeDescription) { d.Fields[0].Label = map[string]string{"en_US!": "x"} }, "language"},
		"an empty label":             {func(d *AttributeDescription) { d.Fields[0].Label = map[string]string{"en": " "} }, "label"},
		"a label of two lines":       {func(d *AttributeDescription) { d.Fields[0].Label = map[string]string{"en": "a\nb"} }, "label"},
		"a long label": {func(d *AttributeDescription) {
			d.Fields[0].Label = map[string]string{"en": strings.Repeat("x", MaxAttributeLabel+1)}
		}, "label"},
		"a long help": {func(d *AttributeDescription) {
			d.Fields[0].Help = map[string]string{"en": strings.Repeat("x", MaxAttributeHelp+1)}
		}, "help"},
		"a max length past the type": {func(d *AttributeDescription) { d.Fields[2].MaxLength = MaxAttributeText + 1 }, "max_length"},
		"a min length past the max":  {func(d *AttributeDescription) { d.Fields[2].MinLength = 21 }, "min_length"},
		"a length on a number":       {func(d *AttributeDescription) { d.Fields[3].MaxLength = 3 }, "max_length"},
		"a min past the max": {func(d *AttributeDescription) {
			d.Fields[3].Min = &two
			d.Fields[3].Max = &two
			d.Fields[3].Max = new(float64)
		}, "min"},
		"a min on a text":              {func(d *AttributeDescription) { d.Fields[2].Min = &two }, "min"},
		"a date bound that is no date": {func(d *AttributeDescription) { d.Fields[0].MinDate = "tomorrow" }, "min_date"},
		"a date bound on a text":       {func(d *AttributeDescription) { d.Fields[2].MinDate = "P1D" }, "min_date"},
		"a min date past the max":      {func(d *AttributeDescription) { d.Fields[0].MinDate = "P90D" }, "min_date"},
		"skus on an order field":       {func(d *AttributeDescription) { d.Fields[0].SKUs = []string{"RING-1"} }, "skus"},
		"an empty sku":                 {func(d *AttributeDescription) { d.Fields[2].SKUs = []string{""} }, "skus"},
		"validates, no fallback":       {func(d *AttributeDescription) { d.WhenUnanswered = "" }, "when_unanswered"},
		"an unknown fallback":          {func(d *AttributeDescription) { d.WhenUnanswered = "skip" }, "when_unanswered"},
		"a fallback, no validation":    {func(d *AttributeDescription) { d.Validates = false }, "when_unanswered"},
		"nothing to offer":             {func(d *AttributeDescription) { *d = AttributeDescription{} }, "nothing"},
	}
	for name, tc := range cases {
		d := slotDescription()
		tc.edit(&d)
		err := d.Validate()
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: refused for another reason: %v", name, err)
		}
	}
	// An address plugin owns no field and is a whole description.
	if err := (AttributeDescription{AddressSuggestions: true}).Validate(); err != nil {
		t.Errorf("an address plugin's description is refused: %v", err)
	}
}

func manyFields(n int) []AttributeField {
	out := make([]AttributeField, n)
	for i := range out {
		out[i] = AttributeField{Key: "f" + strings.Repeat("x", i%30) + string(rune('a'+i%26)), Target: AttributeOrder, Type: AttributeText,
			Label: map[string]string{"en": "F"}, Shown: []string{AttributeShownCheckout}}
	}
	return out
}

// THE ONE RULE FOR A VALUE: every type's value, its bounds, required — each refusal a code the shop words.
func TestAValueIsCheckedAgainstItsField(t *testing.T) {
	d := slotDescription()
	today := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	email := AttributeField{Key: "e", Target: AttributeOrder, Type: AttributeEmail, Label: map[string]string{"en": "E"}, Shown: []string{AttributeShownCheckout}}
	phone := email
	phone.Type = AttributePhone
	link := email
	link.Type = AttributeURL
	tick := AttributeField{Key: "terms", Target: AttributeOrder, Type: AttributeBoolean, Required: true, Label: map[string]string{"en": "T"}, Shown: []string{AttributeShownCheckout}}
	notes := AttributeField{Key: "notes", Target: AttributeOrder, Type: AttributeTextarea, Label: map[string]string{"en": "N"}, Shown: []string{AttributeShownCheckout}}
	for _, tc := range []struct {
		name  string
		f     AttributeField
		value string
		want  string // "" accepted
	}{
		{"a date in range", field(d, "delivery_date"), `"2026-11-06"`, ""},
		{"a date today, the min is tomorrow", field(d, "delivery_date"), `"2026-11-05"`, AttributeTooEarly},
		{"a date past the max", field(d, "delivery_date"), `"2027-01-05"`, AttributeTooLate},
		{"a date that is not one", field(d, "delivery_date"), `"2026-02-30"`, AttributeMalformed},
		{"a date as a number", field(d, "delivery_date"), `20261106`, AttributeWrongType},
		{"a required date missing", field(d, "delivery_date"), ``, AttributeRequired},
		{"a required date null", field(d, "delivery_date"), `null`, AttributeRequired},
		{"a required date empty", field(d, "delivery_date"), `""`, AttributeRequired},
		{"a choice", field(d, "slot"), `"am"`, ""},
		{"not a choice", field(d, "slot"), `"night"`, AttributeNotAChoice},
		{"an optional choice missing", field(d, "slot"), `null`, ""},
		{"a text within its max", field(d, "engraving"), `"For Sara"`, ""},
		{"a text past its max", field(d, "engraving"), `"` + strings.Repeat("ä", 21) + `"`, AttributeTooLong},
		{"a text with a newline", field(d, "engraving"), `"a\nb"`, AttributeMalformed},
		{"a text with a bidi override", field(d, "engraving"), `"a‮b"`, AttributeMalformed},
		{"a text as a number", field(d, "engraving"), `5`, AttributeWrongType},
		{"a long text keeps its newlines", notes, `"line one\nline two"`, ""},
		{"a long text past the type's max", notes, `"` + strings.Repeat("x", MaxAttributeTextarea+1) + `"`, AttributeTooLong},
		{"a number in range", field(d, "bags"), `3`, ""},
		{"a number below", field(d, "bags"), `0`, AttributeTooSmall},
		{"a number above", field(d, "bags"), `10`, AttributeTooLarge},
		{"a number as text", field(d, "bags"), `"3"`, AttributeWrongType},
		{"a boolean", field(d, "fragile"), `false`, ""},
		{"a boolean as text", field(d, "fragile"), `"yes"`, AttributeWrongType},
		{"a required tick ticked", tick, `true`, ""},
		{"a required tick unticked", tick, `false`, AttributeRequired},
		{"an email", email, `"ana@example.com"`, ""},
		{"not an email", email, `"Ana <ana@example.com>"`, AttributeMalformed},
		{"a phone", phone, `"+49 30 1234567"`, ""},
		{"not a phone", phone, `"call me"`, AttributeMalformed},
		{"a url", link, `"https://example.com/a"`, ""},
		{"not a url", link, `"example.com"`, AttributeMalformed},
		{"a NUL", field(d, "engraving"), `"a\u0000b"`, AttributeMalformed},
		{"not JSON", field(d, "engraving"), `{`, AttributeWrongType},
	} {
		err := tc.f.CheckValue(json.RawMessage(tc.value), today)
		var ve *AttributeValueError
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: refused: %v", tc.name, err)
		case tc.want != "" && !errors.As(err, &ve):
			t.Errorf("%s: want the code %s, got %v", tc.name, tc.want, err)
		case tc.want != "" && (ve.Code != tc.want || ve.Field != tc.f.Key):
			t.Errorf("%s: want %s on %s, got %s on %s", tc.name, tc.want, tc.f.Key, ve.Code, ve.Field)
		}
	}
}

// A label is said in the buyer's language when the plugin wrote it, else its base language, else English, else the
// first in order — one rule for the shop, the kit and the app.
func TestALabelIsPickedForTheBuyersLanguage(t *testing.T) {
	l := map[string]string{"en": "Date", "de": "Datum", "pt": "Data", "fa": "تاریخ"}
	for locale, want := range map[string]string{"de": "Datum", "de-AT": "Datum", "pt-BR": "Data", "fr": "Date", "": "Date", "FA": "تاریخ"} {
		if got := LabelIn(l, locale); got != want {
			t.Errorf("LabelIn(%q) = %q, want %q", locale, got, want)
		}
	}
	if got := LabelIn(map[string]string{"nl": "Datum", "es": "Fecha"}, "fr"); got != "Fecha" {
		t.Errorf("without English the first tag in order: got %q", got)
	}
}

// THE SLOT DRAWS NOTHING NEW: every field type is one the forms (and the owner's app) already draw, and a field reads
// as the form field the product form and the app draw.
func TestAnAttributeIsDrawnWithTheFormVocabulary(t *testing.T) {
	forms := map[string]bool{}
	for _, ft := range FormFieldTypes() {
		forms[ft] = true
	}
	for _, at := range AttributeFieldTypes() {
		if !forms[at] {
			t.Errorf("attribute type %q is not a form field type: nothing would draw it", at)
		}
	}
	d := slotDescription()
	ff := field(d, "delivery_date").FormField("de")
	if ff.Key != "delivery_date" || ff.Type != FormDate || ff.Label != "Liefertag" || !ff.Required {
		t.Errorf("the date reads as %+v", ff)
	}
	sel := field(d, "slot").FormField("en")
	if sel.Type != FieldSelect || strings.Join(sel.Choices, ",") != "am,pm" {
		t.Errorf("the select reads as %+v", sel)
	}
	num := field(d, "bags").FormField("en")
	if num.Min == nil || *num.Min != 1 || num.Max == nil || *num.Max != 9 {
		t.Errorf("the number's bounds are lost: %+v", num)
	}
	if got := field(d, "engraving").FormField("en").MaxLength; got != 20 {
		t.Errorf("the text's max length is %d", got)
	}
	if got := AttributeInputName("slots", "delivery_date", ""); got != "attr[slots.delivery_date]" {
		t.Errorf("an order field's input is %q", got)
	}
	if got := AttributeInputName("rings", "engraving", "l2"); got != "attr[rings.engraving][l2]" {
		t.Errorf("a line field's input is %q", got)
	}
}

// THE ADDRESS PROXY IS BOUNDED: a visitor gets AddressSuggestPerMinute calls in any minute, another visitor their own,
// a refused call does not count, and a minute later the visitor may ask again.
func TestTheAddressProxyIsBounded(t *testing.T) {
	l := NewAddressSuggestLimiter()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	for i := 0; i < AddressSuggestPerMinute; i++ {
		if !l.Allow("v1", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("call %d refused within the bound", i+1)
		}
	}
	at := now.Add(AddressSuggestPerMinute * time.Second)
	if l.Allow("v1", at) {
		t.Fatal("a call past the bound was allowed")
	}
	if !l.Allow("v2", at) {
		t.Fatal("another visitor shares the first one's bound")
	}
	if l.Allow("", at) {
		t.Fatal("a call naming no visitor was allowed")
	}
	if !l.Allow("v1", now.Add(time.Minute+time.Second)) {
		t.Fatal("the first call left the window and the visitor still may not ask")
	}
	// A visitor quiet for a minute is forgotten: the limiter does not grow with every visitor ever seen.
	for i := 0; i < 1000; i++ {
		l.Allow("crowd"+itoaTest(i), now)
	}
	l.Allow("late", now.Add(2*time.Hour))
	if n := l.tracked(); n > 1 {
		t.Errorf("the limiter still tracks %d visitors an hour after their last call", n)
	}
}

func itoaTest(i int) string { b, _ := json.Marshal(i); return string(b) }

// What a visitor typed and what a provider answers are bounded before either crosses.
func TestAddressSuggestionsAreBounded(t *testing.T) {
	for name, r := range map[string]AddressSuggestRequest{
		"too short":        {Query: "ab"},
		"too long":         {Query: strings.Repeat("a", MaxAddressQuery+1)},
		"a limit past 5":   {Query: "Hauptstr", Limit: MaxAddressSuggestions + 1},
		"a bad country":    {Query: "Hauptstr", Country: "Germany"},
		"a NUL":            {Query: "Haupt\x00str"},
		"a long session":   {Query: "Hauptstr", Session: strings.Repeat("s", 65)},
		"a negative limit": {Query: "Hauptstr", Limit: -1},
	} {
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	req := AddressSuggestRequest{Query: "Hauptstr", Country: "DE", Limit: 2}
	if err := req.Validate(); err != nil {
		t.Fatalf("a good request is refused: %v", err)
	}
	s := func(n int) AddressSuggestAnswer {
		a := AddressSuggestAnswer{}
		for i := 0; i < n; i++ {
			a.Suggestions = append(a.Suggestions, AddressSuggestion{ID: "p" + itoaTest(i), Label: "Hauptstr. " + itoaTest(i)})
		}
		return a
	}
	if err := s(2).ValidateFor(req); err != nil {
		t.Errorf("two of two: %v", err)
	}
	if s(3).ValidateFor(req) == nil {
		t.Error("three suggestions for a limit of two were accepted")
	}
	if s(MaxAddressSuggestions+1).ValidateFor(AddressSuggestRequest{Query: "Hauptstr"}) == nil {
		t.Error("six suggestions for the default limit were accepted")
	}
	bad := s(1)
	bad.Suggestions[0].ID = ""
	if bad.ValidateFor(req) == nil {
		t.Error("a suggestion without an id was accepted")
	}
	bad = s(1)
	bad.Suggestions[0].Address = &ShipAddress{}
	if bad.ValidateFor(req) == nil {
		t.Error("a suggestion whose address has no country was accepted")
	}
	if (AddressResolveAnswer{Found: true}).Validate() == nil {
		t.Error("a found address without the address was accepted")
	}
	if (AddressResolveRequest{}).Validate() == nil {
		t.Error("a resolve without an id was accepted")
	}
}

// The two contracts are the shop's door D2 as the plan draws it: experimental; describe falls back to the last good
// answer, validate blocks, suggestions skip; setting a value is a write command with its undo and its event.
func TestTheAttributeContractsAreDoorD2(t *testing.T) {
	attrs, ok := ContractNamed(ContractShopAttributes)
	if !ok {
		t.Fatal("no shop.attributes/v1")
	}
	values, ok := ContractNamed(ContractShopAttributeValues)
	if !ok {
		t.Fatal("no shop.attribute.values/v1")
	}
	for _, c := range []ServiceContract{attrs, values} {
		if !c.Experimental() {
			t.Errorf("%s is not experimental", c.Name)
		}
	}
	for method, fallback := range map[string]string{AttributesDescribe: "cached", AttributesValidate: "block",
		AttributesAddressSuggest: "skip", AttributesAddressResolve: "skip"} {
		m, ok := attrs.Method(method)
		if !ok || m.Fallback != fallback {
			t.Errorf("shop.attributes/v1 %s: fallback %q, want %q", method, m.Fallback, fallback)
		}
	}
	set, _ := values.Method(AttributeValueSet)
	if set.Write == nil || set.Write.Undo != AttributeValueUndo || set.Write.Emits != EventAttributeSet || set.Write.Record != AttributeValueRecord {
		t.Errorf("value.set is not a write command undone by value.undo: %+v", set.Write)
	}
	undo, _ := values.Method(AttributeValueUndo)
	if undo.Write == nil || undo.Write.Emits != EventAttributeUndone {
		t.Errorf("value.undo is not the undo write: %+v", undo.Write)
	}
	if get, ok := values.Method(AttributeValuesGet); !ok || get.Write != nil {
		t.Errorf("values.get is missing or writes")
	}
	// The events are the shop's to emit: its capability's namespace.
	for _, e := range []string{EventAttributeSet, EventAttributeUndone} {
		if !strings.HasPrefix(e, "ecommerce.") {
			t.Errorf("%s is not in the shop's namespace", e)
		}
	}
}

// slotPlugin is a field-owning plugin that answers whatever the test sets.
type slotPlugin struct {
	desc    AttributeDescription
	verdict AttributeValidateAnswer
}

func (p slotPlugin) DescribeAttributes(context.Context) (AttributeDescription, error) {
	return p.desc, nil
}
func (p slotPlugin) ValidateAttributes(context.Context, AttributeValidateRequest) (AttributeValidateAnswer, error) {
	return p.verdict, nil
}

type describeOnly struct{ desc AttributeDescription }

func (p describeOnly) DescribeAttributes(context.Context) (AttributeDescription, error) {
	return p.desc, nil
}

// THE PROVIDER'S SIDE CHECKS ITS OWN ANSWERS: a description it cannot keep fails the plugin's own test, a validator
// that says it validates must be one, and a verdict about a field it never declared is refused.
func TestAFieldOwnersAnswersAreCheckedBeforeTheyLeave(t *testing.T) {
	ctx := context.Background()
	call := func(p AttributeProvider, method string, in any) ([]byte, error) {
		raw, _ := json.Marshal(in)
		out, ok, err := DispatchServiceHook(ctx, methodsProvider(AttributeMethods(p)), ServiceHook(ContractShopAttributes, method), raw)
		if !ok {
			t.Fatalf("%s is not answered", method)
		}
		return out, err
	}
	if _, err := call(slotPlugin{desc: slotDescription()}, AttributesDescribe, struct{}{}); err != nil {
		t.Fatalf("a good description: %v", err)
	}
	bad := slotDescription()
	bad.Fields[0].Type = "file"
	if _, err := call(slotPlugin{desc: bad}, AttributesDescribe, struct{}{}); err == nil {
		t.Error("a description the shop cannot draw left the plugin")
	}
	if _, err := call(describeOnly{desc: slotDescription()}, AttributesDescribe, struct{}{}); err == nil {
		t.Error("a description saying validates left a plugin that cannot validate")
	}
	if _, err := call(describeOnly{desc: AttributeDescription{AddressSuggestions: true}}, AttributesDescribe, struct{}{}); err == nil {
		t.Error("a description offering addresses left a plugin that cannot suggest them")
	}
	req := AttributeValidateRequest{Currency: "EUR", Lines: []AttributeBasketLine{{ID: "l1", SKU: "RING-1", Quantity: 1}},
		Values: []AttributeValue{{Field: "delivery_date", Value: json.RawMessage(`"2026-11-06"`)}}}
	stray := slotPlugin{desc: slotDescription(), verdict: AttributeValidateAnswer{Errors: []AttributeError{{Field: "colour", Message: "no"}}}}
	if _, err := call(stray, AttributesValidate, req); err == nil {
		t.Error("a verdict about a field the plugin never declared left it")
	}
	fine := slotPlugin{desc: slotDescription(), verdict: AttributeValidateAnswer{Errors: []AttributeError{{Field: "delivery_date", Message: "That day is full."}}}}
	out, err := call(fine, AttributesValidate, req)
	if err != nil {
		t.Fatalf("a verdict about its own field: %v", err)
	}
	var got AttributeValidateAnswer
	_ = json.Unmarshal(out, &got)
	if len(got.Errors) != 1 || got.Errors[0].Message != "That day is full." {
		t.Errorf("the verdict changed on its way: %+v", got)
	}
	ok := slotPlugin{desc: slotDescription()}
	out, err = call(ok, AttributesValidate, req)
	if err != nil || !strings.Contains(string(out), `"errors":[]`) {
		t.Errorf("no errors is an empty list on the wire: %s %v", out, err)
	}
}

// fieldsHandler is a whole plugin (a Handler) that owns fields and wrote no ServiceMethods.
type fieldsHandler struct {
	Handler
	slotPlugin
}

type valuesHandler struct {
	Handler
}

func (valuesHandler) AttributeValues(context.Context, AttributeValuesRequest) (AttributeValuesAnswer, error) {
	return AttributeValuesAnswer{}, nil
}
func (valuesHandler) SetAttribute(context.Context, AttributeSetRequest) (AttributeWriteAnswer, error) {
	return AttributeWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "r1"}}, nil
}
func (valuesHandler) UndoAttribute(context.Context, UndoInput) (AttributeWriteAnswer, error) {
	return AttributeWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "r2"}}, nil
}

// Serve answers both sides of D2 for a plugin that implements them and forgot the one ServiceMethods line, as it does
// for a carrier or the shop's orders.
func TestServeFindsBothSidesOfTheAttributeDoor(t *testing.T) {
	for _, tc := range []struct {
		h       Handler
		hook    string
		payload string
	}{
		{fieldsHandler{slotPlugin: slotPlugin{desc: slotDescription()}}, ServiceHook(ContractShopAttributes, AttributesDescribe), `{}`},
		{valuesHandler{}, ServiceHook(ContractShopAttributeValues, AttributeValueSet), `{"target":{"kind":"order","id":"o1"},"field":"slot","value":"am"}`},
	} {
		p := serviceProviderOf(tc.h)
		if p == nil {
			t.Errorf("%T: Serve finds no provider", tc.h)
			continue
		}
		if _, ok, err := DispatchServiceHook(context.Background(), p, tc.hook, []byte(tc.payload)); !ok || err != nil {
			t.Errorf("%T %s: answered %v, %v", tc.h, tc.hook, ok, err)
		}
	}
}

// A write's answer is the record, or a refusal that wrote nothing — never both.
func TestAnAttributeWriteAnswerIsARecordOrARefusal(t *testing.T) {
	for name, tc := range map[string]struct {
		a  AttributeWriteAnswer
		ok bool
	}{
		"a record":                {AttributeWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "w1"}}, true},
		"a repeat":                {AttributeWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "w1", Already: true}}, true},
		"a refusal":               {AttributeWriteAnswer{Refused: AttributeNotOffered}, true},
		"nothing":                 {AttributeWriteAnswer{}, false},
		"a refusal with a record": {AttributeWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "w1"}, Refused: AttributeReplaced}, false},
		"an unknown refusal":      {AttributeWriteAnswer{Refused: "nope"}, false},
	} {
		if err := tc.a.Validate(); (err == nil) != tc.ok {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// THE OWNER'S APP DRAWS EVERY ATTRIBUTE TYPE: each is one of the form types the mobile app names (AppFormType in its
// apps_models.dart), so a plugin's field never reaches a screen the app draws as plain text. Skipped when the app is not
// checked out beside plugin-sdk.
func TestTheOwnersAppDrawsEveryAttributeType(t *testing.T) {
	path := filepath.Join("..", "mobile", "app", "lib", "api", "apps_models.dart")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("mobile is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "abstract final class AppFormType {")
	if start < 0 {
		t.Fatalf("%s has no AppFormType — repoint this guard", path)
	}
	body := src[start:]
	body = body[:strings.Index(body, "\n}")]
	drawn := map[string]bool{}
	for _, m := range regexp.MustCompile(`static const \w+ = '([a-z_]+)';`).FindAllStringSubmatch(body, -1) {
		drawn[m[1]] = true
	}
	for _, at := range AttributeFieldTypes() {
		if !drawn[at] {
			t.Errorf("the owner's app does not draw %q (AppFormType): a field of that type would be plain text there", at)
		}
	}
}
