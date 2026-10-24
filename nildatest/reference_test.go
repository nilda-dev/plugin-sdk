package nildatest

import (
	"context"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// The kit answers Reference().PrivacyPolicy and Seller with what the test set, as Core answers them: a site with
// no written policy and no page says so, rather than erroring.
func TestReferencePrivacyPolicyFake(t *testing.T) {
	ctx := context.Background()
	ref := NewReference()
	core, _, srv := ref.Core("shop")
	defer srv.Close()
	api := core.API().Reference()

	p, err := api.PrivacyPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.Published || p.Path != "" {
		t.Errorf("a site with no policy read as %+v", p)
	}
	ref.SetPrivacyPolicy(nilda.PrivacyPolicy{Published: true, Path: "/privacy"})
	ref.SetSeller(nilda.Seller{Name: "Santoor GmbH", Country: "AT"})
	if p, _ = api.PrivacyPolicy(ctx); !p.Published || p.Path != "/privacy" {
		t.Errorf("a published policy read as %+v", p)
	}
	if s, err := api.Seller(ctx); err != nil || s.Name != "Santoor GmbH" || s.Country != "AT" {
		t.Errorf("the seller read as %+v (%v)", s, err)
	}
}
