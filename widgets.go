package nilda

import (
	"context"
	"encoding/json"
	"sort"
)

// The two capabilities that let a plugin put something on a PUBLIC page: `widget` and `render.assets`.
//
// Both were reachable before this file existed, in the sense that Core dispatched hooks for them and a
// plugin could answer. What was missing is the only part that matters to an author: the shape. Core
// marshals a request struct, sends the JSON, and unmarshals a response struct — and those structs live in
// Core's `internal/`, which by Go's own rules no plugin can import. An author's options were to
// reverse-engineer field names from a repository they may not have, or to guess. Guessing wrong is silent:
// a mismatched key unmarshals into a zero value, and a widget that renders nothing looks exactly like a
// widget whose plugin is not running.
//
// So these are MIRRORS. The JSON tags here must match Core's exactly or the seam is dead. Two tests hold
// that line: widgets_test.go feeds in payloads copied byte-for-byte from what Core marshals, and Core's own
// sdk_mirror_test.go — the only place that can import both sides — fails if either struct drifts.
//
// The interfaces below are the point of the file. Implement WidgetProvider and you never see a hook name
// or a byte slice; Serve intercepts the two widget hooks and does the JSON. Implement AssetProvider and
// the `render.assets` subscription is added for you, because "I implemented the interface but forgot to
// list the hook in InitResult" is a bug with no symptom.

// Hook names Core dispatches for these two capabilities. Exported because a plugin that would rather
// handle them by hand in HandleHook still needs to match on something other than a string literal.
const (
	// HookWidgetDescribe asks the plugin what widgets it offers. Core sends `{}` and expects the widget
	// list. Dispatched to every plugin granted `widget` — it is NOT a subscription, so nothing has to be
	// listed in InitResult.Hooks for it to arrive.
	HookWidgetDescribe = "widget.describe"
	// HookWidgetRender asks for one widget's HTML. Also grant-driven, not subscription-driven.
	HookWidgetRender = "widget.render"
	// HookRenderAssets asks for the scripts to add to a public page. Unlike the widget hooks this IS a
	// subscription: it only arrives if "render.assets" is in InitResult.Hooks (AssetProvider adds it).
	HookRenderAssets = "render.assets"
)

// Widget field types Core's page builder understands. A field with any other type is DROPPED — silently,
// on Core's side — so this list is the contract rather than a suggestion.
//
// It used to be seven types where Core accepts twenty-plus, which made a whole class of widget impossible to
// write as a plugin: no repeater, so no list-shaped widget at all; no media beyond a single image; no date,
// no colour, no icon. A plugin author hitting that had no way to know whether the type was unsupported or
// their spelling was wrong, because the failure is a dropped field with no message.
//
// KEEPING IT IN STEP IS ENFORCED, NOT REMEMBERED. Core depends on this module, so `FieldTypes()` below is
// walked by a test in Core (`internal/pagebuilder`) that asserts every type here is one its field registry
// actually accepts, and that nothing Core offers plugin authors is missing here. Adding a type to Core
// without adding it here is a build failure there, not a discovery by a plugin author.
const (
	// Text-like.
	FieldText     = "text"
	FieldTextarea = "textarea"
	FieldRichText = "richtext"
	FieldLink     = "link"
	FieldURL      = "url"
	FieldEmail    = "email"
	FieldPhone    = "phone"
	FieldColor    = "color"
	// FieldIcon is a name from Core's bundled icon catalogue, not an arbitrary string: the picker offers
	// what the build ships, and an unknown name renders nothing.
	FieldIcon = "icon"

	// Numeric.
	FieldNumber = "number"
	FieldRange  = "range"
	// FieldScale is a bounded whole number — a rating, an NPS score, a 1-to-5 answer.
	FieldScale = "scale"

	FieldBoolean = "boolean"

	// Date and time.
	FieldDate     = "date"
	FieldTime     = "time"
	FieldDateTime = "datetime"

	// Choice. FieldSelect and FieldRadio take one value; the other two take several.
	FieldSelect      = "select"
	FieldRadio       = "radio"
	FieldMultiSelect = "multiselect"
	FieldCheckbox    = "checkbox"

	// Media. Values are Core media IDs, resolved at render — a plugin never handles the file.
	FieldImage   = "image"
	FieldFile    = "file"
	FieldGallery = "gallery"

	// Structural. A repeater is what makes a LIST-shaped widget possible at all: rows of sub-fields the
	// author adds, reorders and removes. Its sub-fields go in WidgetField.Fields.
	FieldRepeater = "repeater"
	FieldGroup    = "group"

	// FieldLikert is a matrix: statements down the side, a shared answer scale across the top. Its rows go
	// in WidgetField.Rows and its columns in Choices.
	FieldLikert = "likert"

	// Display-only. A message shows text in the inspector and collects nothing.
	FieldMessage = "message"
)

