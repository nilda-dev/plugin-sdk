package nilda

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// SUBSCRIPTIONS SOLD AND RENEWED BY THE SHOP (doors D3 and D9 of the shop's plan 2026-11-05 §1J; P02 subscriptions).
// EXPERIMENTAL.
//
// One selling-plan concept, the shop's: a subscription plan IS one of the shop's selling plans (the same mechanism as
// its pre-orders), written into the shop by the subscriptions plugin. The shop sells it — the product page's choice, the
// basket line, the price with the plan's discount (a pricing-pipeline input beside a line's own deals), the tax, the
// stock, the checkout — and at checkout keeps the buyer's payment method under a subscription mandate
// (MandateSubscription) with Core. The plugin never holds the method, its id or its token.
//
// The order that started it makes an AGREEMENT the shop keeps: the lines at the unit price agreed (locked — a renewal
// never charges today's catalogue price), the rhythm, the mandate's words and limit, the buyer. The plugin owns the
// rest — the schedule, skips, pauses, retries and dunning, the buyer's account tab, its mails — and asks the shop for
// each renewal: renewal.place places the cycle's order through the shop's own checkout and charges the kept method
// off-session. One order per (agreement, cycle), held by a unique index: asking again for a cycle whose order exists
// charges that order again while it is unpaid (a dunning retry), and is refused once it is paid. A renewal is refunded
// only through the shop's refund_lines, by the owner; the plugin never refunds.
//
//	shop.subscriptions/v1  the shop PROVIDES, a subscriptions plugin USES.

// ContractShopSubscriptions is the shop's door for subscriptions.
const ContractShopSubscriptions = "shop.subscriptions/v1"

// Its methods.
const (
	SubscriptionPlanSet         = "plan.set"
	SubscriptionPlanUndo        = "plan.undo"
	SubscriptionAgreementGet    = "agreements.get"
	SubscriptionAgreementList   = "agreements.list"
	SubscriptionRenewalPlace    = "renewal.place"
	SubscriptionRenewalQuote    = "renewal.quote"
	SubscriptionRenewalUndo     = "renewal.undo"
	SubscriptionAgreementStop   = "agreement.stop"
	SubscriptionAgreementResume = "agreement.resume"
	// The buyer's own two ways out of a failed renewal (v0.69.0, EXPERIMENTAL; SubscriptionLinksProvider): a link to pay
	// the cycle's order on the shop's own pay page, and a link where the signed-in buyer gives another method.
	SubscriptionRenewalPayLink = "renewal.pay_link"
	SubscriptionMethodLink     = "method.update_link"
)

// The records the write commands make, and the events the shop emits (ecommerce.*, the shop's namespace).
const (
	SubscriptionPlanRecord      = "selling_plan"
	SubscriptionRenewalRecord   = "renewal_order"
	SubscriptionAgreementRecord = "subscription_agreement"

	EventSellingPlanSet      = "ecommerce.selling_plan.set"
	EventSellingPlanUndone   = "ecommerce.selling_plan.undone"
	EventRenewalPlaced       = "ecommerce.renewal.placed"
	EventRenewalUndone       = "ecommerce.renewal.undone"
	EventSubscriptionStopped = "ecommerce.subscription.stopped"
	EventSubscriptionResumed = "ecommerce.subscription.resumed"
	// Events no write makes: an agreement started (its first order paid and the method kept), a renewal's charge
	// decided, the buyer's method replaced. Each carries agreement_id (and cycle, order_id, fail_code where it says).
	// method_updated (v0.69.0) carries source — SubscriptionMethodByNetwork or SubscriptionMethodByBuyer; renewal.failed
	// carries by_buyer true when the payment that ended without money was the buyer's own, on the pay page.
	EventSubscriptionStarted       = "ecommerce.subscription.started"
	EventRenewalPaid               = "ecommerce.renewal.paid"
	EventRenewalFailed             = "ecommerce.renewal.failed"
	EventSubscriptionMethodUpdated = "ecommerce.subscription.method_updated"
)

// Why an agreement's method changed (the method_updated event's source, v0.69.0).
const (
	SubscriptionMethodByNetwork = MethodUpdatedByNetwork // the card network updated the kept card (Core's card updater)
	SubscriptionMethodByBuyer   = "buyer"                // the buyer gave another method (method.update_link)
)

// SubscriptionEvents returns every event the door emits, sorted.
func SubscriptionEvents() []string {
	out := []string{EventSellingPlanSet, EventSellingPlanUndone, EventRenewalPlaced, EventRenewalUndone, EventSubscriptionStopped,
		EventSubscriptionResumed, EventSubscriptionStarted, EventRenewalPaid, EventRenewalFailed, EventSubscriptionMethodUpdated}
	slices.Sort(out)
	return out
}

// A plan's pricing (SubscriptionPricing.Kind): the shop's selling-plan pricing policies.
const (
	SubscriptionPercentOff = "percentage"   // Value percent off the unit price, 0 to 100
	SubscriptionAmountOff  = "fixed_amount" // Value minor units off each unit, in the shop's currency
	SubscriptionPriceOf    = "price"        // each unit costs Value, in the shop's currency
)

