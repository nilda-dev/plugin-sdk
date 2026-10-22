package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ANSWERING THE SITE'S PRIVACY REQUESTS ABOUT THE PEOPLE IN YOUR OWN TABLES (Core D-103; docs/PLUGIN_SDK.md,
// "Answering privacy requests").
//
// The site owner is the controller of everything your plugin keeps about a person — a guest's bookings, a waiting
// list, a blocklist — and Nilda is their processor (GDPR Art. 28(3)(e): help them answer a person's requests). Core's
// Privacy screen hands a person their data (Art. 15, 20) and erases it (Art. 17) for everything Core keeps; it cannot
// read your tables, because it cannot know which column is a person. So, granted `personal_data`, you answer two
// hooks Core sends itself:
//
//	privacy.export  your records for the addresses in the request, each labelled with the address it matched
//	privacy.erase   erase or anonymise them, and say what you KEPT (facts the law makes you keep, anonymised) and what
//	                you HELD (records an open dispute keeps whole), each with its basis
//
// Implement PersonalDataHolder on your Handler and Serve subscribes and answers both. Core asks every holder at
// once, each within PrivacyHookBudget, and after an erase it asks privacy.export again: anything still found that
// you did not declare held is reported to the owner as not erased.
//
// Core sends only addresses it is entitled to send: a signed-in person's own verified ones, or the one an operator
// holding privacy.manage typed. Never store them for anything else.

// The two hooks.
const (
	HookPrivacyExport = "privacy.export"
	HookPrivacyErase  = "privacy.erase"
)

// PrivacyHookBudget is how long one privacy hook may take: an erasure may walk several tables, or wait on a service
// your plugin keeps data in. Past it Core reports you as not having answered.
const PrivacyHookBudget = 15 * time.Second

// The grounds a record may be kept or held on (GDPR Art. 17(3)).
const (
	// PrivacyBasisLegalObligation: a law makes you keep it — the accounting facts of a payment (amount, currency,
	// date, reference, status, the processor's reference; Austria's BAO §132 and UGB §212 keep them seven years).
	// Art. 17(3)(b).
	PrivacyBasisLegalObligation = "legal_obligation"
	// PrivacyBasisLegalClaims: an open dispute, a chargeback or a claim keeps the record whole until it is settled.
	// Art. 17(3)(e).
	PrivacyBasisLegalClaims = "legal_claims"
)

// MaxPrivacyRecords is the most records one answer may carry; Core refuses a larger answer whole.
const MaxPrivacyRecords = 5000

// The longest a kind, an id, a note and a kept field may be.
const (
	maxPrivacyKind  = 64
	maxPrivacyID    = 128
	maxPrivacyNote  = 500
	maxPrivacyField = 64
	maxKeptFields   = 32
)

// PrivacyRequest is what Core asks about: one or more email addresses, already trimmed and lower-cased, and the
// request's id (the same on the erase and on the export that checks it), for your own log.
type PrivacyRequest struct {
	Emails    []string `json:"emails"`
	RequestID string   `json:"request_id"`
}

// Asked reports whether email is one of the request's addresses, compared the way Core compares them (trimmed,
// lower-cased). Match your rows with the same rule.
func (r PrivacyRequest) Asked(email string) bool {
	e := NormalizePrivacyEmail(email)
	for _, a := range r.Emails {
		if NormalizePrivacyEmail(a) == e && e != "" {
			return true
		}
	}
	return false
}

// NormalizePrivacyEmail is the one rule an address is compared by: trimmed and lower-cased.
func NormalizePrivacyEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// PrivacyRecord is one thing you keep about the person, as they should read it.
type PrivacyRecord struct {
	// Kind is what it is, in your words: "booking", "waitlist", "blocklist".
	Kind string `json:"kind"`
	// ID is your record's id — the same one an erasure names in Kept or Held.
	ID string `json:"id"`
	// Email is the request address this record matched. Core refuses an answer naming one it did not ask about.
	Email string `json:"email"`
	// Data is the record itself: what the person gave you and what you noted about them.
	Data map[string]any `json:"data"`
}

// PrivacyExport is the answer to privacy.export: every record for the request's addresses. None is an empty list.
type PrivacyExport struct {
	Records []PrivacyRecord `json:"records"`
}

// Validate checks an answer against the request it answers.
func (e PrivacyExport) Validate(req PrivacyRequest) error {
	if len(e.Records) > MaxPrivacyRecords {
		return fmt.Errorf("nilda: a privacy answer carries %d records, more than %d", len(e.Records), MaxPrivacyRecords)
	}
	for i, r := range e.Records {
		if err := checkPrivacyRef(r.Kind, r.ID); err != nil {
			return fmt.Errorf("nilda: privacy record %d: %w", i, err)
		}
		if strings.TrimSpace(r.Email) == "" {
			return fmt.Errorf("nilda: privacy record %d (%s %s) names no address", i, r.Kind, r.ID)
		}
		if !req.Asked(r.Email) {
			return fmt.Errorf("nilda: privacy record %d (%s %s) is about an address Core was not asked about", i, r.Kind, r.ID)
		}
	}
	return nil
}

// PrivacyKept is a record you anonymised and kept part of because a law makes you keep it: the fields that remain
// (which must no longer identify the person) and why.
type PrivacyKept struct {
	Kind   string   `json:"kind"`
	ID     string   `json:"id"`
	Fields []string `json:"fields"`
	Basis  string   `json:"basis"`
	Note   string   `json:"note,omitempty"`
}

