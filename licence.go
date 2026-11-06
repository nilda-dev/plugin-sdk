package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// A PLUGIN'S OWN LICENCE — the paid edition of a plugin of Nilda's own (a shop's live checkout), as the site holds it.
//
// EXPERIMENTAL until the shop's live gate ships green on it: the names may still change.
//
// It reaches the site only inside the site's signed licence, under the plugin's slug (its key with `_` written `-`);
// Core judges it — signature, domain, revocation, the clock, the dates — and answers the plugin its own entry, by the
// key in the plugin's token, never one named in the request (Core's internal/pluginlimits/licence.go and
// internal/license/pluginlicence.go; SPEC_99 §6.2). No capability is needed: reading what the site holds for you asks
// nothing of the site.

// LicenceState is what a plugin's licence is now, as Core judges it.
type LicenceState string

const (
	// LicenceNone: the site holds no licence for this plugin — or its licence cannot be believed (unsigned, revoked,
	// another domain, a clock set back, unvalidated past the offline grace), or Core is older than the route.
	LicenceNone LicenceState = "none"
	// LicenceTrial: a go-live trial, before its end.
	LicenceTrial LicenceState = "trial"
	// LicenceActive: paid, before PaidUntil (or with no end).
	LicenceActive LicenceState = "active"
	// LicenceGrace: past PaidUntil and before LiveUntil — still live, while the owner renews.
	LicenceGrace LicenceState = "grace"
	// LicenceLapsed: from LiveUntil on.
	LicenceLapsed LicenceState = "lapsed"
)

// LicenceStates is every state Core answers, in Core's order.
func LicenceStates() []LicenceState {
	return []LicenceState{LicenceNone, LicenceTrial, LicenceActive, LicenceGrace, LicenceLapsed}
}

// Licence is a plugin's own licence as Core judges it now.
type Licence struct {
	State LicenceState
	// Source is where it came from: "plan" (a Nilda Cloud plan that includes the plugin), "purchase" or "trial".
	Source string
	// PaidUntil is the licence's own end; LiveUntil is PaidUntil plus its grace. The zero time is "no end".
	PaidUntil time.Time
	LiveUntil time.Time
	// Trial is set on a go-live trial.
	Trial bool
	// Quantity is how many were bought, for an edition sold per location; 0 when it is not.
	Quantity int32
}

// TakesRealMoney reports whether the plugin may run its paid edition — for a shop, take real money: trial, active
// or grace. None, lapsed and any state this release does not know do not.
func (l Licence) TakesRealMoney() bool {
	switch l.State {
	case LicenceTrial, LicenceActive, LicenceGrace:
		return true
	}
	return false
}

// licenceWire is a licence as Core writes it (license.PluginLicenceStanding): times in RFC 3339, left out when there
// is no end.
type licenceWire struct {
	State     LicenceState `json:"state"`
	Source    string       `json:"source,omitempty"`
	PaidUntil *time.Time   `json:"paid_until,omitempty"`
	LiveUntil *time.Time   `json:"live_until,omitempty"`
	Trial     bool         `json:"trial,omitempty"`
	Quantity  int32        `json:"quantity,omitempty"`
}