// An agreement's status (SubscriptionAgreement.Status).
const (
	AgreementPending  = "pending"  // its first order is not paid yet, or no method was kept
	AgreementActive   = "active"   // renewals may be placed
	AgreementStopping = "stopping" // stopped: no renewal is placed; the method is kept until EndsAt, resume undoes it
	AgreementStopped  = "stopped"  // the method is forgotten: a new agreement needs the buyer present
)

// A cycle's order and a renewal's charge (SubscriptionCycle.Status, SubscriptionRenewalAnswer.Payment).
const (
	RenewalPaid      = "paid"
	RenewalPending   = "pending"   // with the processor, undecided
	RenewalFailed    = "failed"    // the charge was refused; FailCode says why — place the cycle again to retry
	RenewalCancelled = "cancelled" // renewal.undo, or the owner cancelled it
	RenewalRefunded  = "refunded"  // the owner refunded it (refund_lines)
)

// Why a renewal's charge failed (SubscriptionRenewalAnswer.FailCode, the renewal.failed event).
const (
	RenewalDeclined      = "declined"                        // the bank said no; a retry may pass
	RenewalHardDecline   = "hard_decline"                    // lost, stolen, closed: a retry will not pass
	RenewalAuthRequired  = PaymentFailAuthenticationRequired // the bank wants the buyer present: send them the shop's link
	RenewalMethodExpired = "method_expired"                  // the kept method expired
	RenewalMethodGone    = "method_forgotten"                // the kept method was forgotten
)

// The codes a write may be refused with (SubscriptionWriteAnswer.Refused, SubscriptionRenewalAnswer.Refused).
const (
	SubscriptionUnknownSKU       = "unknown_sku"        // plan.set: a SKU the shop does not sell; renewal: a line not on the agreement
	SubscriptionInvalidPlan      = "invalid_plan"       // plan.set: a pricing or rhythm outside its bounds
	SubscriptionUnknownAgreement = "unknown_agreement"  // no agreement by that id
	SubscriptionNotYours         = "not_yours"          // the agreement is another plugin's
	SubscriptionAgreementStopped = "agreement_stopped"  // stopping or stopped: no renewal
	SubscriptionAgreementPending = "agreement_pending"  // its first order is not paid, or no method was kept
	SubscriptionCyclePaid        = "cycle_paid"         // this cycle's order is paid (OrderID names it)
	SubscriptionCycleOutOfOrder  = "cycle_out_of_order" // a cycle at or before one already placed, or below 2
	SubscriptionTooEarly         = "too_early"          // the cycle is not due yet (a day's grace)
	SubscriptionItemUnavailable  = "item_unavailable"   // an item on it is no longer for sale
	SubscriptionOutOfStock       = "out_of_stock"       // not enough of an item to send
	SubscriptionOverMandate      = "over_mandate"       // the cycle would cost more than the buyer agreed to
	SubscriptionMethodForgotten  = "method_forgotten"   // resume: the kept method is gone
	SubscriptionRenewalIsPaid    = "paid"               // renewal.undo: paid — only the owner refunds it (refund_lines)
	SubscriptionNotStopped       = "not_stopped"        // resume: the agreement is not stopping
	SubscriptionPastMaxCycles    = "past_max_cycles"    // renewal: the agreement ended by itself after its last cycle (max_cycles)
	SubscriptionUnknownWrite     = AttributeUnknownWrite
	SubscriptionUndoneAlready    = AttributeUndoneAlready
	// SubscriptionCharging: renewal.undo of a cycle whose charge is with the processor, undecided — it is let settle;
	// a refused one is then cancelled by the shop when the agreement is no longer active.
	SubscriptionCharging = "charging"
	// SubscriptionOverAgreement: a renewal's lines ask more of an item than the agreement has — a change needs a new
	// agreement, made with the buyer present.
	SubscriptionOverAgreement = "over_agreement"
	// SubscriptionDeliveryUnavailable: the delivery the buyer chose is no longer offered to their address.
	SubscriptionDeliveryUnavailable = "delivery_unavailable"
	// SubscriptionAccountRefused: the buyer's account is suspended or banned on the site; nothing is sold to it.
	SubscriptionAccountRefused = "account_refused"
	// SubscriptionNoRenewal: renewal.pay_link for a cycle no order was placed for.
	SubscriptionNoRenewal = "no_renewal"
	// SubscriptionRenewalClosed: renewal.pay_link for a cycle whose order was cancelled or refunded — nothing to pay.
	SubscriptionRenewalClosed = "renewal_closed"
	// SubscriptionNoAccount: method.update_link for an agreement made without a buyer account — the link is for the
	// signed-in buyer, and a guest has nobody to sign in as.
	SubscriptionNoAccount = "no_account"
)

