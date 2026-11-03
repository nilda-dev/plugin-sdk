package nildatest

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// Site is a stateful fake of Core's own resources as a plugin reaches them through its API — the routes the typed
// client calls (nilda.API's User, Users, Site, PageBySlug, Breadcrumbs, the terms, menus, layouts, publishing and
// media) and the content routes a plugin makes its pages with (POST/GET/PATCH/DELETE /content/{type}/{id}) — served
// from memory, answering as Core answers:
//
//   - a route whose scope the granted capabilities do not carry is refused 403, as Core refuses it (users.read,
//     taxonomy.read/write, menus.read/write, content.read/write, media.read/write; /site, /pages and /breadcrumbs need none);
//   - PUT /content/{type}/{id}/terms REPLACES the item's terms across every taxonomy, as Core's does;
//   - a content body Core would refuse (nilda.CheckBody) is a 422, so is a layout that is not a tree, a menu item
//     without the field its type needs, a term id that does not exist;
//   - a layout written to a published page goes to its working version (written_to), and a write based on an old
//     stamp is a 409;
//   - a private media item (SetMediaPrivate) is "not found" on its public address and opens through MediaFile; making
//     one the site shows (SetMediaUsedOn) private is a 422 naming where;
//   - an id the fake does not have is a 404, one that is not even a uuid a 422 (Core parses every id).
//
// Its pages draw no real permalinks: an item's address in a crumb or a menu entry is "/" + its slug. In a test:
//
//	site := nildatest.NewSite()
//	site.SetSite(nilda.Site{Title: "Santoor", BaseURL: "https://santoor.example"})
//	owner := site.AddUser(nilda.User{Email: "owner@example.com"}, "admin")
//	core, _, srv := site.Core("shop", nilda.CapUsersRead, nilda.CapMenusWrite, nilda.CapContentWrite)
//	defer srv.Close()
//	// ... run your plugin with core ...
//	items := site.MenuItems(nilda.MenuLocationFooter) // what the footer now holds
type Site struct {
	mu       sync.Mutex
	site     nilda.Site
	users    []siteUser
	items    map[string]*sitePageState
	order    []string
	taxIDs   map[string]string // taxonomy key → id
	terms    map[string]*siteTerm
	termSeq  []string
	filed    map[string][]string // content id → term ids
	menus    []*siteMenu
	media    map[string]*siteMedia
	failNext []int
	clock    time.Time
	ref      *Reference
}

// SitePage is a content item of the fake site, as a test adds it and reads it back.
type SitePage struct {
	ID string
	// Type is "page" when empty.
	Type  string
	Title string
	Slug  string
	// Status is "published" when empty, or "draft".
	Status   string
	Body     json.RawMessage
	HeadMeta *nilda.HeadMeta
	// Layout is the page's layout — after a plugin's write to a published page, the working version it wrote.
	Layout *nilda.LayoutTree
	Locale string
}

type siteUser struct {
	nilda.User
	roles []string
}

type sitePageState struct {
	SitePage
	stamp time.Time
}

type siteTerm struct {
	nilda.Term
	taxonomy string
}

type siteMenu struct {
	nilda.Menu
	items []*siteMenuItem
}

type siteMenuItem struct {
	nilda.MenuItem
}

type siteMedia struct {
	item nilda.MediaItem
	data []byte
	// usedOn is where the site shows the file (SetMediaUsedOn): Core refuses to make such a file private.
	usedOn []string
}

// NewSite builds an empty site with Core's two built-in taxonomies, "category" and "tag".
func NewSite() *Site {
	s := &Site{items: map[string]*sitePageState{}, taxIDs: map[string]string{}, terms: map[string]*siteTerm{},
		filed: map[string][]string{}, media: map[string]*siteMedia{}, clock: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
		ref: NewReference()}
	s.AddTaxonomy("category")
	s.AddTaxonomy("tag")
	return s
}

// Core builds a fake Core for the plugin key, granted exactly the capabilities named, whose API answers this site.
// Grant and Revoke on the Host change what it may reach from the next request on. Close the server when done.
func (s *Site) Core(key string, granted ...string) (*nilda.Core, *Host, *httptest.Server) {
	h := newHost(key, granted)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.serve(h, w, r) }))
	core := nilda.NewCoreForTest(key, granted, h, srv.URL, "test-token", scopesFor(granted))
	return core, h, srv
}

