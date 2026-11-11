package nilda

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CORE'S OWN RESOURCES, TYPED. Every route below is one of Core's REST API (/api/rest/v1, the address Init hands the
// plugin), answering {"data": …}; the structs carry only fields Core's handlers really return, under Core's names
// (coreapi_test.go holds each to Core's source when Core is beside the SDK). A plugin used to call these through the
// untyped Get/Post/JSON and decode into structs it wrote itself — Core's JSON shapes copied by hand, which nothing told
// it when they moved. A non-2xx answer is an *APIError, as from JSON; each method's comment names the capability whose
// scope Core checks (a call without it is a 403 — APIError.Forbidden).

// ---- users (users.read) ----------------------------------------------------------------------------------------

// User is an account as Core's API shows it (Core's user.PublicUser, read-only over the API: no password, no reason
// for a suspension).
type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	// Status is UserStatusActive, UserStatusPending, UserStatusSuspended or UserStatusBanned.
	Status string `json:"status"`
	// EmailVerifiedAt is when the account confirmed its address; nil while it has not — an address nobody has
	// confirmed is one the account's holder may only have typed.
	EmailVerifiedAt *time.Time `json:"email_verified_at"`
	LastLoginAt     *time.Time `json:"last_login_at"`
	// Language is the account's own choice of language ("" for the site's).
	Language  string    `json:"language"`
	Timezone  string    `json:"timezone"`
	CreatedAt time.Time `json:"created_at"`
}

// An account's status (Core's users_status_check).
const (
	UserStatusActive    = "active"
	UserStatusPending   = "pending"
	UserStatusSuspended = "suspended"
	UserStatusBanned    = "banned"
)

// UserQuery narrows Users. An empty field is any.
type UserQuery struct {
	// Role is a role's name ("admin", "editor", …): only accounts holding it.
	Role string
	// Status is one of the UserStatus… values.
	Status string
	// Limit is at most 100 (Core's bound); 0 is Core's default page of 20. Offset skips that many.
	Limit, Offset int
}

// User reads one account — GET /users/{id}, `users.read`. An account Core does not have (or has erased) is a 404
// (APIError.NotFound): the one answer that means it is gone; any other error says nothing about whether it exists.
func (a *API) User(ctx context.Context, id string) (User, error) {
	var out struct {
		Data User `json:"data"`
	}
	if err := a.Get(ctx, "/users/"+url.PathEscape(id), nil, &out); err != nil {
		return User{}, err
	}
	return out.Data, nil
}

// Users lists accounts, newest first — GET /users?role=&status=&limit=&offset=, `users.read`.
func (a *API) Users(ctx context.Context, q UserQuery) ([]User, error) {
	query := url.Values{}
	if q.Role != "" {
		query.Set("role", q.Role)
	}
	if q.Status != "" {
		query.Set("status", q.Status)
	}
	setPaging(query, q.Limit, q.Offset)
	var out struct {
		Data []User `json:"data"`
	}
	if err := a.Get(ctx, "/users", query, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ---- the site and its published pages (no capability: Core's public reads) -------------------------------------

// Site is the site's own settings as Core's stable API answers them.
type Site struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	// BaseURL is the site's public address, "https://example.com" — what a link in a mail or a feed is built on.
	BaseURL string `json:"base_url"`
	// Locale is the site's language; Timezone the IANA zone its owner set ("" is UTC).
	Locale   string `json:"locale"`
	Timezone string `json:"timezone"`
}

// Site reads the site's settings — GET /site, no capability (a public read).
func (a *API) Site(ctx context.Context) (Site, error) {
	var out struct {
		Data Site `json:"data"`
	}
	if err := a.Get(ctx, "/site", nil, &out); err != nil {
		return Site{}, err
	}
	return out.Data, nil
}

// PublishedPage is a published content item in full, as Core's stable API answers it.
type PublishedPage struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Slug  string `json:"slug"`
	// Excerpt is "" when the item has none.
	Excerpt string `json:"excerpt,omitempty"`
	// Body is the stored Lexical document (BodyText reads it as plain text).
	Body        json.RawMessage `json:"body,omitempty"`
	PublishedAt *time.Time      `json:"published_at,omitempty"`
	AuthorID    string          `json:"author_id,omitempty"`
	// Values are the item's custom-field values, by field name.
	Values map[string]any `json:"values,omitempty"`
	// HeadMeta is the SEO head Core resolved for the page; nil when it resolved none.
	HeadMeta *HeadMeta `json:"head_meta,omitempty"`
	Locale   string    `json:"locale"`
}

// HeadMeta is the head Core writes for a page: its canonical address, robots rule and social cards. An empty field is
// one Core resolved nothing for.
type HeadMeta struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	// Canonical is the page's own address as Core states it — the one to link to.
	Canonical          string `json:"canonical,omitempty"`
	Robots             string `json:"robots,omitempty"`
	OGType             string `json:"og_type,omitempty"`
	OGTitle            string `json:"og_title,omitempty"`
	OGDescription      string `json:"og_description,omitempty"`
	OGImage            string `json:"og_image,omitempty"`
	OGURL              string `json:"og_url,omitempty"`
	TwitterCard        string `json:"twitter_card,omitempty"`
	TwitterTitle       string `json:"twitter_title,omitempty"`
	TwitterDescription string `json:"twitter_description,omitempty"`
	TwitterImage       string `json:"twitter_image,omitempty"`
}