// PrivacyHeld is a record you did NOT erase, whole, and why — an open dispute. It is still found by its address,
// and Core tells the owner so.
type PrivacyHeld struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Basis string `json:"basis"`
	Note  string `json:"note,omitempty"`
}

// PrivacyErasure is the answer to privacy.erase. Erased counts the records whose personal data is gone entirely;
// Kept and Held name the rest.
type PrivacyErasure struct {
	Erased int           `json:"erased"`
	Kept   []PrivacyKept `json:"kept,omitempty"`
	Held   []PrivacyHeld `json:"held,omitempty"`
}

// Validate checks an erasure says, for everything it did not erase, what it is and on what basis.
func (e PrivacyErasure) Validate() error {
	if e.Erased < 0 {
		return errors.New("nilda: a privacy erasure counts a negative number of records")
	}
	if len(e.Kept)+len(e.Held) > MaxPrivacyRecords {
		return fmt.Errorf("nilda: a privacy erasure names more than %d records", MaxPrivacyRecords)
	}
	seen := map[string]string{}
	for i, k := range e.Kept {
		if err := checkPrivacyRef(k.Kind, k.ID); err != nil {
			return fmt.Errorf("nilda: kept record %d: %w", i, err)
		}
		if len(k.Fields) == 0 || len(k.Fields) > maxKeptFields {
			return fmt.Errorf("nilda: kept record %d (%s %s) must name the 1 to %d fields it keeps", i, k.Kind, k.ID, maxKeptFields)
		}
		for _, f := range k.Fields {
			if strings.TrimSpace(f) == "" || len(f) > maxPrivacyField {
				return fmt.Errorf("nilda: kept record %d (%s %s) names an empty or overlong field", i, k.Kind, k.ID)
			}
		}
		if err := checkPrivacyBasis(k.Basis, k.Note); err != nil {
			return fmt.Errorf("nilda: kept record %d (%s %s): %w", i, k.Kind, k.ID, err)
		}
		seen[k.Kind+"\x00"+k.ID] = "kept"
	}
	for i, h := range e.Held {
		if err := checkPrivacyRef(h.Kind, h.ID); err != nil {
			return fmt.Errorf("nilda: held record %d: %w", i, err)
		}
		if err := checkPrivacyBasis(h.Basis, h.Note); err != nil {
			return fmt.Errorf("nilda: held record %d (%s %s): %w", i, h.Kind, h.ID, err)
		}
		if seen[h.Kind+"\x00"+h.ID] == "kept" {
			return fmt.Errorf("nilda: %s %s is both kept anonymised and held whole", h.Kind, h.ID)
		}
	}
	return nil
}

func checkPrivacyRef(kind, id string) error {
	switch {
	case strings.TrimSpace(kind) == "" || len(kind) > maxPrivacyKind:
		return fmt.Errorf("a kind of 1 to %d characters is required", maxPrivacyKind)
	case strings.TrimSpace(id) == "" || len(id) > maxPrivacyID:
		return fmt.Errorf("an id of 1 to %d characters is required", maxPrivacyID)
	}
	return nil
}

func checkPrivacyBasis(basis, note string) error {
	if basis != PrivacyBasisLegalObligation && basis != PrivacyBasisLegalClaims {
		return fmt.Errorf("basis %q is not %q or %q", basis, PrivacyBasisLegalObligation, PrivacyBasisLegalClaims)
	}
	if len(note) > maxPrivacyNote {
		return fmt.Errorf("a note is at most %d characters", maxPrivacyNote)
	}
	return nil
}

// PersonalDataHolder is a plugin that keeps data about people and answers the site's privacy requests about them.
// Implement it on your Handler and declare `personal_data`; Serve subscribes privacy.export and privacy.erase and
// answers them, checking your answer before Core does.
type PersonalDataHolder interface {
	// ExportPersonalData returns every record you keep for the request's addresses.
	ExportPersonalData(ctx context.Context, req PrivacyRequest) (PrivacyExport, error)
	// ErasePersonalData erases or anonymises them, and names what it kept or held and why. Called again with the same
	// request, it must change nothing more.
	ErasePersonalData(ctx context.Context, req PrivacyRequest) (PrivacyErasure, error)
}

// DispatchPrivacyHook answers one privacy hook from h. handled is false for any other hook. Serve calls it for a
// Handler that implements PersonalDataHolder; call it yourself only from a HandleHook that routes by hand.
func DispatchPrivacyHook(ctx context.Context, h PersonalDataHolder, hook string, payload []byte) (out []byte, handled bool, err error) {
	if hook != HookPrivacyExport && hook != HookPrivacyErase {
		return nil, false, nil
	}
	var req PrivacyRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, true, fmt.Errorf("nilda: %s payload: %w", hook, err)
	}
	for i, e := range req.Emails {
		req.Emails[i] = NormalizePrivacyEmail(e)
	}
	if hook == HookPrivacyExport {
		res, err := h.ExportPersonalData(ctx, req)
		if err != nil {
			return nil, true, err
		}
		if err := res.Validate(req); err != nil {
			return nil, true, err
		}
		if res.Records == nil {
			res.Records = []PrivacyRecord{}
		}
		b, err := json.Marshal(res)
		return b, true, err
	}
	res, err := h.ErasePersonalData(ctx, req)
	if err != nil {
		return nil, true, err
	}
	if err := res.Validate(); err != nil {
		return nil, true, err
	}
	b, err := json.Marshal(res)
	return b, true, err
}
