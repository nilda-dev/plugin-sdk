package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// LinkTargetsHook asks a plugin where a site's links may lead into it ("Book", "Book — Dinner for 4").
//
// It is how a link picker, a menu editor and the site's assistant offer "Book" instead of asking a person
// to type a URL: the plugin answers a LIST of destinations, each with the path to write and the choices the
// person may make. Requires `route` — the paths are the plugin's own — and subscribing it is done for you
// when you implement LinkTargetProvider (Serve's withProvidedHooks).
const LinkTargetsHook = "link.targets"

// LinkParamSelect and LinkParamNumber are the two kinds of parameter a link target's path may carry — the
// only inputs the link picker knows how to draw. LinkParamNumber is bounded by Min/Max; LinkParamSelect
// draws the declared choices.
const (
	LinkParamSelect = "select"
	LinkParamNumber = "number"
)

// LinkTargetsRequest is what Core sends when it asks where a site's links may lead into this plugin.
type LinkTargetsRequest struct {
	// Locale is the language of the person picking the link, from the request that asked. Empty when Core
	// does not say; the labels you return may be translated through your own plugin.json translations.
	Locale string `json:"locale,omitempty"`
}

// LinkTarget is one destination links may lead to — "Book", "Book — Dinner for 4" — offered to every link
// picker and menu on the site, and to the site's assistant.
//
// What a person picks is stored as Path plus the chosen Params as a query string ("/booking/go?setup=dinner&party=4"),
// and that is all that is stored: a plain path on the site. A link to a plugin that is later removed is a
// link to a page that no longer exists — it 404s like any deleted page, and nothing has to be rewritten.
type LinkTarget struct {
	Key   string `json:"key"`             // stable within the plugin: "book"
	Label string `json:"label"`           // "Book"
	Group string `json:"group,omitempty"` // "Booking" — an optional heading in the picker
	Path  string `json:"path"`            // under the plugin's route prefix: "/booking/go"
	// Params are the optional inputs a person may set on this destination; at most 4.
	Params []LinkParam `json:"params,omitempty"`
}

// LinkParam is one input a LinkTarget offers — a setup to book, a party size to book for. At most 4 a
// target, and at most 100 choices in one param.
type LinkParam struct {
	Key     string       `json:"key"`   // the query key: "setup"
	Label   string       `json:"label"` // "What to book"
	Type    string       `json:"type"`  // LinkParamSelect "select" | LinkParamNumber "number"
	Choices []LinkChoice `json:"choices,omitempty"`
	Min     *float64     `json:"min,omitempty"`
	Max     *float64     `json:"max,omitempty"`
}

// LinkChoice is one option of a LinkParamSelect. An empty Value is a real choice — "Let the visitor choose",
// which writes no query key at all.
type LinkChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// LinkTargetProvider is implemented by a plugin whose pages the site's links may lead into. Requires the
// `route` capability: the paths are the plugin's own, under the prefix Core proxies to it.
//
// Implementing it subscribes LinkTargetsHook and answers it for you — see Serve.
type LinkTargetProvider interface {
	// LinkTargets returns where a site's links may lead into this plugin. Called when the page builder's
	// link picker or the assistant asks, so it is a menu, not a query: keep it cheap, and translate Labels
	// for req.Locale when you can.
	LinkTargets(ctx context.Context, req LinkTargetsRequest) ([]LinkTarget, error)
}

// linkTargetsResponse is the wire envelope. Unexported, like widgetDescribeResponse: an author who
// implements LinkTargetProvider never constructs one, and an author who answers the hook by hand is better
// served by the documented JSON than by a type that would then be part of the SDK's API.
type linkTargetsResponse struct {
	Targets []LinkTarget `json:"targets"`
}

// The bounds Core enforces on one target. Validate states them here as well so an author hears them at Init
// rather than through a target Core silently dropped — the same numbers, in the same words.
const (
	maxLinkTargets = 20  // targets in one answer
	maxLinkParams  = 4   // params in one target
	maxLinkChoices = 100 // choices in one param
)