// Reference is the site's reference data — who sells, whether a privacy policy is published, the EU VAT rates, the
// exchange rates, the tax table — which its /reference routes answer, as Core's do: set it as a Reference is set
// (SetSeller, SetPrivacyPolicy, …). A new site publishes no imprint and no privacy policy.
func (s *Site) Reference() *Reference { return s.ref }

// SetSite sets what GET /site answers.
func (s *Site) SetSite(site nilda.Site) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.site = site
}

// AddUser adds an account holding the roles named ("admin", "editor", …) and answers its id. An empty ID gets one, an
// empty Status is active, and a zero CreatedAt is a moment after the previous account's, so Users lists the last
// added first, as Core lists the newest first.
func (s *Site) AddUser(u nilda.User, roles ...string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u.ID == "" {
		u.ID = newID()
	}
	if u.Status == "" {
		u.Status = nilda.UserStatusActive
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = s.tick()
	}
	s.users = append(s.users, siteUser{User: u, roles: append([]string(nil), roles...)})
	return u.ID
}

// RemoveUser takes an account away — an erasure: GET /users/{id} answers 404 from then on.
func (s *Site) RemoveUser(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if u.ID == id {
			s.users = append(s.users[:i], s.users[i+1:]...)
			return
		}
	}
}

// AddPage adds a content item and answers its id (one is made when ID is empty).
func (s *Site) AddPage(p SitePage) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = newID()
	}
	if p.Type == "" {
		p.Type = "page"
	}
	if p.Status == "" {
		p.Status = "published"
	}
	if p.Slug == "" {
		p.Slug = s.uniqueSlug(p.Type, slugOf(p.Title), "")
	}
	s.put(p)
	return p.ID
}

// Page is a content item as it stands now, after whatever the plugin wrote.
func (s *Site) Page(id string) (SitePage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok {
		return SitePage{}, false
	}
	return it.SitePage, true
}

// Layout is the layout last written to a content item (its working version, for a published page); false when it has
// none.
func (s *Site) Layout(id string) (nilda.LayoutTree, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[id]
	if !ok || it.Layout == nil {
		return nilda.LayoutTree{}, false
	}
	return *it.Layout, true
}

// AddTaxonomy declares a taxonomy by key (a plugin's own, as its manifest would).
func (s *Site) AddTaxonomy(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.taxIDs[key]; !ok {
		s.taxIDs[key] = newID()
	}
}

// AddTerm adds a term to a taxonomy (declaring it when it is not yet) and answers it as Core made it.
func (s *Site) AddTerm(taxonomy string, in nilda.NewTerm) nilda.Term {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.taxIDs[taxonomy]; !ok {
		s.taxIDs[taxonomy] = newID()
	}
	return s.createTerm(taxonomy, in)
}

// FileUnder adds terms to a content item, as a person filing it in Core's editor would.
func (s *Site) FileUnder(contentID string, termIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range termIDs {
		if !contains(s.filed[contentID], id) {
			s.filed[contentID] = append(s.filed[contentID], id)
		}
	}
}

// Terms is every term a content item carries, by taxonomy key.
func (s *Site) Terms(contentID string) map[string][]nilda.Term {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.termsOf(contentID)
}

// AddMenu adds a menu at a location (a nilda.MenuLocation… value, or "") and answers its id.
func (s *Site) AddMenu(name, location string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := &siteMenu{Menu: nilda.Menu{ID: newID(), Name: name, Location: location}}
	s.menus = append(s.menus, m)
	return m.ID
}

// AddMenuItem adds an entry to a menu, as a person would in Core's Menus screen, and answers its id.
func (s *Site) AddMenuItem(menuID string, in nilda.NewMenuItem) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.menu(menuID)
	if m == nil {
		return "", fmt.Errorf("nildatest: the site has no menu %q", menuID)
	}
	it, err := s.addItem(m, in)
	if err != nil {
		return "", err
	}
	return it.ID, nil
}

// MenuItems is the tree of every menu at a location, in the order the menus were made.
func (s *Site) MenuItems(location string) []nilda.MenuItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []nilda.MenuItem
	for _, m := range s.menus {
		if m.Location == location {
			out = append(out, s.tree(m)...)
		}
	}
	return out
}

