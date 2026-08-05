package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// CONTRIBUTING A KIND OF FIELD.
//
// A star rating, a country picker, a postcode, an IBAN, a map coordinate. Your field type appears in the
// content-type builder under its own name, an editor configures it, and the value is stored, exported,
// searched and read by themes like any other.
//
// # You declare a control; you do not draw one
//
// Your field type names one of Nilda's own controls as its BASE, and Nilda draws that. This is the one
// thing about the design worth understanding, because the obvious alternative looks easier and is not
// available: the admin is a React application, so markup you send can be displayed but cannot capture a
// value — and the only thing that would work is your JavaScript running in the admin's own origin, which
// would hand every plugin author a session-stealing primitive. No capability in Nilda ships code into a
// browser.
//
// What you add on top of the base is everything a bare control cannot do:
//
//	a name and a label   "Star rating" appears in the builder as itself, not as "number"
//	your own settings    "how many stars", "which countries"
//	choices              a picker whose options come from YOU, not typed by an editor
//	validation           asked on every SAVE, in Nilda's path — not in a browser somebody can bypass
//
// # In your manifest
//
//	"capabilities": ["field"],
//	"fields": [{
//	  "key": "postcode", "label": "Postcode", "base": "text", "validates": true,
//	  "settings": [{ "key": "country", "label": "Country", "type": "select",
//	                 "choices": ["GB", "NL", "DE"] }]
//	}]
//
// Nilda namespaces it as `<your plugin key>.postcode`, which is what a content type stores — so two plugins
// may both offer "postcode" and a saved field always records which one it meant.
//
// # What the base decides, and you do not
//
// How the value is STORED. A `text` base stores a string, a `select` stores a choice value, an `image`
// stores a media id. That is not negotiable, because the value has to be readable by the API, the exporter,
// the search indexer and the theme — none of which will ever ask a plugin what a byte means.

// Hook names Nilda dispatches for a declared field type. `ServeField` handles them; they are exported
// because a plugin that also owns other hooks may prefer its own switch.
const (
	// HookFieldChoices asks what a picker should offer. Only for a type whose manifest says `choices`.
	HookFieldChoices = "field.choices"
	// HookFieldValidate asks whether a value is acceptable. Only for a type whose manifest says
	// `validates`, and only AFTER the base type's own rules have passed.
	HookFieldValidate = "field.validate"
)

// FieldChoicesRequest asks for a picker's options.
type FieldChoicesRequest struct {
	// Field is your LOCAL key, as declared in your manifest.
	Field string `json:"field"`
	// Options are the settings an editor configured for this particular field — your declared settings,
	// with the values they typed. "Which country's postcodes", "how many stars".
	Options map[string]any `json:"options"`
}

// FieldChoice is one option in a picker. Label falls back to Value when empty.
type FieldChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldValidateRequest asks whether one value may be stored.
type FieldValidateRequest struct {
	// Field is your LOCAL key; Key is the field's key within the content type, for your message.
	Field string `json:"field"`
	Key   string `json:"key"`
	// Value has already passed the BASE type's own rules, so a `text` base means this is a string.
	Value any `json:"value"`
	// Options are the settings an editor configured for this field.
	Options map[string]any `json:"options"`
}

// FieldProvider is what you implement.
//
// A type that declares neither `choices` nor `validates` needs neither method to do anything: return nil
// and "" — Nilda never asks.
type FieldProvider interface {
	// Choices returns a picker's options. Bounded by Nilda; a very long list is trimmed rather than
	// refused, so answer with what a person could plausibly scroll.
	Choices(ctx context.Context, core *Core, req FieldChoicesRequest) ([]FieldChoice, error)
	// Validate returns "" to accept, or a message an EDITOR can act on. It is shown to them verbatim, so
	// "must be six digits" beats "invalid".
	//
	// Nilda calls this on every save of every content item using your type. Keep it fast and local: a call
	// out to somebody's API here puts their downtime in the way of writing.
	Validate(ctx context.Context, core *Core, req FieldValidateRequest) string
}

// ServeField is Serve for a plugin whose whole job is a field type.
func ServeField(p FieldProvider) { Serve(&fieldOnlyHandler{p: p}) }

// DispatchFieldHook handles the two field hooks and reports whether it recognised this one.
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchFieldHook(ctx, p.core, p, hook, payload); handled {
//			return out, err
//		}
//		// ... your own hooks
//	}
func DispatchFieldHook(ctx context.Context, core *Core, p FieldProvider, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookFieldChoices:
		var req FieldChoicesRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, fmt.Errorf("field.choices: %w", err)
		}
		choices, err := p.Choices(ctx, core, req)
		if err != nil {
			return nil, true, err
		}
		if choices == nil {
			choices = []FieldChoice{}
		}
		body, err := json.Marshal(map[string]any{"choices": choices})
		return body, true, err

	case HookFieldValidate:
		var req FieldValidateRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, fmt.Errorf("field.validate: %w", err)
		}
		msg := p.Validate(ctx, core, req)
		body, err := json.Marshal(map[string]any{"ok": msg == "", "message": msg})
		return body, true, err
	}
	return nil, false, nil
}

// fieldOnlyHandler adapts a FieldProvider onto the full Handler interface.
type fieldOnlyHandler struct {
	p    FieldProvider
	core *Core
}

func (h *fieldOnlyHandler) Init(_ context.Context, core *Core) (InitResult, error) {
	h.core = core
	// Both hooks unconditionally. Nilda delivers them only to a plugin granted `field` and only for a type
	// whose manifest asked, so subscribing to one you never use is harmless — and NOT subscribing would be
	// a plugin that installs cleanly and is never asked anything.
	return InitResult{Hooks: []string{HookFieldChoices, HookFieldValidate}}, nil
}

func (h *fieldOnlyHandler) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, handled, err := DispatchFieldHook(ctx, h.core, h.p, hook, payload)
	if !handled {
		return nil, fmt.Errorf("unknown hook %q", hook)
	}
	return out, err
}

func (h *fieldOnlyHandler) HandleEvent(context.Context, string, []byte) error { return nil }
