package nilda

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// AN ABILITY'S WAY BACK (v0.66.0, EXPERIMENTAL until Core reads it — Core's agent-control unit B4b).
//
// Core keeps every change the site's assistant or a connected AI app made on its AI changes ledger ("What did the AI
// do?"), with the way to take it back in one press — for its own tools. A plugin's ability has had none: its line says
// "no recorded way back; undo it in the plugin". An ability may now say how its change is taken back, or why it cannot
// be, in two halves:
//
//   - THE DECLARATION, at Init and in plugin.json (Ability.Undo): WHICH ability of this plugin takes the change back
//     (UndoBy), optionally which read ability tells whether the target is still as the change left it (Check) — or that
//     there is no way back, with a reason Core translates and an ability that makes up for it (NoUndo).
//   - THE WAY BACK FROM ONE CALL, in that call's result (WayBack, the result's `undo` field): the inverse's ARGUMENTS,
//     the target the call changed and the version it left.
//
// The arguments ride in the result, not in a function the declaration carries, because they are known only once the
// change has run: a creation's inverse needs the id it created, a price change's inverse needs the price it replaced
// — which Run read to change it and which neither the call's input nor any later look can recover. A function of
// (input, result) would have to be handed the before-state through the result anyway, and could not cross to Core,
// which records the line; so Run, which already holds all three, writes the way back where Core reads it.
//
// Serve holds both halves to what Core can run: Init refuses a declaration Core could not take (checkAbilityUndo), and
// a call's way back that does not fit the declaration is taken off the answer before Core sees it — the change
// happened, so the call still succeeds, and Core records it as having no way back rather than one that would fail.
//
// An ability that declares nothing (the zero AbilityUndo) is every ability before: no recorded way back.

// AbilityUndo is an ability's declared way back on Core's AI changes ledger, or why it has none. Build one with UndoBy
// or NoUndo, or write the struct to add a Check. The zero value declares nothing.
type AbilityUndo struct {
	// By is the inverse: an ability of this plugin — this one, for a change that is its own inverse (set a price back)
	// — that Core calls with the result's undo.args when a person presses Undo. It changes something (not read or
	// analyze), and its effect obliges at least what this ability's does (Core's Rule.Loses: a person's act, asked
	// every call, asked before a run, charged; no effect counts as outward) — stricter, never weaker.
	By string
	// Check is a read-class ability of this plugin that says whether the target is still as the change left it. Core
	// calls it with {"kind": …, "id": …} (the result's undo.target) — its InputSchema must be exactly that object, both
	// strings, both required — and compares its answer's "version", a JSON string, for equality with the result's
	// undo.version before running the inverse, so an Undo never overwrites a later edit. Empty: no check (as a creation
	// undone into the bin needs none). Only beside By.
	Check string
	// NoUndo is why there is no way back, as a key Core says in the reader's language (UndoReasons).
	NoUndo UndoReason
	// Compensation is an ability of this plugin that makes up for a change with no way back — a correction mail for
	// mail already sent. Empty when there is none. Only beside NoUndo.
	Compensation string
}

// UndoBy declares the ability of this plugin that takes this one's change back. Each call's result carries its
// arguments (WayBack).
func UndoBy(ability string) AbilityUndo { return AbilityUndo{By: ability} }

// NoUndo declares that this ability's change cannot be taken back, why, and — when there is one — the ability of this
// plugin that makes up for it ("" for none).
func NoUndo(reason UndoReason, compensation string) AbilityUndo {
	return AbilityUndo{NoUndo: reason, Compensation: compensation}
}

// declared reports whether u says anything.
func (u AbilityUndo) declared() bool { return u != AbilityUndo{} }

// UndoReason is why a change has no way back, in a vocabulary Core translates into the reader's own words.
type UndoReason string