// SetMediaFile puts a file in the media library under id (made when empty) and answers the id: GET /media/{id} answers
// the item, and its address the bytes.
func (s *Site) SetMediaFile(id, filename, contentType string, data []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		id = newID()
	}
	s.media[id] = &siteMedia{item: nilda.MediaItem{ID: id, Filename: filename, MimeType: contentType,
		Size: int64(len(data)), Status: "ready", URL: "/api/v1/media/" + id + "/file", CreatedAt: s.tick()},
		data: append([]byte(nil), data...)}
	return id
}

// SetMediaUsedOn says the site shows the media item id on each of where (a page's title, a setting's name), as Core's
// where-used answers it: making that item private is then refused, as Core refuses it, naming them. None clears it.
func (s *Site) SetMediaUsedOn(id string, where ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.media[id]; ok {
		m.usedOn = append([]string(nil), where...)
	}
}

// FailNext makes the next request answer status, whatever it asks — Core restarting (503), a rate limit (429). Each
// call queues one failure.
func (s *Site) FailNext(status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, status)
}

// ---- the routes -------------------------------------------------------------------------------------------------

func (s *Site) serve(h *Host, w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.failNext) > 0 {
		status := s.failNext[0]
		s.failNext = s.failNext[1:]
		writeError(w, &apiError{status, "UNAVAILABLE", "the fake site was told to fail this request"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/reference/") {
		s.ref.serve(w, r)
		return
	}
	var parts []string
	for _, p := range strings.Split(strings.Trim(r.URL.EscapedPath(), "/"), "/") {
		seg, err := url.PathUnescape(p)
		if err != nil {
			writeError(w, invalid("the path is not a path"))
			return
		}
		parts = append(parts, seg)
	}
	q := r.URL.Query()
	route := r.Method + " " + shape(parts)
	need := ""
	switch {
	case strings.HasPrefix(route, "GET users"):
		need = "read:users"
	case route == "GET taxonomies/*/terms", route == "GET terms/*/content", route == "GET content/*/*/terms":
		need = "read:taxonomy"
	case route == "POST taxonomies/*/terms", route == "PUT content/*/*/terms":
		need = "write:taxonomy"
	case strings.HasPrefix(route, "GET menus"):
		need = "read:menus"
	case strings.HasPrefix(route, "POST menus"):
		need = "write:menus"
	case route == "GET media/*", route == "GET media/*/file":
		need = "read:media"
	case route == "PATCH media/*":
		need = "write:media"
	case strings.HasPrefix(route, "GET content/"), route == "GET layouts/holding":
		need = "read:content"
	case strings.HasPrefix(route, "POST content/"), strings.HasPrefix(route, "PUT content/"),
		strings.HasPrefix(route, "PATCH content/"), strings.HasPrefix(route, "DELETE content/"):
		need = "write:content"
	}
	if need != "" && !s.allowed(h, need) {
		writeError(w, &apiError{http.StatusForbidden, "FORBIDDEN", "API token is missing the required scope: " + need})
		return
	}
	var body map[string]json.RawMessage
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(raw) > 0 && json.Unmarshal(raw, &body) != nil {
			writeError(w, invalid("the body is not a JSON object"))
			return
		}
	}
	switch route {
	case "GET users/*":
		for _, u := range s.users {
			if u.ID == parts[1] {
				answer(w, http.StatusOK, u.User, nil)
				return
			}
		}
		writeError(w, missing(parts[1], "user not found"))
	case "GET users":
		limit, offset, err := paging(q)
		if err != nil {
			writeError(w, err)
			return
		}
		var out []nilda.User
		for _, u := range s.newestUsers() {
			if (q.Get("status") == "" || u.Status == q.Get("status")) && (q.Get("role") == "" || contains(u.roles, q.Get("role"))) {
				out = append(out, u.User)
			}
		}
		answer(w, http.StatusOK, window(out, limit, offset), nil)
	case "GET site":
		answer(w, http.StatusOK, s.site, nil)
	case "GET pages/*":
		for _, id := range s.order {
			it := s.items[id]
			if it.Slug == parts[1] && it.Status == "published" && (q.Get("type") == "" || it.Type == q.Get("type")) {
				answer(w, http.StatusOK, nilda.PublishedPage{ID: it.ID, Type: it.Type, Title: it.Title, Slug: it.Slug,
					Body: it.Body, HeadMeta: it.HeadMeta, Locale: it.Locale}, nil)
				return
			}
		}
		writeError(w, notFound("page not found"))
	case "GET breadcrumbs/*":
		it, ok := s.items[parts[1]]
		if !ok {
			writeError(w, notFound("content not found"))
			return
		}
		answer(w, http.StatusOK, []nilda.Crumb{{Label: "Home", URL: "/"}, {Label: it.Title, URL: "/" + it.Slug}}, nil)
	case "GET taxonomies/*/terms":
		if _, ok := s.taxIDs[parts[1]]; !ok {
			writeError(w, notFound("taxonomy not found"))
			return
		}
		limit, offset, err := paging(q)
		if err != nil {
			writeError(w, err)
			return
		}
		var out []nilda.Term
		for _, id := range s.termSeq {
			if t := s.terms[id]; t.taxonomy == parts[1] {
				term := t.Term
				term.Count = s.count(id)
				out = append(out, term)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
		answer(w, http.StatusOK, window(out, limit, offset), nil)
	case "POST taxonomies/*/terms":
		if _, ok := s.taxIDs[parts[1]]; !ok {
			writeError(w, notFound("taxonomy not found"))
			return
		}
		var in nilda.NewTerm
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			writeError(w, invalid("term name is required"))
			return
		}
		if in.ParentID != "" {
			if p, ok := s.terms[in.ParentID]; !ok || p.taxonomy != parts[1] {
				writeError(w, invalid("the parent is not a term of this taxonomy"))
				return
			}
		}
		answer(w, http.StatusCreated, s.createTerm(parts[1], in), nil)
	case "GET terms/*/content":
		t, ok := s.terms[parts[1]]
		if !ok {
			writeError(w, missing(parts[1], "term not found"))
			return
		}
		limit, offset, err := paging(q)
		if err != nil {
			writeError(w, err)
			return
		}
		want := map[string]bool{t.ID: true}
		if q.Get("include_children") == "true" {
			for grew := true; grew; {
				grew = false
				for _, other := range s.terms {
					if other.ParentID != "" && want[other.ParentID] && !want[other.ID] {
						want[other.ID], grew = true, true
					}
				}
			}
		}
		var out []nilda.ContentRef
		for _, id := range s.order {
			it := s.items[id]
			if it.Status != "published" {
				continue
			}
			for _, tid := range s.filed[id] {
				if want[tid] {
					out = append(out, nilda.ContentRef{ID: it.ID, Type: it.Type, Title: it.Title, Slug: it.Slug, Status: it.Status})
					break
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": window(out, limit, offset),
			"meta": map[string]any{"page": offset/limit + 1, "size": 0, "total": len(out)}})
	case "GET content/*/*/terms":
		if _, ok := s.typed(w, parts); ok {
			answer(w, http.StatusOK, s.termsOf(parts[2]), nil)
		}
	case "PUT content/*/*/terms":
		if _, ok := s.typed(w, parts); !ok {
			return
		}
		var in struct {
			TermIDs []string `json:"term_ids"`
		}
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		var set []string
		for _, id := range in.TermIDs {
			if _, ok := s.terms[id]; !ok {
				writeError(w, invalid("term %s does not exist", id))
				return
			}
			if !contains(set, id) {
				set = append(set, id)
			}
		}
		s.filed[parts[2]] = set // the whole set, every taxonomy: Core's PUT replaces
		answer(w, http.StatusOK, map[string]bool{"ok": true}, nil)
	case "GET menus":
		out := []nilda.Menu{}
		for _, m := range s.menus {
			out = append(out, m.Menu)
		}
		answer(w, http.StatusOK, out, nil)
	case "GET menus/*":
		m := s.menu(parts[1])
		if m == nil {
			writeError(w, missing(parts[1], "menu not found"))
			return
		}
		answer(w, http.StatusOK, nilda.MenuTree{Menu: m.Menu, Items: s.tree(m)}, nil)
	case "POST menus":
		var in struct{ Name, Location string }
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		if strings.TrimSpace(in.Name) == "" {
			writeError(w, invalid("a menu name is required"))
			return
		}
		m := &siteMenu{Menu: nilda.Menu{ID: newID(), Name: in.Name, Location: in.Location}}
		s.menus = append(s.menus, m)
		answer(w, http.StatusCreated, m.Menu, nil)
	case "POST menus/*/items":
		m := s.menu(parts[1])
		if m == nil {
			writeError(w, missing(parts[1], "menu not found"))
			return
		}
		var in nilda.NewMenuItem
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		it, err := s.addItem(m, in)
		if err != nil {
			writeError(w, invalid("%s", err.Error()))
			return
		}
		answer(w, http.StatusCreated, s.derive(it, nil), nil)
	case "GET layouts/holding":
		widget := q.Get("widget")
		if !widgetKey(widget) {
			writeError(w, invalid("widget must be a widget type, such as cart"))
			return
		}
		out := []nilda.HoldingPage{}
		for _, published := range []bool{true, false} {
			for _, id := range s.order {
				it := s.items[id]
				if (it.Status == "published") == published && it.Layout != nil && holds(it.Layout.Nodes, widget) && len(out) < 50 {
					out = append(out, nilda.HoldingPage{ID: it.ID, Type: it.Type, Slug: it.Slug, Status: it.Status})
				}
			}
		}
		answer(w, http.StatusOK, out, nil)
	case "PUT content/*/*/layout":
		it, ok := s.typed(w, parts)
		if !ok {
			return
		}
		var tree nilda.LayoutTree
		raw, has := body["tree"]
		if !has || json.Unmarshal(raw, &tree) != nil || tree.Nodes == nil || checkNodes(tree.Nodes) != nil {
			msg := "tree must be a layout tree: {\"nodes\": [...]}, as layout_get returns it"
			if has && tree.Nodes != nil {
				msg = checkNodes(tree.Nodes).Error()
			}
			writeError(w, invalid("%s", msg))
			return
		}
		var basedOn string
		_ = json.Unmarshal(body["based_on"], &basedOn)
		if basedOn != "" && basedOn != it.stamp.Format(time.RFC3339Nano) {
			writeError(w, &apiError{http.StatusConflict, "CONFLICT", "the page has changed since it was read; read it again"})
			return
		}
		it.Layout, it.stamp = &tree, s.tick()
		out := nilda.LayoutWrite{ContentID: it.ID, WrittenTo: nilda.LayoutWrittenToPage, Live: it.Status == "published",
			LastEditedAt: it.stamp.Format(time.RFC3339Nano)}
		if out.Live {
			out.WrittenTo = nilda.LayoutWrittenToWorkingVersion
		}
		answer(w, http.StatusOK, out, nil)
	case "POST content/*/*/publish":
		if it, ok := s.typed(w, parts); ok {
			it.Status, it.stamp = "published", s.tick()
			answer(w, http.StatusOK, nilda.PublishResult{Status: "published"}, nil)
		}
	case "POST content/*":
		var in struct {
			Title, Slug, Status string
			Body                json.RawMessage
		}
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		if err := nilda.CheckBody(in.Body); err != nil {
			writeError(w, invalid("body: %s", err.Error()))
			return
		}
		if in.Status == "" {
			in.Status = "draft"
		}
		slug := in.Slug
		if slug == "" {
			slug = slugOf(in.Title)
		}
		p := SitePage{ID: newID(), Type: parts[1], Title: in.Title, Slug: s.uniqueSlug(parts[1], slug, ""), Status: in.Status,
			Body: in.Body}
		s.put(p)
		answer(w, http.StatusCreated, contentAnswer(s.items[p.ID]), nil)
	case "GET content/*/*":
		if it, ok := s.typed(w, parts); ok {
			answer(w, http.StatusOK, contentAnswer(it), nil)
		}
	case "PATCH content/*/*":
		it, ok := s.typed(w, parts)
		if !ok {
			return
		}
		if raw, has := body["body"]; has {
			if err := nilda.CheckBody(raw); err != nil {
				writeError(w, invalid("body: %s", err.Error()))
				return
			}
			it.Body = raw
		}
		_ = json.Unmarshal(body["title"], &it.Title)
		var slug string
		if json.Unmarshal(body["slug"], &slug) == nil && slug != "" {
			it.Slug = s.uniqueSlug(it.Type, slug, it.ID)
		}
		it.stamp = s.tick()
		answer(w, http.StatusOK, contentAnswer(it), nil)
	case "DELETE content/*/*":
		if _, ok := s.typed(w, parts); ok {
			delete(s.items, parts[2])
			delete(s.filed, parts[2])
			for i, id := range s.order {
				if id == parts[2] {
					s.order = append(s.order[:i], s.order[i+1:]...)
					break
				}
			}
			answer(w, http.StatusOK, map[string]bool{"ok": true}, nil)
		}
	case "GET media/*":
		if m, ok := s.media[parts[1]]; ok {
			answer(w, http.StatusOK, m.item, nil)
			return
		}
		writeError(w, missing(parts[1], "media not found"))
	case "GET media/*/file":
		// The API's own file route: the original, private or not, always as a download that is never cached.
		m, ok := s.media[parts[1]]
		if !ok {
			writeError(w, missing(parts[1], "media not found"))
			return
		}
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.item.Filename}))
		serveMediaBytes(w, m)
	case "PATCH media/*":
		m, ok := s.media[parts[1]]
		if !ok {
			writeError(w, missing(parts[1], "media not found"))
			return
		}
		var in struct {
			Private *bool `json:"private"`
		}
		if err := decodeInto(body, &in); err != nil {
			writeError(w, err)
			return
		}
		if in.Private == nil {
			writeError(w, invalid("private is required"))
			return
		}
		if *in.Private && len(m.usedOn) > 0 {
			writeError(w, invalid("this file is shown on %s; making it private would break them — upload a copy to sell",
				strings.Join(m.usedOn, ", ")))
			return
		}
		m.item.Private = *in.Private
		m.item.URL = "/api/v1/media/" + m.item.ID + "/file"
		if m.item.Private {
			m.item.URL = "/api/v1/media/" + m.item.ID + "/private-file"
		}
		answer(w, http.StatusOK, m.item, nil)
	case "GET api/v1/media/*/file":
		// The public address a page draws a file from: a private item is not there at all.
		m, ok := s.media[parts[3]]
		if !ok || m.item.Private {
			writeError(w, notFound("media not found"))
			return
		}
		// As Core's media serveFile: only a raster image is shown inline; anything else is an attachment by its name.
		switch strings.ToLower(strings.TrimSpace(strings.SplitN(m.item.MimeType, ";", 2)[0])) {
		case "image/jpeg", "image/png", "image/webp", "image/gif":
		default:
			w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.item.Filename}))
		}
		serveMediaBytes(w, m)
	default:
		writeError(w, notFound("no such route: %s %s", r.Method, r.URL.Path))
	}
}

