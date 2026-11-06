package nildatest_test

import (
	"context"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

// THE FAKE LICENCE answers as Core does: none until a test sets one, the state set, a bind refused with the reason set
// (none_free by default) and the licence unchanged, a bind granted with the licence it binds, a failed bind an error —
// and it counts the binds, because "did going live ask for the licence" is the assertion a shop's test wants.
func TestTheFakeLicenceAnswersAsCoreDoes(t *testing.T) {
	ctx := context.Background()
	fake := nildatest.NewLicence()
	core, _, srv := fake.Core("shop", "admin_page")
	defer srv.Close()

	lic, err := core.API().Licence(ctx)
	if err != nil || lic.State != nilda.LicenceNone || lic.TakesRealMoney() {
		t.Fatalf("a fresh fake reads %+v, %v; want none", lic, err)
	}

	paid := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	fake.Set(nilda.Licence{State: nilda.LicenceGrace, Source: "purchase", PaidUntil: paid, LiveUntil: paid.AddDate(0, 0, 14)})
	if lic, err = core.API().Licence(ctx); err != nil || lic.State != nilda.LicenceGrace || !lic.PaidUntil.Equal(paid) ||
		!lic.TakesRealMoney() {
		t.Fatalf("the set licence reads %+v, %v", lic, err)
	}
	fake.Set(nilda.Licence{})
	if lic, err = core.API().Licence(ctx); err != nil || lic.State != nilda.LicenceNone {
		t.Fatalf("a licence set with no state reads %+v, %v; want none", lic, err)
	}

	res, err := core.API().BindLicence(ctx)
	if err != nil || res.Bound || res.Reason != nilda.BindReasonNoneFree || res.Licence.State != nilda.LicenceNone {
		t.Fatalf("a default bind = %+v, %v; want not bound, none_free", res, err)
	}
	fake.BindRefuses(nilda.BindReasonNotConnected)
	if res, err = core.API().BindLicence(ctx); err != nil || res.Bound || res.Reason != nilda.BindReasonNotConnected {
		t.Fatalf("a refused bind = %+v, %v", res, err)
	}
	trial := nilda.Licence{State: nilda.LicenceTrial, Source: "trial", Trial: true, PaidUntil: paid, LiveUntil: paid}
	fake.BindGrants(trial)
	if res, err = core.API().BindLicence(ctx); err != nil || !res.Bound || res.Reason != "" || res.Licence.State != nilda.LicenceTrial {
		t.Fatalf("a granted bind = %+v, %v", res, err)
	}
	if lic, err = core.API().Licence(ctx); err != nil || lic.State != nilda.LicenceTrial || !lic.Trial {
		t.Fatalf("after a granted bind the licence reads %+v, %v", lic, err)
	}
	if fake.Binds() != 3 {
		t.Fatalf("Binds() = %d; want 3", fake.Binds())
	}
	if got := fake.Licence(); got.State != nilda.LicenceTrial {
		t.Fatalf("Licence() = %+v", got)
	}

	// A bind that fails is an error, never bound, and leaves the licence as it was. The client retries Core's 503,
	// and each attempt reached Core, so it counts at least once more.
	fake.BindFails()
	if res, err = core.API().BindLicence(ctx); err == nil || res.Bound {
		t.Fatalf("a failed bind = %+v, %v; want an error", res, err)
	}
	if fake.Binds() <= 3 || fake.Licence().State != nilda.LicenceTrial {
		t.Fatalf("after a failed bind: %d binds, licence %+v", fake.Binds(), fake.Licence())
	}
}
