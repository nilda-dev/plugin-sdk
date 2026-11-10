// Package siteimport is the neutral description of a site that a reader plugin hands to Core and Core's importer writes:
// the model every platform's reader produces, and the streaming JSON encoding Core's snapshot stores (snapshot version 5).
//
// EXPERIMENTAL. The JSON shape follows Core's snapshot version, not this module's: a reader names the snapshot version of
// every piece it sends and Core refuses one it cannot read. A change here is a change to what Core stores, so it is made
// with Core's tests (the golden sites in Core's internal/importer/testdata/golden are the contract).
//
// Copied from Core's internal/importer (model.go, snapshot.go) with its json tags unchanged; Core keeps its own copy of the
// types until its readers leave (stage 2, card K7) and a test there holds the two equal.
package siteimport

import (
	"encoding/json"
	"time"
)

// Site is the common intermediate model every parser produces. The pipeline consumes ONLY this, so a
// new format is just a new parser — the map/convert/fetch/dedupe/redirect logic is shared. The json tags are the
// shape a live read's snapshot stores (plan §C.2/§C.3): one model, no parallel DTO.
type Site struct {
	BaseURL string `json:"base_url,omitempty"` // the source site's base URL (to resolve relative links), best-effort
	// SiteKey is which site an export came from — its address without the scheme — or "" for a source that does not
	// say (Ghost, Medium, Substack, a feed). It namespaces the ids a source numbers per site (a post id, a term id) and
	// names the release an import puts its items in.
	SiteKey string            `json:"site_key,omitempty"`
	Authors map[string]Author `json:"authors"` // author registry keyed by the platform's author login/id (WXR dc:creator, Ghost author_id)
	// Vocabularies and Terms are the categories, tags and custom vocabularies the source names, with each term's own
	// slug, parent and description. Items point at them by TermRef; a source that only names a term on the item
	// (Ghost, a feed) has none here, and the term is made from the name.
	Vocabularies []Vocabulary `json:"vocabularies,omitempty"`
	Terms        []Term       `json:"terms,omitempty"`
	Items        []Item       `json:"items"`
	// Media is every file the source's media library lists, not just those referenced as a featured image or inline
	// in a body — WXR exports PDFs and gallery-only images as standalone attachment items, and collecting them only
	// for _thumbnail_id resolution dropped all of those on the floor.
	Media []MediaItem `json:"media,omitempty"`
	// MediaPolicy is what an import of this site may fetch (Rule MEDIA, mediaphase.go): its library and its own hosts.
	// nil = any public address (Ghost, Medium, Substack, a feed), as these sources always had.
	MediaPolicy *MediaPolicy `json:"media_policy,omitempty"`
	// Identity is what the old site calls itself and the clock it keeps. Milestone 1 only REPORTED it: a migration that
	// renamed the site it lands on would be a surprise nobody asked for. Since M2b-7 it is OFFERED: the settings phase stages
	// it (a fill where this site's field is empty, an unticked replacement where it holds something), and the publish act
	// writes only what the owner left ticked.
	Identity *SiteIdentity `json:"identity,omitempty"`
	// THE SITE'S STRUCTURE (milestone 2, plan M2a §C.2): what the old site is built of besides its items. Each reader
	// fills what its door can see; the structure phases (phases.go) turn it into this site's types, menus, SEO and
	// staged redirects.
	Types       []SourcePostType     `json:"types,omitempty"`        // custom post types carried (never post, page, attachment)
	FieldGroups []SourceFieldGroup   `json:"field_groups,omitempty"` // ACF field-group definitions (file door only)
	Menus       []SourceMenu         `json:"menus,omitempty"`        // classic menus and block navigation
	Locations   []SourceMenuLocation `json:"menu_locations,omitempty"`
	Redirects   []SourceRedirect     `json:"redirects,omitempty"` // redirect-plugin rules (Redirection live, Safe Redirect Manager file)
	Settings    *SiteSettings        `json:"settings,omitempty"`  // allow-listed reading settings (live only); offered by the settings phase (M2b-7)
	Plugins     []string             `json:"plugins,omitempty"`   // detected: yoast | rankmath | seopress | aioseo | acf | redirection | srm (wording only)
	// THE SITE'S CHROME (plan M2b card M2b-2, D-106): what the old site is dressed in besides its items. Each reader fills
	// what its door can see; the chrome phases (phases.go) stage it for the one publish act.
	Sidebars  []SourceSidebar `json:"sidebars,omitempty"`   // the active sidebars and their widgets (live only)
	Blocks    []SourceBlock   `json:"blocks,omitempty"`     // wp_block: synced and unsynced patterns (both doors)
	CustomCSS *SourceCSS      `json:"custom_css,omitempty"` // the newest custom_css post (file door only: the live door never follows the theme's link to the user's styles, V23)
	Theme     *SourceTheme    `json:"theme,omitempty"`      // the block theme: its templates, parts and global styles
	// THE SITE'S SHOP (milestone 3, PLAN-M3-execution §C.2): a WooCommerce catalogue as both doors read it, each thing one
	// allow-listed record (shoprecords.go). nil when the site has no shop, or a door did not read it.
	Shop *SourceShop `json:"shop,omitempty"`
	// NotCarried is every kind of thing the source holds and the import leaves behind, with how many — what the read
	// found, kept with the site (a live read's snapshot holds it). The report carries only the sentences
	// notCarriedWarnings turns it into (Report.Warnings), so nothing a file carries is dropped without a word.
	NotCarried []NotCarried `json:"not_carried,omitempty"`
	// Warnings are source-level problems that silently COST the operator content — an OPML list
	// truncated at the fan-out cap, a feed that would not fetch or parse. They are not item failures
	// (the item never made it into Items to fail), so without carrying them here they exist only in
	// the server log while the operator reads "import complete" and never learns what was dropped.
	Warnings []string `json:"warnings,omitempty"`
}