var subscriptionRefusals = []string{SubscriptionNoRenewal, SubscriptionRenewalClosed, SubscriptionNoAccount, SubscriptionAccountRefused, SubscriptionCharging, SubscriptionOverAgreement, SubscriptionDeliveryUnavailable, SubscriptionUnknownSKU, SubscriptionInvalidPlan, SubscriptionUnknownAgreement,
	SubscriptionNotYours, SubscriptionAgreementStopped, SubscriptionAgreementPending, SubscriptionCyclePaid,
	SubscriptionCycleOutOfOrder, SubscriptionTooEarly, SubscriptionItemUnavailable, SubscriptionOutOfStock,
	SubscriptionOverMandate, SubscriptionMethodForgotten, SubscriptionRenewalIsPaid, SubscriptionNotStopped, SubscriptionPastMaxCycles,
	SubscriptionUnknownWrite, SubscriptionUndoneAlready}

// SubscriptionRefusals returns every code a subscriptions write may be refused with, sorted.
func SubscriptionRefusals() []string {
	out := slices.Clone(subscriptionRefusals)
	slices.Sort(out)
	return out
}

// The contract's bounds.
const (
	MaxSubscriptionSKUs  = 200 // SKUs one plan is offered on
	MaxSubscriptionLines = 50  // lines one agreement or one renewal carries
	maxSubscriptionName  = 100
	maxSubscriptionID    = 128
	maxSubscriptionPage  = 100
	// MaxSubscriptionCycles bounds a plan's discount_cycles, min_cycles and max_cycles.
	MaxSubscriptionCycles = 1000
)

// SubscriptionCycles are a plan's terms in deliveries, copied onto every agreement sold on it.
type SubscriptionCycles struct {
	// DiscountCycles is how many deliveries the plan's discount applies to, the first one included; 0 is every one.
	// A renewal past it is charged at the unit price agreed, without the discount.
	DiscountCycles int `json:"discount_cycles,omitempty"`
	// MinCycles is the minimum term the buyer agreed to, shown above the order button and in the mandate; 0 is none.
	// A stop before it is the plugin's call — the shop records it.
	MinCycles int `json:"min_cycles,omitempty"`
	// MaxCycles is how many deliveries the subscription makes before it ends by itself; 0 is until stopped. A renewal
	// past it is refused past_max_cycles.
	MaxCycles int `json:"max_cycles,omitempty"`
}

// Validate refuses terms the shop could not keep.
func (c SubscriptionCycles) Validate() error {
	for _, n := range []int{c.DiscountCycles, c.MinCycles, c.MaxCycles} {
		if n < 0 || n > MaxSubscriptionCycles {
			return fmt.Errorf("nilda: a plan's cycles are 0 to %d", MaxSubscriptionCycles)
		}
	}
	if c.MaxCycles > 0 && (c.MinCycles > c.MaxCycles || c.DiscountCycles > c.MaxCycles) {
		return errors.New("nilda: a plan's minimum term and its discount's deliveries are at most its max_cycles")
	}
	return nil
}

// SubscriptionPricing is what a plan does to a unit's price.
type SubscriptionPricing struct {
	Kind  string `json:"kind"`
	Value int64  `json:"value"`
}

// Validate refuses a pricing the shop could not apply.
func (p SubscriptionPricing) Validate() error {
	switch p.Kind {
	case SubscriptionPercentOff:
		if p.Value < 0 || p.Value > 100 {
			return errors.New("nilda: a plan's percentage is 0 to 100")
		}
	case SubscriptionAmountOff, SubscriptionPriceOf:
		if p.Value < 0 {
			return errors.New("nilda: a plan's amount cannot be negative")
		}
	default:
		return fmt.Errorf("nilda: a plan's pricing is %s, %s or %s", SubscriptionPercentOff, SubscriptionAmountOff, SubscriptionPriceOf)
	}
	return nil
}

// SubscriptionPlanRequest is plan.set: a new subscription plan, offered on the SKUs named. A plan is never edited: a
// change is a new plan and plan.undo of the old one, so every agreement keeps the terms it was sold on.
type SubscriptionPlanRequest struct {
	// Name is what the product page and the basket show: "Every month, 10% off".
	Name string   `json:"name"`
	SKUs []string `json:"skus"`
	// Interval and IntervalCount are the rhythm: every IntervalCount Intervals (IntervalDay … IntervalYear).
	Interval      string              `json:"interval"`
	IntervalCount int                 `json:"interval_count"`
	Pricing       SubscriptionPricing `json:"pricing"`
	SubscriptionCycles
}

// Validate refuses a plan no shop could sell.
func (r SubscriptionPlanRequest) Validate() error {
	if err := attributeText("the plan's name", r.Name, maxSubscriptionName*4, true); err != nil {
		return err
	}
	if len(r.SKUs) == 0 || len(r.SKUs) > MaxSubscriptionSKUs {
		return fmt.Errorf("nilda: a plan is offered on 1 to %d SKUs", MaxSubscriptionSKUs)
	}
	for _, s := range r.SKUs {
		if err := attributeText("a SKU", s, maxSubscriptionID, true); err != nil {
			return err
		}
	}
	if err := validRhythm(r.Interval, r.IntervalCount); err != nil {
		return err
	}
	if err := r.SubscriptionCycles.Validate(); err != nil {
		return err
	}
	return r.Pricing.Validate()
}

