package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type holder struct {
	list   []Obligation
	left   []Obligation
	err    error
	asked  []WindDownRequest
	locale string
}

func (h *holder) Obligations(_ context.Context, req ObligationsRequest) ([]Obligation, error) {
	h.locale = req.Locale
	return h.list, h.err
}

func (h *holder) WindDown(_ context.Context, req WindDownRequest) (WindDownResult, error) {
	h.asked = append(h.asked, req)
	return WindDownResult{Remaining: h.left, Told: int64(len(h.list))}, h.err
}

func subs() Obligation {
	return Obligation{Kind: "active_subscriptions", Count: 12, Summary: "12 subscriptions renew monthly",
		WindDown: "renewals stop; each subscriber is mailed that it ends"}
}

// DOOR D11: the list hook answers what is live, checked before Core sees it; nil is an empty list, never null.
func TestTheObligationsListIsAnsweredAndChecked(t *testing.T) {
	ctx := context.Background()
	h := &holder{list: []Obligation{subs()}}
	out, handled, err := DispatchObligations(ctx, h, ObligationsListHook, []byte(`{"locale":"de"}`))
	if !handled || err != nil {
		t.Fatalf("handled %v, %v", handled, err)
	}
	var ans ObligationsAnswer
	if err := json.Unmarshal(out, &ans); err != nil || len(ans.Obligations) != 1 || ans.Obligations[0].Count != 12 {
		t.Fatalf("the answer: %s, %v", out, err)
	}
	if h.locale != "de" {
		t.Fatalf("the locale did not reach the plugin: %q", h.locale)
	}

	h.list = nil
	out, _, err = DispatchObligations(ctx, h, ObligationsListHook, nil)
	if err != nil || string(out) != `{"obligations":[]}` {
		t.Fatalf("none held: %s, %v", out, err)
	}

	for name, bad := range map[string]Obligation{
		"a zero count":       {Kind: "x", Count: 0, Summary: "s", WindDown: "w"},
		"an upper-case kind": {Kind: "Subs", Count: 1, Summary: "s", WindDown: "w"},
		"no summary":         {Kind: "x", Count: 1, Summary: " ", WindDown: "w"},
		"no wind-down":       {Kind: "x", Count: 1, Summary: "s"},
		"an amount alone":    {Kind: "x", Count: 1, Summary: "s", WindDown: "w", AmountMinor: 5},
		"a long summary":     {Kind: "x", Count: 1, Summary: strings.Repeat("a", MaxObligationText+1), WindDown: "w"},
		"a bad currency":     {Kind: "x", Count: 1, Summary: "s", WindDown: "w", AmountMinor: 5, Currency: "eur"},
	} {
		h.list = []Obligation{bad}
		if _, _, err := DispatchObligations(ctx, h, ObligationsListHook, nil); err == nil {
			t.Errorf("%s was answered", name)
		}
	}
	h.list = []Obligation{subs(), subs()}
	if _, _, err := DispatchObligations(ctx, h, ObligationsListHook, nil); err == nil {
		t.Error("one kind listed twice was answered")
	}
	h.list, h.err = nil, errors.New("the database blinked")
	if _, _, err := DispatchObligations(ctx, h, ObligationsListHook, nil); err == nil {
		t.Error("an error that could not count was answered as none held")
	}
}

// DOOR D11: the wind-down carries its reason, refuses one it does not know, and answers what remains.
func TestAWindDownCarriesItsReasonAndAnswersWhatRemains(t *testing.T) {
	ctx := context.Background()
	left := subs()
	left.Count, left.Summary = 1, "1 renewal is already at the processor"
	h := &holder{list: []Obligation{subs()}, left: []Obligation{left}}
	out, handled, err := DispatchObligations(ctx, h, ObligationsWindDownHook, []byte(`{"reason":"uninstall","locale":"fa","for":"subscriptions"}`))
	if !handled || err != nil {
		t.Fatalf("handled %v, %v", handled, err)
	}
	var res WindDownResult
	if err := json.Unmarshal(out, &res); err != nil || len(res.Remaining) != 1 || res.Told != 1 {
		t.Fatalf("the result: %s, %v", out, err)
	}
	if len(h.asked) != 1 || h.asked[0].Reason != WindDownUninstall || h.asked[0].Locale != "fa" || h.asked[0].For != "subscriptions" {
		t.Fatalf("the plugin was asked %+v", h.asked)
	}
	if _, _, err := DispatchObligations(ctx, h, ObligationsWindDownHook, []byte(`{"reason":"because"}`)); err == nil {
		t.Error("an unknown reason was wound down")
	}
	if len(h.asked) != 1 {
		t.Error("the plugin was asked to wind down for an unknown reason")
	}
	h.left = nil
	out, _, err = DispatchObligations(ctx, h, ObligationsWindDownHook, []byte(`{"reason":"disable"}`))
	if err != nil || !strings.Contains(string(out), `"remaining":[]`) {
		t.Fatalf("nothing left: %s, %v", out, err)
	}
	if _, handled, _ := DispatchObligations(ctx, h, "content.saved", nil); handled {
		t.Error("another hook was handled")
	}
}
