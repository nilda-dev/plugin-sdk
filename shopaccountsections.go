package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// ANOTHER PLUGIN'S SECTION ON THE BUYER'S ACCOUNT PAGE (shop.account.sections/v1; SHOP_ACCOUNT_SECTIONS.md). EXPERIMENTAL.
//
// A subscriptions plugin shows a buyer their subscriptions and lets them pause or cancel; a loyalty plugin shows the
// points balance, its history and the referral link. Both belong on the account page the buyer already uses, beside
// their orders — not on a bare page of the plugin's own.
//
// WHOSE PAGE IT IS. Core's My Account widget is a mount (COMMERCE.md "Account views"): Core draws the shell, its sign-in
// panel, its profile panel and the account vocabulary; the SHOP draws everything inside it — the menu, the router, every
// view. So the shop is where a section joins the menu, exactly as WooCommerce's My Account takes other plugins' endpoints
// (woocommerce_account_menu_items) while WordPress says who is signed in:
//
//	shop.account.sections/v1  a plugin with a section PROVIDES, the shop USES: describe the sections — a key, the menu's
//	                          words in each language, and the plugin's own route that draws the section.
//
// WHO THE SECTION IS FOR IS NEVER IN A REQUEST. The shop does not render the section and never sends a user id. It draws
// the menu and, for the section's view, a mount of Core's storefront runtime pointing at the provider's own route; the
// buyer's browser fetches that route through Core's proxy, which says who is signed in (CurrentUser: X-Nilda-User, from
// Core's session, every client-sent X-Nilda-* header stripped). So a section answers for exactly the account Core vouches
// for, and a plugin that is asked about somebody else has nothing to read the somebody from. A form in a section posts
// to the provider's own route the same way, under Core's CSRF check.
//
// WHERE THE ROUTE MAY POINT. A section's path is a path under the provider's own route_prefix. Core stamps each desk
// answer with the answering plugin's route_prefix (ServiceAnswer.RoutePrefix, Core's word, never the plugin's), and
// Services.AccountSections drops a section whose path is anywhere else, so no provider can make the shop's page fetch
// Core's API, the shop's own routes or another plugin's.
//
// The owner turns a section on or off and orders it with the shop's own abilities (the page is the shop's); a support
// read of what a buyer's section shows is the providing plugin's own ability, keyed by the buyer the owner names.

// ContractShopAccountSections is what a plugin with a section on the buyer's account page provides to the shop.
const ContractShopAccountSections = "shop.account.sections/v1"

// AccountSectionsDescribe is the one method: the plugin's sections.
const AccountSectionsDescribe = "sections.describe"

// The bounds a section keeps.
const (
	// MaxAccountSections is how many sections one plugin may put on the account page.
	MaxAccountSections = 4
	// MaxAccountSectionLabel is how many characters the menu's words may have, in any language.
	MaxAccountSectionLabel = 40
	// MaxAccountSectionPath is how many bytes a section's route may have.
	MaxAccountSectionPath = 200
	// MaxAccountSectionOrder is the largest position hint a section may ask for.
	MaxAccountSectionOrder = 1000
)

// AccountSectionView and AccountSectionParam name a section on the account page: the shop's view "section", the section
// in "section" as "<plugin>.<key>". Both are the shop's and are never forwarded to the section's route.
const (
	AccountSectionView  = "section"
	AccountSectionParam = "section"
)

// AccountSection is one section a plugin puts on the buyer's account page.
type AccountSection struct {
	// Key names the section within the plugin: lowercase letters, digits and underscores, starting with a letter, at
	// most 32. The shop keeps it as "<plugin>.<key>" (AccountSectionID).
	Key string `json:"key"`
	// Label is the menu's words and the section's heading, per language ("en", "fa", "pt-BR"): one plain line of at
	// most MaxAccountSectionLabel characters each. English is required, so a language the plugin did not write still
	// has words.
	Label map[string]string `json:"label"`
	// Path is the plugin's own route that draws the section for the signed-in buyer, under its route_prefix:
	// "/loyalty/account". A GET answers an HTML fragment wearing AccountVocabulary (WriteAccountSection), or
	// CommerceSignInRequired for a visitor who is not signed in. No query: the shop adds the section's own.
	Path string `json:"path"`
	// Order is where the shop puts the section in its menu until the owner chooses (0 to MaxAccountSectionOrder):
	// the shop's own views stand at 10, 20 … 90, so 0 — or anything above 90 — is after them.
	Order int `json:"order,omitempty"`
}

// AccountSectionsRequest is what the shop tells a provider when it asks for its sections.
type AccountSectionsRequest struct {
	// AccountPage is the path of the site's published account page — the page the My Account widget is on ("/account")
	// — or "" while there is none. A provider keeps the last one it was told, so a link it mails ("your points") opens
	// the account page at its section (AccountSectionPageURL); while it is "", the provider's own bare page answers.
	AccountPage string `json:"account_page,omitempty"`
}