func validRhythm(interval string, count int) error {
	switch interval {
	case IntervalDay, IntervalWeek, IntervalMonth, IntervalYear:
	default:
		return fmt.Errorf("nilda: a plan's interval is %s, %s, %s or %s", IntervalDay, IntervalWeek, IntervalMonth, IntervalYear)
	}
	if count < 1 || count > maxIntervalStep {
		return fmt.Errorf("nilda: a plan's interval count is 1 to %d", maxIntervalStep)
	}
	return nil
}

// SubscriptionWriteAnswer is plan.set's, plan.undo's, renewal.undo's, agreement.stop's and agreement.resume's answer.
type SubscriptionWriteAnswer struct {
	WriteAnswer
	Refused string `json:"refused,omitempty"`
}

// Validate refuses an answer that is neither a record nor a known refusal, or both.
func (a SubscriptionWriteAnswer) Validate() error {
	return subscriptionWriteValid(a.WriteAnswer, a.Refused)
}

func subscriptionWriteValid(w WriteAnswer, refused string) error {
	switch {
	case refused == "" && w.RecordID == "":
		return errors.New("nilda: a write's answer names its record, or refused says why nothing was written")
	case refused != "" && (w.RecordID != "" || w.Already):
		return errors.New("nilda: a refused write names no record")
	case refused != "" && !slices.Contains(subscriptionRefusals, refused):
		return fmt.Errorf("nilda: refused %q is not a code shop.subscriptions/v1 knows (%s)", refused, strings.Join(subscriptionRefusals, ", "))
	}
	return attributeText("record_id", w.RecordID, maxSubscriptionID, false)
}

// SubscriptionLine is one line of an agreement: what is sent each cycle, at the price the buyer agreed to.
type SubscriptionLine struct {
	SKU      string `json:"sku"`
	Title    string `json:"title"`
	Quantity int64  `json:"quantity"`
	PlanID   string `json:"plan_id"`
	PlanName string `json:"plan_name"`
	// UnitPriceMinor is the unit price at sign-up, before the plan's discount — locked: renewals charge it, never
	// today's catalogue price. PlanUnitMinor is the unit after the plan's discount.
	UnitPriceMinor int64               `json:"unit_price_minor"`
	PlanUnitMinor  int64               `json:"plan_unit_minor"`
	Pricing        SubscriptionPricing `json:"pricing"`
}

