package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// widget.check: a widget provider looks at one placed widget's settings and answers warnings for the owner
// (v0.73.0). Optional — a plugin that does not implement WidgetChecker is never asked.

// HookWidgetCheck asks a plugin to look at one placed widget's settings and answer warnings for the owner. Core asks
// it of a plugin that subscribed to it, under the `widget` grant; Serve subscribes it for a WidgetProvider that
// also implements WidgetChecker.
const HookWidgetCheck = "widget.check"

// Bounds on a widget.check answer.
const (
	// MaxWidgetCheckWarnings bounds one widget.check answer: a count.
	MaxWidgetCheckWarnings = 50
	// MaxWidgetCheckMessageLen bounds a warning's message: characters.
	MaxWidgetCheckMessageLen = 300
	// MaxWidgetCheckPathLen bounds a warning's path: characters.
	MaxWidgetCheckPathLen = 200
)

// widgetCheckCode is a warning's code: a lowercase identifier a caller can match on.
var widgetCheckCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// WidgetCheckRequest is what Core sends with widget.check: one placed widget of yours and its settings, as the page
// builder stores them. Like a render it is PAGE-level — no user, no session.
type WidgetCheckRequest struct {
	Type   string         `json:"type"` // the local name, namespace already stripped
	Config map[string]any `json:"config,omitempty"`
	Locale string         `json:"locale,omitempty"` // the language to write the messages in
}

// WidgetWarning is one thing the owner should know about a placed widget's settings. Core shows it where it
// checks a page (widget_set, layout_check, publish_check); a warning never stops a save or a publish.
type WidgetWarning struct {
	// Path names the setting: a field's key, a repeater row's as `rows[2].title`; empty for the widget as a whole.
	// Plain text, at most MaxWidgetCheckPathLen characters, with no control or bidi character.
	Path string `json:"path,omitempty"`
	// Code is a lowercase identifier (`^[a-z][a-z0-9_]{0,63}$`) a caller can match on, e.g. "too_long".
	Code string `json:"code"`
	// Message is the sentence the owner reads: PLAIN TEXT (never markup — Core escapes it), not empty, at most
	// MaxWidgetCheckMessageLen characters, with no control or bidi character.
	Message string `json:"message"`
}

// WidgetChecker is implemented by a WidgetProvider that can look at its placed widgets' settings. Optional: Serve
// subscribes widget.check for a WidgetProvider that implements it and answers it; a plugin that does not is never
// asked. An error, or an answer ValidateWidgetWarnings refuses, reaches Core as a failed check, not as warnings.
type WidgetChecker interface {
	CheckWidget(ctx context.Context, req WidgetCheckRequest) ([]WidgetWarning, error)
}

// ValidateWidgetWarnings reports whether Core accepts a widget.check answer: at most MaxWidgetCheckWarnings
// warnings, each with a code matching `^[a-z][a-z0-9_]{0,63}$`, a message of 1 to MaxWidgetCheckMessageLen
// characters and a path of at most MaxWidgetCheckPathLen, both plain text — no control character (a newline, a tab,
// an escape) and no bidi embedding, override or isolate (U+202A–U+202E, U+2066–U+2069). Core treats both as text,
// escapes them where it shows them, and runs this check again on its side: a plugin is not trusted with markup.
func ValidateWidgetWarnings(ws []WidgetWarning) error {
	if len(ws) > MaxWidgetCheckWarnings {
		return fmt.Errorf("nilda: widget.check answered %d warnings; the bound is %d", len(ws), MaxWidgetCheckWarnings)
	}
	for i, w := range ws {
		switch n := utf8.RuneCountInString(w.Message); {
		case n == 0:
			return fmt.Errorf("nilda: widget.check warning %d has no message", i)
		case n > MaxWidgetCheckMessageLen:
			return fmt.Errorf("nilda: widget.check warning %d: its message is %d runes; the bound is %d", i, n, MaxWidgetCheckMessageLen)
		}
		if n := utf8.RuneCountInString(w.Path); n > MaxWidgetCheckPathLen {
			return fmt.Errorf("nilda: widget.check warning %d: its path is %d characters; the bound is %d", i, n, MaxWidgetCheckPathLen)
		}
		// Plain text an owner reads: no control character and no bidi embedding, override or isolate (plainLine,
		// Core's rule). Core escapes it and re-validates; this is where the plugin's author hears it first.
		if !plainLine(w.Message) {
			return fmt.Errorf("nilda: widget.check warning %d: its message holds a control or bidi character", i)
		}
		if !plainLine(w.Path) {
			return fmt.Errorf("nilda: widget.check warning %d: its path holds a control or bidi character", i)
		}
		if !widgetCheckCode.MatchString(w.Code) {
			return fmt.Errorf("nilda: widget.check warning %d: its code %q is not %s", i, w.Code, widgetCheckCode)
		}
	}
	return nil
}

type widgetCheckResponse struct {
	Warnings []WidgetWarning `json:"warnings"`
}

// dispatchWidgetCheck answers widget.check for a WidgetProvider that implements WidgetChecker.
func dispatchWidgetCheck(ctx context.Context, h Handler, payload []byte) ([]byte, bool, error) {
	_, wp := h.(WidgetProvider)
	wc, is := h.(WidgetChecker)
	if !wp || !is {
		return nil, false, nil
	}
	var req WidgetCheckRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: widget.check payload: %w", err)
	}
	ws, err := wc.CheckWidget(ctx, req)
	if err != nil {
		return nil, true, err
	}
	if err := ValidateWidgetWarnings(ws); err != nil {
		return nil, true, err
	}
	if ws == nil {
		ws = []WidgetWarning{}
	}
	raw, err := json.Marshal(widgetCheckResponse{Warnings: ws})
	return raw, true, err
}