// SiteIdentity is the old site's own name and tagline.
type SiteIdentity struct {
	Title   string `json:"title,omitempty"`
	Tagline string `json:"tagline,omitempty"`
	// Timezone and GMTOffset are the site's clock as its REST index states it (timezone_string, gmt_offset); "" and 0
	// from an export file, which does not say.
	Timezone  string  `json:"timezone,omitempty"`
	GMTOffset float64 `json:"gmt_offset,omitempty"`
}

// NotCarried is one kind of thing the import leaves behind, counted.
type NotCarried struct {
	Kind   string `json:"kind"` // "post_type" | "taxonomy" | "flag" | "field"
	Name   string `json:"name"`
	Count  int    `json:"count"`
	Reason string `json:"reason"` // a reason code (nc_*, notcarried.go); the sentence is chosen by notCarriedSentence
	// Detail is the one more thing a sentence says that is neither the name nor the count — why a vocabulary could not
	// be created. "" for every loss a file parse finds.
	Detail string `json:"detail,omitempty"`
}

// Vocabulary is one set of terms the source has: WordPress's categories, its tags, or a custom taxonomy a theme or
// plugin registered. The key is the source's own name for it.
type Vocabulary struct {
	Key          string `json:"key"`
	Name         string `json:"name,omitempty"` // what the source calls it, when it says ("" → the key is the label)
	Hierarchical bool   `json:"hierarchical,omitempty"`
	// Types are the WordPress object types it applies to (both doors): a custom type's vocabulary is filed under it.
	Types []string `json:"types,omitempty"`
}

// Term is one term as the source names it. Slug is the source's own (a WordPress nicename), never derived from Name.
type Term struct {
	SourceID       string `json:"source_id"` // siteKey+"#term:"+vocab+":"+id; "" when the source has no id
	Vocab          string `json:"vocab"`
	Name           string `json:"name"`
	Slug           string `json:"slug"`
	ParentSourceID string `json:"parent_source_id,omitempty"`
	Description    string `json:"description,omitempty"`
	Link           string `json:"link,omitempty"` // the old archive address, when the source says it (a live read); "" from a file
	// SEO is what an SEO plugin kept for the term's archive page; nil when it kept nothing.
	SEO *ItemSEO `json:"seo,omitempty"`
}