// SubscriptionMethod is how a person recognises the kept method — never its id or its token.
type SubscriptionMethod struct {
	Label     string     `json:"label"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// SubscriptionCycle is one cycle's order.
type SubscriptionCycle struct {
	Cycle       int        `json:"cycle"`
	OrderID     string     `json:"order_id"`
	OrderRef    string     `json:"order_ref"`
	Status      string     `json:"status"`
	FailCode    string     `json:"fail_code,omitempty"`
	TotalMinor  int64      `json:"total_minor"`
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
	PlacedAt    time.Time  `json:"placed_at"`
}

// SubscriptionAgreement is what the shop keeps of one subscription. Email is here to write to the buyer about THEIR
// subscription (the pre-renewal notice, a failed charge) and for nothing else.
type SubscriptionAgreement struct {
	ID             string `json:"id"`
	Plugin         string `json:"plugin"`
	OriginOrderID  string `json:"origin_order_id"`
	Status         string `json:"status"`
	Currency       string `json:"currency"`
	CustomerUserID string `json:"customer_user_id,omitempty"`
	Email          string `json:"email"`
	Locale         string `json:"locale"`
	Country        string `json:"country"`
	Interval       string `json:"interval"`
	IntervalCount  int    `json:"interval_count"`
	// The plan\'s terms in deliveries, as they were when the buyer agreed.
	SubscriptionCycles
	Lines  []SubscriptionLine  `json:"lines"`
	Method *SubscriptionMethod `json:"method,omitempty"`
	// MandateText is the agreement as the buyer read it; MaxAmountMinor the most one renewal may charge.
	MandateText    string `json:"mandate_text"`
	MaxAmountMinor int64  `json:"max_amount_minor"`
	// Digital: nothing on it is posted. Test: placed with the processor's test keys.
	Digital         bool                `json:"digital,omitempty"`
	Test            bool                `json:"test,omitempty"`
	AgreedAt        time.Time           `json:"agreed_at"`
	FirstTotalMinor int64               `json:"first_total_minor"`
	Cycles          []SubscriptionCycle `json:"cycles"`
	EndsAt          *time.Time          `json:"ends_at,omitempty"`
	StoppedAt       *time.Time          `json:"stopped_at,omitempty"`
	StopReason      string              `json:"stop_reason,omitempty"`
}

// Validate refuses an agreement the plugin could not keep to.
func (a SubscriptionAgreement) Validate() error {
	switch a.Status {
	case AgreementPending, AgreementActive, AgreementStopping, AgreementStopped:
	default:
		return fmt.Errorf("nilda: agreement %s has status %q", a.ID, a.Status)
	}
	if strings.TrimSpace(a.ID) == "" || strings.TrimSpace(a.OriginOrderID) == "" {
		return errors.New("nilda: an agreement has an id and the order that started it")
	}
	if err := validCurrency("the agreement's currency", a.Currency); err != nil {
		return err
	}
	if err := validRhythm(a.Interval, a.IntervalCount); err != nil {
		return err
	}
	if len(a.Lines) == 0 || len(a.Lines) > MaxSubscriptionLines {
		return fmt.Errorf("nilda: agreement %s has 1 to %d lines", a.ID, MaxSubscriptionLines)
	}
	for _, l := range a.Lines {
		if strings.TrimSpace(l.SKU) == "" || l.Quantity < 1 || l.UnitPriceMinor < 0 || l.PlanUnitMinor < 0 {
			return fmt.Errorf("nilda: agreement %s has a line with no SKU or impossible amounts", a.ID)
		}
	}
	if a.MaxAmountMinor < 0 || a.FirstTotalMinor < 0 {
		return fmt.Errorf("nilda: agreement %s has a negative amount", a.ID)
	}
	return nil
}

// SubscriptionAgreementRequest is agreements.get: one of the caller's agreements.
type SubscriptionAgreementRequest struct {
	AgreementID string `json:"agreement_id"`
}

// SubscriptionAgreementAnswer is agreements.get's answer; Found false for one that is not the caller's.
type SubscriptionAgreementAnswer struct {
	Found     bool                   `json:"found"`
	Agreement *SubscriptionAgreement `json:"agreement,omitempty"`
}

// Validate refuses an answer that says it found nothing and carries something, or the reverse.
func (a SubscriptionAgreementAnswer) Validate() error {
	if a.Found != (a.Agreement != nil) {
		return errors.New("nilda: agreements.get carries the agreement exactly when it found one")
	}
	if a.Agreement != nil {
		return a.Agreement.Validate()
	}
	return nil
}

// SubscriptionAgreementsRequest is agreements.list: the caller's agreements, oldest first, after an id.
type SubscriptionAgreementsRequest struct {
	Status string `json:"status,omitempty"`
	After  string `json:"after,omitempty"`
	Limit  int    `json:"limit,omitempty"` // 1 to 100; 0 is 100
}

// SubscriptionAgreementsAnswer is a page of agreements; More says another follows (after the last id).
type SubscriptionAgreementsAnswer struct {
	Agreements []SubscriptionAgreement `json:"agreements"`
	More       bool                    `json:"more"`
}

// SubscriptionRenewalLine is a buyer's change for one cycle: an agreement SKU and how many — at the locked price.
type SubscriptionRenewalLine struct {
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
}

// SubscriptionRenewalRequest is renewal.place: the order of one cycle.
type SubscriptionRenewalRequest struct {
	AgreementID string `json:"agreement_id"`
	// Cycle is 2 for the first renewal (the order that started it is cycle 1). Cycles may be skipped, never gone back to.
	Cycle int `json:"cycle"`
	// Lines, when given, replace the agreement's quantities for this cycle — its own SKUs only, never more of an item than
	// the agreement has (over_agreement), at the locked price.
	Lines []SubscriptionRenewalLine `json:"lines,omitempty"`
	// PeriodStart and PeriodEnd are the period this cycle pays for, as the plugin counts it; written on the order.
	PeriodStart *time.Time `json:"period_start,omitempty"`
	PeriodEnd   *time.Time `json:"period_end,omitempty"`
}

// Validate refuses a request no shop could place.
func (r SubscriptionRenewalRequest) Validate() error {
	if err := attributeText("agreement_id", r.AgreementID, maxSubscriptionID, true); err != nil {
		return err
	}
	if r.Cycle < 2 {
		return errors.New("nilda: a renewal is cycle 2 or later — the order that started the agreement is cycle 1")
	}
	if len(r.Lines) > MaxSubscriptionLines {
		return fmt.Errorf("nilda: a renewal carries at most %d lines", MaxSubscriptionLines)
	}
	for _, l := range r.Lines {
		if strings.TrimSpace(l.SKU) == "" || l.Quantity < 1 {
			return errors.New("nilda: a renewal's line names a SKU and at least one unit")
		}
	}
	if r.PeriodStart != nil && r.PeriodEnd != nil && !r.PeriodEnd.After(*r.PeriodStart) {
		return errors.New("nilda: a renewal's period ends after it starts")
	}
	return nil
}

// SubscriptionRenewalAnswer is renewal.place's answer: the cycle's order and how its charge went, or the refusal.
type SubscriptionRenewalAnswer struct {
	WriteAnswer
	Refused string `json:"refused,omitempty"`
	// OrderID names the cycle's order when Refused is cycle_paid (RecordID otherwise).
	OrderID    string `json:"order_id,omitempty"`
	OrderRef   string `json:"order_ref,omitempty"`
	TotalMinor int64  `json:"total_minor,omitempty"`
	Currency   string `json:"currency,omitempty"`
	Payment    string `json:"payment,omitempty"`
	FailCode   string `json:"fail_code,omitempty"`
}

// Validate refuses an answer that is neither a record nor a known refusal, or both.
func (a SubscriptionRenewalAnswer) Validate() error {
	if err := subscriptionWriteValid(a.WriteAnswer, a.Refused); err != nil {
		return err
	}
	if a.Refused == "" {
		switch a.Payment {
		case RenewalPaid, RenewalPending, RenewalFailed:
		default:
			return fmt.Errorf("nilda: a renewal's payment is %s, %s or %s", RenewalPaid, RenewalPending, RenewalFailed)
		}
		if (a.Payment == RenewalFailed) != (a.FailCode != "") {
			return errors.New("nilda: a failed renewal says why (fail_code), and only a failed one")
		}
	}
	if a.TotalMinor < 0 {
		return errors.New("nilda: a renewal's total cannot be negative")
	}
	return nil
}

// SubscriptionQuoteRequest is renewal.quote: what a cycle would cost if it were placed now — for the reminder the card
// schemes ask a merchant to send before a renewal charge. Nothing is placed, held or charged.
type SubscriptionQuoteRequest struct {
	AgreementID string                    `json:"agreement_id"`
	Cycle       int                       `json:"cycle"`
	Lines       []SubscriptionRenewalLine `json:"lines,omitempty"`
}

// Validate refuses a request no shop could price.
func (r SubscriptionQuoteRequest) Validate() error {
	return SubscriptionRenewalRequest{AgreementID: r.AgreementID, Cycle: r.Cycle, Lines: r.Lines}.Validate()
}

// SubscriptionQuoteAnswer is the cycle's price as renewal.place would charge it now, or why it could not be placed.
type SubscriptionQuoteAnswer struct {
	Refused       string `json:"refused,omitempty"`
	AmountMinor   int64  `json:"amount_minor"`
	Currency      string `json:"currency,omitempty"`
	TaxMinor      int64  `json:"tax_minor"`
	ShippingMinor int64  `json:"shipping_minor"`
	DiscountMinor int64  `json:"discount_minor"`
}

// Validate refuses a quote with a negative amount or an unknown refusal.
func (a SubscriptionQuoteAnswer) Validate() error {
	if a.Refused != "" && !slices.Contains(subscriptionRefusals, a.Refused) {
		return fmt.Errorf("nilda: refused %q is not a code shop.subscriptions/v1 knows", a.Refused)
	}
	if a.AmountMinor < 0 || a.TaxMinor < 0 || a.ShippingMinor < 0 || a.DiscountMinor < 0 {
		return errors.New("nilda: a renewal's quote cannot be negative")
	}
	return nil
}

// SubscriptionStopRequest is agreement.stop: no renewal is placed from now on, and a cycle's order still unpaid is
// cancelled (its goods go back) — a plugin that stops mid-retry need not know the order.
type SubscriptionStopRequest struct {
	AgreementID string `json:"agreement_id"`
	// Reason is what the owner reads: "cancelled by the buyer", "payment failed four times".
	Reason string `json:"reason"`
	// EndsAt is the end of what the buyer paid for: the method is forgotten then (now when nil), and until then
	// agreement.resume takes the stop back.
	EndsAt *time.Time `json:"ends_at,omitempty"`
}

// Validate refuses a stop with no agreement or no reason.
func (r SubscriptionStopRequest) Validate() error {
	if err := attributeText("agreement_id", r.AgreementID, maxSubscriptionID, true); err != nil {
		return err
	}
	return rewardReason(r.Reason, true)
}

// SubscriptionsProvider is the shop.
type SubscriptionsProvider interface {
	SetSubscriptionPlan(ctx context.Context, req SubscriptionPlanRequest) (SubscriptionWriteAnswer, error)
	UndoSubscriptionPlan(ctx context.Context, req UndoInput) (SubscriptionWriteAnswer, error)
	SubscriptionAgreement(ctx context.Context, req SubscriptionAgreementRequest) (SubscriptionAgreementAnswer, error)
	SubscriptionAgreements(ctx context.Context, req SubscriptionAgreementsRequest) (SubscriptionAgreementsAnswer, error)
	PlaceRenewal(ctx context.Context, req SubscriptionRenewalRequest) (SubscriptionRenewalAnswer, error)
	QuoteRenewal(ctx context.Context, req SubscriptionQuoteRequest) (SubscriptionQuoteAnswer, error)
	UndoRenewal(ctx context.Context, req UndoInput) (SubscriptionWriteAnswer, error)
	StopAgreement(ctx context.Context, req SubscriptionStopRequest) (SubscriptionWriteAnswer, error)
	ResumeAgreement(ctx context.Context, req UndoInput) (SubscriptionWriteAnswer, error)
}

// SubscriptionPayLinkRequest is renewal.pay_link: a link for the buyer to pay one cycle's order themselves — a renewal
// whose charge was refused — on the shop's own pay page. Nothing is charged by asking.
type SubscriptionPayLinkRequest struct {
	AgreementID string `json:"agreement_id"`
	Cycle       int    `json:"cycle"`
}

// Validate refuses a request no shop could answer.
func (r SubscriptionPayLinkRequest) Validate() error {
	if err := attributeText("agreement_id", r.AgreementID, maxSubscriptionID, true); err != nil {
		return err
	}
	if r.Cycle < 2 {
		return errors.New("nilda: a renewal is cycle 2 or later — the order that started the agreement is cycle 1")
	}
	return nil
}

// SubscriptionMethodLinkRequest is method.update_link: a link where the signed-in buyer gives another payment method for
// the agreement — through Core's payments, on the processor's own page; the agreement then renews on it.
type SubscriptionMethodLinkRequest struct {
	AgreementID string `json:"agreement_id"`
}

// Validate refuses a request no shop could answer.
func (r SubscriptionMethodLinkRequest) Validate() error {
	return attributeText("agreement_id", r.AgreementID, maxSubscriptionID, true)
}

// SubscriptionLinkAnswer is a link for the buyer — signed, expiring at ExpiresAt, opening only for the buyer it is for —
// or why there is none: renewal.pay_link refuses unknown_agreement, not_yours, no_renewal, cycle_paid (paid already),
// renewal_closed and charging (a payment of it is with the processor); method.update_link unknown_agreement,
// not_yours, agreement_stopped and no_account.
type SubscriptionLinkAnswer struct {
	Refused   string     `json:"refused,omitempty"`
	URL       string     `json:"url,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Validate refuses an answer that is neither an https link that expires nor a known refusal, or both.
func (a SubscriptionLinkAnswer) Validate() error {
	switch {
	case a.Refused != "" && (a.URL != "" || a.ExpiresAt != nil):
		return errors.New("nilda: a refused link carries no URL")
	case a.Refused != "" && !slices.Contains(subscriptionRefusals, a.Refused):
		return fmt.Errorf("nilda: refused %q is not a code shop.subscriptions/v1 knows", a.Refused)
	case a.Refused != "":
		return nil
	case a.ExpiresAt == nil:
		return errors.New("nilda: a buyer's link says when it stops working")
	}
	u, err := url.Parse(a.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(a.URL) > maxPayerURL {
		return fmt.Errorf("nilda: a buyer's link is an absolute https URL of at most %d bytes", maxPayerURL)
	}
	return nil
}

// SubscriptionLinksProvider is a shop that answers the buyer's two links (v0.69.0, EXPERIMENTAL). OPTIONAL: a shop that
// does not implement it is not asked, and a subscriptions plugin's mail says the shop will try again.
type SubscriptionLinksProvider interface {
	RenewalPayLink(ctx context.Context, req SubscriptionPayLinkRequest) (SubscriptionLinkAnswer, error)
	MethodUpdateLink(ctx context.Context, req SubscriptionMethodLinkRequest) (SubscriptionLinkAnswer, error)
}

func subscriptionLinkMethods(p SubscriptionsProvider) []ServiceMethod {
	lp, is := p.(SubscriptionLinksProvider)
	if !is {
		return nil
	}
	checked := func(a SubscriptionLinkAnswer, err error) (SubscriptionLinkAnswer, error) {
		if err != nil {
			return a, err
		}
		return a, a.Validate()
	}
	return []ServiceMethod{
		Serves(ContractShopSubscriptions, SubscriptionRenewalPayLink, func(ctx context.Context, req SubscriptionPayLinkRequest) (SubscriptionLinkAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionLinkAnswer{}, err
			}
			return checked(lp.RenewalPayLink(ctx, req))
		}),
		Serves(ContractShopSubscriptions, SubscriptionMethodLink, func(ctx context.Context, req SubscriptionMethodLinkRequest) (SubscriptionLinkAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionLinkAnswer{}, err
			}
			return checked(lp.MethodUpdateLink(ctx, req))
		}),
	}
}