// linkKeyPattern is how a target's key and its params' keys are spelled: lowercase, at least two
// characters, and stable — a key identifies a destination the site has stored.
var linkKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,30}$`)

// linkChoiceValuePattern is what a select choice's value may be: it is written into a stored link's query
// string, so it is a short token, never markup or a separator. Empty is allowed — "let the visitor choose".
var linkChoiceValuePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{0,64}$`)

// linkPathPattern is the characters a target's path may hold: letters, digits and `/ _ - . ~`. No `%` (so no
// `%2e%2e`), no quote, space or backslash — the path is written into an href.
var linkPathPattern = regexp.MustCompile(`^[A-Za-z0-9/_.~-]+$`)

// Validate reports whether Core would offer this target, under the rules it applies before it does: the key
// pattern (keys are trimmed first, as Core trims them), a non-empty label, the counts (at most 4 params, 100
// choices a param), no two params with one key, a number whose Min is not above its Max, a select with at
// least one choice whose values are only `[A-Za-z0-9_.-]` and at most 64 long (an empty value is allowed),
// and the path — at or under the plugin's route prefix, holding no `?`, `#`, `//` or `..`, and only the
// characters `[A-Za-z0-9/_.~-]` (no `%`, quote, space or backslash).
//
// Two rules span targets and so cannot be checked on one: at most 20 targets in an answer, and no two with
// the same key. DispatchLinkTargets holds them — it drops the extra target and logs it.
//
// Call it at Init, with the prefix Core gave you, so a mistake is a sentence in your own log instead of a
// target dropped from a site owner's picker:
//
//	func (b *Booking) Init(_ context.Context, core *nilda.Core) (nilda.InitResult, error) {
//		for _, t := range b.linkTargets() {
//			if err := t.Validate(core.RoutePrefix); err != nil {
//				return nilda.InitResult{}, err
//			}
//		}
//		return nilda.InitResult{}, nil
//	}
//
// prefix is the plugin's route prefix (Core.RoutePrefix), with or without its trailing slash. An EMPTY
// prefix checks every rule but that one: the SDK's own dispatch cannot know the prefix — Core sends it at
// Init, not with every call — and Core itself checks the prefix before it offers a target.
func (t LinkTarget) Validate(prefix string) error {
	key := strings.TrimSpace(t.Key)
	if !linkKeyPattern.MatchString(key) {
		return fmt.Errorf("nilda: link target key %q is not %s", key, linkKeyPattern)
	}
	if strings.TrimSpace(t.Label) == "" {
		return fmt.Errorf("nilda: link target %q: its label is empty", key)
	}
	if err := validateLinkPath(t.Path, prefix); err != nil {
		return fmt.Errorf("nilda: link target %q: %w", key, err)
	}
	if len(t.Params) > maxLinkParams {
		return fmt.Errorf("nilda: link target %q has %d params; the bound is %d",
			key, len(t.Params), maxLinkParams)
	}
	seen := make(map[string]bool, len(t.Params))
	for _, p := range t.Params {
		pk := strings.TrimSpace(p.Key)
		if !linkKeyPattern.MatchString(pk) {
			return fmt.Errorf("nilda: link target %q: param key %q is not %s", key, pk, linkKeyPattern)
		}
		if seen[pk] {
			return fmt.Errorf("nilda: link target %q: param key %q is duplicate", key, pk)
		}
		seen[pk] = true
		if err := validateLinkParam(p); err != nil {
			return fmt.Errorf("nilda: link target %q: param %q %w", key, pk, err)
		}
	}
	return nil
}

// validateLinkParam holds one param's type and its type's own rules; the key is the caller's.
func validateLinkParam(p LinkParam) error {
	switch p.Type {
	case LinkParamSelect:
		if len(p.Choices) == 0 {
			return fmt.Errorf("is a select with no choice; offer at least one")
		}
		if len(p.Choices) > maxLinkChoices {
			return fmt.Errorf("has %d choices; the bound is %d", len(p.Choices), maxLinkChoices)
		}
		for _, c := range p.Choices {
			if !linkChoiceValuePattern.MatchString(c.Value) {
				return fmt.Errorf("has a choice value %q that is not %s", c.Value, linkChoiceValuePattern)
			}
		}
	case LinkParamNumber:
		if len(p.Choices) > maxLinkChoices {
			return fmt.Errorf("has %d choices; the bound is %d", len(p.Choices), maxLinkChoices)
		}
		if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
			return fmt.Errorf("has a min (%v) above its max (%v)", *p.Min, *p.Max)
		}
	default:
		return fmt.Errorf("has type %q; it is %q or %q", p.Type, LinkParamSelect, LinkParamNumber)
	}
	return nil
}

