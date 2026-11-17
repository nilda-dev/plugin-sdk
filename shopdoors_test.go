package nilda

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// A rewards write's answer is a record or a refusal the contract knows, never both and never neither.
func TestARewardsWriteAnswerIsARecordOrARefusal(t *testing.T) {
	for _, c := range []struct {
		a  RewardWriteAnswer
		ok bool
	}{
		{RewardWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "r1"}, Code: "LOYAL-1"}, true},
		{RewardWriteAnswer{Refused: RewardSpent}, true},
		{RewardWriteAnswer{}, false},
		{RewardWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "r1"}, Refused: RewardSpent}, false},
		{RewardWriteAnswer{Refused: "because"}, false},
		{RewardWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "r1"}, BalanceMinor: -1}, false},
	} {
		if err := c.a.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v, want ok=%v", c.a, err, c.ok)
		}
	}
}

// A reward request names its kind, a currency for an amount, and a title.
func TestARewardRequestIsHeldToItsRules(t *testing.T) {
	good := RewardDiscountRequest{CustomerUserID: "u1", Kind: RewardFixedAmount, Value: 500, Currency: "EUR", Title: "500 points"}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []RewardDiscountRequest{
		{Kind: "free", Value: 1, Title: "x"},
		{Kind: RewardFixedAmount, Value: 1, Title: "x"},                           // no currency
		{Kind: RewardPercentage, Value: 10, Title: ""},                            // no title
		{Kind: RewardPercentage, Value: 10, Title: "x", MinimumSubtotalMinor: -1}, // negative minimum
		{Kind: RewardPercentage, Value: 10, Title: "two\nlines"},                  // not one line
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v was let through", bad)
		}
	}
	if (RewardCreditRequest{AmountMinor: 100, Currency: "EUR", Reason: "x"}).Validate() != nil {
		t.Error("a credit with no account is the shop's refusal (no_customer), not a malformed request")
	}
	if (RewardCreditRequest{AmountMinor: 100, Currency: "XXQ", Reason: "x"}).Validate() == nil {
		t.Error("a credit in no currency was let through")
	}
}