// TermRef is an item's filing under one term: the term it points at when the source says which (SourceID), else the
// name alone.
type TermRef struct {
	Vocab    string `json:"vocab"`
	SourceID string `json:"source_id,omitempty"`
	Name     string `json:"name"`
	// Slug is the term's own slug when the filing names it (a WordPress item's <category nicename>), so a term the
	// source lists nowhere else — a "posts only" export lists no terms in its channel — keeps its slug, and with it its
	// old archive address, instead of one made from its name. "" for a source that names terms by name alone.
	Slug string `json:"slug,omitempty"`
}

// MediaItem is one file of the source's media library.
type MediaItem struct {
	SourceID string `json:"source_id,omitempty"` // siteKey+"#media:"+id; "" when the source has no id
	URL      string `json:"url"`                 // the original file (REST source_url / WXR attachment_url)
	Alt      string `json:"alt,omitempty"`
	// Caption is what the source showed under the file. Nilda's library keeps none, so it is named as left behind
	// (ncCaption) rather than carried.
	Caption  string   `json:"caption,omitempty"`
	Title    string   `json:"title,omitempty"`
	MimeType string   `json:"mime_type,omitempty"` // as the source declares it; the media service decides from the bytes
	Size     int64    `json:"size,omitempty"`      // bytes, when the source says (REST media_details.filesize); 0 = unknown
	Sizes    []string `json:"sizes,omitempty"`     // resized copies' URLs, when the source lists them (a live read)
	// Private says the file is a shop product's DOWNLOAD (milestone 3): it is copied into the library born private, never
	// served to the public, and handed to the shop by id (§C.1 resolved.downloads).
	Private bool `json:"private,omitempty"`
	// UploadedAt is the day the source says the file was uploaded (WXR post_date_gmt of the attachment, REST date_gmt), in UTC; nil when
	// the source does not say (a zone-less date with no GMT one is not a date: the zone is a guess). Snapshot version 5. The reader drops
	// a date before 1990 or more than a day ahead of its own clock; Core bounds it again. It dates a file Core ADDS to the library: a file
	// the library already holds (identical bytes) keeps the date it had.
	UploadedAt *time.Time `json:"uploaded_at,omitempty"`
}

// SourceShop is a WooCommerce catalogue (milestone 3, architecture §2.3). Core reads of it only what it resolves — the
// pictures, the downloads, the description, the old addresses, the SEO — and hands every record to the shop, which maps
// it into its own tables; Core never interprets the rest.
type SourceShop struct {
	Settings ShopSettings `json:"settings"`
	// Records are the shop's things, one allow-listed WooCommerce REST v3 object each (shoprecords.go), in the order the
	// shop phase sends them.
	Records []ShopRecord `json:"records"`
	Pages   ShopPages    `json:"pages"` // WooCommerce's own Shop, Cart, Checkout and Account pages
}

// ShopRecord is one thing of the shop. Data is the record of its kind exactly as encodeShopRecord wrote it — the named
// fields of shoprecords.go and nothing else — and stays raw JSON in the snapshot.
type ShopRecord struct {
	Kind           string          `json:"kind"`                       // category | tag | attribute | product | variation | links | review | coupon
	SourceID       string          `json:"source_id"`                  // siteKey#<post id>, siteKey#term:<taxonomy>:<id>, siteKey#attribute:<slug>, siteKey#review:<id>
	ParentSourceID string          `json:"parent_source_id,omitempty"` // a variation's product, a review's product
	Permalink      string          `json:"permalink,omitempty"`        // the old address (product, category, tag)
	Status         string          `json:"status"`                     // what it was at the source (publish, draft, pending, private, future; a review's approved | hold)
	Data           json.RawMessage `json:"data"`
	SEO            *ItemSEO        `json:"seo,omitempty"` // a product's or a category's SEO, as M2's readers keep an item's
}

