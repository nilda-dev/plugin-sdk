package nilda

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// WHAT A REWARDS PLUGIN GIVES A BUYER, THROUGH THE SHOP (the shop's plan 2026-11-05 §1J, "points / store credit spend";
// P08 loyalty and its referral reward). EXPERIMENTAL.
//
// A loyalty plugin never prices a basket and never keeps money of its own: a buyer's points are SPENT as something the
// shop already knows how to honour — store credit on their account (the shop's store-credit ledger, the one
// issue_store_credit writes) or a single-use discount code through the shop's discount engine, usable once and only by
// that signed-in buyer. Both are write commands (WRITE_COMMANDS.md): keyed for as long as the record lives, kept with the
// plugin that wrote them ("added by loyalty" on the owner's screens and abilities), announced once committed, and taken
// back by their undo while nothing depends on them (credit not yet spent, a code not yet used).
//
//	shop.rewards/v1  the shop PROVIDES, a rewards plugin USES: credit.issue / credit.undo, discount.issue / discount.undo,
//	                 issued.get.
//
// Points come back with a refund, never as a second credit: the plugin hears the shop's refunded event (shop.events/v1)
// and adjusts its own balance; it does not issue credit to stand for money the shop refunds.

// ContractShopRewards is the shop's door for what a rewards plugin gives a buyer.
const ContractShopRewards = "shop.rewards/v1"

// Its methods.
const (
	RewardsCreditIssue   = "credit.issue"
	RewardsCreditUndo    = "credit.undo"
	RewardsDiscountIssue = "discount.issue"
	RewardsDiscountUndo  = "discount.undo"
	RewardsIssuedGet     = "issued.get"
)

// The records the write commands make, and the events the shop emits once each is committed (ecommerce.*, the shop's
// namespace).
const (
	RewardCreditRecord   = "store_credit"
	RewardDiscountRecord = "reward_discount"

	EventStoreCreditIssued    = "ecommerce.store_credit.issued"
	EventStoreCreditUndone    = "ecommerce.store_credit.undone"
	EventRewardDiscountIssued = "ecommerce.reward_discount.issued"
	EventRewardDiscountUndone = "ecommerce.reward_discount.undone"
)

// A reward discount's kind (RewardDiscountRequest.Kind).
const (
	RewardFixedAmount = "fixed_amount" // Value minor units off, in Currency
	RewardPercentage  = "percentage"   // Value percent off the goods, 1 to 100
)

// What an issued record is now (RewardIssued.Status).
const (
	RewardOpen    = "open"    // credit on the account / a code not used yet
	RewardUsed    = "used"    // a code an order used (OrderID names it)
	RewardExpired = "expired" // a code past its ExpiresAt, unused
	RewardUndone  = "undone"  // taken back by its undo
)

// The codes a write may be refused with (RewardWriteAnswer.Refused). A refusal is the ANSWER, never an error
// (WRITE_COMMANDS.md §7): nothing was written, nothing is kept under the key.
const (
	RewardNoCustomer    = "no_customer"    // no customer_user_id: a reward is an account's, never a guest's
	RewardInvalidAmount = "invalid_amount" // an amount or value outside its bounds
	RewardWrongCurrency = "wrong_currency" // not the shop's currency
	RewardInvalidExpiry = "invalid_expiry" // an expiry in the past
	RewardSpent         = "spent"          // credit.undo: the buyer spent the credit already
	RewardInUse         = "used"           // discount.undo: an order used the code already
	RewardUnknownWrite  = AttributeUnknownWrite
	RewardUndoneAlready = AttributeUndoneAlready
)

// rewardRefusals is every code a write may be refused with.
var rewardRefusals = []string{RewardNoCustomer, RewardInvalidAmount, RewardWrongCurrency, RewardInvalidExpiry, RewardSpent,
	RewardInUse, RewardUnknownWrite, RewardUndoneAlready}

// RewardRefusals returns every code a rewards write may be refused with, sorted.
func RewardRefusals() []string {
	out := slices.Clone(rewardRefusals)
	slices.Sort(out)
	return out
}

// The contract's bounds.
const (
	MaxRewardCreditMinor = 100_000_00 // one credit: the shop's own store-credit bound
	MaxRewardReason      = 200        // characters of a reason or a code's title, shown to the owner and the buyer
	maxRewardID          = 128        // a Core account id, a record id
)