// SubscriptionsMethods are the shop's subscription methods, each checking the request first and the answer after.
func SubscriptionsMethods(p SubscriptionsProvider) []ServiceMethod {
	undo := func(fn func(context.Context, UndoInput) (SubscriptionWriteAnswer, error)) func(context.Context, UndoInput) (SubscriptionWriteAnswer, error) {
		return func(ctx context.Context, req UndoInput) (SubscriptionWriteAnswer, error) {
			if err := attributeText("record_id", req.RecordID, maxSubscriptionID, true); err != nil {
				return SubscriptionWriteAnswer{}, err
			}
			return checkedSubscription(fn(ctx, req))
		}
	}
	return append([]ServiceMethod{
		Serves(ContractShopSubscriptions, SubscriptionPlanSet, func(ctx context.Context, req SubscriptionPlanRequest) (SubscriptionWriteAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionWriteAnswer{}, err
			}
			return checkedSubscription(p.SetSubscriptionPlan(ctx, req))
		}),
		Serves(ContractShopSubscriptions, SubscriptionPlanUndo, undo(p.UndoSubscriptionPlan)),
		Serves(ContractShopSubscriptions, SubscriptionAgreementGet, func(ctx context.Context, req SubscriptionAgreementRequest) (SubscriptionAgreementAnswer, error) {
			if err := attributeText("agreement_id", req.AgreementID, maxSubscriptionID, true); err != nil {
				return SubscriptionAgreementAnswer{}, err
			}
			a, err := p.SubscriptionAgreement(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}),
		Serves(ContractShopSubscriptions, SubscriptionAgreementList, func(ctx context.Context, req SubscriptionAgreementsRequest) (SubscriptionAgreementsAnswer, error) {
			if req.Limit < 0 || req.Limit > maxSubscriptionPage {
				return SubscriptionAgreementsAnswer{}, fmt.Errorf("nilda: a page is 1 to %d agreements", maxSubscriptionPage)
			}
			switch req.Status {
			case "", AgreementPending, AgreementActive, AgreementStopping, AgreementStopped:
			default:
				return SubscriptionAgreementsAnswer{}, fmt.Errorf("nilda: %q is not an agreement's status", req.Status)
			}
			a, err := p.SubscriptionAgreements(ctx, req)
			if err != nil {
				return a, err
			}
			if a.Agreements == nil {
				a.Agreements = []SubscriptionAgreement{}
			}
			for _, ag := range a.Agreements {
				if err := ag.Validate(); err != nil {
					return SubscriptionAgreementsAnswer{}, err
				}
			}
			return a, nil
		}),
		Serves(ContractShopSubscriptions, SubscriptionRenewalPlace, func(ctx context.Context, req SubscriptionRenewalRequest) (SubscriptionRenewalAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionRenewalAnswer{}, err
			}
			a, err := p.PlaceRenewal(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}),
		Serves(ContractShopSubscriptions, SubscriptionRenewalQuote, func(ctx context.Context, req SubscriptionQuoteRequest) (SubscriptionQuoteAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionQuoteAnswer{}, err
			}
			a, err := p.QuoteRenewal(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}),
		Serves(ContractShopSubscriptions, SubscriptionRenewalUndo, undo(p.UndoRenewal)),
		Serves(ContractShopSubscriptions, SubscriptionAgreementStop, func(ctx context.Context, req SubscriptionStopRequest) (SubscriptionWriteAnswer, error) {
			if err := req.Validate(); err != nil {
				return SubscriptionWriteAnswer{}, err
			}
			return checkedSubscription(p.StopAgreement(ctx, req))
		}),
		Serves(ContractShopSubscriptions, SubscriptionAgreementResume, undo(p.ResumeAgreement)),
	}, subscriptionLinkMethods(p)...)
}

