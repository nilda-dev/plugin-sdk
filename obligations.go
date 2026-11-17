package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// LIVE OBLIGATIONS, AND WINDING THEM DOWN (door D11, EXPERIMENTAL).
//
// A plugin can hold promises to people other than the owner: subscriptions that renew, points a buyer has earned,
// listings live on a marketplace, slots somebody booked. Switching such a plugin off, or uninstalling it, breaks those
// promises silently — the renewal just never comes. So Core asks first:
//
//   - ObligationsListHook: "what do you hold right now?" Answer every kind you hold at least one of, counted, in a
//     sentence a person reads, and what winding it down does. Core shows it on the Plugins screen before a switch-off
//     or an uninstall, and to the assistant (plugin_obligations). An empty list means you hold none — never "I could
//     not count": that is an error, and Core says the plugin did not answer.
//   - ObligationsWindDownHook: "settle them". Stop what would happen next (no renewal is charged, no points are
//     earned) and tell the people it concerns, through Core's mail door — a buyer learns their subscription ends,
//     before it would have renewed. Answer what is still live once you are done: Remaining empty means nothing is
//     left. Called again, it must change nothing more and send nobody a second mail. Core calls it before an
//     uninstall (always, when you answered any obligation) and before a switch-off the owner asked to wind down —
//     never for a plain switch-off, which is the owner's kill switch and may be undone a minute later.
//
// ON ANOTHER PLUGIN'S BEHALF (For). A plugin that provides a service contract may hold obligations for the plugins
// that use it — the shop holds the renewal orders a subscriptions plugin asked for, the store credit a loyalty plugin
// issued. When such a plugin is switched off or uninstalled, Core asks its providers too, with For naming it: answer
// only what you hold on its behalf, and wind down only that. The leaving plugin may be dead or already switched off;
// what its providers hold is still named and still settled. For "" is the plugin itself.
//
// Both are admitted by `datastore` — a plugin that keeps records of its own, durably; one that stores nothing has
// nothing it could owe — never by `hooks`. Core sends nothing from the site, and you answer only about your own
// records. Keep within the budget (15 seconds): queue the mails, do not wait for them. Before an uninstall
// Core also offers the owner the export of your stored data (your tables, as JSON), so the records stay theirs.
//
// Implement ObligationsHolder on your Handler: Serve subscribes both hooks and answers them, checking your answer before
// Core does. A HandleHook that routes by hand calls DispatchObligations.

// The two hooks Core calls about a plugin's live obligations.
const (
	ObligationsListHook     = "obligations.list"
	ObligationsWindDownHook = "obligations.wind_down"
)

// Why Core asks for a wind-down (WindDownRequest.Reason).
const (
	WindDownUninstall = "uninstall" // the owner is removing the plugin
	WindDownDisable   = "disable"   // the owner is switching it off and asked for its obligations to be settled
)

// The shapes Core accepts. A kind is your short name for a sort of obligation ("active_subscriptions"); a summary and a
// wind-down are one sentence each, in the site's language (ObligationsRequest.Locale).
const (
	MaxObligations     = 32
	MaxObligationKind  = 40
	MaxObligationText  = 300
	maxObligationCount = 1 << 53 // what a JSON number carries exactly
)

var obligationKindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// ObligationsRequest is what both hooks carry: the language the owner reads the answer in.
type ObligationsRequest struct {
	Locale string `json:"locale,omitempty"`
	// For is the key of the plugin being switched off or uninstalled when you are asked as one of its providers (a
	// contract it uses, you provide); "" when it is you.
	For string `json:"for,omitempty"`
}

// WindDownRequest is what ObligationsWindDownHook carries.
type WindDownRequest struct {
	ObligationsRequest
	// Reason is WindDownUninstall or WindDownDisable.
	Reason string `json:"reason"`
}