func serveMediaBytes(w http.ResponseWriter, m *siteMedia) {
	w.Header().Set("Content-Type", m.item.MimeType)
	w.Header().Set("Content-Length", strconv.Itoa(len(m.data)))
	_, _ = w.Write(m.data)
}

// allowed reports whether the capabilities granted now carry scope.
func (s *Site) allowed(h *Host, scope string) bool {
	h.mu.Lock()
	var granted []string
	for c, on := range h.granted {
		if on {
			granted = append(granted, c)
		}
	}
	h.mu.Unlock()
	return contains(scopesFor(granted), scope)
}

// typed is Core's loadTyped: the item by id, whose type must be the path's.
func (s *Site) typed(w http.ResponseWriter, parts []string) (*sitePageState, bool) {
	it, ok := s.items[parts[2]]
	if !ok || it.Type != parts[1] {
		writeError(w, missing(parts[2], "content not found"))
		return nil, false
	}
	return it, true
}

func (s *Site) put(p SitePage) {
	if _, ok := s.items[p.ID]; !ok {
		s.order = append(s.order, p.ID)
	}
	s.items[p.ID] = &sitePageState{SitePage: p, stamp: s.tick()}
}

func (s *Site) tick() time.Time {
	s.clock = s.clock.Add(time.Second)
	return s.clock
}