// ShopSettings are the shop's settings a door could read (live: wc/v3 settings, allow-listed) or the owner stated (the
// file door: a WXR holds none). Nothing is pre-filled (D-M3-7): what is not known stays empty.
type ShopSettings struct {
	Currency         string `json:"currency,omitempty"`
	PriceDecimals    int    `json:"price_decimals,omitempty"`
	PricesIncludeTax *bool  `json:"prices_include_tax,omitempty"` // nil = not known
	WeightUnit       string `json:"weight_unit,omitempty"`        // kg | g | lbs | oz
	DimensionUnit    string `json:"dimension_unit,omitempty"`     // m | cm | mm | in | yd
	From             string `json:"from"`                         // site | owner
}

// ShopPages are WooCommerce's own pages, by source id ("" = not known).
type ShopPages struct {
	Shop      string `json:"shop,omitempty"`
	Cart      string `json:"cart,omitempty"`
	Checkout  string `json:"checkout,omitempty"`
	MyAccount string `json:"myaccount,omitempty"`
}

// MediaPolicy is Rule MEDIA for one site (plan §C.11): what an import of it may fetch.
type MediaPolicy struct {
	OwnHosts []string `json:"own_hosts,omitempty"` // liveHost of the site's own addresses (subdomains included)
	Library  []string `json:"library,omitempty"`   // canonicalMediaURL of every Media URL and size URL — fetched whatever host serves it
	// Authenticated says the site was read with a login the owner gave for it (the live door): its own hosts are then the
	// owner's. An export file names its own hosts itself and proves nothing, so a file import's own hosts are the owner's
	// only where the site-ownership proof says so (ownFileHost).
	Authenticated bool `json:"authenticated,omitempty"`
}

// Author is a source author definition. It always becomes a guest byline (D-104): an address in an export file
// is whatever whoever wrote the file put there, so it is not read at all and never matched to a user of this site.
type Author struct {
	Login string `json:"login,omitempty"` // the platform's author key (WXR wp:author_login, Ghost user id)
	Name  string `json:"name,omitempty"`
	// SourceID is siteKey+"#author:"+the WordPress user number (WXR wp:author_id, REST users id — the same $user->ID,
	// research-m1-verified §11), so the guest byline it became is remembered across imports of the site (importmap.go).
	// "" when the source does not number its authors, or does not say which site it is: then the byline is made once
	// per run.
	SourceID string `json:"source_id,omitempty"`
	// Bio and Link (snapshot version 5) are what the source says about the author: the plain-text biography and the author's own
	// website. A live WordPress read has both (REST /users description and url); an export file has neither, and a reader that
	// has none leaves them empty, which writes the bytes version 4 wrote. The reader bounds them (a bio of at most 2,000
	// characters, as long as Nilda's own author bio may be; one address of at most 2,048 bytes) and strips control and format characters
	// (a right-to-left override can make a name or an address read backwards);
	// Core checks them again and drops a link that is not http(s) without dropping the author. There is no slug: a guest author
	// has none.
	Bio  string `json:"bio,omitempty"`
	Link string `json:"link,omitempty"`
}