// The reasons.
const (
	// UndoReasonSent: something left the site — mail, a message, a webhook — and cannot be called back.
	UndoReasonSent UndoReason = "sent"
	// UndoReasonOutside: the change was made at a service outside the site (a carrier, a marketplace) that offers no
	// way to take it back.
	UndoReasonOutside UndoReason = "outside"
	// UndoReasonPermanent: removed for good; nothing brings it back.
	UndoReasonPermanent UndoReason = "permanent"
	// UndoReasonQueued: the work went to the plugin's background queue and runs apart from the command that asked for
	// it, so what it changes is not that command's to undo (Core's Compensation.Queued).
	UndoReasonQueued UndoReason = "queued"
	// UndoReasonInPlugin: take it back in the plugin's own screens, where it was made — what Core says today of every
	// plugin change.
	UndoReasonInPlugin UndoReason = "in_plugin"
)

var undoReasons = map[UndoReason]bool{
	UndoReasonSent: true, UndoReasonOutside: true, UndoReasonPermanent: true, UndoReasonQueued: true,
	UndoReasonInPlugin: true,
}

// Known reports whether r is in the vocabulary.
func (r UndoReason) Known() bool { return undoReasons[r] }

// UndoReasons lists the vocabulary, sorted.
func UndoReasons() []UndoReason {
	out := make([]UndoReason, 0, len(undoReasons))
	for r := range undoReasons {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// WayBack is the way back from ONE call of an ability declared with UndoBy: put it in the result as a field `undo`.
//
//	type created struct {
//		ID   string         `json:"id"`
//		Undo *nilda.WayBack `json:"undo,omitempty"`
//	}
//	return created{ID: id, Undo: &nilda.WayBack{
//		Args:   map[string]any{"id": id},
//		Target: nilda.UndoTarget{Kind: "product", ID: id},
//	}}, nil
//
// A result with no `undo` changed nothing that needs one, and Core records no way back for that call.
type WayBack struct {
	// Args are the inverse's arguments, a JSON object held to its InputSchema.
	Args any `json:"args"`
	// Target is what the call changed — the ledger's object, which also finds the later changes an Undo would overwrite.
	// Required, both its kind and its id: without it Core could not tell what a later change stands on.
	Target UndoTarget `json:"target,omitzero"`
	// Version is the target's version as the call left it, a string compared for equality with the "version" the
	// declared Check answers; the Undo runs only while they are equal. Required with a Check, refused without one.
	Version string `json:"version,omitempty"`
}

// UndoTarget names the thing one call changed: a kind of this plugin's own ("product") and its id.
type UndoTarget struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// undoContract is the declaration as the Init answer carries it; nil for none.
func undoContract(u AbilityUndo) *contract.AbilityUndo {
	if !u.declared() {
		return nil
	}
	return &contract.AbilityUndo{By: u.By, Check: u.Check, NoUndo: string(u.NoUndo), Compensation: u.Compensation}
}

// reachedByUndo reports whether both doors an Undo is pressed at reach a: the panel's (CallerKindAI) and a connected
// app's command undo (CallerKindMCP). Core runs an inverse through its agent pipeline, which refuses a kind an ability
// does not name; one only some doors reach is an Undo that works for one person and fails for another.
func reachedByUndo(a Ability) bool {
	return len(a.Callers) == 0 || (slices.Contains(a.Callers, CallerKindAI) && slices.Contains(a.Callers, CallerKindMCP))
}

// changesNothing reports whether an ability of class c changes nothing, so has nothing to take back.
func changesNothing(c Class) bool { return c == ClassRead || c == ClassAnalyze }

// checkAbilityUndo refuses a way back Core could not take: one that names an ability this plugin does not declare, a
// person's own act, a door an Undo is not pressed at, an inverse that changes nothing or obliges less than the change
// (a money change undone by something that is not money, among them), a check that is not a read or is called with
// more or less than the target, or a reason Core does not translate.
func checkAbilityUndo(list []Ability) error {
	byName := map[string]Ability{}
	for _, a := range list {
		if _, seen := byName[a.Name]; !seen {
			byName[a.Name] = a
		}
	}
	// other is a named ability of this plugin that Core will call for an Undo, as role says.
	other := func(a Ability, role, name string) (Ability, error) {
		o, ok := byName[name]
		switch {
		case !ok:
			return o, fmt.Errorf("ability %q names %q as its %s, which this plugin does not declare", a.Name, name, role)
		case abilityRule(o).humanOnly:
			return o, fmt.Errorf("ability %q names %q as its %s, whose effect %q is a person's own act that no tool "+
				"performs", a.Name, name, role, o.Effect)
		case !reachedByUndo(o):
			return o, fmt.Errorf("ability %q names %q as its %s, which does not take calls from both doors an Undo is "+
				"pressed at — give it no Callers, or name CallerKindAI and CallerKindMCP", a.Name, name, role)
		}
		return o, nil
	}
	for _, a := range list {
		u := a.Undo
		if !u.declared() {
			continue
		}
		if changesNothing(a.Class) {
			return fmt.Errorf("ability %q is of class %q and changes nothing to take back — declare no Undo", a.Name, a.Class)
		}
		switch {
		case u.By != "" && u.NoUndo != "":
			return fmt.Errorf("ability %q declares both a way back (%q) and none (%q) — choose one", a.Name, u.By, u.NoUndo)
		case u.By != "":
			if u.Compensation != "" {
				return fmt.Errorf("ability %q has a way back, so it needs no compensation (%q)", a.Name, u.Compensation)
			}
			inv, err := other(a, "inverse", u.By)
			if err != nil {
				return err
			}
			if changesNothing(inv.Class) {
				return fmt.Errorf("ability %q names %q as its inverse, which is of class %q and changes nothing — "+
					"name the ability that puts the change back", a.Name, u.By, inv.Class)
			}
			if a.Effect == EffectMoney && inv.Effect != EffectMoney {
				return fmt.Errorf("ability %q moves money (effect money), and its inverse %q does not — taking a money "+
					"change back moves money too, so declare the inverse's effect money", a.Name, u.By)
			}
			// AN INVERSE MAY BE STRICTER, NEVER WEAKER (Core's module Rule.Loses): an Undo is a call of its own, and an
			// inverse that drops an obligation the change had — asked every call, asked before a run, charged — is a
			// door round it. An ability with no effect is outward to Core, so its inverse must ask before a run too.
			if lost := abilityRule(a).loses(abilityRule(inv)); len(lost) > 0 {
				return fmt.Errorf("ability %q is undone by %q, which drops what %q obliges: %s — declare an inverse "+
					"whose effect obliges at least as much", a.Name, u.By, a.Name, strings.Join(lost, ", "))
			}
			if u.Check != "" {
				chk, err := other(a, "check", u.Check)
				if err != nil {
					return err
				}
				if chk.Class != ClassRead {
					return fmt.Errorf("ability %q names %q as its check, which is of class %q — a check only reads, "+
						"so it must be of class read", a.Name, u.Check, chk.Class)
				}
				if err := checkSchemaIsTarget(chk.InputSchema); err != nil {
					return fmt.Errorf("ability %q names %q as its check, whose input schema %v — Core calls a check "+
						"with the target alone, {\"kind\": string, \"id\": string}, both required", a.Name, u.Check, err)
				}
			}
		case u.NoUndo != "":
			if !u.NoUndo.Known() {
				return fmt.Errorf("ability %q says it has no way back because %q, which Core does not translate — use "+
					"one of %v", a.Name, u.NoUndo, UndoReasons())
			}
			if u.Compensation != "" {
				if _, err := other(a, "compensation", u.Compensation); err != nil {
					return err
				}
			}
		case u.Check != "":
			return fmt.Errorf("ability %q names a check (%q) and no inverse for it to guard — set By", a.Name, u.Check)
		default:
			return fmt.Errorf("ability %q names a compensation (%q) and no reason there is no way back — use NoUndo",
				a.Name, u.Compensation)
		}
	}
	return nil
}

// checkSchemaIsTarget holds a check's input schema to what Core calls it with: an object of exactly "kind" and "id",
// both strings, both required. The error reads after "whose input schema".
func checkSchemaIsTarget(raw json.RawMessage) error {
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &schema) != nil || schema.Type != "object" {
		return fmt.Errorf("is not an object of kind and id")
	}
	for name, p := range schema.Properties {
		if name != "kind" && name != "id" {
			return fmt.Errorf("takes %q as well", name)
		}
		if p.Type != "string" {
			return fmt.Errorf("takes %q as %q, not a string", name, p.Type)
		}
	}
	for _, name := range []string{"kind", "id"} {
		if _, ok := schema.Properties[name]; !ok {
			return fmt.Errorf("does not take %q", name)
		}
		if !slices.Contains(schema.Required, name) {
			return fmt.Errorf("does not mark %q required", name)
		}
	}
	return nil
}

// wayBacks are the declared inverses, by the hook of the ability that declares one.
type wayBacks map[string]AbilityUndo

// declaredWayBacks indexes the abilities declared with UndoBy (the first declaration of a name wins, as for Run).
func declaredWayBacks(list []Ability) wayBacks {
	out := wayBacks{}
	for _, a := range list {
		hook := AbilityHook(a.Name)
		if _, seen := out[hook]; !seen && a.Undo.By != "" {
			out[hook] = a.Undo
		}
	}
	return out
}

// keep holds one call's answer to its declared way back: the answer as it was when it carries none, or a sound one;
// without its `undo` field, and logged, when that way back could not run — the change happened, so the call still
// succeeds, and Core records it as having no way back. An ability with no declared inverse is never touched: an
// `undo` field of its own is its own business.
func (w wayBacks) keep(hook string, args argSchemas, out []byte) []byte {
	decl, ok := w[hook]
	if !ok {
		return out
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(out, &fields) != nil {
		return out
	}
	raw, ok := fields["undo"]
	if !ok {
		return out
	}
	err := checkWayBack(decl, args, raw)
	if err == nil {
		return out
	}
	defaultLogger().Error("an ability's way back was taken off its answer: Core records the change with none",
		"hook", hook, "error", err)
	delete(fields, "undo")
	b, merr := json.Marshal(fields)
	if merr != nil {
		return out
	}
	return b
}

// checkWayBack is one call's way back against its declaration: an object of arguments the inverse's schema accepts,
// a whole target (always), and a version exactly when there is a check to compare it with.
func checkWayBack(decl AbilityUndo, args argSchemas, raw json.RawMessage) error {
	var wb struct {
		Args    json.RawMessage `json:"args"`
		Target  UndoTarget      `json:"target"`
		Version string          `json:"version"`
	}
	if err := json.Unmarshal(raw, &wb); err != nil {
		return fmt.Errorf("undo is not a way back ({\"args\", \"target\", \"version\"}): %w", err)
	}
	var obj map[string]any
	if s := string(wb.Args); s == "" || s == "null" {
		obj = map[string]any{}
	} else if err := json.Unmarshal(wb.Args, &obj); err != nil {
		return fmt.Errorf("undo.args is not a JSON object")
	}
	if r, ok := args[AbilityHook(decl.By)]; ok {
		if err := r.Validate(obj); err != nil {
			return fmt.Errorf("undo.args do not match the input schema of %q: %s", decl.By, schemaFailure(err))
		}
	}
	// The target, whole, always: Core finds the later changes an Undo would overwrite by it, and finds none without one.
	if wb.Target.Kind == "" || wb.Target.ID == "" {
		return fmt.Errorf("undo.target needs both its kind and its id — Core finds the later changes an Undo would " +
			"overwrite by them")
	}
	switch {
	case decl.Check != "" && wb.Version == "":
		return fmt.Errorf("the declared check %q has nothing to compare without undo.version", decl.Check)
	case decl.Check == "" && wb.Version != "":
		return fmt.Errorf("undo.version is set and no check is declared to compare it — declare AbilityUndo.Check")
	}
	return nil
}
