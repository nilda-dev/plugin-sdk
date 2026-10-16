package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A form's open and save each reach their own handler, and the answer travels in the fields Core reads.
func TestAFormsLoadAndSaveReachTheirHandlers(t *testing.T) {
	var loaded, saved AdminFormRequest
	load := func(_ context.Context, r AdminFormRequest) (AdminFormResult, error) {
		loaded = r
		return AdminFormResult{ID: r.ID, Title: "Flour sack", Values: map[string]any{"price": 2000}, Version: "v1"}, nil
	}
	save := func(_ context.Context, r AdminFormRequest) (AdminFormResult, error) {
		saved = r
		return AdminFormResult{Errors: []FormError{{Field: "variants.1.price", Message: "Enter a price above zero"}}}, nil
	}
	out, handled, err := DispatchAdminForm(context.Background(), AdminFormLoadHook,
		[]byte(`{"page":"products","id":"p1"}`), load, save)
	if !handled || err != nil || loaded.Page != "products" || loaded.ID != "p1" {
		t.Fatalf("load: handled=%v err=%v req=%+v", handled, err, loaded)
	}
	var wire map[string]any
	_ = json.Unmarshal(out, &wire)
	if wire["title"] != "Flour sack" || wire["version"] != "v1" {
		t.Errorf("the load answered %s", out)
	}

	out, handled, err = DispatchAdminForm(context.Background(), AdminFormSaveHook,
		[]byte(`{"page":"products","id":"p1","values":{"title":"Flour"},"version":"v1"}`), load, save)
	if !handled || err != nil || saved.Values["title"] != "Flour" || saved.Version != "v1" {
		t.Fatalf("save: handled=%v err=%v req=%+v", handled, err, saved)
	}
	var res struct {
		Errors []struct{ Field, Message string } `json:"errors"`
	}
	_ = json.Unmarshal(out, &res)
	if len(res.Errors) != 1 || res.Errors[0].Field != "variants.1.price" {
		t.Errorf("a field's error must arrive under `errors` with its path; the reply was %s", out)
	}
}

// A form opened or saved with no handler says so rather than answering an empty form or a silent "saved".
func TestAFormWithNoHandlerIsAnError(t *testing.T) {
	for _, hook := range []string{AdminFormLoadHook, AdminFormSaveHook} {
		if _, handled, err := DispatchAdminForm(context.Background(), hook, []byte(`{}`), nil, nil); !handled || err == nil {
			t.Errorf("%s: handled=%v err=%v — a missing handler must be an error", hook, handled, err)
		}
	}
}

// The handler's own error reaches the owner as the reason; other hooks pass through untouched.
func TestAFormErrorReachesTheOwnerAndOtherHooksPassThrough(t *testing.T) {
	boom := errors.New("This product was deleted")
	fail := func(context.Context, AdminFormRequest) (AdminFormResult, error) { return AdminFormResult{}, boom }
	if _, _, err := DispatchAdminForm(context.Background(), AdminFormSaveHook, []byte(`{}`), fail, fail); !errors.Is(err, boom) {
		t.Errorf("the reason was lost: %v", err)
	}
	if _, handled, _ := DispatchAdminForm(context.Background(), AdminActionHook, nil, fail, fail); handled {
		t.Error("a row action must not be claimed by the form dispatcher")
	}
}

// The form vocabulary is the settings one plus the date, media and repeater types of the widget vocabulary and
// four of its own — and never a type no list names.
func TestTheFormVocabularyExtendsTheSettingsOne(t *testing.T) {
	form := map[string]bool{}
	for _, t := range FormFieldTypes() {
		form[t] = true
	}
	for _, s := range SettingFieldTypes() {
		if !form[s] {
			t.Errorf("settings type %q is not a form type", s)
		}
	}
	widget := map[string]bool{}
	for _, w := range FieldTypes() {
		widget[w] = true
	}
	own := map[string]bool{FormMoney: true, FormTags: true, FormReference: true, FormStat: true}
	for f := range form {
		if !widget[f] && !own[f] {
			t.Errorf("form type %q is neither a widget type nor one of the form's own", f)
		}
	}
}