// Validate refuses an account page that is not a plain rooted path.
func (r AccountSectionsRequest) Validate() error {
	if r.AccountPage == "" {
		return nil
	}
	if err := ValidAccountSectionPath(r.AccountPage); err != nil {
		return fmt.Errorf("nilda: the account page: %w", err)
	}
	return nil
}

// AccountSectionPageURL is the address that opens the account page at a section — accountPage with the section's view
// (AccountSectionHref) — or "" while there is no account page. Prefix it with the site's address for a mail.
func AccountSectionPageURL(accountPage, plugin, key string, q url.Values) string {
	if accountPage == "" || ValidAccountSectionPath(accountPage) != nil {
		return ""
	}
	return accountPage + AccountSectionHref(plugin, key, q)
}

// AccountSectionsAnswer is a plugin's answer to sections.describe.
type AccountSectionsAnswer struct {
	Sections []AccountSection `json:"sections"`
}

var (
	accountSectionKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// A path segment: unreserved characters only (RFC 3986 §2.3), so nothing in it is decoded, escaped or reinterpreted
	// on its way through the shop, the browser and Core's proxy.
	accountSectionSegment = regexp.MustCompile(`^[A-Za-z0-9._~-]+$`)
)

// Validate refuses an answer the shop could not draw.
func (a AccountSectionsAnswer) Validate() error {
	if len(a.Sections) > MaxAccountSections {
		return fmt.Errorf("nilda: a plugin puts at most %d sections on the account page", MaxAccountSections)
	}
	seen := map[string]bool{}
	for _, s := range a.Sections {
		if err := s.Validate(); err != nil {
			return err
		}
		if seen[s.Key] {
			return fmt.Errorf("nilda: the account section %q is described twice", s.Key)
		}
		seen[s.Key] = true
	}
	return nil
}

// Validate refuses a section the shop could not draw. It does not know the plugin's route_prefix; the shop's side
// (Services.AccountSections) holds the path to the prefix Core stamped on the answer.
func (s AccountSection) Validate() error {
	if !accountSectionKeyPattern.MatchString(s.Key) {
		return fmt.Errorf("nilda: the account section key %q is lowercase letters, digits and _ , starting with a letter, at most 32", s.Key)
	}
	if err := attributeWords("account section "+s.Key+"'s label", s.Label, MaxAccountSectionLabel, true); err != nil {
		return fmt.Errorf("nilda: %w", err)
	}
	if !hasLanguage(s.Label, "en") {
		return fmt.Errorf("nilda: the account section %q's label has no English words", s.Key)
	}
	if err := ValidAccountSectionPath(s.Path); err != nil {
		return fmt.Errorf("nilda: the account section %q: %w", s.Key, err)
	}
	if s.Order < 0 || s.Order > MaxAccountSectionOrder {
		return fmt.Errorf("nilda: the account section %q's order is 0 to %d", s.Key, MaxAccountSectionOrder)
	}
	return nil
}

func hasLanguage(words map[string]string, base string) bool {
	for tag := range words {
		b, _, _ := strings.Cut(strings.ToLower(tag), "-")
		if b == base {
			return true
		}
	}
	return false
}

// ValidAccountSectionPath says whether p is a route a section may be drawn from: rooted, at most MaxAccountSectionPath
// bytes, every segment unreserved characters with no "." or ".." segment, and no query, fragment or escape.
func ValidAccountSectionPath(p string) error {
	if p == "" || len(p) > MaxAccountSectionPath || p[0] != '/' || p == "/" {
		return fmt.Errorf("the path %q is a rooted route of at most %d bytes", p, MaxAccountSectionPath)
	}
	for _, seg := range strings.Split(p[1:], "/") {
		if seg == "" || seg == "." || seg == ".." || !accountSectionSegment.MatchString(seg) {
			return fmt.Errorf("the path %q has a segment that is empty, a dot, or not plain letters, digits and -._~", p)
		}
	}
	return nil
}

// UnderRoutePrefix says whether path lies under prefix: the prefix itself or below it, never beside it ("/loyalty"
// holds "/loyalty/account", not "/loyaltyx"). An empty prefix — a provider with no routes — holds nothing.
func UnderRoutePrefix(path, prefix string) bool {
	prefix = strings.TrimRight(prefix, "/")
	if prefix == "" || ValidAccountSectionPath(prefix) != nil {
		return false
	}
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// AccountSectionID is the section as the shop names it on the account page: "<plugin>.<key>".
func AccountSectionID(plugin, key string) string { return plugin + "." + key }

// AccountSectionHref is a link from inside a section to another of its own states (a history's second page, a
// subscription's detail), written as the account page writes its views: "?shop=view%3Dsection%26section%3D…" with q
// carried along. Mark it with AccountNavAttr and Core's shell follows it in place; the shop forwards q to the section's
// route. view and section in q are the shop's and are dropped.
//
//	<a class="pb-account-link" data-pb-nav href="{{AccountSectionHref "loyalty" "points" {"page": {"2"}}}}">Older</a>
func AccountSectionHref(plugin, key string, q url.Values) string {
	inner := url.Values{}
	for k, v := range q {
		if k != "view" && k != AccountSectionParam && k != "shop" {
			inner[k] = v
		}
	}
	// view and section first, so the shop reads them before anything a section carries.
	enc := "view=" + AccountSectionView + "&" + AccountSectionParam + "=" + url.QueryEscape(AccountSectionID(plugin, key))
	if rest := inner.Encode(); rest != "" {
		enc += "&" + rest
	}
	return "?shop=" + url.QueryEscape(enc)
}

// WriteAccountSection answers a section's GET (or a form's POST) with its fragment: HTML, never stored by Core, a
// proxy or the browser (Cache-Control: private, no-store; Vary: Cookie), so one buyer's section is never served to
// another. Draw it with AccountVocabulary and no inline colour; mark a part that answers a form for itself with
// data-pb-fragment.
func WriteAccountSection(w http.ResponseWriter, status int, fragment string) {
	AccountSectionHeaders(w)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fragment))
}