func (s *Site) newestUsers() []siteUser {
	out := append([]siteUser(nil), s.users...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *Site) createTerm(taxonomy string, in nilda.NewTerm) nilda.Term {
	slug := in.Slug
	if slug == "" {
		slug = slugOf(in.Name)
	}
	base, n := slug, 1
	for taken := true; taken; {
		taken = false
		for _, t := range s.terms {
			if t.taxonomy == taxonomy && t.Slug == slug {
				n++
				slug, taken = base+"-"+strconv.Itoa(n), true
				break
			}
		}
	}
	t := &siteTerm{Term: nilda.Term{ID: newID(), TaxonomyID: s.taxIDs[taxonomy], Name: strings.TrimSpace(in.Name),
		Slug: slug, ParentID: in.ParentID, Description: in.Description}, taxonomy: taxonomy}
	s.terms[t.ID] = t
	s.termSeq = append(s.termSeq, t.ID)
	return t.Term
}

func (s *Site) termsOf(contentID string) map[string][]nilda.Term {
	out := map[string][]nilda.Term{}
	for _, id := range s.filed[contentID] {
		if t, ok := s.terms[id]; ok {
			term := t.Term
			term.Count = s.count(id)
			out[t.taxonomy] = append(out[t.taxonomy], term)
		}
	}
	return out
}

func (s *Site) count(termID string) int {
	n := 0
	for _, ids := range s.filed {
		if contains(ids, termID) {
			n++
		}
	}
	return n
}

func (s *Site) uniqueSlug(contentType, slug, self string) string {
	if slug == "" {
		slug = "untitled"
	}
	base := slug
	for n := 2; ; n++ {
		taken := false
		for _, it := range s.items {
			if it.Type == contentType && it.Slug == slug && it.ID != self {
				taken = true
				break
			}
		}
		if !taken {
			return slug
		}
		slug = base + "-" + strconv.Itoa(n)
	}
}

func (s *Site) menu(id string) *siteMenu {
	for _, m := range s.menus {
		if m.ID == id {
			return m
		}
	}
	return nil
}

// addItem is Core's AddItem: the field the type needs, a parent in the same menu, last among its siblings.
func (s *Site) addItem(m *siteMenu, in nilda.NewMenuItem) (*siteMenuItem, error) {
	switch in.Type {
	case nilda.MenuItemContent:
		if in.ContentID == "" {
			return nil, fmt.Errorf("content_id: required for a content item")
		}
	case nilda.MenuItemTerm:
		if in.TermID == "" {
			return nil, fmt.Errorf("term_id: required for a term item")
		}
	case nilda.MenuItemURL:
		if strings.TrimSpace(in.URL) == "" {
			return nil, fmt.Errorf("url: required for a URL item")
		}
		if !safeMenuURL(in.URL) {
			return nil, fmt.Errorf("url: must be an http(s), mailto, tel, or relative (/, #) URL")
		}
	case nilda.MenuItemHome, nilda.MenuItemLabel:
		if strings.TrimSpace(in.Label) == "" {
			return nil, fmt.Errorf("label: a label is required")
		}
	default:
		return nil, fmt.Errorf("type: must be content, term, url, home, or label")
	}
	pos := 0
	found := in.ParentID == ""
	for _, it := range m.items {
		if it.ID == in.ParentID {
			found = true
		}
		if it.ParentID == in.ParentID && it.Position > pos {
			pos = it.Position
		}
	}
	if !found {
		return nil, fmt.Errorf("parent_id: parent is not in this menu")
	}
	it := &siteMenuItem{MenuItem: nilda.MenuItem{ID: newID(), Type: in.Type, Label: in.Label, Description: in.Description,
		URL: in.URL, ContentID: in.ContentID, TermID: in.TermID, ParentID: in.ParentID, Position: pos + 1,
		OpenNew: in.OpenNew}}
	m.items = append(m.items, it)
	return it, nil
}

// derive is the entry as Core answers it: a content or term entry's address and words follow what it links to, and
// one whose target is gone or unpublished is marked broken.
func (s *Site) derive(it *siteMenuItem, children []nilda.MenuItem) nilda.MenuItem {
	out := it.MenuItem
	out.Children = children
	if out.Children == nil {
		out.Children = []nilda.MenuItem{}
	}
	switch out.Type {
	case nilda.MenuItemHome:
		out.URL = "/"
	case nilda.MenuItemContent:
		if c, ok := s.items[out.ContentID]; ok && c.Status == "published" {
			out.URL = "/" + c.Slug
			if out.Label == "" {
				out.Label = c.Title
			}
		} else {
			out.Broken, out.BrokenReason = true, "unpublished"
			if !ok {
				out.BrokenReason = "missing"
			}
		}
	case nilda.MenuItemTerm:
		if t, ok := s.terms[out.TermID]; ok {
			out.URL = "/" + t.taxonomy + "/" + t.Slug
			if out.Label == "" {
				out.Label = t.Name
			}
		} else {
			out.Broken, out.BrokenReason = true, "missing"
		}
	}
	return out
}

func (s *Site) tree(m *siteMenu) []nilda.MenuItem {
	var build func(parent string) []nilda.MenuItem
	build = func(parent string) []nilda.MenuItem {
		var level []*siteMenuItem
		for _, it := range m.items {
			if it.ParentID == parent {
				level = append(level, it)
			}
		}
		sort.SliceStable(level, func(i, j int) bool { return level[i].Position < level[j].Position })
		out := []nilda.MenuItem{}
		for _, it := range level {
			out = append(out, s.derive(it, build(it.ID)))
		}
		return out
	}
	return build("")
}

// ---- helpers ----------------------------------------------------------------------------------------------------

// shape is a path with its ids as "*": "content/*/*/terms".
func shape(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	out := append([]string(nil), parts...)
	fixed := map[string][]int{ // the segments that are words, per first segment
		"users": {0}, "site": {0}, "pages": {0}, "breadcrumbs": {0}, "taxonomies": {0, 2}, "terms": {0, 2},
		"content": {0, 3}, "menus": {0, 2}, "layouts": {0, 1}, "media": {0, 2}, "api": {0, 1, 2, 4},
	}
	keep, ok := fixed[parts[0]]
	if !ok {
		return strings.Join(parts, "/")
	}
	for i := range out {
		if !containsInt(keep, i) {
			out[i] = "*"
		}
	}
	return strings.Join(out, "/")
}

func paging(q url.Values) (limit, offset int, err error) {
	limit, _ = strconv.Atoi(q.Get("limit"))
	offset, _ = strconv.Atoi(q.Get("offset"))
	switch {
	case limit <= 0:
		limit = 20
	case limit > 100:
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 10000 {
		return 0, 0, invalid("offset is limited to 10000; narrow the query with filters instead of paging this deep")
	}
	return limit, offset, nil
}

func window[T any](all []T, limit, offset int) []T {
	if offset >= len(all) {
		return []T{}
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end]
}

func decodeInto(body map[string]json.RawMessage, out any) error {
	raw, _ := json.Marshal(body)
	if err := json.Unmarshal(raw, out); err != nil {
		return invalid("the body does not have the shape this route reads: %s", err.Error())
	}
	return nil
}

// missing is Core's answer for an id it does not have: a 404, or a 422 when the id is not a uuid at all.
func missing(id, what string) error {
	if !uuidShape(id) {
		return invalid("a valid id is required")
	}
	return notFound("%s", what)
}

func uuidShape(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
				return false
			}
		}
	}
	return true
}

