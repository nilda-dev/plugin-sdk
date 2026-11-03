package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// typedCall is one request a typed method sent, as Core's API receives it.
type typedCall struct {
	Method, Path, Query string
	Body                map[string]any
	Auth                string
}

// coreAPIStub answers each request with the canned body for its "METHOD /path" (the path below /api/rest/v1, or a
// full path for one outside it) and records what arrived.
func coreAPIStub(t *testing.T, answers map[string]string) (*API, *[]typedCall) {
	t.Helper()
	var calls []typedCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := typedCall{Method: r.Method, Path: strings.TrimPrefix(r.URL.EscapedPath(), "/api/rest/v1"),
			Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &c.Body); err != nil {
				t.Errorf("%s %s sent a body that is not a JSON object: %s", r.Method, r.URL.Path, raw)
			}
		}
		calls = append(calls, c)
		ans, ok := answers[r.Method+" "+c.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"no such route"}}`))
			return
		}
		if strings.HasPrefix(ans, "FILE ") {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", "attachment; filename=guide.pdf")
			_, _ = w.Write([]byte(strings.TrimPrefix(ans, "FILE ")))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = w.Write([]byte(ans))
	}))
	t.Cleanup(srv.Close)
	return newAPI(srv.URL+"/api/rest/v1", "nlda_tok", nil), &calls
}

func onlyCall(t *testing.T, calls []typedCall, method, path, query string) typedCall {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("sent %d requests, want one: %+v", len(calls), calls)
	}
	c := calls[0]
	if c.Method != method || c.Path != path || c.Query != query {
		t.Errorf("sent %s %s?%s, want %s %s?%s", c.Method, c.Path, c.Query, method, path, query)
	}
	if c.Auth != "Bearer nlda_tok" {
		t.Errorf("the token did not ride the request: %q", c.Auth)
	}
	return c
}

func TestUserAndUsersReadCoresAccounts(t *testing.T) {
	ctx := context.Background()
	api, calls := coreAPIStub(t, map[string]string{
		"GET /users/7c9e6679-7425-40de-944b-e07fc1f90ae7": `{"data":{"id":"7c9e6679-7425-40de-944b-e07fc1f90ae7",
			"email":"ana@example.com","display_name":"Ana","avatar_media_id":null,"status":"active",
			"email_verified_at":"2026-10-01T09:30:00.123456Z","last_login_at":null,"created_at":"2026-09-30T08:00:00Z",
			"updated_at":"2026-10-01T09:30:00Z","bio":null,"language":"fa","timezone":"Asia/Tehran","author_slug":null,
			"profile_public":false,"job_title":null,"location":null,"pronouns":null,"website":null,"social_links":null}}`,
		"GET /users": `{"data":[{"id":"u2","email":"new@example.com","status":"active","email_verified_at":null,
			"created_at":"2026-10-02T00:00:00Z"},{"id":"u1","email":"owner@example.com","status":"active",
			"email_verified_at":"2026-01-01T00:00:00Z","created_at":"2026-01-01T00:00:00Z"}]}`,
	})
	u, err := api.User(ctx, "7c9e6679-7425-40de-944b-e07fc1f90ae7")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/users/7c9e6679-7425-40de-944b-e07fc1f90ae7", "")
	if u.Email != "ana@example.com" || u.DisplayName != "Ana" || u.Status != UserStatusActive || u.EmailVerifiedAt == nil ||
		u.EmailVerifiedAt.Day() != 1 || u.LastLoginAt != nil || u.Language != "fa" || u.Timezone != "Asia/Tehran" ||
		u.CreatedAt.IsZero() {
		t.Errorf("the account decoded as %+v", u)
	}

	*calls = nil
	list, err := api.Users(ctx, UserQuery{Role: "admin", Status: UserStatusActive, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/users", "limit=100&role=admin&status=active")
	if len(list) != 2 || list[1].Email != "owner@example.com" || list[0].EmailVerifiedAt != nil {
		t.Errorf("the list decoded as %+v", list)
	}

	*calls = nil
	_, err = api.User(ctx, "gone")
	var ae *APIError
	if !errors.As(err, &ae) || !ae.NotFound() {
		t.Errorf("an account Core does not have answered %v, want a 404", err)
	}
}

func TestSitePageAndBreadcrumbsReadTheStableAPI(t *testing.T) {
	ctx := context.Background()
	api, calls := coreAPIStub(t, map[string]string{
		"GET /site": `{"data":{"title":"Santoor","description":"Instruments","base_url":"https://santoor.example",
			"locale":"de","timezone":"Europe/Vienna"}}`,
		"GET /pages/agb": `{"data":{"id":"p1","type":"page","title":"AGB","slug":"agb","body":{"root":{"type":"root",
			"children":[]}},"published_at":"2026-10-01T00:00:00Z","head_meta":{"title":"AGB","canonical":
			"https://santoor.example/agb","robots":"index,follow"},"locale":"de"}}`,
		"GET /breadcrumbs/c1": `{"data":[{"label":"Home","url":"/"},{"label":"Kettles","url":"/shop/kettles"},
			{"label":"Copper kettle","url":"/shop/kettles/copper"}]}`,
	})
	site, err := api.Site(ctx)
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/site", "")
	if site != (Site{Title: "Santoor", Description: "Instruments", BaseURL: "https://santoor.example", Locale: "de",
		Timezone: "Europe/Vienna"}) {
		t.Errorf("the site decoded as %+v", site)
	}

	*calls = nil
	page, err := api.PageBySlug(ctx, "/agb", "page")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/pages/agb", "type=page")
	if page.ID != "p1" || page.Slug != "agb" || page.HeadMeta == nil || page.HeadMeta.Canonical != "https://santoor.example/agb" ||
		page.PublishedAt == nil || page.Locale != "de" || CheckBody(page.Body) != nil {
		t.Errorf("the page decoded as %+v", page)
	}

	*calls = nil
	crumbs, err := api.Breadcrumbs(ctx, "c1")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/breadcrumbs/c1", "")
	if len(crumbs) != 3 || crumbs[2] != (Crumb{Label: "Copper kettle", URL: "/shop/kettles/copper"}) {
		t.Errorf("the trail decoded as %+v", crumbs)
	}
}

func TestTermsReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	api, calls := coreAPIStub(t, map[string]string{
		"GET /taxonomies/product_category/terms": `{"data":[{"id":"t1","taxonomy_id":"x1","name":"Kitchen","slug":"kitchen",
			"count":3},{"id":"t2","taxonomy_id":"x1","name":"Knives","slug":"knives","parent_id":"t1","count":1}]}`,
		"POST /taxonomies/product_category/terms": `{"data":{"id":"t9","taxonomy_id":"x1","name":"Tea","slug":"tea-2","count":0}}`,
		"GET /terms/t1/content": `{"data":[{"id":"c1","type":"product","title":"Kettle","slug":"kettle","status":"published"},
			{"id":"c2","type":"post","title":"News","slug":"news","status":"published"}],"meta":{"page":1,"size":0,"total":41}}`,
		"GET /content/product/c1/terms": `{"data":{"product_category":[{"id":"t1","taxonomy_id":"x1","name":"Kitchen",
			"slug":"kitchen","count":3}],"tag":[{"id":"g1","taxonomy_id":"x2","name":"Sale","slug":"sale","count":9}]}}`,
		"PUT /content/product/c1/terms": `{"data":{"ok":true}}`,
	})
	terms, err := api.TaxonomyTerms(ctx, "product_category", Paging{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/taxonomies/product_category/terms", "limit=200")
	if len(terms) != 2 || terms[1].ParentID != "t1" || terms[0].Count != 3 || terms[0].TaxonomyID != "x1" {
		t.Errorf("the terms decoded as %+v", terms)
	}

	*calls = nil
	made, err := api.CreateTerm(ctx, "product_category", NewTerm{Name: "Tea"})
	if err != nil {
		t.Fatal(err)
	}
	c := onlyCall(t, *calls, "POST", "/taxonomies/product_category/terms", "")
	if !reflect.DeepEqual(c.Body, map[string]any{"name": "Tea"}) {
		t.Errorf("the create sent %v; an absent parent must be absent (Core reads null as the top level, \"\" as no id)", c.Body)
	}
	if made.ID != "t9" || made.Slug != "tea-2" {
		t.Errorf("the created term decoded as %+v", made)
	}

	*calls = nil
	items, total, err := api.TermContent(ctx, "t1", TermContentQuery{Limit: 1000, IncludeChildren: true})
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/terms/t1/content", "include_children=true&limit=1000")
	if len(items) != 2 || items[0].Type != "product" || items[1].Status != "published" || total != 41 {
		t.Errorf("the term's items decoded as %+v (total %d)", items, total)
	}

	*calls = nil
	byTax, err := api.ContentTerms(ctx, "product", "c1")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/content/product/c1/terms", "")
	if len(byTax["product_category"]) != 1 || byTax["tag"][0].Name != "Sale" {
		t.Errorf("the item's terms decoded as %+v", byTax)
	}

	// The merge: the tag a person gave the item stays, the shop's own taxonomy is replaced.
	*calls = nil
	if err := api.SetContentTerms(ctx, "product", "c1", "product_category", []string{"t9", "t2", "t9"}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].Method != "GET" || (*calls)[1].Method != "PUT" ||
		(*calls)[1].Path != "/content/product/c1/terms" {
		t.Fatalf("the merge sent %+v, want the read and then the PUT", *calls)
	}
	var sent []string
	for _, v := range (*calls)[1].Body["term_ids"].([]any) {
		sent = append(sent, v.(string))
	}
	sort.Strings(sent)
	if !reflect.DeepEqual(sent, []string{"g1", "t2", "t9"}) {
		t.Errorf("the PUT carried %v, want the new categories and the tag kept (t1 taken off, t9 once)", sent)
	}
}

func TestMenusReadsAndWrites(t *testing.T) {
	ctx := context.Background()
	api, calls := coreAPIStub(t, map[string]string{
		"GET /menus": `{"data":[{"id":"m1","name":"Main","location":"primary"},{"id":"m2","name":"Footer","location":"footer"}]}`,
		"GET /menus/m2": `{"data":{"menu":{"id":"m2","name":"Footer","location":"footer"},"items":[{"id":"i1","type":"label",
			"label":"Legal","url":"","position":1,"mega":{"enabled":false},"children":[{"id":"i2","type":"url",
			"label":"Withdraw","url":"/withdraw","parent_id":"i1","position":1,"mega":{"enabled":false},"children":[]}]}]}}`,
		"POST /menus":          `{"data":{"id":"m3","name":"Footer","location":"footer"}}`,
		"POST /menus/m3/items": `{"data":{"id":"i9","type":"url","label":"Withdraw","url":"/withdraw","position":1,"children":null}}`,
	})
	menus, err := api.Menus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/menus", "")
	if len(menus) != 2 || menus[0].Location != MenuLocationPrimary || menus[1].Location != MenuLocationFooter {
		t.Errorf("the menus decoded as %+v", menus)
	}

	*calls = nil
	tree, err := api.Menu(ctx, "m2")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/menus/m2", "")
	if tree.Menu.Name != "Footer" || len(tree.Items) != 1 || tree.Items[0].Type != MenuItemLabel ||
		len(tree.Items[0].Children) != 1 || tree.Items[0].Children[0].URL != "/withdraw" ||
		tree.Items[0].Children[0].ParentID != "i1" {
		t.Errorf("the menu decoded as %+v", tree)
	}

	*calls = nil
	m, err := api.CreateMenu(ctx, "Footer", MenuLocationFooter)
	if err != nil {
		t.Fatal(err)
	}
	c := onlyCall(t, *calls, "POST", "/menus", "")
	if !reflect.DeepEqual(c.Body, map[string]any{"name": "Footer", "location": "footer"}) || m.ID != "m3" {
		t.Errorf("the create sent %v and decoded %+v", c.Body, m)
	}

	*calls = nil
	item, err := api.AddMenuItem(ctx, "m3", NewMenuItem{Type: MenuItemURL, Label: "Withdraw", URL: "/withdraw"})
	if err != nil {
		t.Fatal(err)
	}
	c = onlyCall(t, *calls, "POST", "/menus/m3/items", "")
	if !reflect.DeepEqual(c.Body, map[string]any{"type": "url", "label": "Withdraw", "url": "/withdraw"}) {
		t.Errorf("the item sent %v; an id it does not name must be absent (Core parses it as a uuid)", c.Body)
	}
	if item.ID != "i9" || item.Position != 1 {
		t.Errorf("the item decoded as %+v", item)
	}
}

func TestLayoutsPublishAndMedia(t *testing.T) {
	ctx := context.Background()
	api, calls := coreAPIStub(t, map[string]string{
		"GET /layouts/holding": `{"data":[{"id":"p1","type":"page","slug":"cart","status":"published"},
			{"id":"p2","type":"page","slug":"cart-2","status":"draft"}]}`,
		"PUT /content/page/p2/layout": `{"data":{"content_id":"p2","written_to":"page","live":false,
			"last_edited_at":"2026-11-03T10:00:00.5Z"}}`,
		"POST /content/page/p2/publish": `{"data":{"status":"published","became_front_page":true}}`,
		"GET /media/f1": `{"data":{"id":"f1","filename":"guide.pdf","mime_type":"application/pdf","size_bytes":7,
			"alt_text":"","tags":[],"focal_x":0.5,"focal_y":0.5,"status":"ready","url":"/api/v1/media/f1/file?c=ab12",
			"created_at":"2026-10-01T00:00:00Z"}}`,
		"GET /media/f1/file": "FILE %PDF-1.",
		"PATCH /media/f1": `{"data":{"id":"f1","filename":"guide.pdf","private":true,
			"url":"/api/v1/media/f1/private-file"}}`,
	})
	holders, err := api.HoldingPages(ctx, WidgetCart)
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "GET", "/layouts/holding", "widget=cart")
	if len(holders) != 2 || holders[0] != (HoldingPage{ID: "p1", Type: "page", Slug: "cart", Status: "published"}) {
		t.Errorf("the holders decoded as %+v", holders)
	}

	*calls = nil
	tree := LayoutTree{Nodes: []LayoutNode{{ID: "shop-cart", Kind: LayoutSection, Children: []LayoutNode{{ID: "c",
		Kind: LayoutColumn, Children: []LayoutNode{{ID: "w", Kind: LayoutWidget, Widget: WidgetCart}}}}}}}
	w, err := api.SetLayout(ctx, "page", "p2", tree, "2026-11-03T09:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	c := onlyCall(t, *calls, "PUT", "/content/page/p2/layout", "")
	if c.Body["based_on"] != "2026-11-03T09:00:00Z" {
		t.Errorf("based_on was not sent: %v", c.Body)
	}
	var back struct {
		Tree LayoutTree `json:"tree"`
	}
	raw, _ := json.Marshal(c.Body)
	if err := json.Unmarshal(raw, &back); err != nil || !reflect.DeepEqual(back.Tree, tree) {
		t.Errorf("the tree sent was %s", raw)
	}
	if w.WrittenTo != LayoutWrittenToPage || w.Live || w.LastEditedAt == "" || w.ContentID != "p2" {
		t.Errorf("the write decoded as %+v", w)
	}

	*calls = nil
	if _, err := api.SetLayout(ctx, "page", "p2", LayoutTree{}, ""); err != nil {
		t.Fatal(err)
	}
	if b := (*calls)[0].Body; b["based_on"] != nil || !reflect.DeepEqual(b["tree"], map[string]any{"nodes": []any{}}) {
		t.Errorf("an empty tree with no stamp sent %v, want {\"tree\":{\"nodes\":[]}}", b)
	}

	*calls = nil
	pub, err := api.PublishContent(ctx, "page", "p2")
	if err != nil {
		t.Fatal(err)
	}
	onlyCall(t, *calls, "POST", "/content/page/p2/publish", "")
	if pub.Status != "published" || !pub.BecameFrontPage {
		t.Errorf("the publish decoded as %+v", pub)
	}

	*calls = nil
	file, err := api.MediaFile(ctx, "f1")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(file)
	_ = file.Close()
	if string(body) != "%PDF-1." || file.ContentType != "application/pdf" {
		t.Errorf("the file read as %q (%s)", body, file.ContentType)
	}
	if file.Item.Filename != "guide.pdf" || file.Size != 7 || file.Disposition != `attachment; filename=guide.pdf` {
		t.Errorf("the file came with %+v, want the item, its length and Core's attachment header", file)
	}
	if len(*calls) != 2 || (*calls)[0].Path != "/media/f1" || (*calls)[1].Path != "/media/f1/file" {
		t.Errorf("the file was fetched by %+v, want the item and then the API's own file route", *calls)
	}
	if (*calls)[1].Auth != "Bearer nlda_tok" {
		t.Error("the file was fetched without the plugin's token — a private file would not open")
	}

	*calls = nil
	item, err := api.SetMediaPrivate(ctx, "f1", true)
	if err != nil {
		t.Fatal(err)
	}
	pc := onlyCall(t, *calls, "PATCH", "/media/f1", "")
	if !reflect.DeepEqual(pc.Body, map[string]any{"private": true}) || !item.Private {
		t.Errorf("SetMediaPrivate sent %v and answered %+v, want {\"private\":true} and the private item", pc.Body, item)
	}
}

// MediaFile never fetches the address an item's url names: a url off Core's host is not followed, and the bytes come
// from the API's own file route either way.
func TestMediaFileNeverFollowsTheItemsURL(t *testing.T) {
	api, calls := coreAPIStub(t, map[string]string{
		"GET /media/f1":      `{"data":{"id":"f1","url":"https://images.example.com/x.jpg"}}`,
		"GET /media/f1/file": "FILE %PDF-1.",
	})
	f, err := api.MediaFile(context.Background(), "f1")
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if len(*calls) != 2 || (*calls)[1].Path != "/media/f1/file" {
		t.Errorf("sent %+v, want the item and its file through the API", *calls)
	}
	if _, err := (*API)(nil).MediaFile(context.Background(), "f1"); err == nil {
		t.Error("a plugin with no API read a file")
	}
	if _, err := (*API)(nil).SetMediaPrivate(context.Background(), "f1", true); err == nil {
		t.Error("a plugin with no API changed a file")
	}
}

// ---- held to Core's source -----------------------------------------------------------------------------------

// jsonNames is every json field name a struct's tags name.
func jsonNames(v any) []string {
	rt := reflect.TypeOf(v)
	var out []string
	for i := 0; i < rt.NumField(); i++ {
		if name := regexp.MustCompile(`^[a-z_]+`).FindString(rt.Field(i).Tag.Get("json")); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// coreStruct is the body of `type <name> struct {` in one of Core's files.
func coreStruct(t *testing.T, src, name, where string) string {
	t.Helper()
	return upTo(t, from(t, src, "type "+name+" struct {", where), "\n}", where+" "+name)
}

// Every route the typed client calls is one Core registers, under the scope the doc comment names.
func TestTheTypedRoutesAreCoresOwn(t *testing.T) {
	resources := coreSource(t, "internal", "api", "rest", "resources_core.go")
	crud := coreSource(t, "internal", "api", "rest", "crud.go")
	layouts := coreSource(t, "internal", "api", "rest", "layouts.go")
	stable := coreSource(t, "internal", "api", "stable", "handlers.go")
	media := coreSource(t, "internal", "media", "handlers.go")
	for _, r := range []struct{ src, where, route string }{
		{resources, "rest/resources_core.go", `g.GET("/users/:id", s.getUser)`},
		{resources, "rest/resources_core.go", `g.GET("/users", s.listUsers)`},
		{stable, "stable/handlers.go", `rg.GET("/site", h.site)`},
		{stable, "stable/handlers.go", `rg.GET("/pages/:slug", h.pageBySlug)`},
		{stable, "stable/handlers.go", `rg.GET("/breadcrumbs/:id", h.breadcrumbs)`},
		{resources, "rest/resources_core.go", `g.GET("/content/:type/:id/terms", s.getContentTerms)`},
		{resources, "rest/resources_core.go", `g.PUT("/content/:type/:id/terms", s.assignContentTerms)`},
		{resources, "rest/resources_core.go", `g.GET("/terms/:id/content", s.getTermContent)`},
		{resources, "rest/resources_core.go", `g.GET("/taxonomies/:key/terms", s.listTerms)`},
		{resources, "rest/resources_core.go", `g.POST("/taxonomies/:key/terms", s.createTerm)`},
		{resources, "rest/resources_core.go", `g.GET("/menus", s.listMenus)`},
		{resources, "rest/resources_core.go", `g.GET("/menus/:id", s.getMenu)`},
		{resources, "rest/resources_core.go", `g.POST("/menus", s.createMenu)`},
		{resources, "rest/resources_core.go", `g.POST("/menus/:id/items", s.addMenuItem)`},
		{resources, "rest/resources_core.go", `g.GET("/media/:id", s.getMedia)`},
		{layouts, "rest/layouts.go", `g.GET("/layouts/holding", s.layoutsHolding)`},
		{layouts, "rest/layouts.go", `g.PUT("/content/:type/:id/layout", s.putLayout)`},
		{crud, "rest/crud.go", `g.POST("/content/:type/:id/publish", s.publishContent)`},
		{media, "media/handlers.go", `m.GET("/:id/file", h.serveFile)`},
	} {
		if !strings.Contains(r.src, r.route) {
			t.Errorf("Core's %s no longer registers %s", r.where, r.route)
		}
	}
	// The scopes each comment names: the handler's own requireScope call.
	for handler, scope := range map[string]string{
		"getUser": "ScopeReadUsers", "listUsers": "ScopeReadUsers",
		"getContentTerms": "ScopeReadTaxonomy", "assignContentTerms": "ScopeWriteTaxonomy",
		"getTermContent": "ScopeReadTaxonomy", "listTerms": "ScopeReadTaxonomy", "createTerm": "ScopeWriteTaxonomy",
		"listMenus": "ScopeReadMenus", "getMenu": "ScopeReadMenus", "createMenu": "ScopeWriteMenus",
		"addMenuItem": "ScopeWriteMenus", "getMedia": "ScopeReadMedia",
	} {
		body := upTo(t, from(t, resources, "func (s *Server) "+handler+"(", "rest/resources_core.go"), "\n}",
			"rest/resources_core.go "+handler)
		if !strings.Contains(body, "apistandards."+scope) {
			t.Errorf("Core's %s no longer checks %s — the SDK's doc comment names it", handler, scope)
		}
	}
	// The layout and publish routes take the content scope from the method, on the content group.
	if !strings.Contains(crud, `g := root.Group("", apistandards.RequireContentScope())`) ||
		!strings.Contains(from(t, crud, `g := root.Group("", apistandards.RequireContentScope())`, "rest/crud.go"),
			"s.registerLayouts(g)") {
		t.Error("Core's layout and publish routes are no longer on the content-scope group")
	}
	// The plugin's API base is /api/rest/v1, where these routes are mounted, and the media file's address is Core's
	// /api/v1 (the one MediaFile fetches on Core's own host).
	if !strings.Contains(coreSource(t, "pkg", "config", "config.go"), `"http://127.0.0.1:%d/api/rest/v1"`) {
		t.Error("Core's plugin API base is no longer /api/rest/v1")
	}
	if !strings.Contains(coreSource(t, "internal", "media", "service.go"), `u := "/api/v1/media/" + id.String() + "/file"`) {
		t.Error("Core's media url is no longer /api/v1/media/{id}/file — MediaFile resolves it on Core's host")
	}
	// Core's REST API still has no file route of its own; when it gains one, MediaFile should call it.
	if strings.Contains(resources, `"/media/:id/file"`) {
		t.Error("Core's REST API serves /media/:id/file now — point MediaFile at it")
	}
}

// Every json name a typed struct reads is one Core's handler writes.
func TestTheTypedShapesAreCores(t *testing.T) {
	user := coreStruct(t, coreSource(t, "internal", "user", "public.go"), "PublicUser", "core user/public.go")
	stableContract := coreSource(t, "internal", "api", "stable", "contract.go")
	discovery := coreSource(t, "internal", "api", "stable", "discovery.go")
	taxSvc := coreSource(t, "internal", "taxonomy", "service.go")
	menuSvc := coreSource(t, "internal", "menu", "service.go")
	plugLayout := coreSource(t, "internal", "pagebuilder", "pluginlayout.go")
	mediaSvc := coreSource(t, "internal", "media", "service.go")
	resources := coreSource(t, "internal", "api", "rest", "resources_core.go")
	crud := coreSource(t, "internal", "api", "rest", "crud.go")
	for _, c := range []struct {
		name string
		ours any
		core string
	}{
		{"User", User{}, user},
		{"Site", Site{}, coreStruct(t, stableContract, "Site", "core stable/contract.go")},
		{"PublishedPage", PublishedPage{}, coreStruct(t, stableContract, "Page", "core stable/contract.go")},
		{"HeadMeta", HeadMeta{}, coreStruct(t, stableContract, "HeadMeta", "core stable/contract.go")},
		{"Crumb", Crumb{}, coreStruct(t, discovery, "Crumb", "core stable/discovery.go")},
		{"Term", Term{}, coreStruct(t, taxSvc, "Term", "core taxonomy/service.go")},
		{"NewTerm", NewTerm{}, coreStruct(t, taxSvc, "TermInput", "core taxonomy/service.go")},
		{"ContentRef", ContentRef{}, coreStruct(t, taxSvc, "ContentRef", "core taxonomy/service.go")},
		{"Menu", Menu{}, coreStruct(t, menuSvc, "Menu", "core menu/service.go")},
		{"MenuItem", MenuItem{}, coreStruct(t, menuSvc, "MenuItem", "core menu/service.go")},
		{"NewMenuItem", NewMenuItem{}, coreStruct(t, menuSvc, "MenuItemInput", "core menu/service.go")},
		{"HoldingPage", HoldingPage{}, coreStruct(t, plugLayout, "WidgetHolder", "core pagebuilder/pluginlayout.go")},
		{"LayoutWrite", LayoutWrite{}, coreStruct(t, plugLayout, "LayoutWrite", "core pagebuilder/pluginlayout.go")},
		{"MediaItem", MediaItem{}, coreStruct(t, mediaSvc, "Media", "core media/service.go")},
		// The two answers built in the handler itself: the menu read and the publish.
		{"MenuTree", MenuTree{}, upTo(t, from(t, resources, "func (s *Server) getMenu(", "rest/resources_core.go"), "\n}",
			"rest/resources_core.go getMenu")},
		{"PublishResult", PublishResult{}, upTo(t, from(t, crud, "func (s *Server) publishContent(", "rest/crud.go"), "\n}",
			"rest/crud.go publishContent")},
	} {
		for _, name := range jsonNames(c.ours) {
			if !regexp.MustCompile(`"` + name + `[",]`).MatchString(c.core) {
				t.Errorf("%s reads %q, which Core's answer does not carry", c.name, name)
			}
		}
	}
	// The terms of an item are keyed by taxonomy; the term content's total rides meta; the menu create's body.
	if !strings.Contains(coreSource(t, "internal", "taxonomy", "query.go"),
		"func (s *Service) TermsForContent(ctx context.Context, contentID uuid.UUID) (map[string][]Term, error)") {
		t.Error("Core's item terms are no longer a map of taxonomy key to terms")
	}
	if !strings.Contains(resources, "Envelope{Data: items, Meta: &Meta{Page: (offset / limit) + 1, Total: total}}") {
		t.Error("Core's term content no longer carries its total in meta")
	}
	if !regexp.MustCompile(`TermIDs \[\]uuid\.UUID +` + "`" + `json:"term_ids"`).MatchString(resources) {
		t.Error("Core's term PUT no longer reads term_ids")
	}
	body := coreStruct(t, resources, "menuBody", "core rest/resources_core.go")
	for _, f := range []string{`json:"name"`, `json:"location"`} {
		if !strings.Contains(body, f) {
			t.Errorf("Core's menu create no longer reads %s", f)
		}
	}
	put := coreStruct(t, coreSource(t, "internal", "api", "rest", "layouts.go"), "putLayoutReq", "core rest/layouts.go")
	for _, f := range []string{`json:"tree"`, `json:"based_on"`} {
		if !strings.Contains(put, f) {
			t.Errorf("Core's layout PUT no longer reads %s", f)
		}
	}
}