// Item is one importable piece of content (a post or page). SourceID is the dedupe key.
type Item struct {
	SourceID  string `json:"source_id"` // the platform's stable item id (WXR wp:post_id, Ghost id, Medium/Substack file, RSS guid)
	Type      string `json:"type"`      // "post" | "page"
	Title     string `json:"title"`
	Slug      string `json:"slug,omitempty"`      // preserved where the source provides one
	Permalink string `json:"permalink,omitempty"` // the old absolute URL (→ an old→new redirect); "" unless the item was published
	BodyHTML  string `json:"body_html,omitempty"` // raw source HTML → CleanPaste → Lexical
	Excerpt   string `json:"excerpt,omitempty"`
	// SourceStatus is what the item was at the source: published | scheduled | draft. It is NOT the status it gets
	// here — every item lands as a draft (Rule HOLD, D-104) — but what the publish step reads to put back what was
	// public.
	SourceStatus string     `json:"source_status"`
	PublishedAt  *time.Time `json:"published_at,omitempty"` // preserved original publish date (nil → derive on create)
	// ScheduledAt is the day a scheduled item was due to go out at the source. It is REPORTED, not applied: the item
	// is held as a draft (Rule HOLD) and its owner schedules it again. nil for anything that was not scheduled.
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	// ParentSourceID is the SourceID of the page this page sits under; "" for a page at the top level and for anything
	// that is not a page.
	ParentSourceID string    `json:"parent_source_id,omitempty"`
	MenuOrder      int32     `json:"menu_order,omitempty"`   // the page's place among its siblings
	AuthorKey      string    `json:"author_key,omitempty"`   // key into Site.Authors, when the source separates authors from items
	AuthorName     string    `json:"author_name,omitempty"`  // inline author name (Medium/RSS carry it on the item)
	TermRefs       []TermRef `json:"term_refs,omitempty"`    // the categories, tags and custom terms the item is filed under
	FeaturedURL    string    `json:"featured_url,omitempty"` // featured-image URL (WXR _thumbnail_id attachment, Ghost feature_image)
	Comments       []Comment `json:"comments,omitempty"`     // the item's discussion, when the source exports it inline (WXR)
	// Values are the item's custom-field values by SOURCE field name (the ACF name), raw as the source gives them — a
	// repeater's or flexible field's as rows (both doors: the file's reader recomposes an export file's flattened rows,
	// M2-10; the live door gets them so, M2-11).
	Values   map[string]any `json:"values,omitempty"`
	SEO      *ItemSEO       `json:"seo,omitempty"`
	Template string         `json:"template,omitempty"` // _wp_page_template / REST template; kept by the page templates phase when the theme has one of that name, else named
	// ModifiedAt is when the source says the item was last edited, in UTC (WXR post_modified_gmt, REST modified_gmt); nil when the
	// source does not say (a zone-less modified time with no GMT one is not a date: the zone is a guess). Snapshot version 5. Core
	// keeps it beside the item's own edit time, not in place of it, and a search engine is told it as the item's modified date until
	// the owner first edits the item. A reader hands the date as it reads it; Core drops one before 1990, more than a day ahead of its
	// own clock, or before the item's publish date.
	ModifiedAt *time.Time `json:"modified_at,omitempty"`
}

// SourcePostType is one custom post type the old site registered.
type SourcePostType struct {
	Key          string   `json:"key"`                // the WordPress key as given (may hold hyphens)
	Name         string   `json:"name,omitempty"`     // plural label
	Singular     string   `json:"singular,omitempty"` // singular label
	Hierarchical bool     `json:"hierarchical,omitempty"`
	Taxonomies   []string `json:"taxonomies,omitempty"`
	Count        int      `json:"count,omitempty"` // items the source holds of it (all statuses read)
}

// SourceFieldGroup is one ACF field group: the fields it defines and the post types its location rules name.
type SourceFieldGroup struct {
	SourceID string        `json:"source_id"` // siteKey+"#fieldgroup:"+ACF group key (group_…)
	Title    string        `json:"title"`
	Origin   string        `json:"origin"` // "acf"
	Types    []string      `json:"types"`  // WordPress post types its location rules name (param post_type, operator ==)
	Fields   []SourceField `json:"fields"`
}

// SourceField is one field of a group (or a sub-field of a repeater, group or flexible layout).
type SourceField struct {
	SourceID string              `json:"source_id"` // siteKey+"#field:"+ACF field key (field_…)
	Key      string              `json:"key"`       // ACF field key
	Name     string              `json:"name"`      // the meta name its values are stored under
	Label    string              `json:"label"`
	Type     string              `json:"type"` // the ACF type, e.g. "wysiwyg" (plan §C.9.3 maps it)
	Choices  []SourceFieldChoice `json:"choices,omitempty"`
	Multiple bool                `json:"multiple,omitempty"`
	Targets  []string            `json:"targets,omitempty"`  // post_object/relationship/page_link: post types; taxonomy: the taxonomy
	Sub      []SourceField       `json:"sub,omitempty"`      // repeater, group
	Layouts  []SourceFieldLayout `json:"layouts,omitempty"`  // flexible content
	CloneOf  []string            `json:"clone_of,omitempty"` // clone: the field or group keys it clones
}

