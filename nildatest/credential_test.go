package nildatest

import (
	"context"
	"fmt"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// An agent paying for its buyer (v0.20.0): the credential reaches a gateway that charges one, once, on a method that
// takes its type; the session ends resolved with no page to send anyone to; a method that does not take it is
// refused before any gateway is asked.
func TestADelegatedCredentialIsChargedOnTheSpot(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	spt := &nilda.PaymentCredential{Type: nilda.CredentialStripeSPT, Token: "spt_test_123"}

	refused := params()
	refused.Credential = spt
	if _, err := w.shopAPI.CreateSession(ctx, "k-refused", refused); err == nil || httpStatus(t, err) != 422 {
		t.Fatalf("a method that takes no credential: %v", err)
	}
	if len(w.gw.Charged) != 0 {
		t.Fatal("the gateway was asked")
	}

	w.gw.Methods = []nilda.PaymentMethod{{Key: "card", Label: "Card", Refunds: true, Test: true,
		CredentialTypes: []string{nilda.CredentialStripeSPT}}}
	p := params()
	p.Credential = spt
	s, err := w.shopAPI.CreateSession(ctx, "k-agent", p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != nilda.PaymentResolved || s.RedirectURL != "" || len(w.gw.Charged) != 1 || w.gw.Charged[0].Token != "spt_test_123" {
		t.Fatalf("the session: %+v, charged %v", s, w.gw.Charged)
	}
}

// A credential printed by mistake shows its type and never its token.
func TestACredentialNeverPrintsItsToken(t *testing.T) {
	c := nilda.PaymentCredential{Type: nilda.CredentialStripeSPT, Token: "spt_secret"}
	for _, out := range []string{c.String(), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c), fmt.Sprintf("%#v", c)} {
		if strings.Contains(out, "spt_secret") {
			t.Errorf("printed: %s", out)
		}
	}
}