// MarshalJSON writes the licence in Core's shape: a time with no end is left out, never written as year one.
func (l Licence) MarshalJSON() ([]byte, error) {
	w := licenceWire{State: l.State, Source: l.Source, Trial: l.Trial, Quantity: l.Quantity}
	if !l.PaidUntil.IsZero() {
		t := l.PaidUntil
		w.PaidUntil = &t
	}
	if !l.LiveUntil.IsZero() {
		t := l.LiveUntil
		w.LiveUntil = &t
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads Core's shape. An answer naming no state is none.
func (l *Licence) UnmarshalJSON(raw []byte) error {
	var w licenceWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return err
	}
	*l = Licence{State: w.State, Source: w.Source, Trial: w.Trial, Quantity: w.Quantity}
	if l.State == "" {
		l.State = LicenceNone
	}
	if w.PaidUntil != nil {
		l.PaidUntil = *w.PaidUntil
	}
	if w.LiveUntil != nil {
		l.LiveUntil = *w.LiveUntil
	}
	return nil
}

// The routes, under Core's REST root (internal/pluginlimits/rest.go).
const (
	licencePath     = "/plugin-licence"
	licenceBindPath = "/plugin-licence/bind"
)

// Licence reads this plugin's own licence.
//
// A Core older than the route answers 404, read as LicenceNone with no error: a plugin built on this release runs
// unchanged there and simply holds no licence. ANY OTHER ERROR answers LicenceNone and the error — a licence that
// could not be read is never read as licensed. Nil-safe: a plugin with no API gets none and an error.
func (a *API) Licence(ctx context.Context) (Licence, error) {
	var out struct {
		Data Licence `json:"data"`
	}
	if err := a.Get(ctx, licencePath, nil, &out); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.NotFound() {
			return Licence{State: LicenceNone}, nil
		}
		return Licence{State: LicenceNone}, err
	}
	if out.Data.State == "" {
		out.Data.State = LicenceNone
	}
	return out.Data, nil
}

// Why a bind bound nothing (Core's connect.BindReason*; Central's answers passed through).
const (
	// BindReasonNoneFree: every licence of the edition the account bought is held by another of its sites (the owner
	// frees one on nilda.dev), or none was bought.
	BindReasonNoneFree = "none_free"
	// BindReasonNotConnected: the site is not connected to a nilda.dev account, so there is no account to ask.
	BindReasonNotConnected = "not_connected"
	// BindReasonHosted: a Nilda Cloud site — its plugin licences arrive with its own licence; nothing is asked.
	BindReasonHosted = "hosted"
	// BindReasonNoSiteLicenceLeft: every Nilda Plus licence of the account is held by its other sites, so this site has
	// no licence to carry the plugin's in.
	BindReasonNoSiteLicenceLeft = "no_site_licence_left"
	// BindReasonAddressNotActivatable: the site's address is a development or private one and cannot hold a licence.
	BindReasonAddressNotActivatable = "address_not_activatable"
	// BindReasonAccountBlocked: the account is blocked and its sites run free.
	BindReasonAccountBlocked = "account_blocked"
)

// BindReasons is every reason Core answers a bind with.
func BindReasons() []string {
	return []string{BindReasonNoneFree, BindReasonNotConnected, BindReasonHosted, BindReasonNoSiteLicenceLeft,
		BindReasonAddressNotActivatable, BindReasonAccountBlocked}
}

// BindResult is Core's answer to BindLicence: whether the site holds the plugin's licence now, why not when it does
// not (one of BindReasons, set exactly when Bound is false), and the licence as the plugin reads it from here on.
type BindResult struct {
	Bound   bool    `json:"bound"`
	Reason  string  `json:"reason,omitempty"`
	Licence Licence `json:"licence"`
}

// BindLicence asks the site to bind the account's licence of this plugin's paid edition to this install — what a
// SELF-HOSTED shop does when its owner presses "go live": Core asks nilda.dev and installs the licence it answers.
// Asking again from the site that holds it changes nothing. Core audits every bind.
//
// An error is a call that did not complete — Central unreachable, a Core older than the route (404) — and is returned
// with Bound false, no reason and LicenceNone: never read as bound. Nil-safe.
func (a *API) BindLicence(ctx context.Context) (BindResult, error) {
	var out struct {
		Data BindResult `json:"data"`
	}
	if err := a.Post(ctx, licenceBindPath, nil, &out); err != nil {
		return BindResult{Licence: Licence{State: LicenceNone}}, err
	}
	if out.Data.Licence.State == "" {
		out.Data.Licence.State = LicenceNone
	}
	return out.Data, nil
}