// FieldTypes returns every type a plugin widget may declare, sorted.
//
// Exported so the contract can be CHECKED rather than described: Core walks this list in a test and fails
// if it names a type Core would drop, or omits one Core accepts. That is what makes "the vocabulary is
// shared" a fact about the build instead of a claim in a document.
func FieldTypes() []string {
	out := []string{
		FieldText, FieldTextarea, FieldRichText, FieldLink, FieldURL, FieldEmail, FieldPhone,
		FieldColor, FieldIcon, FieldNumber, FieldRange, FieldScale, FieldBoolean,
		FieldDate, FieldTime, FieldDateTime,
		FieldSelect, FieldRadio, FieldMultiSelect, FieldCheckbox,
		FieldImage, FieldFile, FieldGallery,
		FieldRepeater, FieldGroup, FieldLikert, FieldMessage,
	}
	sort.Strings(out)
	return out
}

// Limits Core enforces on these two surfaces. Exceeding one is not an error a plugin is told about — the
// excess is dropped and logged on Core's side — so they are here to be designed against rather than
// discovered.
const (
	// MaxWidgetsPerPlugin is how many widgets one plugin may contribute; the rest are ignored.
	MaxWidgetsPerPlugin = 20
	// MaxWidgetHTMLBytes caps one rendered widget. Larger output is dropped, not truncated.
	MaxWidgetHTMLBytes = 64 << 10
	// MaxAssetsPerPlugin is how many scripts one plugin may add to a page.
	MaxAssetsPerPlugin = 5
)

// WidgetField is one typed control shown in the page builder for a widget's configuration.
type WidgetField struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // one of the Field* constants
	Required bool   `json:"required,omitempty"`
	// Choices are the options for FieldSelect, FieldRadio, FieldMultiSelect and FieldCheckbox. A choice
	// field with none is dropped: an empty dropdown is a control nobody can answer.
	Choices []string `json:"choices,omitempty"`
	// Fields are the sub-fields of a FieldRepeater or FieldGroup — the rows an author adds and reorders.
	// This is what makes a list-shaped widget expressible at all, and it is why the type is recursive.
	//
	// ONE LEVEL. Core validates nested structures, but a repeater inside a repeater inside a repeater is an
	// inspector nobody can use; the limit is stated here so it is designed against rather than discovered.
	Fields []WidgetField `json:"fields,omitempty"`
	// Min, Max and Step bound FieldNumber, FieldRange and FieldScale. Pointers because unset and zero are
	// different answers: a scale starting at 0 is a real scale, and defaulting an unset Min to 0 would
	// silently move every 1-to-5 rating down a notch.
	// One declaration per line, and not for style: `Min, Max, Step *float64 \`json:"min,omitempty"\`` gives
	// all THREE the tag "min", and Go's encoder drops a duplicated tag entirely — so a 1-to-5 scale with
	// 0.5 steps serialised as `{}`. Every numeric bound a widget could declare was silently discarded, in
	// every direction, since the field was written. `go vet` names it; nothing else did.
	Min  *float64 `json:"min,omitempty" jsonschema:"-"`
	Max  *float64 `json:"max,omitempty" jsonschema:"-"`
	Step *float64 `json:"step,omitempty" jsonschema:"-"`
	// Rows are the statements of a FieldLikert — the questions down the side, answered with Choices across
	// the top. Separate from Choices because they are different axes, and a single list cannot be both.
	Rows []string `json:"rows,omitempty"`
	// Placeholder and Help are inspector affordances. Help is where a plugin explains a field it could not
	// name clearly enough — better there than in a README nobody reads while configuring a widget.
	Placeholder string `json:"placeholder,omitempty"`
	Help        string `json:"help,omitempty"`
}

// WidgetDef is one widget as the plugin offers it.
type WidgetDef struct {
	// Type is the plugin's LOCAL name, e.g. "product-grid". Core namespaces it as "<plugin key>.<type>"
	// using the key it already knows — a plugin cannot claim another's namespace by returning one here.
	Type     string        `json:"type"`
	Label    string        `json:"label"`
	Category string        `json:"category,omitempty"`
	Fields   []WidgetField `json:"fields,omitempty"`
}

// WidgetRenderRequest is what Core sends for one widget render.
//
// Deliberately PAGE-level: there is no user, session or request here, and that is not an oversight. Rendered
// pages are cached, so a widget that varied by viewer would serve one visitor's output to the next. Anything
// per-viewer belongs in the browser, against the plugin's own `route`.
type WidgetRenderRequest struct {
	Type    string         `json:"type"` // the local name, namespace already stripped
	Config  map[string]any `json:"config,omitempty"`
	Kind    string         `json:"kind"` // home | single | archive | search | notfound | …
	Locale  string         `json:"locale,omitempty"`
	RTL     bool           `json:"rtl,omitempty"`
	Title   string         `json:"title,omitempty"`
	URL     string         `json:"url,omitempty"`
	SiteURL string         `json:"site_url,omitempty"`
}

// RenderAssetsRequest is what Core sends once per public page render to each `render.assets` subscriber.
type RenderAssetsRequest struct {
	PageKind string `json:"page_kind"` // home | single | archive | search | notfound | …
	Locale   string `json:"locale"`
}