// SourceFieldChoice is one choice of a select, radio or checkbox field.
type SourceFieldChoice struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// SourceFieldLayout is one layout of a flexible-content field.
type SourceFieldLayout struct {
	Name  string        `json:"name"`
	Label string        `json:"label"`
	Sub   []SourceField `json:"sub"`
}

// ItemSEO is what an SEO plugin kept for one item or term archive.
type ItemSEO struct {
	Origin            string   `json:"origin"`          // yoast | rankmath | seopress | aioseo
	Title             string   `json:"title,omitempty"` // as stored: variables NOT expanded
	Description       string   `json:"description,omitempty"`
	Canonical         string   `json:"canonical,omitempty"`
	FocusKeyword      string   `json:"focus_keyword,omitempty"`
	SecondaryKeywords []string `json:"secondary_keywords,omitempty"`
	NoIndex           *bool    `json:"noindex,omitempty"` // nil = the plugin's default (nothing stored)
	NoFollow          *bool    `json:"nofollow,omitempty"`
	NoArchive         bool     `json:"noarchive,omitempty"`
	NoSnippet         bool     `json:"nosnippet,omitempty"`
	OGTitle           string   `json:"og_title,omitempty"`
	OGDescription     string   `json:"og_description,omitempty"`
	OGImageSourceID   string   `json:"og_image_source_id,omitempty"` // siteKey+"#media:"+attachment id
	OGImageURL        string   `json:"og_image_url,omitempty"`
	Cornerstone       bool     `json:"cornerstone,omitempty"`
	Rendered          bool     `json:"rendered,omitempty"` // from a rendered head (live): variables expanded, defaults included
	Unmapped          []string `json:"unmapped,omitempty"` // what Nilda has no field for: twitter | breadcrumb | schema | noimageindex
}

// SourceMenu is one classic menu or one block navigation.
type SourceMenu struct {
	SourceID string           `json:"source_id"` // siteKey+"#menu:"+nav_menu term id | siteKey+"#navigation:"+wp_navigation post id
	Name     string           `json:"name"`
	Origin   string           `json:"origin"` // classic | block
	Items    []SourceMenuItem `json:"items"`
}

// SourceMenuItem is one link of a menu.
type SourceMenuItem struct {
	SourceID       string   `json:"source_id"` // siteKey+"#menuitem:"+post id | siteKey+"#navigation:"+post id+":"+n (1-based, document order)
	ParentSourceID string   `json:"parent_source_id,omitempty"`
	Order          int      `json:"order"`
	Label          string   `json:"label,omitempty"`            // "" = the target's own title
	Kind           string   `json:"kind"`                       // content | term | url | archive | label
	TargetSourceID string   `json:"target_source_id,omitempty"` // siteKey#<post id> or siteKey#term:<vocab>:<id>
	TargetType     string   `json:"target_type,omitempty"`      // the WordPress object: page | post | category | <cpt> | <taxonomy>
	URL            string   `json:"url,omitempty"`              // absolute or as given, for url/archive (and the fallback of content/term)
	OpenNew        bool     `json:"open_new,omitempty"`
	CSSClasses     []string `json:"css_classes,omitempty"`
	Description    string   `json:"description,omitempty"`
}

// SourceMenuLocation is one place of the old theme a menu was shown in.
type SourceMenuLocation struct {
	Location     string `json:"location"`              // the theme's location slug
	Description  string `json:"description,omitempty"` // the theme's label for it
	MenuSourceID string `json:"menu_source_id"`
}

// SourceRedirect is one rule of a redirect plugin.
type SourceRedirect struct {
	SourceID string `json:"source_id"` // siteKey+"#redirect:"+origin+":"+id
	Origin   string `json:"origin"`    // redirection | srm
	From     string `json:"from"`      // as the source gives it
	To       string `json:"to,omitempty"`
	Code     int    `json:"code"`
	Regex    bool   `json:"regex,omitempty"`
	Match    string `json:"match,omitempty"`  // Redirection match_type; "" or "url" = plain
	Action   string `json:"action,omitempty"` // Redirection action_type; "" or "url" = a redirect
	Enabled  bool   `json:"enabled"`
}