func checkedSubscription(a SubscriptionWriteAnswer, err error) (SubscriptionWriteAnswer, error) {
	if err != nil {
		return a, err
	}
	return a, a.Validate()
}

// ---- a subscriptions plugin's side ---------------------------------------------------------------------------

// PlaceRenewal asks the shop for one cycle's order. key is derived from the cycle and the attempt
// ("<agreement>/<cycle>/<attempt>"): the same key is the same attempt, a new attempt for an unpaid cycle charges its
// order again, and no key ever makes a second order for a cycle.
func (s *Services) PlaceRenewal(ctx context.Context, key string, req SubscriptionRenewalRequest) (SubscriptionRenewalAnswer, error) {
	if err := req.Validate(); err != nil {
		return SubscriptionRenewalAnswer{}, err
	}
	if strings.TrimSpace(key) == "" {
		return SubscriptionRenewalAnswer{}, errors.New("nilda: a renewal needs an idempotency key — derive it from the cycle and the attempt")
	}
	res, err := s.Call(ctx, ContractShopSubscriptions, SubscriptionRenewalPlace, req, ServiceCallOptions{IdempotencyKey: key})
	if err != nil {
		return SubscriptionRenewalAnswer{}, err
	}
	if len(res.Answers) == 0 {
		return SubscriptionRenewalAnswer{}, fmt.Errorf("nilda: no shop answered %s %s", ContractShopSubscriptions, SubscriptionRenewalPlace)
	}
	var a SubscriptionRenewalAnswer
	if err := res.Answers[0].Decode(&a); err != nil {
		return SubscriptionRenewalAnswer{}, err
	}
	return a, a.Validate()
}