// RewardCreditRequest is credit.issue: store credit on one buyer's account.
type RewardCreditRequest struct {
	// CustomerUserID is Core's account id for the buyer (ShopOrder.CustomerUserID, the shop's events): store credit is
	// an account's.
	CustomerUserID string `json:"customer_user_id"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	// Reason is what the owner and the buyer read beside the entry: "500 points", "Referral: a friend's first order".
	Reason string `json:"reason"`
}

// Validate refuses a request no shop could honour; what the shop refuses by its own rules is the answer's Refused.
func (r RewardCreditRequest) Validate() error {
	if err := attributeText("customer_user_id", r.CustomerUserID, maxRewardID, false); err != nil {
		return err
	}
	if err := validCurrency("the credit's currency", r.Currency); err != nil {
		return err
	}
	return rewardReason(r.Reason, true)
}

// RewardDiscountRequest is discount.issue: one single-use code for one signed-in buyer.
type RewardDiscountRequest struct {
	CustomerUserID string `json:"customer_user_id"`
	// Kind is RewardFixedAmount or RewardPercentage; Value its minor units or its percent.
	Kind  string `json:"kind"`
	Value int64  `json:"value"`
	// Currency is the shop's, required for a fixed amount.
	Currency string `json:"currency,omitempty"`
	// Title is what the basket shows beside the discount: "Loyalty reward: 500 points".
	Title string `json:"title"`
	// ExpiresAt is when the code stops working; nil keeps it until it is used or taken back.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	// MinimumSubtotalMinor is the least the goods must come to for the code to apply; 0 for none.
	MinimumSubtotalMinor int64 `json:"minimum_subtotal_minor,omitempty"`
}

// Validate refuses a request no shop could honour.
func (r RewardDiscountRequest) Validate() error {
	if err := attributeText("customer_user_id", r.CustomerUserID, maxRewardID, false); err != nil {
		return err
	}
	switch r.Kind {
	case RewardFixedAmount:
		if err := validCurrency("the discount's currency", r.Currency); err != nil {
			return err
		}
	case RewardPercentage:
		if r.Currency != "" {
			if err := validCurrency("the discount's currency", r.Currency); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("nilda: a reward discount is %s or %s", RewardFixedAmount, RewardPercentage)
	}
	if r.MinimumSubtotalMinor < 0 {
		return errors.New("nilda: a reward discount's minimum cannot be negative")
	}
	return rewardReason(r.Title, true)
}

// RewardWriteAnswer is every rewards write's answer: the record, or the refusal.
type RewardWriteAnswer struct {
	WriteAnswer
	Refused string `json:"refused,omitempty"`
	// BalanceMinor is the buyer's store credit after credit.issue / credit.undo.
	BalanceMinor int64 `json:"balance_minor,omitempty"`
	// Code is the discount code discount.issue made — the buyer types it, or the plugin shows it on their account.
	Code string `json:"code,omitempty"`
}

// Validate refuses an answer that is neither a record nor a known refusal, or both.
func (a RewardWriteAnswer) Validate() error {
	switch {
	case a.Refused == "" && a.RecordID == "":
		return errors.New("nilda: a write's answer names its record, or refused says why nothing was written")
	case a.Refused != "" && (a.RecordID != "" || a.Already):
		return errors.New("nilda: a refused write names no record")
	case a.Refused != "" && !slices.Contains(rewardRefusals, a.Refused):
		return fmt.Errorf("nilda: refused %q is not a code shop.rewards/v1 knows (%s)", a.Refused, strings.Join(rewardRefusals, ", "))
	case a.BalanceMinor < 0:
		return errors.New("nilda: a store credit balance cannot be negative")
	}
	return attributeText("record_id", a.RecordID, maxRewardID, false)
}

// RewardIssuedRequest is issued.get: one record this plugin issued.
type RewardIssuedRequest struct {
	RecordID string `json:"record_id"`
}

// RewardIssued is what one record the calling plugin issued is now.
type RewardIssued struct {
	RecordID       string `json:"record_id"`
	Kind           string `json:"kind"` // RewardCreditRecord or RewardDiscountRecord
	Status         string `json:"status"`
	CustomerUserID string `json:"customer_user_id"`
	// AmountMinor is a credit's amount or a fixed code's; Value a percentage code's percent.
	AmountMinor int64      `json:"amount_minor,omitempty"`
	Percent     int64      `json:"percent,omitempty"`
	Currency    string     `json:"currency,omitempty"`
	Code        string     `json:"code,omitempty"`
	OrderID     string     `json:"order_id,omitempty"` // the order that used a code
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	IssuedAt    time.Time  `json:"issued_at"`
}

// RewardIssuedAnswer is issued.get's answer; Found false for a record that is not the caller's.
type RewardIssuedAnswer struct {
	Found  bool          `json:"found"`
	Issued *RewardIssued `json:"issued,omitempty"`
}

// Validate refuses an answer that says it found nothing and carries something, or the reverse.
func (a RewardIssuedAnswer) Validate() error {
	if a.Found != (a.Issued != nil) {
		return errors.New("nilda: issued.get carries the record exactly when it found one")
	}
	if a.Issued == nil {
		return nil
	}
	switch a.Issued.Status {
	case RewardOpen, RewardUsed, RewardExpired, RewardUndone:
	default:
		return fmt.Errorf("nilda: an issued record's status is not %q", a.Issued.Status)
	}
	if a.Issued.Kind != RewardCreditRecord && a.Issued.Kind != RewardDiscountRecord {
		return fmt.Errorf("nilda: an issued record's kind is not %q", a.Issued.Kind)
	}
	return nil
}

func rewardReason(v string, required bool) error {
	switch {
	case v == "" && !required:
		return nil
	case strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > MaxRewardReason || !plainLine(v):
		return fmt.Errorf("nilda: a reward's reason or title is one plain line of 1 to %d characters", MaxRewardReason)
	}
	return nil
}

// RewardsProvider is the shop. Each write is answered through the shop's one write-command helper (WRITE_COMMANDS.md §3).
type RewardsProvider interface {
	IssueCredit(ctx context.Context, req RewardCreditRequest) (RewardWriteAnswer, error)
	UndoCredit(ctx context.Context, req UndoInput) (RewardWriteAnswer, error)
	IssueDiscount(ctx context.Context, req RewardDiscountRequest) (RewardWriteAnswer, error)
	UndoDiscount(ctx context.Context, req UndoInput) (RewardWriteAnswer, error)
	RewardIssued(ctx context.Context, req RewardIssuedRequest) (RewardIssuedAnswer, error)
}

// RewardsMethods are the shop's rewards methods, each checking the request before the shop sees it and the answer
// before it leaves.
func RewardsMethods(p RewardsProvider) []ServiceMethod {
	undo := func(fn func(context.Context, UndoInput) (RewardWriteAnswer, error)) func(context.Context, UndoInput) (RewardWriteAnswer, error) {
		return func(ctx context.Context, req UndoInput) (RewardWriteAnswer, error) {
			if err := attributeText("record_id", req.RecordID, maxRewardID, true); err != nil {
				return RewardWriteAnswer{}, err
			}
			return checkedReward(fn(ctx, req))
		}
	}
	return []ServiceMethod{
		Serves(ContractShopRewards, RewardsCreditIssue, func(ctx context.Context, req RewardCreditRequest) (RewardWriteAnswer, error) {
			if err := req.Validate(); err != nil {
				return RewardWriteAnswer{}, err
			}
			return checkedReward(p.IssueCredit(ctx, req))
		}),
		Serves(ContractShopRewards, RewardsCreditUndo, undo(p.UndoCredit)),
		Serves(ContractShopRewards, RewardsDiscountIssue, func(ctx context.Context, req RewardDiscountRequest) (RewardWriteAnswer, error) {
			if err := req.Validate(); err != nil {
				return RewardWriteAnswer{}, err
			}
			return checkedReward(p.IssueDiscount(ctx, req))
		}),
		Serves(ContractShopRewards, RewardsDiscountUndo, undo(p.UndoDiscount)),
		Serves(ContractShopRewards, RewardsIssuedGet, func(ctx context.Context, req RewardIssuedRequest) (RewardIssuedAnswer, error) {
			if err := attributeText("record_id", req.RecordID, maxRewardID, true); err != nil {
				return RewardIssuedAnswer{}, err
			}
			a, err := p.RewardIssued(ctx, req)
			if err != nil {
				return a, err
			}
			return a, a.Validate()
		}),
	}
}

func checkedReward(a RewardWriteAnswer, err error) (RewardWriteAnswer, error) {
	if err != nil {
		return a, err
	}
	return a, a.Validate()
}

// ---- a rewards plugin's side ---------------------------------------------------------------------------------

// IssueStoreCredit asks the shop to put store credit on a buyer's account. key is derived from what the credit is FOR
// ("redeem:<redemption id>"), never random: a retry with it is answered with the first credit.
func (s *Services) IssueStoreCredit(ctx context.Context, key string, req RewardCreditRequest) (RewardWriteAnswer, error) {
	if err := req.Validate(); err != nil {
		return RewardWriteAnswer{}, err
	}
	return rewardCall(ctx, s, RewardsCreditIssue, key, req)
}

// IssueRewardDiscount asks the shop for a single-use code for one buyer.
func (s *Services) IssueRewardDiscount(ctx context.Context, key string, req RewardDiscountRequest) (RewardWriteAnswer, error) {
	if err := req.Validate(); err != nil {
		return RewardWriteAnswer{}, err
	}
	return rewardCall(ctx, s, RewardsDiscountIssue, key, req)
}

// UndoReward takes back a credit (method RewardsCreditUndo) or a code (RewardsDiscountUndo) this plugin issued.
func (s *Services) UndoReward(ctx context.Context, method, key, recordID string) (RewardWriteAnswer, error) {
	if method != RewardsCreditUndo && method != RewardsDiscountUndo {
		return RewardWriteAnswer{}, fmt.Errorf("nilda: %q is not an undo of %s", method, ContractShopRewards)
	}
	return rewardCall(ctx, s, method, key, UndoInput{RecordID: recordID})
}

func rewardCall(ctx context.Context, s *Services, method, key string, req any) (RewardWriteAnswer, error) {
	if strings.TrimSpace(key) == "" {
		return RewardWriteAnswer{}, errors.New("nilda: a write into the shop needs an idempotency key — derive it from what the write is for")
	}
	res, err := s.Call(ctx, ContractShopRewards, method, req, ServiceCallOptions{IdempotencyKey: key})
	if err != nil {
		return RewardWriteAnswer{}, err
	}
	if len(res.Answers) == 0 {
		return RewardWriteAnswer{}, fmt.Errorf("nilda: no shop answered %s %s", ContractShopRewards, method)
	}
	var a RewardWriteAnswer
	if err := res.Answers[0].Decode(&a); err != nil {
		return RewardWriteAnswer{}, err
	}
	return a, a.Validate()
}
