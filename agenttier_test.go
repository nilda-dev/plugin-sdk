package nilda_test

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

type askedBy struct{ caller string }

func (a *askedBy) ServiceMethods() []nilda.ServiceMethod {
	return []nilda.ServiceMethod{nilda.Serves(nilda.ContractShopCatalog, nilda.CatalogDescribe,
		func(ctx context.Context, _ struct{}) (nilda.CatalogDescription, error) {
			a.caller = nilda.CallOf(ctx).Caller
			return nilda.CatalogDescription{Name: "Kari", Origin: "https://kari.example", Currency: "EUR", Locale: "en",
				ShipsTo: []string{"*"}}, nil
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
	core, _, srv := desk.Core("feed", nilda.ContractShopCatalog)
	defer srv.Close()
	for op, want := range map[string]nilda.AgentTier{"chatgpt.com": nilda.AgentBuy, "ChatGPT.com": nilda.AgentBuy,
		"bad.example": nilda.AgentBlocked, "new.example": nilda.AgentBrowse} {
		if got, err := core.API().AgentTier(ctx, op); err != nil || got != want {
			t.Errorf("%s: %q %v", op, got, err)
		}
	}
	if _, err := core.API().Services().CatalogDescription(ctx); err != nil || shop.caller != "feed" {
		t.Errorf("the provider was told %q asked (%v)", shop.caller, err)
	}
}