// AccountSectionHeaders are the headers WriteAccountSection sets, for a handler that writes its own body.
func AccountSectionHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "private, no-store")
	h.Add("Vary", "Cookie")
	h.Set("X-Content-Type-Options", "nosniff")
}

// AccountSectionsProvider is a plugin with sections on the buyer's account page.
type AccountSectionsProvider interface {
	AccountSections(ctx context.Context, req AccountSectionsRequest) (AccountSectionsAnswer, error)
}

// AccountSectionsMethods are a section provider's methods, each answer checked before it leaves.
func AccountSectionsMethods(p AccountSectionsProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopAccountSections, AccountSectionsDescribe, func(ctx context.Context, req AccountSectionsRequest) (AccountSectionsAnswer, error) {
			if err := req.Validate(); err != nil {
				return AccountSectionsAnswer{}, err
			}
			a, err := p.AccountSections(ctx, req)
			if err != nil {
				return a, err
			}
			if a.Sections == nil {
				a.Sections = []AccountSection{}
			}
			return a, a.Validate()
		}),
	}
}

// ---- the shop's side -----------------------------------------------------------------------------------------------

// ProvidedAccountSection is a section as the shop draws it: the plugin Core says answered, and its section.
type ProvidedAccountSection struct {
	Provider string
	AccountSection
}

// ID is "<plugin>.<key>".
func (s ProvidedAccountSection) ID() string { return AccountSectionID(s.Provider, s.Key) }

// AccountSectionsResult is every section the shop may draw, and the providers whose answer it may not.
type AccountSectionsResult struct {
	Sections []ProvidedAccountSection
	// Dropped are the providers that did not answer, or answered something the shop must not draw — a section whose
	// path is outside its own route_prefix ("path_outside_routes"), or an answer that does not validate ("invalid_answer").
	Dropped []ServiceMiss
	// Cached is true when one answer at least is a provider's last good one, given because it did not answer now.
	Cached bool
}

// AccountSections asks every provider of shop.account.sections/v1 for its sections, telling them where the account page is, holds each to the rules — and each
// section's path to the route_prefix Core stamped on its provider's answer — and answers them by provider and key.
func (s *Services) AccountSections(ctx context.Context, req AccountSectionsRequest) (AccountSectionsResult, error) {
	if err := req.Validate(); err != nil {
		return AccountSectionsResult{}, err
	}
	res, err := s.Call(ctx, ContractShopAccountSections, AccountSectionsDescribe, req, ServiceCallOptions{})
	if err != nil {
		return AccountSectionsResult{}, err
	}
	return AccountSectionsFrom(res), nil
}

// AccountSectionsFrom is AccountSections' reading of a desk result, for a shop that made the call itself.
func AccountSectionsFrom(res ServiceResult) AccountSectionsResult {
	out := AccountSectionsResult{Dropped: append([]ServiceMiss(nil), res.Missed...)}
	for _, a := range res.Answers {
		var ans AccountSectionsAnswer
		if err := json.Unmarshal(a.Output, &ans); err != nil || ans.Validate() != nil {
			out.Dropped = append(out.Dropped, ServiceMiss{Provider: a.Provider, Reason: "invalid_answer"})
			continue
		}
		outside := false
		for _, sec := range ans.Sections {
			if !UnderRoutePrefix(sec.Path, a.RoutePrefix) {
				outside = true
				break
			}
		}
		if outside {
			// The whole answer, not the one section: a provider naming a route it does not own is not one to half-trust.
			out.Dropped = append(out.Dropped, ServiceMiss{Provider: a.Provider, Reason: AccountSectionOutsideRoutes})
			continue
		}
		out.Cached = out.Cached || a.Cached
		for _, sec := range ans.Sections {
			out.Sections = append(out.Sections, ProvidedAccountSection{Provider: a.Provider, AccountSection: sec})
		}
	}
	sort.SliceStable(out.Sections, func(i, j int) bool { return out.Sections[i].ID() < out.Sections[j].ID() })
	return out
}

// AccountSectionOutsideRoutes is the reason a provider's sections were dropped for naming a path it does not own.
const AccountSectionOutsideRoutes = "path_outside_routes"