// ScriptAsset is one script a plugin adds to a page.
//
// A PATH, never markup: Core renders the <script> tag itself, so there is nothing for a plugin to inject
// through. Src must be root-relative, same-origin, and under this plugin's own declared route prefix
// ("/shop/cart.js" for a plugin serving "/shop"). An absolute URL, a protocol-relative "//host/x.js", or a
// path under someone else's prefix is dropped — a plugin cannot pull third-party JavaScript onto the site.
// Src therefore requires the `route` capability as well: a plugin that serves no paths owns none.
type ScriptAsset struct {
	Src   string `json:"src"`
	Defer bool   `json:"defer,omitempty"` // the sane default for a UI script
	Async bool   `json:"async,omitempty"`
}

// WidgetProvider is implemented by a plugin that contributes page-builder widgets. Requires the `widget`
// capability; without it Core never asks, and these methods are never called.
type WidgetProvider interface {
	// Widgets returns the catalogue. Called when the plugin set changes, not per page — treat it as static.
	Widgets() []WidgetDef
	// RenderWidget returns the widget's HTML.
	//
	// SANITIZED by Core before it reaches a page: <script>, inline styles, class, nonce and data-* are
	// stripped. Markup that depends on any of those will render, but not as written — which is why anything
	// interactive belongs in a ScriptAsset served from the plugin's own route, not in this string.
	//
	// An error, or output over MaxWidgetHTMLBytes, renders nothing. A page is never failed by a plugin.
	RenderWidget(ctx context.Context, req WidgetRenderRequest) (string, error)
}

// AssetProvider is implemented by a plugin that loads its own script on public pages. Requires the
// `render.assets` capability, and `route` to have a path worth naming.
//
// Implementing it subscribes the plugin to the hook automatically — see Serve.
type AssetProvider interface {
	// FooterScripts returns the scripts for this page, or nil for none. Called on every uncached public
	// render, so it must not block: no network, no database, just a decision.
	FooterScripts(ctx context.Context, req RenderAssetsRequest) []ScriptAsset
}

// widgetDescribeResponse / widgetRenderResponse / renderAssetsResponse are the wire envelopes. Unexported:
// an author who implements the interfaces never constructs one, and an author who handles the hooks by hand
// is better served by the documented JSON than by a type that would then be part of the SDK's API.
type widgetDescribeResponse struct {
	Widgets []WidgetDef `json:"widgets"`
}

type widgetRenderResponse struct {
	HTML string `json:"html"`
}

type renderAssetsResponse struct {
	FooterScripts []ScriptAsset `json:"footer_scripts,omitempty"`
}

// dispatchProvided handles the hooks the optional interfaces cover, and reports whether it did.
//
// Returning ok=false rather than an error for an unrecognised hook is what keeps this composable: Serve
// tries it first and falls through to the author's HandleHook, so implementing WidgetProvider does not take
// "widget.render" away from a plugin that also wants to see it.
func dispatchProvided(ctx context.Context, h Handler, hook string, payload []byte) (out []byte, ok bool, err error) {
	switch hook {
	case HookWidgetDescribe:
		wp, is := h.(WidgetProvider)
		if !is {
			return nil, false, nil
		}
		defs := wp.Widgets()
		if defs == nil {
			// An explicit empty list, not null: Core unmarshals this, and `{"widgets":null}` and
			// `{"widgets":[]}` are the same to it, but only one of them reads as an answer.
			defs = []WidgetDef{}
		}
		raw, err := json.Marshal(widgetDescribeResponse{Widgets: defs})
		return raw, true, err

	case HookWidgetRender:
		wp, is := h.(WidgetProvider)
		if !is {
			return nil, false, nil
		}
		var req WidgetRenderRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, err
		}
		html, err := wp.RenderWidget(ctx, req)
		if err != nil {
			return nil, true, err
		}
		raw, err := json.Marshal(widgetRenderResponse{HTML: html})
		return raw, true, err

	case HookRenderAssets:
		ap, is := h.(AssetProvider)
		if !is {
			return nil, false, nil
		}
		var req RenderAssetsRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			return nil, true, err
		}
		raw, err := json.Marshal(renderAssetsResponse{FooterScripts: ap.FooterScripts(ctx, req)})
		return raw, true, err
	}
	return nil, false, nil
}

// withProvidedHooks adds the subscriptions the optional interfaces imply.
//
// Only `render.assets` needs this. The widget hooks are dispatched to every plugin holding the capability,
// so listing them would be noise; this one is a subscription Core reads from InitResult, and forgetting it
// produces a plugin that is wired, granted, running, and silent.
//
// Nothing is added for a plugin that already listed it, and an ungranted subscription is dropped by Core
// rather than refused — so this can never turn a working plugin into a failing one.
func withProvidedHooks(h Handler, hooks []string) []string {
	if _, is := h.(AssetProvider); !is {
		return hooks
	}
	for _, existing := range hooks {
		if existing == HookRenderAssets {
			return hooks
		}
	}
	return append(hooks, HookRenderAssets)
}
