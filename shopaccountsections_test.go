package nilda

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func points() AccountSection {
	return AccountSection{Key: "points", Label: map[string]string{"en": "Points", "fa": "امتیازها"}, Path: "/loyalty/account"}
}

// A section the shop could not draw — or could be led astray by — is refused before it leaves the plugin.
func TestAnAccountSectionIsHeldToItsRules(t *testing.T) {
	if err := (AccountSectionsAnswer{Sections: []AccountSection{points()}}).Validate(); err != nil {
		t.Fatalf("a good section is refused: %v", err)
	}
	bad := map[string]func(s *AccountSection){
		"an uppercase key":       func(s *AccountSection) { s.Key = "Points" },
		"no label":               func(s *AccountSection) { s.Label = nil },
		"no English":             func(s *AccountSection) { s.Label = map[string]string{"fa": "امتیازها"} },
		"a long label":           func(s *AccountSection) { s.Label["en"] = strings.Repeat("a", MaxAccountSectionLabel+1) },
		"a label of two lines":   func(s *AccountSection) { s.Label["en"] = "Points\nnow" },
		"a relative path":        func(s *AccountSection) { s.Path = "loyalty/account" },
		"a scheme-relative path": func(s *AccountSection) { s.Path = "//evil.example/x" },
		"a full URL":             func(s *AccountSection) { s.Path = "https://evil.example/x" },
		"a dot-dot":              func(s *AccountSection) { s.Path = "/loyalty/../api/v1/account" },
		"an escape":              func(s *AccountSection) { s.Path = "/loyalty/%2e%2e/api" },
		"a query":                func(s *AccountSection) { s.Path = "/loyalty/account?user=1" },
		"a backslash":            func(s *AccountSection) { s.Path = `/loyalty\account` },
		"the root":               func(s *AccountSection) { s.Path = "/" },
		"an order out of bounds": func(s *AccountSection) { s.Order = MaxAccountSectionOrder + 1 },
		"a negative order":       func(s *AccountSection) { s.Order = -1 },
	}
	for name, spoil := range bad {
		s := points()
		s.Label = map[string]string{"en": "Points", "fa": "امتیازها"}
		spoil(&s)
		if err := s.Validate(); err == nil {
			t.Errorf("%s was accepted: %+v", name, s)
		}
	}
	five := AccountSectionsAnswer{}
	for i := 0; i < MaxAccountSections+1; i++ {
		s := points()
		s.Key = string(rune('a' + i))
		five.Sections = append(five.Sections, s)
	}
	if five.Validate() == nil {
		t.Error("more sections than MaxAccountSections were accepted")
	}
	if (AccountSectionsAnswer{Sections: []AccountSection{points(), points()}}).Validate() == nil {
		t.Error("a section described twice was accepted")
	}
}

// A path lies under a prefix or it does not: "/loyaltyx" is beside "/loyalty", and an empty prefix holds nothing.
func TestUnderRoutePrefixIsBelowNotBeside(t *testing.T) {
	for _, c := range []struct {
		path, prefix string
		want         bool
	}{
		{"/loyalty/account", "/loyalty", true},
		{"/loyalty", "/loyalty", true},
		{"/loyalty/account", "/loyalty/", true},
		{"/loyaltyx/account", "/loyalty", false},
		{"/shop/account", "/loyalty", false},
		{"/loyalty/account", "", false},
		{"/loyalty/account", "/", false},
	} {
		if got := UnderRoutePrefix(c.path, c.prefix); got != c.want {
			t.Errorf("UnderRoutePrefix(%q, %q) = %v", c.path, c.prefix, got)
		}
	}
}

// The shop draws only sections whose path is under the route_prefix Core stamped on their provider's answer; a provider
// that names another plugin's route, the shop's or Core's API loses its whole answer, and the shop is told why.
func TestTheShopDrawsOnlyRoutesTheProviderOwns(t *testing.T) {
	own, _ := json.Marshal(AccountSectionsAnswer{Sections: []AccountSection{points()}})
	stray := points()
	stray.Key, stray.Path = "credit", "/shop/account"
	strays, _ := json.Marshal(AccountSectionsAnswer{Sections: []AccountSection{points(), stray}})
	res := AccountSectionsFrom(ServiceResult{Answers: []ServiceAnswer{
		{Provider: "loyalty", Output: own, RoutePrefix: "/loyalty"},
		{Provider: "rogue", Output: strays, RoutePrefix: "/loyalty"},
		{Provider: "older-core", Output: own},
		{Provider: "broken", Output: json.RawMessage(`{"sections":[{"key":"X"}]}`), RoutePrefix: "/broken"},
	}, Missed: []ServiceMiss{{Provider: "slow", Reason: "timeout"}}})
	if len(res.Sections) != 1 || res.Sections[0].ID() != "loyalty.points" {
		t.Fatalf("sections drawn: %+v", res.Sections)
	}
	why := map[string]string{}
	for _, d := range res.Dropped {
		why[d.Provider] = d.Reason
	}
	want := map[string]string{"rogue": AccountSectionOutsideRoutes, "older-core": AccountSectionOutsideRoutes,
		"broken": "invalid_answer", "slow": "timeout"}
	for p, r := range want {
		if why[p] != r {
			t.Errorf("%s dropped as %q, want %q (all: %+v)", p, why[p], r, res.Dropped)
		}
	}
}