// A renewal says how its charge went, and a failed one why; a cycle is 2 or later; a plan's pricing is bounded.
func TestASubscriptionDoorIsHeldToItsRules(t *testing.T) {
	for _, c := range []struct {
		a  SubscriptionRenewalAnswer
		ok bool
	}{
		{SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o1"}, Payment: RenewalPaid}, true},
		{SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o1"}, Payment: RenewalFailed, FailCode: RenewalDeclined}, true},
		{SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o1"}, Payment: RenewalFailed}, false},
		{SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o1"}, Payment: RenewalPaid, FailCode: RenewalDeclined}, false},
		{SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o1"}}, false},
		{SubscriptionRenewalAnswer{Refused: SubscriptionCyclePaid, OrderID: "o1"}, true},
		{SubscriptionRenewalAnswer{Refused: "nope"}, false},
	} {
		if err := c.a.Validate(); (err == nil) != c.ok {
			t.Errorf("%+v: %v, want ok=%v", c.a, err, c.ok)
		}
	}
	if (SubscriptionRenewalRequest{AgreementID: "a", Cycle: 1}).Validate() == nil {
		t.Error("cycle 1 is the order that started the agreement, never a renewal")
	}
	now := time.Now()
	earlier := now.Add(-time.Hour)
	if (SubscriptionRenewalRequest{AgreementID: "a", Cycle: 2, PeriodStart: &now, PeriodEnd: &earlier}).Validate() == nil {
		t.Error("a period that ends before it starts was let through")
	}
	plan := SubscriptionPlanRequest{Name: "Monthly", SKUs: []string{"TEA"}, Interval: IntervalMonth, IntervalCount: 1,
		Pricing: SubscriptionPricing{Kind: SubscriptionPercentOff, Value: 10}}
	if err := plan.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mut := range []func(*SubscriptionPlanRequest){
		func(p *SubscriptionPlanRequest) { p.Pricing.Value = 101 },
		func(p *SubscriptionPlanRequest) { p.Interval = "fortnight" },
		func(p *SubscriptionPlanRequest) { p.IntervalCount = 0 },
		func(p *SubscriptionPlanRequest) { p.SKUs = nil },
		func(p *SubscriptionPlanRequest) { p.Pricing.Kind = "free" },
		func(p *SubscriptionPlanRequest) { p.DiscountCycles = -1 },
		func(p *SubscriptionPlanRequest) { p.MaxCycles, p.MinCycles = 3, 6 },
		func(p *SubscriptionPlanRequest) { p.MaxCycles, p.DiscountCycles = 3, 4 },
		func(p *SubscriptionPlanRequest) { p.MaxCycles = MaxSubscriptionCycles + 1 },
	} {
		bad := plan
		bad.SKUs = append([]string(nil), plan.SKUs...)
		mut(&bad)
		if bad.Validate() == nil {
			t.Errorf("%+v was let through", bad)
		}
	}
}

type fakeShopDoors struct {
	Handler
	agreement *SubscriptionAgreement
}

func (fakeShopDoors) IssueCredit(context.Context, RewardCreditRequest) (RewardWriteAnswer, error) {
	return RewardWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "c1"}, BalanceMinor: 100}, nil
}
func (fakeShopDoors) UndoCredit(context.Context, UndoInput) (RewardWriteAnswer, error) {
	return RewardWriteAnswer{Refused: "made up"}, nil
}
func (fakeShopDoors) IssueDiscount(context.Context, RewardDiscountRequest) (RewardWriteAnswer, error) {
	return RewardWriteAnswer{}, nil
}
func (fakeShopDoors) UndoDiscount(context.Context, UndoInput) (RewardWriteAnswer, error) {
	return RewardWriteAnswer{Refused: RewardInUse}, nil
}
func (fakeShopDoors) RewardIssued(context.Context, RewardIssuedRequest) (RewardIssuedAnswer, error) {
	return RewardIssuedAnswer{Found: true}, nil
}
func (fakeShopDoors) SetSubscriptionPlan(context.Context, SubscriptionPlanRequest) (SubscriptionWriteAnswer, error) {
	return SubscriptionWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "p1"}}, nil
}
func (fakeShopDoors) UndoSubscriptionPlan(context.Context, UndoInput) (SubscriptionWriteAnswer, error) {
	return SubscriptionWriteAnswer{}, nil
}
func (f fakeShopDoors) SubscriptionAgreement(context.Context, SubscriptionAgreementRequest) (SubscriptionAgreementAnswer, error) {
	return SubscriptionAgreementAnswer{Found: f.agreement != nil, Agreement: f.agreement}, nil
}
func (fakeShopDoors) SubscriptionAgreements(context.Context, SubscriptionAgreementsRequest) (SubscriptionAgreementsAnswer, error) {
	return SubscriptionAgreementsAnswer{}, nil
}
func (fakeShopDoors) PlaceRenewal(context.Context, SubscriptionRenewalRequest) (SubscriptionRenewalAnswer, error) {
	return SubscriptionRenewalAnswer{WriteAnswer: WriteAnswer{RecordID: "o2"}, Payment: RenewalPaid}, nil
}
func (fakeShopDoors) QuoteRenewal(context.Context, SubscriptionQuoteRequest) (SubscriptionQuoteAnswer, error) {
	return SubscriptionQuoteAnswer{AmountMinor: 100, Currency: "EUR"}, nil
}
func (fakeShopDoors) UndoRenewal(context.Context, UndoInput) (SubscriptionWriteAnswer, error) {
	return SubscriptionWriteAnswer{Refused: SubscriptionRenewalIsPaid}, nil
}
func (fakeShopDoors) StopAgreement(context.Context, SubscriptionStopRequest) (SubscriptionWriteAnswer, error) {
	return SubscriptionWriteAnswer{WriteAnswer: WriteAnswer{RecordID: "a1"}}, nil
}
func (fakeShopDoors) ResumeAgreement(context.Context, UndoInput) (SubscriptionWriteAnswer, error) {
	return SubscriptionWriteAnswer{Refused: SubscriptionMethodForgotten}, nil
}

// Serve finds both doors on a shop that wrote no ServiceMethods, and every answer is checked before it leaves: a
// refusal the contract does not know, an empty write, an agreement that breaks its rules all fail the provider's call.
func TestServeFindsTheRewardsAndSubscriptionsDoors(t *testing.T) {
	sp := serviceProviderOf(fakeShopDoors{agreement: &SubscriptionAgreement{ID: "a1", Status: "lapsed"}})
	if sp == nil {
		t.Fatal("no provider found")
	}
	byName := map[string]ServiceMethod{}
	for _, m := range sp.ServiceMethods() {
		byName[m.Contract+" "+m.Method] = m
	}
	call := func(contract, method string, in any) error {
		t.Helper()
		m, ok := byName[contract+" "+method]
		if !ok {
			t.Fatalf("%s %s is not served", contract, method)
		}
		raw, _ := json.Marshal(in)
		_, err := m.Handle(context.Background(), raw)
		return err
	}
	if err := call(ContractShopRewards, RewardsCreditIssue, RewardCreditRequest{CustomerUserID: "u", AmountMinor: 100, Currency: "EUR", Reason: "points"}); err != nil {
		t.Errorf("a good credit: %v", err)
	}
	if call(ContractShopRewards, RewardsCreditUndo, UndoInput{RecordID: "c1"}) == nil {
		t.Error("a refusal the contract does not know left the shop")
	}
	if call(ContractShopRewards, RewardsDiscountIssue, RewardDiscountRequest{Kind: RewardPercentage, Value: 5, Title: "x"}) == nil {
		t.Error("an answer with no record and no refusal left the shop")
	}
	if call(ContractShopRewards, RewardsIssuedGet, RewardIssuedRequest{RecordID: "c1"}) == nil {
		t.Error("found with nothing found left the shop")
	}
	if call(ContractShopSubscriptions, SubscriptionAgreementGet, SubscriptionAgreementRequest{AgreementID: "a1"}) == nil {
		t.Error("an agreement in no status left the shop")
	}
	if err := call(ContractShopSubscriptions, SubscriptionRenewalPlace, SubscriptionRenewalRequest{AgreementID: "a1", Cycle: 2}); err != nil {
		t.Errorf("a good renewal: %v", err)
	}
	if call(ContractShopSubscriptions, SubscriptionRenewalPlace, SubscriptionRenewalRequest{AgreementID: "a1", Cycle: 1}) == nil {
		t.Error("a renewal of cycle 1 reached the shop")
	}
	if len(byName) != 14 {
		t.Errorf("%d methods served, want 14 (5 rewards + 9 subscriptions)", len(byName))
	}
}