func contentAnswer(it *sitePageState) map[string]any {
	out := map[string]any{"id": it.ID, "type": it.Type, "status": it.Status, "title": it.Title, "slug": it.Slug,
		"version": 1, "trashed": false, "values": map[string]any{}, "locale": it.Locale}
	if len(it.Body) > 0 {
		out["body"] = it.Body
	}
	return out
}

func checkNodes(nodes []nilda.LayoutNode) error {
	for _, n := range nodes {
		switch n.Kind {
		case nilda.LayoutSection, nilda.LayoutColumn:
		case nilda.LayoutWidget:
			if !widgetKey(n.Widget) {
				return fmt.Errorf("node %q is a widget with no widget type", n.ID)
			}
		default:
			return fmt.Errorf("node %q has the kind %q; a node is a section, a column or a widget", n.ID, n.Kind)
		}
		if err := checkNodes(n.Children); err != nil {
			return err
		}
	}
	return nil
}

func holds(nodes []nilda.LayoutNode, widget string) bool {
	for _, n := range nodes {
		if (n.Kind == nilda.LayoutWidget && n.Widget == widget) || holds(n.Children, widget) {
			return true
		}
	}
	return false
}

// widgetKey is Core's rule for a widget's key.
func widgetKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func safeMenuURL(u string) bool {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "//") {
		return false
	}
	if strings.HasPrefix(u, "/") || strings.HasPrefix(u, "#") {
		return true
	}
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return parsed.Host != ""
	case "mailto", "tel":
		return true
	}
	return false
}

func slugOf(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func containsInt(list []int, n int) bool {
	for _, v := range list {
		if v == n {
			return true
		}
	}
	return false
}