// validateLinkPath holds one path to the rules that keep a plugin inside its own route prefix. An empty
// prefix skips the prefix comparison alone.
func validateLinkPath(path, prefix string) error {
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("its path %q is not root-relative", path)
	}
	if strings.Contains(path, "?") {
		return fmt.Errorf("its path %q carries a query; a param is how a path takes values", path)
	}
	if strings.Contains(path, "#") {
		return fmt.Errorf("its path %q carries a fragment", path)
	}
	if strings.Contains(path, "//") {
		return fmt.Errorf("its path %q carries a double slash", path)
	}
	if strings.Contains(path, "..") {
		return fmt.Errorf("its path %q carries a parent step", path)
	}
	if prefix = strings.TrimRight(prefix, "/"); prefix != "" && path != prefix && !strings.HasPrefix(path, prefix+"/") {
		return fmt.Errorf("its path %q is outside the plugin's route prefix %q", path, prefix)
	}
	if !linkPathPattern.MatchString(path) {
		return fmt.Errorf("its path %q holds a character outside %s", path, linkPathPattern)
	}
	return nil
}

// DispatchLinkTargets routes a link-targets question to your handler, the same shape DispatchAdminReport
// uses:
//
//	func (p *Plugin) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
//		if out, handled, err := nilda.DispatchLinkTargets(ctx, hook, payload, p.onLinkTargets); handled {
//			return out, err
//		}
//		// … your own hooks
//	}
//
//	func (p *Plugin) onLinkTargets(ctx context.Context, req nilda.LinkTargetsRequest) ([]nilda.LinkTarget, error) {
//		return p.linkTargets(req.Locale), nil
//	}
//
// handled=false means the hook was not a link-targets question, so a plugin with hooks of its own passes it
// on. A target Core would refuse is DROPPED and logged here rather than failing the call: one bad target
// must not take the whole menu away from the page builder. The check runs with an empty prefix — the SDK
// cannot know the plugin's route prefix on this path (Core sends only LinkTargetsRequest) — so the
// under-the-prefix rule is the one it leaves to Core, which drops such a target on its own side.
//
// You do not have to call this: implementing LinkTargetProvider subscribes the hook and answers it through
// dispatchProvided.
func DispatchLinkTargets(ctx context.Context, hook string, payload []byte,
	fn func(context.Context, LinkTargetsRequest) ([]LinkTarget, error)) (out []byte, handled bool, err error) {
	if hook != LinkTargetsHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: a link picker asked where this plugin's links lead and it registered no handler")
	}
	var req LinkTargetsRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: link.targets payload: %w", err)
	}
	targets, err := fn(ctx, req)
	if err != nil {
		return nil, true, err
	}
	kept := make([]LinkTarget, 0, len(targets)) // an empty answer is [], never null
	keys := make(map[string]bool, len(targets))
	for _, t := range targets {
		if len(kept) >= maxLinkTargets {
			Log().Warn("dropping link targets past the limit Core offers; fix it and they will be offered",
				"target", t.Key, "limit", maxLinkTargets)
			continue
		}
		if err := t.Validate(""); err != nil {
			Log().Warn("dropping a link target Core would refuse; fix it and it will be offered",
				"target", t.Key, "error", err)
			continue
		}
		if key := strings.TrimSpace(t.Key); keys[key] {
			Log().Warn("dropping a link target whose key an earlier target already has",
				"target", key)
			continue
		} else {
			keys[key] = true
		}
		kept = append(kept, t)
	}
	raw, err := json.Marshal(linkTargetsResponse{Targets: kept})
	return raw, true, err
}
