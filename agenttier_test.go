package nilda_test

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

type askedBy struct{ caller string }

func (a *askedBy) ServiceMethods() []nilda.ServiceMethod {
	return []nilda.ServiceMethod{nilda.Serves(nilda.ContractShopCheckout, nilda.CheckoutDescribe,
		func(ctx context.Context, _ struct{}) (nilda.CheckoutDescription, error) {
			a.caller = nilda.CallOf(ctx).Caller
			return nilda.CheckoutDescription{Name: "Kari", Origin: "https://kari.example", Currency: "EUR", Locale: "en",
				CredentialTypes: []string{}}, nil
		})}
}

// A plugin asks Core what the owner lets an operator do; an operator never named browses; a provider is told which
// plugin asked (CallOf), so a shop need not trust what a calling plugin claims.
func TestAPluginReadsAnOperatorsTierAndAProviderKnowsWhoAsked(t *testing.T) {
	ctx := context.Background()
	desk := nildatest.NewServices()
	desk.SetAgentTier("chatgpt.com", nilda.AgentBuy)
	desk.SetAgentTier("bad.example", nilda.AgentBlocked)
	shop := &askedBy{}
	desk.Provider("shop", shop)
	core, _, srv := desk.Core("acp", nilda.ContractShopCheckout)
	defer srv.Close()
	for op, want := range map[string]nilda.AgentTier{"chatgpt.com": nilda.AgentBuy, "ChatGPT.com": nilda.AgentBuy,
		"bad.example": nilda.AgentBlocked, "new.example": nilda.AgentBrowse} {
		if got, err := core.API().AgentTier(ctx, op); err != nil || got != want {
			t.Errorf("%s: %q %v", op, got, err)
		}
	}
	if _, err := core.API().Services().CheckoutDescription(ctx); err != nil || shop.caller != "acp" {
		t.Errorf("the provider was told %q asked (%v)", shop.caller, err)
	}
}