// A provider's describe answers through the methods the SDK registers for it, and an answer it should not send is its
// own failure, never the shop's to draw.
func TestAnAccountSectionsProviderIsAnsweredAndChecked(t *testing.T) {
	good := sectionsFunc(func() AccountSectionsAnswer { return AccountSectionsAnswer{Sections: []AccountSection{points()}} })
	out, ok, err := DispatchServiceHook(context.Background(), methodsProvider(AccountSectionsMethods(good)), ServiceHook(ContractShopAccountSections, AccountSectionsDescribe), []byte(`{}`))
	if !ok || err != nil || !strings.Contains(string(out), `"/loyalty/account"`) {
		t.Fatalf("describe answered %s, %v, %v", out, ok, err)
	}
	none := sectionsFunc(func() AccountSectionsAnswer { return AccountSectionsAnswer{} })
	if out, _, err := DispatchServiceHook(context.Background(), methodsProvider(AccountSectionsMethods(none)), ServiceHook(ContractShopAccountSections, AccountSectionsDescribe), []byte(`{}`)); err != nil || string(out) != `{"sections":[]}` {
		t.Fatalf("no sections answered %s, %v", out, err)
	}
	bad := sectionsFunc(func() AccountSectionsAnswer {
		s := points()
		s.Path = "/api/v1/account/../me"
		return AccountSectionsAnswer{Sections: []AccountSection{s}}
	})
	if _, _, err := DispatchServiceHook(context.Background(), methodsProvider(AccountSectionsMethods(bad)), ServiceHook(ContractShopAccountSections, AccountSectionsDescribe), []byte(`{}`)); err == nil {
		t.Fatal("a section with a traversing path left the plugin")
	}
	c, ok := ContractNamed(ContractShopAccountSections)
	if !ok || !c.Experimental() {
		t.Fatalf("the contract is not published as experimental: %+v %v", c, ok)
	}
	if m, ok := c.Method(AccountSectionsDescribe); !ok || m.Fallback != "cached" || m.Agent != "none" {
		t.Fatalf("describe's row: %+v", m)
	}
}

type sectionsFunc func() AccountSectionsAnswer

func (f sectionsFunc) AccountSections(context.Context, AccountSectionsRequest) (AccountSectionsAnswer, error) {
	return f(), nil
}

// A link inside a section is the account page's own view of it, carrying the section's query and never one that names
// another view or section.
func TestAnAccountSectionLinkIsTheAccountPagesView(t *testing.T) {
	href := AccountSectionHref("loyalty", "points", url.Values{"page": {"2"}, "view": {"orders"}, "section": {"other.x"}})
	shop, err := url.ParseQuery(strings.TrimPrefix(href, "?"))
	if err != nil {
		t.Fatal(err)
	}
	inner, err := url.ParseQuery(shop.Get("shop"))
	if err != nil {
		t.Fatal(err)
	}
	if inner.Get("view") != AccountSectionView || inner.Get(AccountSectionParam) != "loyalty.points" || inner.Get("page") != "2" ||
		len(inner["view"]) != 1 || len(inner[AccountSectionParam]) != 1 {
		t.Fatalf("the link %s reads %v", href, inner)
	}
	if !strings.HasPrefix(shop.Get("shop"), "view=section&section=loyalty.points") {
		t.Errorf("view and section are not first: %s", shop.Get("shop"))
	}
}

// A section's answer is never stored where another buyer could be served it.
func TestASectionIsNeverStored(t *testing.T) {
	w := httptest.NewRecorder()
	WriteAccountSection(w, 200, `<p class="pb-account-row">12 points</p>`)
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") || !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control = %q", cc)
	}
	if w.Header().Get("Vary") != "Cookie" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Errorf("headers = %v", w.Header())
	}
}

// The account page a provider is told is a plain rooted path or nothing, and a mailed link opens it at the section.
func TestAnAccountSectionOpensOnTheAccountPage(t *testing.T) {
	if AccountSectionPageURL("", "loyalty", "points", nil) != "" {
		t.Error("a link was made with no account page")
	}
	if got := AccountSectionPageURL("/account", "loyalty", "points", nil); got != "/account?shop=view%3Dsection%26section%3Dloyalty.points" {
		t.Errorf("the link is %q", got)
	}
	for _, bad := range []string{"//evil.example", "https://evil.example/account", "/account?x=1", "/a/../b"} {
		if (AccountSectionsRequest{AccountPage: bad}).Validate() == nil || AccountSectionPageURL(bad, "loyalty", "points", nil) != "" {
			t.Errorf("the account page %q was taken", bad)
		}
	}
	out, _, err := DispatchServiceHook(context.Background(), methodsProvider(AccountSectionsMethods(sectionsFunc(func() AccountSectionsAnswer { return AccountSectionsAnswer{} }))),
		ServiceHook(ContractShopAccountSections, AccountSectionsDescribe), []byte(`{"account_page":"//evil.example"}`))
	if err == nil {
		t.Fatalf("a describe naming a stray account page was answered: %s", out)
	}
}
