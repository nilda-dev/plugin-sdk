package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// A PLUGIN THAT NEEDS SETTING UP, AND WHAT GOES WELL WITH IT (v0.42.0).
//
// Two manifest keys and one hook, so that installing a plugin does not end on a toast and an empty screen:
//
//   - `setup_page`: the key of one of your declared REPORT pages — the checklist of what is left to do. Core opens
//     it after the plugin is installed or switched on, and Core's own onboarding list shows "Finish setting up
//     <plugin> (x of y)" from your SetupStatusHook answer until it says Completed. An older Core ignores the key.
//   - `recommends`: at most MaxRecommendations Recommendation entries, each naming a capability ("payment_gateway")
//     or a service contract ("shipping.carrier/v1") another plugin would bring. Core shows them as "Goes well with"
//     under the install result and on your row in Plugins, with the marketplace's plugins for each and an Install
//     button — and hides one once a plugin on the site satisfies it. Advice, never a requirement: Debian's
//     "Recommends", not "Depends" (docs/SERVICES.md).

// SetupStatusHook is the hook Core calls for how far a plugin's setup is: the onboarding row's "x of y". Subscribe
// it like any hook; the grant is `admin_page`, which a setup page already needs.
const SetupStatusHook = "setup.status"

// MaxSetupSteps bounds Total: a checklist longer than this is not a setup, it is a manual.
const MaxSetupSteps = 50

// SetupStatus is how far setup is. Completed is what removes the onboarding row; every step Done is Completed.
type SetupStatus struct {
	Done      int  `json:"done"`
	Total     int  `json:"total"`
	Completed bool `json:"completed"`
}

// Validate refuses an answer that cannot be true.
func (s SetupStatus) Validate() error {
	switch {
	case s.Done < 0 || s.Total < 0:
		return fmt.Errorf("nilda: setup.status cannot count below zero (%d of %d)", s.Done, s.Total)
	case s.Total > MaxSetupSteps:
		return fmt.Errorf("nilda: setup.status counts %d steps; at most %d", s.Total, MaxSetupSteps)
	case s.Done > s.Total:
		return fmt.Errorf("nilda: setup.status says %d of %d steps are done", s.Done, s.Total)
	case s.Completed && s.Done < s.Total:
		return fmt.Errorf("nilda: setup.status says completed with %d of %d steps done", s.Done, s.Total)
	}
	return nil
}

// DispatchSetupStatus routes a setup.status call to fn and shapes the answer, the same way DispatchAdminReport
// does; handled=false means the hook was another one. Every step done is answered Completed.
func DispatchSetupStatus(ctx context.Context, hook string, _ []byte, fn func(context.Context) (SetupStatus, error)) (out []byte, handled bool, err error) {
	if hook != SetupStatusHook {
		return nil, false, nil
	}
	if fn == nil {
		return nil, true, fmt.Errorf("nilda: setup.status was asked and this plugin registered no handler")
	}
	s, err := fn(ctx)
	if err != nil {
		return nil, true, err
	}
	if s.Total > 0 && s.Done == s.Total {
		s.Completed = true
	}
	if err := s.Validate(); err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(s)
	return b, true, err
}

// MaxRecommendations bounds a manifest's `recommends`; MaxRecommendationWhy bounds each one's reason.
const (
	MaxRecommendations   = 8
	MaxRecommendationWhy = 200
)

// Recommendation is one entry of a manifest's `recommends`: exactly one of Capability and Contract, and Why — the
// line the owner reads ("Take card payments").
type Recommendation struct {
	Capability string `json:"capability,omitempty"`
	Contract   string `json:"contract,omitempty"`
	Why        string `json:"why,omitempty"`
}

var capabilityRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Target is the capability or the contract the recommendation names — what a ReportLink's Install names.
func (r Recommendation) Target() string {
	if r.Capability != "" {
		return r.Capability
	}
	return r.Contract
}

// ValidateRecommendations checks a manifest's `recommends` as Core does at install: the bound, one target each,
// a target's shape, and no target twice. Whether the capability is one Core knows, and that the plugin does not
// satisfy it itself, Core checks against its catalogue.
func ValidateRecommendations(list []Recommendation) error {
	if len(list) > MaxRecommendations {
		return fmt.Errorf("nilda: recommends lists %d entries; at most %d", len(list), MaxRecommendations)
	}
	seen := map[string]bool{}
	for i, r := range list {
		if (r.Capability == "") == (r.Contract == "") {
			return fmt.Errorf("nilda: recommends[%d] names exactly one of capability and contract", i)
		}
		if r.Capability != "" && !capabilityRe.MatchString(r.Capability) {
			return fmt.Errorf("nilda: recommends[%d]: %q is not a capability", i, r.Capability)
		}
		if r.Contract != "" && !contractNameRe.MatchString(r.Contract) {
			return fmt.Errorf("nilda: recommends[%d]: %q is not a contract with its major, such as shipping.carrier/v1", i, r.Contract)
		}
		if len([]rune(r.Why)) > MaxRecommendationWhy {
			return fmt.Errorf("nilda: recommends[%d]'s why is longer than %d characters", i, MaxRecommendationWhy)
		}
		if seen[r.Target()] {
			return fmt.Errorf("nilda: recommends names %q twice", r.Target())
		}
		seen[r.Target()] = true
	}
	return nil
}