// Obligation is one sort of live promise the plugin holds.
type Obligation struct {
	// Kind is your name for it: lowercase letters, digits and underscores, a letter first, at most 40
	// ("active_subscriptions", "points_balances").
	Kind string `json:"kind"`
	// Count is how many are live: at least 1 — a kind you hold none of is left out.
	Count int64 `json:"count"`
	// Summary says what they are, for a person: "12 subscriptions renew monthly; the next renews on 3 December".
	Summary string `json:"summary"`
	// WindDown says what winding them down does: "renewals stop; each subscriber is mailed that it ends".
	WindDown string `json:"wind_down"`
	// AmountMinor and Currency are money the obligations stand for, when they do — points' value, credit owed — in the
	// currency's smallest unit. Both or neither.
	AmountMinor int64  `json:"amount_minor,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// Validate reports the first thing Core would refuse about the obligation.
func (o Obligation) Validate() error {
	switch {
	case len(o.Kind) == 0 || len(o.Kind) > MaxObligationKind || !obligationKindRe.MatchString(o.Kind):
		return fmt.Errorf("nilda: an obligation's kind is lowercase letters, digits and _, a letter first, at most %d; got %q",
			MaxObligationKind, o.Kind)
	case o.Count < 1 || o.Count > maxObligationCount:
		return fmt.Errorf("nilda: obligation %q counts at least 1 (leave out a kind you hold none of)", o.Kind)
	case !obligationText(o.Summary):
		return fmt.Errorf("nilda: obligation %q needs a summary of 1-%d characters", o.Kind, MaxObligationText)
	case !obligationText(o.WindDown):
		return fmt.Errorf("nilda: obligation %q needs a wind_down of 1-%d characters: what winding it down does",
			o.Kind, MaxObligationText)
	case (o.AmountMinor != 0) != (o.Currency != ""):
		return fmt.Errorf("nilda: obligation %q names an amount and its currency together, or neither", o.Kind)
	case o.AmountMinor < 0:
		return fmt.Errorf("nilda: obligation %q has a negative amount", o.Kind)
	case o.Currency != "" && !currencyCodeRe.MatchString(o.Currency):
		return fmt.Errorf("nilda: obligation %q: currency %q is not an ISO 4217 code", o.Kind, o.Currency)
	}
	return nil
}

var currencyCodeRe = regexp.MustCompile(`^[A-Z]{3}$`)

func obligationText(s string) bool {
	t := strings.TrimSpace(s)
	return t != "" && utf8.RuneCountInString(t) <= MaxObligationText && !strings.ContainsRune(t, 0)
}

// ObligationsAnswer is what ObligationsListHook answers.
type ObligationsAnswer struct {
	Obligations []Obligation `json:"obligations"`
}

// WindDownResult is what ObligationsWindDownHook answers.
type WindDownResult struct {
	// Remaining is what is still live after the wind-down — a renewal already at the processor, a booking for
	// tomorrow — each saying why in its Summary. Empty: nothing is left.
	Remaining []Obligation `json:"remaining"`
	// Told is how many people were (or are queued to be) told; 0 when nobody had to be.
	Told int64 `json:"told,omitempty"`
}

func validObligations(list []Obligation) error {
	if len(list) > MaxObligations {
		return fmt.Errorf("nilda: at most %d kinds of obligation, got %d", MaxObligations, len(list))
	}
	seen := map[string]bool{}
	for _, o := range list {
		if err := o.Validate(); err != nil {
			return err
		}
		if seen[o.Kind] {
			return fmt.Errorf("nilda: obligation kind %q is listed twice", o.Kind)
		}
		seen[o.Kind] = true
	}
	return nil
}

// Validate reports the first thing Core would refuse about the answer.
func (a ObligationsAnswer) Validate() error { return validObligations(a.Obligations) }

// Validate reports the first thing Core would refuse about the result.
func (r WindDownResult) Validate() error {
	if r.Told < 0 {
		return fmt.Errorf("nilda: a wind-down told a negative number of people")
	}
	return validObligations(r.Remaining)
}

// ObligationsHolder is a plugin that holds live obligations.
type ObligationsHolder interface {
	// Obligations lists what is live right now.
	Obligations(ctx context.Context, req ObligationsRequest) ([]Obligation, error)
	// WindDown settles them, and says what is still live. Called again, it changes nothing more.
	WindDown(ctx context.Context, req WindDownRequest) (WindDownResult, error)
}

// DispatchObligations answers one obligations hook from h, checking the answer before Core does. handled is false for
// any other hook.
func DispatchObligations(ctx context.Context, h ObligationsHolder, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case ObligationsListHook:
		var req ObligationsRequest
		if err := unmarshalObligations(hook, payload, &req); err != nil {
			return nil, true, err
		}
		list, err := h.Obligations(ctx, req)
		if err != nil {
			return nil, true, err
		}
		ans := ObligationsAnswer{Obligations: list}
		if ans.Obligations == nil {
			ans.Obligations = []Obligation{}
		}
		if err := ans.Validate(); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(ans)
		return b, true, err
	case ObligationsWindDownHook:
		var req WindDownRequest
		if err := unmarshalObligations(hook, payload, &req); err != nil {
			return nil, true, err
		}
		if req.Reason != WindDownUninstall && req.Reason != WindDownDisable {
			return nil, true, fmt.Errorf("nilda: %s: reason %q is not %q or %q", hook, req.Reason, WindDownUninstall, WindDownDisable)
		}
		res, err := h.WindDown(ctx, req)
		if err != nil {
			return nil, true, err
		}
		if res.Remaining == nil {
			res.Remaining = []Obligation{}
		}
		if err := res.Validate(); err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err
	}
	return nil, false, nil
}

func unmarshalObligations(hook string, payload []byte, v any) error {
	if len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return fmt.Errorf("nilda: %s payload: %w", hook, err)
	}
	return nil
}
