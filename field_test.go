package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// What an author writes, and what Nilda reads. The dispatch is small; the risk is the shapes — a field
// renamed on one side is a plugin that installs, appears in the builder, and validates nothing.

type recordingField struct {
	choices []FieldChoice
	msg     string
	err     error

	sawChoices  FieldChoicesRequest
	sawValidate FieldValidateRequest
}

func (f *recordingField) Choices(_ context.Context, _ *Core, req FieldChoicesRequest) ([]FieldChoice, error) {
	f.sawChoices = req
	return f.choices, f.err
}

func (f *recordingField) Validate(_ context.Context, _ *Core, req FieldValidateRequest) string {
	f.sawValidate = req
	return f.msg
}

func dispatchField(t *testing.T, p FieldProvider, hook string, payload any) map[string]any {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	out, handled, err := DispatchFieldHook(context.Background(), nil, p, hook, body)
	if !handled {
		t.Fatalf("%s was not handled", hook)
	}
	if err != nil {
		t.Fatalf("%s: %v", hook, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s answered with something that is not JSON: %s", hook, out)
	}
	return got
}

func TestChoicesAnswerTheShapeCoreReads(t *testing.T) {
	p := &recordingField{choices: []FieldChoice{{Value: "ir", Label: "Iran"}, {Value: "de", Label: "Germany"}}}
	got := dispatchField(t, p, HookFieldChoices, FieldChoicesRequest{
		Field: "country", Options: map[string]any{"region": "asia"},
	})

	list, _ := got["choices"].([]any)
	if len(list) != 2 {
		t.Fatalf("choices = %v", got["choices"])
	}
	first, _ := list[0].(map[string]any)
	if first["value"] != "ir" || first["label"] != "Iran" {
		t.Errorf("first = %v", first)
	}
	// The editor's own settings reach the author: "which country's postcodes" is the whole reason a picker
	// is a plugin rather than a fixed list.
	if p.sawChoices.Field != "country" || p.sawChoices.Options["region"] != "asia" {
		t.Errorf("request = %+v", p.sawChoices)
	}
}

// TestNoChoicesIsAnEmptyListNotNull — `null` deserialises into a nil slice on Core's side and reads as a
// malformed answer rather than as "this picker has nothing to offer right now".
func TestNoChoicesIsAnEmptyListNotNull(t *testing.T) {
	got := dispatchField(t, &recordingField{}, HookFieldChoices, FieldChoicesRequest{Field: "x"})
	list, ok := got["choices"].([]any)
	if !ok || list == nil {
		t.Fatalf("choices = %v, want an empty list", got["choices"])
	}
	if len(list) != 0 {
		t.Fatalf("choices = %v", list)
	}
}

func TestAnAcceptedValueAnswersOK(t *testing.T) {
	p := &recordingField{}
	got := dispatchField(t, p, HookFieldValidate, FieldValidateRequest{
		Field: "postcode", Key: "shipping_pc", Value: "SW1A 1AA",
		Options: map[string]any{"country": "GB"},
	})
	if got["ok"] != true {
		t.Fatalf("ok = %v", got["ok"])
	}
	if p.sawValidate.Value != "SW1A 1AA" || p.sawValidate.Key != "shipping_pc" {
		t.Errorf("request = %+v", p.sawValidate)
	}
	if p.sawValidate.Options["country"] != "GB" {
		t.Errorf("the editor's settings did not reach the author: %+v", p.sawValidate)
	}
}

// TestARefusalCarriesTheAuthorsOwnWORDS — they are shown to an editor verbatim, and a refusal nobody can
// act on is worse than no refusal.
func TestARefusalCarriesTheAuthorsOwnWords(t *testing.T) {
	p := &recordingField{msg: "must be six digits"}
	got := dispatchField(t, p, HookFieldValidate, FieldValidateRequest{Field: "postcode", Value: "x"})
	if got["ok"] != false {
		t.Fatalf("ok = %v", got["ok"])
	}
	if got["message"] != "must be six digits" {
		t.Fatalf("message = %v", got["message"])
	}
}

func TestAnUnknownHookIsNotClaimedByTheFieldDispatch(t *testing.T) {
	_, handled, _ := DispatchFieldHook(context.Background(), nil, &recordingField{}, "content.saved", nil)
	if handled {
		t.Fatal("the field dispatch claimed a hook that is not its own")
	}
}

func TestAnAuthorsChoicesErrorReachesCore(t *testing.T) {
	p := &recordingField{err: errors.New("the country service is down")}
	body, _ := json.Marshal(FieldChoicesRequest{Field: "country"})
	_, handled, err := DispatchFieldHook(context.Background(), nil, p, HookFieldChoices, body)
	if !handled {
		t.Fatal("not handled")
	}
	if err == nil || !strings.Contains(err.Error(), "country service") {
		t.Fatalf("the author's error did not reach Core: %v", err)
	}
}

// TestMalformedPayloadIsAnErrorNotAPanicForFields — the payload comes from another process, and a plugin
// that panics on an unexpected shape is one the supervisor restarts in a loop.
func TestMalformedPayloadIsAnErrorNotAPanicForFields(t *testing.T) {
	for _, hook := range []string{HookFieldChoices, HookFieldValidate} {
		_, handled, err := DispatchFieldHook(context.Background(), nil, &recordingField{}, hook, []byte("not json"))
		if !handled || err == nil {
			t.Errorf("%s: handled=%v err=%v", hook, handled, err)
		}
	}
}

// TestTheFieldOnlyHandlerSubscribesToBoth — a plugin whose whole job is a field type must not install
// cleanly and then never be asked anything.
func TestTheFieldOnlyHandlerSubscribesToBoth(t *testing.T) {
	h := &fieldOnlyHandler{p: &recordingField{}}
	res, err := h.Init(context.Background(), &Core{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{HookFieldChoices: false, HookFieldValidate: false}
	for _, hook := range res.Hooks {
		want[hook] = true
	}
	for hook, subscribed := range want {
		if !subscribed {
			t.Errorf("%s was not subscribed", hook)
		}
	}
}
