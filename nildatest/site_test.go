package nildatest

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	nilda "github.com/nilda-dev/plugin-sdk"
)

func apiStatus(err error) int {
	var ae *nilda.APIError
	if errors.As(err, &ae) {
		return ae.Status
	}
	return 0
}

// Accounts, the site and its published pages read back through the typed client as the test set them; a read the
// capabilities do not cover is refused until it is granted.
func TestSiteAnswersAccountsAndPages(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	site.SetSite(nilda.Site{Title: "Santoor", BaseURL: "https://santoor.example", Locale: "de"})
	owner := site.AddUser(nilda.User{Email: "owner@example.com"}, "admin")
	member := site.AddUser(nilda.User{Email: "ana@example.com", Status: nilda.UserStatusPending}, "subscriber")
	core, host, srv := site.Core("shop")
	defer srv.Close()
	api := core.API()

	if _, err := api.User(ctx, owner); apiStatus(err) != http.StatusForbidden {
		t.Fatalf("a plugin without users.read read an account: %v", err)
	}
	host.Grant(nilda.CapUsersRead)
	u, err := api.User(ctx, owner)
	if err != nil || u.Email != "owner@example.com" || u.Status != nilda.UserStatusActive {
		t.Fatalf("the owner read as %+v (%v)", u, err)
	}
	all, err := api.Users(ctx, nilda.UserQuery{})
	if err != nil || len(all) != 2 || all[0].ID != member {
		t.Errorf("the accounts read as %+v (%v), want the newest first", all, err)
	}
	admins, err := api.Users(ctx, nilda.UserQuery{Role: "admin", Status: nilda.UserStatusActive})
	if err != nil || len(admins) != 1 || admins[0].ID != owner {
		t.Errorf("the active admins read as %+v (%v)", admins, err)
	}
	site.RemoveUser(member)
	if _, err := api.User(ctx, member); apiStatus(err) != http.StatusNotFound {
		t.Errorf("an erased account answered %v, want 404", err)
	}
	if _, err := api.User(ctx, "u1"); apiStatus(err) != http.StatusUnprocessableEntity {
		t.Errorf("an id that is not a uuid answered %v, want 422 as Core parses it", err)
	}

	if s, err := api.Site(ctx); err != nil || s.BaseURL != "https://santoor.example" {
		t.Errorf("the site read as %+v (%v)", s, err)
	}
	terms := site.AddPage(SitePage{Title: "AGB", Slug: "agb", Body: nilda.TextBody("Unsere Bedingungen."),
		HeadMeta: &nilda.HeadMeta{Canonical: "https://santoor.example/agb"}})
	site.AddPage(SitePage{Title: "Draft", Slug: "draft", Status: "draft"})
	p, err := api.PageBySlug(ctx, "agb", "page")
	if err != nil || p.ID != terms || p.HeadMeta == nil || p.HeadMeta.Canonical != "https://santoor.example/agb" ||
		nilda.BodyText(p.Body) != "Unsere Bedingungen." {
		t.Errorf("the published page read as %+v (%v)", p, err)
	}
	if _, err := api.PageBySlug(ctx, "draft", ""); apiStatus(err) != http.StatusNotFound {
		t.Errorf("a draft was found by its slug: %v", err)
	}
	if _, err := api.PageBySlug(ctx, "agb", "product"); apiStatus(err) != http.StatusNotFound {
		t.Errorf("the page was found as a product: %v", err)
	}
	crumbs, err := api.Breadcrumbs(ctx, terms)
	if err != nil || len(crumbs) != 2 || crumbs[1].URL != "/agb" {
		t.Errorf("the trail read as %+v (%v)", crumbs, err)
	}
}

