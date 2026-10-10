package nilda

import (
	"context"
	"errors"
	"fmt"
	"regexp"
)

// PlanLimit reads one of this plugin's own limits from the site's plan: a number Nilda Cloud's Plans screen sets per
// plan for a plugin of Nilda's own — a shop's products, a calendar's resources — that the plugin then enforces.
//
// The plugin declares each limit in plugin.json (`limits`: name, label, unit — "count" or "words"); the operator sets
// it per plan under "<plugin key>.<name>"; it reaches the site in the licence and is answered here. Core takes the
// plugin's key from its token, never from the request, so no call can read another plugin's limit.
//
// limited is false when the site's plan sets no such limit: NO LIMIT — a self-hosted site, a plan that does not
// set it, a Core older than v0.11.0's (which answers the route 404, read here as no limit, so a plugin built on this
// release runs unchanged on an older Core). No capability is needed: reading what the plan allows asks nothing of
// the site.
func (a *API) PlanLimit(ctx context.Context, name string) (limit int64, limited bool, err error) {
	if !planLimitName.MatchString(name) {
		return 0, false, fmt.Errorf("nilda: a limit's name is a lowercase letter, then up to 47 lowercase letters, "+
			"digits or underscores, got %q", name)
	}
	var out struct {
		Data struct {
			Limited bool  `json:"limited"`
			Limit   int64 `json:"limit"`
		} `json:"data"`
	}
	if err := a.Get(ctx, "/plugin-limits/"+name, nil, &out); err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.NotFound() {
			return 0, false, nil
		}
		return 0, false, err
	}
	return out.Data.Limit, out.Data.Limited, nil
}

// planLimitName is a declared limit's name: Core's and Central's rule.
var planLimitName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
