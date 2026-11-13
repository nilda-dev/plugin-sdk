package nilda

import (
	"fmt"
	"sort"
)

// Effect is WHAT A CALL DOES IN THE WORLD that its risk class cannot say: money moving, a credential, the place the
// site's mail or money goes, paid credit spent, something deleted for good, a role handed out, mail to customers,
// many things at once, something made public, an address outside the site reached. The class says how dangerous the
// action is to the site's content; the effect says what a person must be asked before the assistant does it.
//
// It is Core's one vocabulary for that (Core's module.ToolEffect, its agent-control unit A0): Core's own tools
// declare one, and so does an ability (Ability.Effect) and a settings field (SettingField.Effect). Core holds each
// effect to one rule, the same at every door — the panel's chat, a connected AI app, a build, an automation. A guard
// in this package holds the words and which of them ask (AlwaysAsks) to Core's own (core's internal/module/effect.go).
//
// Declare the TRUEST effect. Empty means none declared: the class gates the call as before. An effect this Core does
// not know is asked before every call (it fails closed); Serve refuses one at Init, so a typo is caught at your
// keyboard.
type Effect string

// The vocabulary, as Core spells it.
const (
	// EffectNone: none of the below — the class's own gate is the whole answer.
	EffectNone Effect = "none"
	// EffectMoney: a refund, a capture, a void, a payout. Asked before every call.
	EffectMoney Effect = "money"
	// EffectCredential: an API key, a secret, a password. A person's to change, never a tool's. Asked before every
	// call.
	EffectCredential Effect = "credential"
	// EffectRedirect: where money, mail, data or sign-ins go — a payout account, a webhook, a mail relay. Asked
	// before every call.
	EffectRedirect Effect = "redirect"
	// EffectSpend: spends the owner's AI credit or a paid provider. A build asks before running it.
	EffectSpend Effect = "spend"
	// EffectDeletePermanent: gone for good, no recycle bin brings it back. Asked before every call.
	EffectDeletePermanent Effect = "delete_permanent"
	// EffectRole: hands somebody powers — a role, an invite, who may sign in. Asked before every call.
	EffectRole Effect = "role"
	// EffectOutboundMail: mail to customers or visitors. Asked before every call.
	EffectOutboundMail Effect = "outbound_mail"
	// EffectBulk: many items in one call. Asked before every call.
	EffectBulk Effect = "bulk"
	// EffectPublish: makes something visible to the public. The class's own preview and confirm are the question.
	EffectPublish Effect = "publish"
	// EffectOutward: fetches from or sends to an address outside the site. A build asks before running it.
	EffectOutward Effect = "outward"
)

// effectAsks is Core's rule table (module.effectRules) reduced to the half a plugin's declaration carries to a Core
// that reads only always_ask: whether the person is asked before every call.
var effectAsks = map[Effect]bool{
	EffectNone:            false,
	EffectMoney:           true,
	EffectCredential:      true,
	EffectRedirect:        true,
	EffectSpend:           false,
	EffectDeletePermanent: true,
	EffectRole:            true,
	EffectOutboundMail:    true,
	EffectBulk:            true,
	EffectPublish:         false,
	EffectOutward:         false,
}

// AlwaysAsks reports whether Core asks the person before EVERY call with this effect, whatever a connector was
// granted — Core's rule for it. An empty effect obliges nothing; an effect outside the vocabulary asks (fail closed).
//
// The SDK writes it as `always_ask` beside `effect` (on the wire, and in plugin.json through
// nildatest.ManifestAbilities and SettingField's JSON), so a Core that reads only always_ask asks all the same. The
// reverse is refused: AlwaysAsk set beside an effect that does not ask contradicts it, and Core holds the effect.
func (e Effect) AlwaysAsks() bool {
	if e == "" {
		return false
	}
	asks, ok := effectAsks[e]
	return asks || !ok
}

// Known reports whether e is in Core's vocabulary.
func (e Effect) Known() bool {
	_, ok := effectAsks[e]
	return ok
}

// Effects lists the vocabulary, sorted.
func Effects() []Effect {
	out := make([]Effect, 0, len(effectAsks))
	for e := range effectAsks {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// abilityAlwaysAsks is what an ability's always_ask says to Core: the author's own AlwaysAsk, or its effect's rule.
func abilityAlwaysAsks(a Ability) bool { return a.AlwaysAsk || a.Effect.AlwaysAsks() }

// checkAbilityEffects refuses an effect Core does not know — Core would ask before every call of it, and nobody would
// know why — and AlwaysAsk beside an effect whose rule does not ask, which Core refuses as a contradiction.
func checkAbilityEffects(list []Ability) error {
	for _, a := range list {
		if err := checkEffect("ability", a.Name, a.Effect, a.AlwaysAsk); err != nil {
			return err
		}
	}
	return nil
}

// checkEffect is one declaration's effect against Core's rules: known, and not contradicted by always_ask. The
// manifest's effect is Core's authority (its A0b), so always_ask true beside an effect that does not ask is an error
// there, not a louder effect.
func checkEffect(what, name string, e Effect, alwaysAsk bool) error {
	switch {
	case e == "":
		return nil
	case !e.Known():
		return fmt.Errorf("%s %q declares the effect %q, which Core does not know — use one of %v", what, name, e, Effects())
	case alwaysAsk && !e.AlwaysAsks():
		return fmt.Errorf("%s %q sets AlwaysAsk beside the effect %q, whose rule does not ask — declare the effect "+
			"that says why it asks (money, redirect, outbound_mail, …) and drop AlwaysAsk", what, name, e)
	}
	return nil
}