// Terms: a plugin's taxonomy merged over what a person filed, Core's full-replace PUT taking everything else off, the
// slug Core makes unique, and a term's items with its children's.
func TestSiteTermsAreReplacedAsCoreReplacesThem(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	site.AddTaxonomy("product_category")
	sale := site.AddTerm("tag", nilda.NewTerm{Name: "Sale"})
	kettle := site.AddPage(SitePage{Type: "product", Title: "Copper kettle"})
	site.FileUnder(kettle, sale.ID)
	core, host, srv := site.Core("shop", nilda.CapTaxonomyRead)
	defer srv.Close()
	api := core.API()

	if _, err := api.CreateTerm(ctx, "product_category", nilda.NewTerm{Name: "Kitchen"}); apiStatus(err) != http.StatusForbidden {
		t.Fatalf("a plugin with taxonomy.read made a term: %v", err)
	}
	host.Grant(nilda.CapTaxonomyWrite)

	kitchen, err := api.CreateTerm(ctx, "product_category", nilda.NewTerm{Name: "Kitchen"})
	if err != nil {
		t.Fatal(err)
	}
	knives, _ := api.CreateTerm(ctx, "product_category", nilda.NewTerm{Name: "Knives", ParentID: kitchen.ID})
	again, _ := api.CreateTerm(ctx, "product_category", nilda.NewTerm{Name: "Kitchen"})
	if again.Slug != "kitchen-2" || again.ID == kitchen.ID {
		t.Errorf("a second Kitchen was made as %+v, want Core's unique slug", again)
	}
	if _, err := api.CreateTerm(ctx, "no_such", nilda.NewTerm{Name: "X"}); apiStatus(err) != http.StatusNotFound {
		t.Errorf("a term was made in a taxonomy the site lacks: %v", err)
	}
	if _, err := api.CreateTerm(ctx, "product_category", nilda.NewTerm{Name: " "}); apiStatus(err) != http.StatusUnprocessableEntity {
		t.Errorf("a term with no name answered %v, want 422", err)
	}

	if err := api.SetContentTerms(ctx, "product", kettle, "product_category", []string{knives.ID}); err != nil {
		t.Fatal(err)
	}
	got, _ := api.ContentTerms(ctx, "product", kettle)
	if len(got["tag"]) != 1 || got["tag"][0].ID != sale.ID || len(got["product_category"]) != 1 ||
		got["product_category"][0].ID != knives.ID {
		t.Errorf("after the merge the item carries %+v, want the person's tag kept beside the category", got)
	}
	// Core's own PUT, sent without the merge, takes the tag off: the reason SetContentTerms reads first.
	if err := api.JSON(ctx, http.MethodPut, "/content/product/"+kettle+"/terms",
		map[string]any{"term_ids": []string{kitchen.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if got := site.Terms(kettle); len(got["tag"]) != 0 || len(got["product_category"]) != 1 {
		t.Errorf("a full-replace PUT left %+v, want only the category it sent", got)
	}
	if err := api.JSON(ctx, http.MethodPut, "/content/product/"+kettle+"/terms",
		map[string]any{"term_ids": []string{newID()}}, nil); apiStatus(err) != http.StatusUnprocessableEntity {
		t.Errorf("a term that does not exist was filed: %v", err)
	}
	if _, err := api.ContentTerms(ctx, "page", kettle); apiStatus(err) != http.StatusNotFound {
		t.Errorf("the product's terms were read as a page's: %v", err)
	}

	site.FileUnder(kettle, knives.ID)
	draft := site.AddPage(SitePage{Type: "product", Title: "Unreleased", Status: "draft"})
	site.FileUnder(draft, knives.ID)
	direct, total, err := api.TermContent(ctx, knives.ID, nilda.TermContentQuery{})
	if err != nil || total != 1 || len(direct) != 1 || direct[0].ID != kettle {
		t.Errorf("the knives list %+v (total %d, %v), want the published kettle only", direct, total, err)
	}
	if err := api.JSON(ctx, http.MethodPut, "/content/product/"+kettle+"/terms",
		map[string]any{"term_ids": []string{knives.ID}}, nil); err != nil {
		t.Fatal(err)
	}
	if items, _, _ := api.TermContent(ctx, kitchen.ID, nilda.TermContentQuery{}); len(items) != 0 {
		t.Errorf("the kitchen lists %+v without its children asked for", items)
	}
	if items, _, _ := api.TermContent(ctx, kitchen.ID, nilda.TermContentQuery{IncludeChildren: true}); len(items) != 1 {
		t.Errorf("the kitchen with its children lists %+v, want the kettle filed under knives", items)
	}
	list, err := api.TaxonomyTerms(ctx, "product_category", nilda.Paging{Limit: 2})
	if err != nil || len(list) != 2 || list[0].Name != "Kitchen" {
		t.Errorf("the first page of categories is %+v (%v)", list, err)
	}
}

// Menus: made, filled and read back by location; an entry Core would refuse is refused, and a menus.read plugin
// cannot write.
func TestSiteMenus(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	main := site.AddMenu("Main", nilda.MenuLocationPrimary)
	shopPage := site.AddPage(SitePage{Title: "Shop", Slug: "shop"})
	if _, err := site.AddMenuItem(main, nilda.NewMenuItem{Type: nilda.MenuItemContent, ContentID: shopPage}); err != nil {
		t.Fatal(err)
	}
	core, _, srv := site.Core("shop", nilda.CapMenusRead)
	defer srv.Close()
	api := core.API()
	if _, err := api.CreateMenu(ctx, "Footer", nilda.MenuLocationFooter); apiStatus(err) != http.StatusForbidden {
		t.Fatalf("a menus.read plugin made a menu: %v", err)
	}
	tree, err := api.Menu(ctx, main)
	if err != nil || len(tree.Items) != 1 || tree.Items[0].URL != "/shop" || tree.Items[0].Label != "Shop" {
		t.Fatalf("the main menu read as %+v (%v), want the content entry's address and words derived", tree, err)
	}

	core, host, srv2 := site.Core("shop", nilda.CapMenusWrite)
	defer srv2.Close()
	api = core.API()
	footer, err := api.CreateMenu(ctx, "Footer", nilda.MenuLocationFooter)
	if err != nil {
		t.Fatal(err)
	}
	legal, err := api.AddMenuItem(ctx, footer.ID, nilda.NewMenuItem{Type: nilda.MenuItemLabel, Label: "Legal"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.AddMenuItem(ctx, footer.ID, nilda.NewMenuItem{Type: nilda.MenuItemURL, Label: "Withdraw",
		URL: "/withdraw", ParentID: legal.ID}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []nilda.NewMenuItem{{Type: nilda.MenuItemURL, Label: "x"}, {Type: nilda.MenuItemURL,
		URL: "javascript:alert(1)"}, {Type: "page"}, {Type: nilda.MenuItemLabel, Label: "x", ParentID: newID()}} {
		if _, err := api.AddMenuItem(ctx, footer.ID, bad); apiStatus(err) != http.StatusUnprocessableEntity {
			t.Errorf("%+v answered %v, want 422", bad, err)
		}
	}
	items := site.MenuItems(nilda.MenuLocationFooter)
	if len(items) != 1 || items[0].Label != "Legal" || len(items[0].Children) != 1 || items[0].Children[0].URL != "/withdraw" {
		t.Errorf("the footer holds %+v", items)
	}
	menus, _ := api.Menus(ctx)
	if len(menus) != 2 || menus[1].Location != nilda.MenuLocationFooter {
		t.Errorf("the menus read as %+v", menus)
	}
	host.Revoke(nilda.CapMenusWrite)
	if _, err := api.Menus(ctx); apiStatus(err) != http.StatusForbidden {
		t.Errorf("a revoked capability still read menus: %v", err)
	}
}

// A shop page made the way the shop makes it: created with a body Core takes, laid out, found by its widget, published;
// a body or a tree Core would refuse is refused, and a write based on an old stamp is a conflict.
func TestSitePagesLayoutsAndMedia(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	core, host, srv := site.Core("shop", nilda.CapContentWrite)
	defer srv.Close()
	api := core.API()

	var made struct {
		Data struct {
			ID, Slug, Status string
		} `json:"data"`
	}
	if err := api.Post(ctx, "/content/page", map[string]any{"title": "Cart", "body": map[string]any{"text": "no"}},
		nil); apiStatus(err) != http.StatusUnprocessableEntity {
		t.Fatalf("a body Core refuses was taken: %v", err)
	}
	if err := api.Post(ctx, "/content/page", map[string]any{"title": "Cart", "body": nilda.TextBody("Your basket.")},
		&made); err != nil {
		t.Fatal(err)
	}
	if made.Data.Slug != "cart" || made.Data.Status != "draft" {
		t.Errorf("the page was made as %+v", made.Data)
	}
	if p, _ := site.Page(made.Data.ID); nilda.CheckBody(p.Body) != nil || nilda.BodyText(p.Body) != "Your basket." {
		t.Errorf("the page's body is %s", p.Body)
	}
	if err := api.Patch(ctx, "/content/page/"+made.Data.ID, map[string]any{"body": "plain words"}, nil); apiStatus(err) !=
		http.StatusUnprocessableEntity {
		t.Errorf("a patch with a body Core refuses answered %v", err)
	}

	tree := nilda.LayoutTree{Nodes: []nilda.LayoutNode{{ID: "s", Kind: nilda.LayoutSection, Children: []nilda.LayoutNode{
		{ID: "c", Kind: nilda.LayoutColumn, Children: []nilda.LayoutNode{{ID: "w", Kind: nilda.LayoutWidget,
			Widget: nilda.WidgetCart}}}}}}}
	if _, err := api.SetLayout(ctx, "page", made.Data.ID, nilda.LayoutTree{Nodes: []nilda.LayoutNode{{ID: "x",
		Kind: "box"}}}, ""); apiStatus(err) != http.StatusUnprocessableEntity {
		t.Errorf("a node of no kind Core has answered %v", err)
	}
	w, err := api.SetLayout(ctx, "page", made.Data.ID, tree, "")
	if err != nil || w.WrittenTo != nilda.LayoutWrittenToPage || w.Live {
		t.Fatalf("the draft's layout write answered %+v (%v)", w, err)
	}
	if got, ok := site.Layout(made.Data.ID); !ok || got.Nodes[0].Children[0].Children[0].Widget != nilda.WidgetCart {
		t.Errorf("the layout kept is %+v", got)
	}
	holders, err := api.HoldingPages(ctx, nilda.WidgetCart)
	if err != nil || len(holders) != 1 || holders[0].ID != made.Data.ID || holders[0].Status != "draft" {
		t.Errorf("the pages holding the cart are %+v (%v)", holders, err)
	}
	if pub, err := api.PublishContent(ctx, "page", made.Data.ID); err != nil || pub.Status != "published" {
		t.Fatalf("the publish answered %+v (%v)", pub, err)
	}
	live, err := api.SetLayout(ctx, "page", made.Data.ID, tree, w.LastEditedAt)
	if apiStatus(err) != http.StatusConflict {
		t.Errorf("a write based on the stamp before the publish answered %+v (%v), want 409", live, err)
	}
	var back struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	_ = api.Get(ctx, "/content/page/"+made.Data.ID, nil, &back)
	live, err = api.SetLayout(ctx, "page", made.Data.ID, tree, "")
	if err != nil || live.WrittenTo != nilda.LayoutWrittenToWorkingVersion || !live.Live || back.Data.Status != "published" {
		t.Errorf("a published page's layout write answered %+v (%v)", live, err)
	}
	if _, err := api.PublishContent(ctx, "product", made.Data.ID); apiStatus(err) != http.StatusNotFound {
		t.Errorf("the page was published as a product: %v", err)
	}

	// Media: refused without media.read, then the item and its bytes.
	id := site.SetMediaFile("", "guide.pdf", "application/pdf", []byte("%PDF-1.7"))
	if _, err := api.MediaFile(ctx, id); apiStatus(err) != http.StatusForbidden {
		t.Errorf("a plugin without media.read read a file: %v", err)
	}
	host.Grant(nilda.CapMediaRead)
	file, err := api.MediaFile(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(file)
	_ = file.Close()
	if string(data) != "%PDF-1.7" || file.ContentType != "application/pdf" || file.Size != 8 ||
		file.Disposition != "attachment; filename=guide.pdf" || file.Item.Filename != "guide.pdf" {
		t.Errorf("the file read as %q with %+v", data, file)
	}
	pic := site.SetMediaFile("", "cup.png", "image/png", []byte("PNG"))
	if img, err := api.MediaFile(ctx, pic); err != nil || img.Disposition != "" {
		t.Errorf("a picture came as %+v (%v), want it shown inline as Core does", img, err)
	} else {
		_ = img.Close()
	}
	if _, err := api.MediaFile(ctx, newID()); apiStatus(err) != http.StatusNotFound {
		t.Errorf("a file the library lacks answered %v", err)
	}
}

// FailNext fails exactly the next request: the SDK's retry survives it, a client without retries sees the status.
func TestSiteFailNext(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	site.SetSite(nilda.Site{Title: "Santoor"})
	core, _, srv := site.Core("shop")
	defer srv.Close()
	site.FailNext(http.StatusServiceUnavailable)
	if _, err := core.API().WithMaxRetries(0).Site(ctx); apiStatus(err) != http.StatusServiceUnavailable {
		t.Errorf("the failed request answered %v", err)
	}
	if s, err := core.API().WithMaxRetries(0).Site(ctx); err != nil || s.Title != "Santoor" {
		t.Errorf("the request after it answered %+v (%v)", s, err)
	}
	site.FailNext(http.StatusServiceUnavailable)
	if s, err := core.API().Site(ctx); err != nil || s.Title != "Santoor" {
		t.Errorf("a retried read answered %+v (%v)", s, err)
	}
}

// The fake site answers Core's /reference routes too, from its own Reference: a plugin reading both Core's resources
// and its reference data runs against one fake Core, and a site nobody set up answers as Core does with no imprint and
// no policy — an answer, not a missing route.
func TestSiteAnswersItsReferenceRoutes(t *testing.T) {
	ctx := context.Background()
	site := NewSite()
	core, _, srv := site.Core("shop")
	defer srv.Close()
	ref := core.API().Reference()

	pp, err := ref.PrivacyPolicy(ctx)
	if err != nil || pp.Published {
		t.Fatalf("a new site's privacy policy read %+v (%v), want an answer that none is published", pp, err)
	}
	site.Reference().SetPrivacyPolicy(nilda.PrivacyPolicy{Published: true, Path: "/privacy"})
	site.Reference().SetSeller(nilda.Seller{Name: "Santoor GmbH"})
	if pp, err := ref.PrivacyPolicy(ctx); err != nil || !pp.Published || pp.Path != "/privacy" {
		t.Errorf("the policy set read %+v (%v)", pp, err)
	}
	if s, err := ref.Seller(ctx); err != nil || s.Name != "Santoor GmbH" {
		t.Errorf("the seller set read %+v (%v)", s, err)
	}
	if _, err := ref.TaxTable(ctx, time.Now()); apiStatus(err) != http.StatusNotFound {
		t.Errorf("a tax table nobody set answered %v, want Core's 404", err)
	}
	site.Reference().Down("/reference/privacy-policy", http.StatusServiceUnavailable)
	if _, err := ref.PrivacyPolicy(ctx); err == nil {
		t.Error("a reference route that is down answered")
	}
	if got := site.Reference().Asked(); len(got) < 4 || got[0] != "/reference/privacy-policy" {
		t.Errorf("asked %v", got)
	}
}
