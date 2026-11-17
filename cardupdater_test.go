package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAMethodUpdateIsRefusedWithoutWhatCoreNeeds(t *testing.T) {
	ok := PaymentMethodUpdate{Token: "cus_1/pm_1", Label: "Visa •••• 4242, 11/30", Source: MethodUpdatedByNetwork, EventID: "evt_1"}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a whole update: %v", err)
	}
	for name, mutate := range map[string]func(*PaymentMethodUpdate){
		"no token":       func(u *PaymentMethodUpdate) { u.Token = "" },
		"no label":       func(u *PaymentMethodUpdate) { u.Label = " " },
		"unknown source": func(u *PaymentMethodUpdate) { u.Source = "buyer" },
		"no event":       func(u *PaymentMethodUpdate) { u.EventID = "" },
		"a NUL":          func(u *PaymentMethodUpdate) { u.EventID = "evt\x00" },
		"a long event":   func(u *PaymentMethodUpdate) { u.EventID = strings.Repeat("e", 256) },
	} {
		u := ok
		mutate(&u)
		if u.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if s := fmt.Sprintf("%v %#v %s", ok, ok, ok); strings.Contains(s, "cus_1") {
		t.Fatalf("an update printed its token: %s", s)
	}
}

func TestAnOnSessionPaymentOfACycleIsBoundedAsCoresIs(t *testing.T) {
	base := PaymentSessionParams{Gateway: "g", Method: "card", Reference: "o", AmountMinor: 100, Currency: "EUR",
		ReturnURL: "https://site.test/order"}
	good := base
	good.Agreement, good.Cycle = "agr-1", 3
	if err := good.Validate(); err != nil {
		t.Fatalf("a payment of a cycle: %v", err)
	}
	for name, mutate := range map[string]func(*PaymentSessionParams){
		"a cycle with no agreement": func(p *PaymentSessionParams) { p.Agreement = "" },
		"no cycle":                  func(p *PaymentSessionParams) { p.Cycle = 0 },
		"a cycle past the bound":    func(p *PaymentSessionParams) { p.Cycle = MaxPaymentCycle + 1 },
		"a bad agreement":           func(p *PaymentSessionParams) { p.Agreement = "-x" },
		"one that keeps a method": func(p *PaymentSessionParams) {
			p.Save = &PaymentMandate{Purpose: MandateSubscription, Text: "t", AmountMinor: 1, Currency: "EUR", Interval: IntervalMonth, IntervalCount: 1}
		},
		"one at a reader": func(p *PaymentSessionParams) { p.Reader = "tmr_1" },
	} {
		p := good
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestABuyersLinkIsAnHTTPSURLThatExpiresOrAKnownRefusal(t *testing.T) {
	at := time.Now().Add(time.Hour)
	for name, c := range map[string]struct {
		a  SubscriptionLinkAnswer
		ok bool
	}{
		"a link":                 {SubscriptionLinkAnswer{URL: "https://shop.test/order/l/x", ExpiresAt: &at}, true},
		"a refusal":              {SubscriptionLinkAnswer{Refused: SubscriptionNoAccount}, true},
		"a link with no expiry":  {SubscriptionLinkAnswer{URL: "https://shop.test/x"}, false},
		"an http link":           {SubscriptionLinkAnswer{URL: "http://shop.test/x", ExpiresAt: &at}, false},
		"a relative link":        {SubscriptionLinkAnswer{URL: "/x", ExpiresAt: &at}, false},
		"a refusal with a link":  {SubscriptionLinkAnswer{Refused: SubscriptionCyclePaid, URL: "https://shop.test/x"}, false},
		"an unknown refusal":     {SubscriptionLinkAnswer{Refused: "nope"}, false},
		"neither link nor no":    {SubscriptionLinkAnswer{}, false},
		"a link of 3000 bytes":   {SubscriptionLinkAnswer{URL: "https://shop.test/" + strings.Repeat("a", 3000), ExpiresAt: &at}, false},
		"refused renewal_closed": {SubscriptionLinkAnswer{Refused: SubscriptionRenewalClosed}, true},
	} {
		if err := c.a.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: Validate = %v", name, err)
		}
	}
	if (SubscriptionPayLinkRequest{AgreementID: "a", Cycle: 1}).Validate() == nil {
		t.Error("a pay link for cycle 1, the order that started it, was accepted")
	}
	if (SubscriptionMethodLinkRequest{}).Validate() == nil {
		t.Error("a method link for no agreement was accepted")
	}
}

type linkShop struct{ shopForSubscriptions }

type shopForSubscriptions struct{ SubscriptionsProvider }

func (linkShop) RenewalPayLink(context.Context, SubscriptionPayLinkRequest) (SubscriptionLinkAnswer, error) {
	return SubscriptionLinkAnswer{Refused: SubscriptionCyclePaid}, nil
}

func (linkShop) MethodUpdateLink(context.Context, SubscriptionMethodLinkRequest) (SubscriptionLinkAnswer, error) {
	return SubscriptionLinkAnswer{URL: "http://not-https.test"}, nil
}

// A shop answers the two links only when it implements SubscriptionLinksProvider, and its answers are checked.
func TestTheLinkDoorsAreServedOnlyByAShopThatAnswersThem(t *testing.T) {
	names := func(ms []ServiceMethod) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Method)
		}
		return out
	}
	plain := names(SubscriptionsMethods(shopForSubscriptions{}))
	if slices.Contains(plain, SubscriptionRenewalPayLink) || slices.Contains(plain, SubscriptionMethodLink) {
		t.Fatalf("a shop without the links serves them: %v", plain)
	}
	var pay, method ServiceMethod
	for _, m := range SubscriptionsMethods(linkShop{}) {
		switch m.Method {
		case SubscriptionRenewalPayLink:
			pay = m
		case SubscriptionMethodLink:
			method = m
		}
	}
	if pay.Method == "" || method.Method == "" {
		t.Fatal("a shop with the links does not serve them")
	}
	ctx := context.Background()
	if _, err := pay.Handle(ctx, json.RawMessage(`{"agreement_id":"a","cycle":1}`)); err == nil {
		t.Error("a pay link for cycle 1 reached the shop")
	}
	if out, err := pay.Handle(ctx, json.RawMessage(`{"agreement_id":"a","cycle":2}`)); err != nil || !strings.Contains(string(mustJSON(t, out)), SubscriptionCyclePaid) {
		t.Errorf("the pay link's refusal: %s, %v", mustJSON(t, out), err)
	}
	if _, err := method.Handle(ctx, json.RawMessage(`{"agreement_id":"a"}`)); err == nil {
		t.Error("a shop's http link was passed on")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

type plainConsumerBase struct{}

func (plainConsumerBase) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (plainConsumerBase) HandleHook(_ context.Context, _ string, p []byte) ([]byte, error) {
	return p, nil
}
func (plainConsumerBase) HandleEvent(context.Context, string, []byte) error { return nil }
func (plainConsumerBase) ConfirmPayment(context.Context, PaymentSession) (PaymentConfirmResult, error) {
	return PaymentConfirmResult{Proceed: true}, nil
}
func (plainConsumerBase) PaymentUpdated(context.Context, PaymentSession) error { return nil }
func (plainConsumerBase) RefundUpdated(context.Context, PaymentRefund, PaymentSession) error {
	return nil
}

type plainConsumer struct{ plainConsumerBase }

type watcherConsumer struct {
	plainConsumerBase
	heard []PaymentSavedUpdate
}

func (w *watcherConsumer) SavedMethodUpdated(_ context.Context, u PaymentSavedUpdate) error {
	w.heard = append(w.heard, u)
	return nil
}

func TestTheSavedMethodHookReachesOnlyAWatcher(t *testing.T) {
	ctx := context.Background()
	w := &watcherConsumer{}
	raw := mustJSON(t, PaymentSavedUpdate{Method: SavedPaymentMethod{ID: "m1", Label: "Visa"}, PreviousLabel: "Visa old", Source: MethodUpdatedByNetwork})
	if out, handled, err := DispatchPaymentConsumerHook(ctx, nil, w, HookPaymentSavedUpdated, raw); err != nil || !handled || string(out) != `{}` ||
		len(w.heard) != 1 || w.heard[0].PreviousLabel != "Visa old" {
		t.Fatalf("a watcher: %s %v %v %+v", out, handled, err, w.heard)
	}
	if _, _, err := DispatchPaymentConsumerHook(ctx, nil, w, HookPaymentSavedUpdated, []byte(`{"method":{}}`)); err == nil {
		t.Error("an update naming no method was handed on")
	}
	if _, handled, _ := DispatchPaymentConsumerHook(ctx, nil, plainConsumer{}, HookPaymentSavedUpdated, raw); handled {
		t.Error("a consumer that does not watch was handed the update")
	}
	if got := withProvidedHooks(w, nil); !slices.Contains(got, HookPaymentSavedUpdated) {
		t.Errorf("a watcher is not subscribed to %s: %v", HookPaymentSavedUpdated, got)
	}
	if got := withProvidedHooks(plainConsumer{}, nil); slices.Contains(got, HookPaymentSavedUpdated) {
		t.Errorf("a consumer that does not watch is subscribed to %s: %v", HookPaymentSavedUpdated, got)
	}
}