// The constants are Core's own words.
func TestTheTypedConstantsAreCores(t *testing.T) {
	menus := coreSource(t, "internal", "menu", "menus.go")
	want := `var Locations = []string{"` + strings.Join([]string{MenuLocationPrimary, MenuLocationFooter,
		MenuLocationMobile, MenuLocationSocial}, `", "`) + `"}`
	if !strings.Contains(menus, want) {
		t.Errorf("Core's menu locations are not %s", want)
	}
	svc := coreSource(t, "internal", "menu", "service.go")
	for core, ours := range map[string]string{"TypeContent": MenuItemContent, "TypeTerm": MenuItemTerm,
		"TypeURL": MenuItemURL, "TypeHome": MenuItemHome, "TypeLabel": MenuItemLabel} {
		if !regexp.MustCompile(core + ` += "` + ours + `"`).MatchString(svc) {
			t.Errorf("Core's %s is not %q", core, ours)
		}
	}
	layout := coreSource(t, "internal", "pagebuilder", "pluginlayout.go")
	for _, c := range []string{`LayoutWrittenToPage           = "` + LayoutWrittenToPage + `"`,
		`LayoutWrittenToWorkingVersion = "` + LayoutWrittenToWorkingVersion + `"`} {
		if !strings.Contains(layout, c) {
			t.Errorf("Core's layout write no longer answers %s", c)
		}
	}
	check := coreSource(t, "migrations", "0113_users_status_check.up.sql")
	want = `CHECK (status IN ('` + strings.Join([]string{UserStatusActive, UserStatusSuspended, UserStatusPending,
		UserStatusBanned}, `', '`) + `'))`
	if !strings.Contains(check, want) {
		t.Errorf("Core's account statuses are not %s", want)
	}
}