// PageBySlug reads the PUBLISHED item at slug — GET /pages/{slug}?type=, no capability. contentType ("page") narrows
// the lookup, "" is any type. A slug nothing published answers is a 404: a draft is not found here.
func (a *API) PageBySlug(ctx context.Context, slug, contentType string) (PublishedPage, error) {
	query := url.Values{}
	if contentType != "" {
		query.Set("type", contentType)
	}
	var out struct {
		Data PublishedPage `json:"data"`
	}
	if err := a.Get(ctx, "/pages/"+url.PathEscape(strings.Trim(slug, "/")), query, &out); err != nil {
		return PublishedPage{}, err
	}
	return out.Data, nil
}

// Crumb is one step of a content item's breadcrumb trail: its words and its address on the site.
type Crumb struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Breadcrumbs reads a content item's trail, the site's home first and the item itself last — GET /breadcrumbs/{id},
// no capability. The last crumb's URL is the item's address on the site, by the site's permalink settings (a crumb
// whose address Core cannot build is left out). A 404 when Core has no trail for it.
func (a *API) Breadcrumbs(ctx context.Context, contentID string) ([]Crumb, error) {
	var out struct {
		Data []Crumb `json:"data"`
	}
	if err := a.Get(ctx, "/breadcrumbs/"+url.PathEscape(contentID), nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// ---- taxonomies and terms (taxonomy.read / taxonomy.write) -----------------------------------------------------

// Term is one term of a taxonomy (a category, a tag, a plugin's own vocabulary).
type Term struct {
	ID         string `json:"id"`
	TaxonomyID string `json:"taxonomy_id"`
	Name       string `json:"name"`
	Slug       string `json:"slug"`
	// ParentID is the term above it in a hierarchical taxonomy; "" at the top.
	ParentID    string `json:"parent_id,omitempty"`
	Description string `json:"description,omitempty"`
	// Count is how many items carry it.
	Count int `json:"count"`
}

// NewTerm is a term to create. Slug is derived from Name when empty, and made unique within the taxonomy by Core.
type NewTerm struct {
	Name        string `json:"name"`
	Slug        string `json:"slug,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	Description string `json:"description,omitempty"`
}

// Paging is one page of a list: Limit at most 100 (0 is Core's default of 20), Offset at most 10,000.
type Paging struct {
	Limit, Offset int
}

// ContentRef is a content item a term lists: who it is, not its body.
type ContentRef struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Slug   string `json:"slug"`
	Status string `json:"status"`
}

// TermContentQuery is one page of a term's items; IncludeChildren also lists what is filed under the term's
// descendants (a parent category showing its subcategories' products).
type TermContentQuery struct {
	Limit, Offset   int
	IncludeChildren bool
}

// TaxonomyTerms lists a taxonomy's terms by name — GET /taxonomies/{key}/terms?limit=&offset=, `taxonomy.read`.
func (a *API) TaxonomyTerms(ctx context.Context, taxonomy string, p Paging) ([]Term, error) {
	query := url.Values{}
	setPaging(query, p.Limit, p.Offset)
	var out struct {
		Data []Term `json:"data"`
	}
	if err := a.Get(ctx, "/taxonomies/"+url.PathEscape(taxonomy)+"/terms", query, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// CreateTerm adds a term to a taxonomy — POST /taxonomies/{key}/terms, `taxonomy.write` — and answers it as Core made
// it (its id and the slug Core gave it).
func (a *API) CreateTerm(ctx context.Context, taxonomy string, in NewTerm) (Term, error) {
	var out struct {
		Data Term `json:"data"`
	}
	if err := a.Post(ctx, "/taxonomies/"+url.PathEscape(taxonomy)+"/terms", in, &out); err != nil {
		return Term{}, err
	}
	return out.Data, nil
}

// TermContent lists the PUBLISHED items filed under a term — GET /terms/{id}/content, `taxonomy.read` — and how many
// there are in all. A taxonomy applies to more than one content type: check each item's Type.
func (a *API) TermContent(ctx context.Context, termID string, q TermContentQuery) (items []ContentRef, total int64, err error) {
	query := url.Values{}
	setPaging(query, q.Limit, q.Offset)
	if q.IncludeChildren {
		query.Set("include_children", "true")
	}
	var out struct {
		Data []ContentRef `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	if err := a.Get(ctx, "/terms/"+url.PathEscape(termID)+"/content", query, &out); err != nil {
		return nil, 0, err
	}
	return out.Data, out.Meta.Total, nil
}

// ContentTerms reads every term a content item carries, by taxonomy key — GET /content/{type}/{id}/terms,
// `taxonomy.read`. A taxonomy the item has no term of is absent.
func (a *API) ContentTerms(ctx context.Context, contentType, id string) (map[string][]Term, error) {
	var out struct {
		Data map[string][]Term `json:"data"`
	}
	if err := a.Get(ctx, contentPath(contentType, id)+"/terms", nil, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = map[string][]Term{}
	}
	return out.Data, nil
}

// SetContentTerms files a content item under exactly termIDs of ONE taxonomy, keeping the terms it carries of every
// other — `taxonomy.write`. Core's PUT /content/{type}/{id}/terms REPLACES the item's terms across EVERY taxonomy, so
// sending one taxonomy's ids would take off the categories and tags a person gave the item in Core's editor; this
// reads the item's terms first and sends the others back with yours. Empty termIDs takes the item out of that taxonomy.
// Two writers of the same item at once can still cross (the read and the write are two calls).
func (a *API) SetContentTerms(ctx context.Context, contentType, id, taxonomy string, termIDs []string) error {
	current, err := a.ContentTerms(ctx, contentType, id)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	ids := []string{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, t := range termIDs {
		add(t)
	}
	for key, terms := range current {
		if key == taxonomy {
			continue
		}
		for _, t := range terms {
			add(t.ID)
		}
	}
	return a.JSON(ctx, http.MethodPut, contentPath(contentType, id)+"/terms", map[string]any{"term_ids": ids}, nil)
}

// ---- menus (menus.read / menus.write) --------------------------------------------------------------------------

// Menu is one of the site's menus; Location is where the theme draws it ("" while it is assigned nowhere).
type Menu struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
}

// The locations Core resolves a menu at (Core's menu.Locations). A menu assigned elsewhere saves and is drawn nowhere.
const (
	MenuLocationPrimary = "primary"
	MenuLocationFooter  = "footer"
	MenuLocationMobile  = "mobile"
	MenuLocationSocial  = "social"
)

// What a menu item links to (Core's menu item types).
const (
	MenuItemURL     = "url"     // an address: URL
	MenuItemContent = "content" // a content item: ContentID; its label and address follow the item
	MenuItemTerm    = "term"    // a term's archive: TermID
	MenuItemHome    = "home"    // the site's home
	MenuItemLabel   = "label"   // words with no link, a parent for the items under it
)

// MenuItem is one entry of a menu's tree, as the admin sees it (broken links included and marked).
type MenuItem struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// URL is the address the entry links to — for a content or term entry, the one Core derived from it.
	URL       string `json:"url"`
	ContentID string `json:"content_id,omitempty"`
	TermID    string `json:"term_id,omitempty"`
	ParentID  string `json:"parent_id,omitempty"`
	Position  int    `json:"position"`
	OpenNew   bool   `json:"open_new,omitempty"`
	// Broken is an entry the site does not draw (its page gone or unpublished); BrokenReason says which.
	Broken       bool       `json:"broken,omitempty"`
	BrokenReason string     `json:"broken_reason,omitempty"`
	Children     []MenuItem `json:"children"`
}

// MenuTree is a menu with its entries, nested.
type MenuTree struct {
	Menu  Menu       `json:"menu"`
	Items []MenuItem `json:"items"`
}

// NewMenuItem is an entry to add: Type is one of the MenuItem… values, and the field it names is required (URL for
// MenuItemURL, ContentID, TermID; Label for MenuItemHome and MenuItemLabel). ParentID nests it under an entry.
type NewMenuItem struct {
	Type        string `json:"type"`
	Label       string `json:"label,omitempty"`
	URL         string `json:"url,omitempty"`
	ContentID   string `json:"content_id,omitempty"`
	TermID      string `json:"term_id,omitempty"`
	ParentID    string `json:"parent_id,omitempty"`
	Description string `json:"description,omitempty"`
	OpenNew     bool   `json:"open_new,omitempty"`
}

// Menus lists the site's menus — GET /menus, `menus.read`.
func (a *API) Menus(ctx context.Context) ([]Menu, error) {
	var out struct {
		Data []Menu `json:"data"`
	}
	if err := a.Get(ctx, "/menus", nil, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// Menu reads one menu and its tree — GET /menus/{id}, `menus.read`.
func (a *API) Menu(ctx context.Context, id string) (MenuTree, error) {
	var out struct {
		Data MenuTree `json:"data"`
	}
	if err := a.Get(ctx, "/menus/"+url.PathEscape(id), nil, &out); err != nil {
		return MenuTree{}, err
	}
	return out.Data, nil
}

// CreateMenu makes a menu at a location (a MenuLocation… value, or "" for none) — POST /menus, `menus.write`.
func (a *API) CreateMenu(ctx context.Context, name, location string) (Menu, error) {
	var out struct {
		Data Menu `json:"data"`
	}
	if err := a.Post(ctx, "/menus", map[string]string{"name": name, "location": location}, &out); err != nil {
		return Menu{}, err
	}
	return out.Data, nil
}

// AddMenuItem appends an entry to a menu (last among its siblings) — POST /menus/{id}/items, `menus.write`.
func (a *API) AddMenuItem(ctx context.Context, menuID string, in NewMenuItem) (MenuItem, error) {
	var out struct {
		Data MenuItem `json:"data"`
	}
	if err := a.Post(ctx, "/menus/"+url.PathEscape(menuID)+"/items", in, &out); err != nil {
		return MenuItem{}, err
	}
	return out.Data, nil
}

// ---- a page's layout and publishing (content.read / content.write) ---------------------------------------------

// HoldingPage is a page whose layout (live, or its working version) holds a widget.
type HoldingPage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Slug string `json:"slug"`
	// Status is "published" or "draft"; the published come first.
	Status string `json:"status"`
}

// LayoutWrite is Core's answer to a layout write: where it went, and the page's stamp after it.
type LayoutWrite struct {
	ContentID string `json:"content_id"`
	// WrittenTo is LayoutWrittenToPage, or LayoutWrittenToWorkingVersion for a live page: the working version holds
	// the layout until the page's working version is published (the site still draws the old one meanwhile).
	WrittenTo string `json:"written_to"`
	Live      bool   `json:"live"`
	// LastEditedAt is the stamp a following SetLayout passes as basedOn.
	LastEditedAt string `json:"last_edited_at,omitempty"`
}

// Where a layout write went (Core's pagebuilder.LayoutWrittenTo…).
const (
	LayoutWrittenToPage           = "page"
	LayoutWrittenToWorkingVersion = "working_version"
)

// PublishResult is Core's answer to a publish.
type PublishResult struct {
	Status string `json:"status"`
	// BecameFrontPage: the site had no home page, and this one became it.
	BecameFrontPage bool `json:"became_front_page,omitempty"`
}

// HoldingPages lists the published and draft pages whose layout holds a widget (a Widget… key), published first, at
// most 50 — GET /layouts/holding?widget=, `content.read`. Core lets a plugin ask 30 times a minute: a page that already
// holds your widget is found once, when you are about to make one.
func (a *API) HoldingPages(ctx context.Context, widget string) ([]HoldingPage, error) {
	var out struct {
		Data []HoldingPage `json:"data"`
	}
	if err := a.Get(ctx, "/layouts/holding", url.Values{"widget": {widget}}, &out); err != nil {
		return nil, err
	}
	return out.Data, nil
}

// SetLayout writes a page's whole layout — PUT /content/{type}/{id}/layout, `content.write`. Core validates every
// widget's config as its page builder does (a 422 names what it refused). basedOn is the LastEditedAt a previous write
// answered: a page edited since is a 409 (APIError.Conflict) and nothing is written; "" writes without that check.
func (a *API) SetLayout(ctx context.Context, contentType, id string, tree LayoutTree, basedOn string) (LayoutWrite, error) {
	if tree.Nodes == nil {
		tree.Nodes = []LayoutNode{}
	}
	body := map[string]any{"tree": tree}
	if basedOn != "" {
		body["based_on"] = basedOn
	}
	var out struct {
		Data LayoutWrite `json:"data"`
	}
	if err := a.JSON(ctx, http.MethodPut, contentPath(contentType, id)+"/layout", body, &out); err != nil {
		return LayoutWrite{}, err
	}
	return out.Data, nil
}

// PublishContent publishes a content item — POST /content/{type}/{id}/publish, `content.write`.
func (a *API) PublishContent(ctx context.Context, contentType, id string) (PublishResult, error) {
	var out struct {
		Data PublishResult `json:"data"`
	}
	if err := a.Post(ctx, contentPath(contentType, id)+"/publish", nil, &out); err != nil {
		return PublishResult{}, err
	}
	return out.Data, nil
}

// ---- media (media.read) ----------------------------------------------------------------------------------------

// MediaItem is one file of the site's media library.
type MediaItem struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size_bytes"`
	// Width and Height are an image's, nil for anything else.
	Width  *int   `json:"width,omitempty"`
	Height *int   `json:"height,omitempty"`
	Alt    string `json:"alt_text"`
	Status string `json:"status"`
	// URL is the file's address on the site, a path ("/api/v1/media/…/file?c=…") — what a page links to. A private
	// item's is not that public address: it opens only for a signed-in person allowed to see the library.
	URL string `json:"url"`
	// Private is true for a file Core serves to nobody on its public address — a file that is sold (a paid
	// download), opened only through MediaFile by a plugin with media.read and by staff in the library. Set it with
	// SetMediaPrivate.
	Private   bool      `json:"private"`
	CreatedAt time.Time `json:"created_at"`
}

// Media reads one media item — GET /media/{id}, `media.read`.
func (a *API) Media(ctx context.Context, id string) (MediaItem, error) {
	var out struct {
		Data MediaItem `json:"data"`
	}
	if err := a.Get(ctx, "/media/"+url.PathEscape(id), nil, &out); err != nil {
		return MediaItem{}, err
	}
	return out.Data, nil
}

// MediaBytes is a media file Core opened: read it, then Close it. What a download needs to answer its own visitor rides
// with the bytes, as Core served them — a plugin that streams the file on (a paid download) copies these headers and
// needs no second read of the item.
type MediaBytes struct {
	io.ReadCloser
	// Item is the media item, as GET /media/{id} answered it (its Filename, MimeType, Size and URL).
	Item MediaItem
	// ContentType is the type Core served the bytes as (Content-Type).
	ContentType string
	// Size is the length Core sent (Content-Length), -1 when it sent none.
	Size int64
	// Disposition is Core's Content-Disposition — "attachment" with the file's name for a file a browser must not
	// show inline (an SVG, a document), "" for one it shows (an image). A plugin offering a download sets its own
	// attachment header when this is empty.
	Disposition string
}

// MediaFile opens a media item's bytes — `media.read`. It reads the item (GET /media/{id}), then the ORIGINAL file
// through the API's own file route (GET /media/{id}/file) with the plugin's token, so a private item opens too —
// the one a plugin sells. Close what it answers. A file Core will not serve is an *APIError.
//
// The bytes are read with no total timeout: ctx is what bounds the read, so a large file a buyer downloads slowly
// arrives whole. A handler streaming it on lifts its own response's write deadline too (see StartHTTP).
func (a *API) MediaFile(ctx context.Context, id string) (*MediaBytes, error) {
	if a == nil {
		return nil, errNoAPI
	}
	m, err := a.Media(ctx, id)
	if err != nil {
		return nil, err
	}
	path := "/media/" + url.PathEscape(id) + "/file"
	// The stream client: no total timeout, so a large file read slowly is not cut mid-download. ctx bounds it — pass
	// the request's own context when you stream to a visitor, and the download ends when they leave.
	client := a.stream
	if client == nil {
		client = a.http
	}
	res, err := a.doWith(ctx, client, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, a.readResponse(res, http.MethodGet, path, nil)
	}
	return &MediaBytes{ReadCloser: res.Body, Item: m, ContentType: res.Header.Get("Content-Type"),
		Size: res.ContentLength, Disposition: res.Header.Get("Content-Disposition")}, nil
}

// SetMediaPrivate makes a media item private, or public again — PATCH /media/{id}, `media.write`. A private item is
// served to nobody on its public address: set it on a file you sell before you deliver it. Core refuses to make
// private a file a page, a post or a setting shows (it would break them), with an *APIError that names them; upload a
// copy for the sale instead. It answers the item as Core now holds it.
func (a *API) SetMediaPrivate(ctx context.Context, id string, private bool) (MediaItem, error) {
	var out struct {
		Data MediaItem `json:"data"`
	}
	if err := a.Patch(ctx, "/media/"+url.PathEscape(id), map[string]bool{"private": private}, &out); err != nil {
		return MediaItem{}, err
	}
	return out.Data, nil
}

func contentPath(contentType, id string) string {
	return "/content/" + url.PathEscape(contentType) + "/" + url.PathEscape(id)
}

func setPaging(q url.Values, limit, offset int) {
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
}