// RenewalPayLink asks the shop for a link the buyer pays a refused renewal with (renewal.pay_link). Signing a link
// writes nothing, so it carries no idempotency key; ask again for a fresh one.
func (s *Services) RenewalPayLink(ctx context.Context, req SubscriptionPayLinkRequest) (SubscriptionLinkAnswer, error) {
	if err := req.Validate(); err != nil {
		return SubscriptionLinkAnswer{}, err
	}
	return s.subscriptionLink(ctx, SubscriptionRenewalPayLink, req)
}

// MethodUpdateLink asks the shop for a link where the signed-in buyer gives another method (method.update_link).
func (s *Services) MethodUpdateLink(ctx context.Context, req SubscriptionMethodLinkRequest) (SubscriptionLinkAnswer, error) {
	if err := req.Validate(); err != nil {
		return SubscriptionLinkAnswer{}, err
	}
	return s.subscriptionLink(ctx, SubscriptionMethodLink, req)
}

func (s *Services) subscriptionLink(ctx context.Context, method string, req any) (SubscriptionLinkAnswer, error) {
	res, err := s.Call(ctx, ContractShopSubscriptions, method, req, ServiceCallOptions{})
	if err != nil {
		return SubscriptionLinkAnswer{}, err
	}
	if len(res.Answers) == 0 {
		return SubscriptionLinkAnswer{}, fmt.Errorf("nilda: no shop answered %s %s", ContractShopSubscriptions, method)
	}
	var a SubscriptionLinkAnswer
	if err := res.Answers[0].Decode(&a); err != nil {
		return SubscriptionLinkAnswer{}, err
	}
	return a, a.Validate()
}