// SiteSettings are the ONLY settings a reader may keep (threat model ST1; PLAN.md §2 allow-list — title, tagline and
// timezone already ride in SiteIdentity from milestone 1).
type SiteSettings struct {
	ShowOnFront       string `json:"show_on_front,omitempty"` // posts | page — needed to know whether page_on_front is the front page
	FrontPageSourceID string `json:"front_page,omitempty"`    // siteKey#<page id>
	PostsPageSourceID string `json:"posts_page,omitempty"`
	PostsPerPage      int    `json:"posts_per_page,omitempty"`
	LogoSourceID      string `json:"logo,omitempty"` // siteKey#media:<id>
	IconSourceID      string `json:"icon,omitempty"`
	// CommentsOpen is default_comment_status (open → true, closed → false); nil = not read (H1 of the M2b plan).
	CommentsOpen *bool `json:"comments_open,omitempty"`
}

// SourceSidebar is one active sidebar of the old site and its widgets, in the order WordPress shows them.
type SourceSidebar struct {
	ID      string         `json:"id"`   // the theme's sidebar id, e.g. "sidebar-1", "footer-2"
	Name    string         `json:"name"` // its label, importText(…, 100)
	Widgets []SourceWidget `json:"widgets"`
}

// SourceWidget is one widget instance. Only the members below are ever kept (threat model WG2): no instance member
// outside them, and Rendered only for a kind Nilda has no widget for.
type SourceWidget struct {
	SourceID     string `json:"source_id"`                // siteKey+"#widget:"+sidebar+":"+widget id (text-3, block-5)
	Kind         string `json:"kind"`                     // id_base: text | custom_html | nav_menu | recent-posts | search | block | <other>
	Title        string `json:"title,omitempty"`          // instance title, importText(…, 200)
	Text         string `json:"text,omitempty"`           // text: instance text (HTML); custom_html: content; block: the block markup — ≤ 64 KiB
	MenuSourceID string `json:"menu_source_id,omitempty"` // nav_menu: siteKey+"#menu:"+term id
	Count        int    `json:"count,omitempty"`          // recent-posts: number (1-50)
	Rendered     string `json:"rendered,omitempty"`       // the HTML WordPress drew — kept ONLY for a kind Nilda has no widget for; ≤ 64 KiB
}

// SourceBlock is a wp_block post: a synced or unsynced pattern.
type SourceBlock struct {
	SourceID string `json:"source_id"` // siteKey+"#block:"+wp_block post id
	Title    string `json:"title"`
	Markup   string `json:"markup"` // block markup, ≤ 1 MiB (BT3)
	Synced   bool   `json:"synced"` // wp_pattern_sync_status != "unsynced"
}

// SourceCSS is the old site's Additional CSS.
type SourceCSS struct {
	Theme  string   `json:"theme,omitempty"`  // the stylesheet slug it belonged to
	CSS    string   `json:"css"`              // ≤ 1 MiB as read; the act judges it by CustomCSSMax
	Others []string `json:"others,omitempty"` // other themes' custom_css posts left out (their slugs)
}

// SourceTheme is the old site's block theme as far as a door could read it.
type SourceTheme struct {
	Stylesheet string           `json:"stylesheet"` // e.g. "twentytwentyfour"
	Name       string           `json:"name,omitempty"`
	Complete   bool             `json:"complete,omitempty"` // the door read the theme's OWN templates too (live); false = only the edited ones (file)
	Templates  []SourceTemplate `json:"templates,omitempty"`
	// GlobalStyles is a theme.json document: the theme's BASE styles (live: the door never follows the theme's link to
	// the user's styles, V23), or the user's alone (file).
	GlobalStyles json.RawMessage `json:"global_styles,omitempty"`
}

