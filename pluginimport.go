package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// CORE'S DOORS FOR AN IMPORT PLUGIN (v0.71.0; core internal/pluginimport, REPLAN-plugin G4–G6, S4 and S5). A plugin holding the
// `import` capability (its token's manage:import scope) reaches exactly these, and no general import or settings power:
//
//   - LookupImported — POST /plugin-import/map/lookup: what an earlier import of the site made of the old site's things (a page,
//     a term, a file, a menu), by the source and the ids that import remembered them by;
//   - AddImportRedirects / RemoveImportRedirects — POST /plugin-import/redirects and /redirects/remove: old addresses leading to
//     this site's pages (301), the same rows Core's own import writes, taken back by the id each was written with while it is
//     still exactly that rule;
//   - ImportCheckpoint — POST /plugin-import/checkpoint: the site's safety copy before an import writes; an error means none was
//     made, and the import must not start.
//
// Core refuses a plugin without `import` 403. Each call carries 1 to MaxImportBatch ids or rules; an answer has one result per
// one asked, in order, which the client checks.

// MaxImportBatch is how many ids one lookup, or rules one redirect call, carries (Core's MaxLookup).
const MaxImportBatch = 200

// MaxImportReason is the longest reason a safety copy names, in characters.
const MaxImportReason = 200

// ImportFound is what an earlier import made of one id: Kind is content, term, media, menu or menu_item, TargetID Core's id;
// Found false when the import made nothing Core tells a plugin about.
type ImportFound struct {
	ExternalID string `json:"external_id"`
	Found      bool   `json:"found"`
	Kind       string `json:"kind,omitempty"`
	TargetID   string `json:"target_id,omitempty"`
}

// ImportRedirect is one old address and the page of this site it now leads to; both are paths on this site ("/shop/apron/").
type ImportRedirect struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ImportRedirectResult is one rule's outcome: Written (with the ID that takes it back), or left as it was — already there, or
// an address this site serves — or Refused: from_not_local, to_not_local.
type ImportRedirectResult struct {
	ID      string `json:"id,omitempty"`
	Written bool   `json:"written"`
	Refused string `json:"refused,omitempty"`
}

// ImportRedirectRemoval names a rule AddImportRedirects wrote, to be taken back while it is still exactly that rule.
type ImportRedirectRemoval struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
}

// ImportSafetyCopy is the safety copy Core made (Reused: one made minutes ago answered).
type ImportSafetyCopy struct {
	ID     string `json:"id"`
	Reused bool   `json:"reused"`
}

// ErrImportAnswer is a door's answer that does not match what was asked: another count of results, a copy with no id.
var ErrImportAnswer = errors.New("nilda: Core's import door answered something other than what was asked")

// LookupImported asks what an earlier import of source made of each external id (1 to MaxImportBatch), in their order.
func (a *API) LookupImported(ctx context.Context, source string, externalIDs []string) ([]ImportFound, error) {
	if a == nil {
		return nil, errNoAPI
	}
	if s := strings.TrimSpace(source); s == "" || len(s) > 200 || len(externalIDs) == 0 || len(externalIDs) > MaxImportBatch {
		return nil, fmt.Errorf("nilda: a lookup names a source and 1 to %d ids", MaxImportBatch)
	}
	var out struct {
		Data []ImportFound `json:"data"`
	}
	if err := a.Post(ctx, "/plugin-import/map/lookup", map[string]any{"source": source, "external_ids": externalIDs}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(externalIDs) {
		return nil, ErrImportAnswer
	}
	for i, f := range out.Data {
		if f.ExternalID != externalIDs[i] || f.Found && f.TargetID == "" {
			return nil, ErrImportAnswer
		}
	}
	return out.Data, nil
}

// AddImportRedirects writes old addresses as 301s to this site's pages (1 to MaxImportBatch rules), one result per rule, in order.
func (a *API) AddImportRedirects(ctx context.Context, rules []ImportRedirect) ([]ImportRedirectResult, error) {
	if a == nil {
		return nil, errNoAPI
	}
	if len(rules) == 0 || len(rules) > MaxImportBatch {
		return nil, fmt.Errorf("nilda: a redirect call carries 1 to %d rules", MaxImportBatch)
	}
	var out struct {
		Data []ImportRedirectResult `json:"data"`
	}
	if err := a.Post(ctx, "/plugin-import/redirects", map[string]any{"rules": rules}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(rules) {
		return nil, ErrImportAnswer
	}
	for _, r := range out.Data {
		if r.Written && r.ID == "" {
			return nil, ErrImportAnswer
		}
	}
	return out.Data, nil
}

// RemoveImportRedirects takes back rules AddImportRedirects wrote (1 to MaxImportBatch): whether each went, in order. A rule
// changed since, or gone, stays as it is and answers false.
func (a *API) RemoveImportRedirects(ctx context.Context, rules []ImportRedirectRemoval) ([]bool, error) {
	if a == nil {
		return nil, errNoAPI
	}
	if len(rules) == 0 || len(rules) > MaxImportBatch {
		return nil, fmt.Errorf("nilda: a redirect call carries 1 to %d rules", MaxImportBatch)
	}
	var out struct {
		Data []struct {
			Removed bool `json:"removed"`
		} `json:"data"`
	}
	if err := a.Post(ctx, "/plugin-import/redirects/remove", map[string]any{"rules": rules}, &out); err != nil {
		return nil, err
	}
	if len(out.Data) != len(rules) {
		return nil, ErrImportAnswer
	}
	gone := make([]bool, len(out.Data))
	for i, r := range out.Data {
		gone[i] = r.Removed
	}
	return gone, nil
}

// ImportCheckpoint asks Core for the site's safety copy before an import writes, naming why (1 to MaxImportReason
// characters). An error means no copy was made: do not start.
func (a *API) ImportCheckpoint(ctx context.Context, reason string) (ImportSafetyCopy, error) {
	if a == nil {
		return ImportSafetyCopy{}, errNoAPI
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > MaxImportReason {
		return ImportSafetyCopy{}, fmt.Errorf("nilda: a safety copy names why it is made, in 1 to %d characters", MaxImportReason)
	}
	var out struct {
		Data ImportSafetyCopy `json:"data"`
	}
	if err := a.Post(ctx, "/plugin-import/checkpoint", map[string]string{"reason": reason}, &out); err != nil {
		return ImportSafetyCopy{}, err
	}
	if out.Data.ID == "" {
		return ImportSafetyCopy{}, ErrImportAnswer
	}
	return out.Data, nil
}