// SourceTemplate is a block-theme template or template part.
type SourceTemplate struct {
	SourceID string `json:"source_id"`        // siteKey+"#template:"+theme+"//"+slug (WordPress's own id form)
	Kind     string `json:"kind"`             // template | part
	Slug     string `json:"slug"`             // "single", "header", "page-wide"
	Area     string `json:"area,omitempty"`   // part: header | footer | uncategorized
	Source   string `json:"source,omitempty"` // theme | custom
	Markup   string `json:"markup"`           // ≤ 1 MiB (BT3)
}

// StructurePlan is the plan of the live-site changes an import stages (structure.go fills it, card M2-4): one line per
// bulk kind, and the rows shown one by one.
type StructurePlan struct {
	Classes []StructureClass  `json:"classes"`           // one line per bulk kind
	Changes []StructureChange `json:"changes"`           // rows shown one by one: non-bulk kinds, and held rows of bulk kinds (≤ 200)
	More    int               `json:"more,omitempty"`    // held rows not listed (over 200)
	Applied bool              `json:"applied,omitempty"` // at least one row applied and not reverted
}

// StructureClass is one bulk kind of change, ticked as a whole. Its counts say, by status, what the class's rows are now
// — what waits (pending, an act's unfinished row included), and what the act and the undo made of the rest — so the
// screen follows the class through the act and the undo (review of milestone 2a, U-6).
type StructureClass struct {
	Kind     string `json:"kind"`
	Count    int    `json:"count"` // rows of the kind that follow the class tick (not held), any status
	Pending  int    `json:"pending"`
	Applied  int    `json:"applied,omitempty"`
	Reverted int    `json:"reverted,omitempty"` // put back by the undo
	Kept     int    `json:"kept,omitempty"`     // left by the undo: changed since
	Refused  int    `json:"refused,omitempty"`  // not allowed to the person acting, or refused by the site
	Stale    int    `json:"stale,omitempty"`    // no longer applied when the act came
	Default  bool   `json:"default"`
	Chosen   bool   `json:"chosen"`
	Sentence string `json:"sentence"`
}

// StructureChange is one staged change, shown on its own.
type StructureChange struct {
	ID       string `json:"id,omitempty"` // the staged row; "" in a dry run
	Kind     string `json:"kind"`
	Target   string `json:"target"`
	Action   string `json:"action"`           // add | replace | fill
	Before   string `json:"before,omitempty"` // a fragment, data ("Main (5 links)")
	After    string `json:"after,omitempty"`
	Default  bool   `json:"default"`
	Chosen   bool   `json:"chosen"`
	Held     bool   `json:"held,omitempty"`   // leads off this site: ticked only one by one
	Status   string `json:"status,omitempty"` // pending | applying | applied | refused | stale | reverted | kept; "" in a dry run
	Sentence string `json:"sentence"`
	Note     string `json:"note,omitempty"` // why refused / stale / kept
	// Unticked is why the row is not ticked for the owner when the reason is not the usual one — that it replaces what is
	// there now, which the screen says itself: a sentence (a widget area of only what WordPress drew), else "".
	Unticked string `json:"unticked,omitempty"`
	// Restyles are the protected things of this site's pages a stylesheet's selectors name (the cookie consent bar, the
	// legal links, the text marked as written by AI), by id — "consent", "legal_notice", "privacy_policy", "ai_text" —
	// for the screen to say in the reader's words and list in the reader's language. Only the imported stylesheet has them.
	Restyles []string `json:"restyles,omitempty"`
	// Subject is what the change is about, as the owner knows it: a location by the Menus screen's name, an address,
	// an item's or a category's title (review of milestone 2a, U-3).
	Subject string `json:"subject,omitempty"`
}

// Comment is one imported discussion entry. SourceID/ParentSourceID are the SOURCE's ids, remapped to
// Nilda ids at insert time so reply threading survives the move.
type Comment struct {
	SourceID       string     `json:"source_id"`
	ParentSourceID string     `json:"parent_source_id,omitempty"` // "" or "0" = top level
	AuthorName     string     `json:"author_name,omitempty"`
	AuthorEmail    string     `json:"author_email,omitempty"`
	Body           string     `json:"body"`
	Approved       bool       `json:"approved"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
}
