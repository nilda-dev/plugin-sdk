package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// BEING THE SITE'S SHOP.
//
// Nilda ships eight storefront widgets — Products, Product Field, Product Categories, Cart, Cart Count,
// Add To Cart, Checkout, My Account — and no commerce code at all. Your plugin supplies the products,
// the prices and the cart URLs; the WIDGETS stay Core's.
//
// # Why the widgets are not yours, and why that is in your interest
//
// The obvious design is for a shop plugin to ship its own thirty widgets. It is what WooCommerce does,
// and it produces a world where a site that switches shops loses every page it built, no theme can style
// a product grid because it does not know what the grid is called, and a new shop has to write thirty
// widgets before it can compete on the one thing that matters.
//
// So the VOCABULARY lives in Core, once, and you supply the data. A site migrating to your plugin keeps
// its pages. A theme that styles `.pb-product-loop` styles yours. And you implement one interface instead
// of a widget library.
//
// # What Core will never ask you, and never do itself
//
// Core learns what a widget DRAWS. It does not learn how a shop WORKS — no tax rules, no coupon logic, no
// order state, and above all NO MONEY ARITHMETIC.
//
// Price is a STRING you have already formatted, symbol and all. Currency, rounding, tax display and locale
// are decisions your shop owns and gets right; a Core that formatted money would be wrong for every shop
// with a rule nobody anticipated. Core never parses it, compares it, or adds it up.
//
// There is no stock COUNT either, only InStock. "3 left" is a merchandising decision with a stock
// accounting model behind it, and a shop that reserves stock at checkout answers it differently from one
// that does not.
//
// # "But my shop has a field yours does not" — CommerceProduct.Extra
//
// It will, and that is expected rather than a problem to raise with us. The named fields are the ones every
// shop has; everything else goes in Extra under your own keys, and an author places it with the Product
// Field widget by typing the key. You do not need a Core release, a new capability or our agreement to
// invent one — which matters, because Core does not change for any single plugin, so a vocabulary with no
// way out would leave you waiting on a release that is never coming.
//
// # The cart cannot be rendered on the server, and that is not about you
//
// Public pages are cached per URL and shared between visitors, so a server-rendered cart would serve one
// shopper's basket to everybody. Core renders SHELLS that call your endpoints from the browser instead.
// That constraint would apply just as much to a widget living inside your plugin, so nothing is lost by
// the widgets being Core's — and the page cache stays correct by construction.
//
// # Forms in your fragments
//
// A fragment may carry ordinary `<form method="post" action="/shop/...">` forms — a checkout, a cart line's
// Remove, an address book. Core's shell sends them itself rather than letting the browser navigate away from
// the themed page: it POSTs the form's fields urlencoded to the form's action (same-origin only; a form with
// no action posts to the mount's own endpoint), and draws your answer back in place — into the nearest
// element inside the mount marked `data-pb-fragment` around the form, else into the whole mount. Answer with
// the HTML that should stand there. To send the shopper to another page (a payment processor's), answer 200
// with the header `Nilda-Redirect: <absolute https URL>` — not a 3xx, which the shell's fetch cannot follow
// across origins — and put the same URL in the body as a link for a browser that posted the form itself. A
// non-2xx answer keeps the form, with what the shopper typed, and shows the mount's failure sentence.
//
// Core's CSRF check lets these through because the browser stamps them same-origin (Sec-Fetch-Site / Origin);
// a plain form a shopper's browser posts to your route without the shell passes the same way.
//
// Your endpoints must be SAME-ORIGIN ROOTED PATHS ("/shop/cart"). Core drops anything that is not a path on
// this site — an absolute URL, "//host", "/\host" — and logs which one, because a shell posts a shopper's
// basket to whatever you name. That is the whole check: Core does not look at the prefix, so put them under
// your own route prefix, the only paths that reach your server.
//
// # The shape
//
//	type shop struct {
//	    core *nilda.Core // kept from Init: Emit (below) is how the storefront hears a price changed
//	    /* your catalogue */
//	}
//
//	func (s *shop) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
//	    s.core = core
//	    addr, err := nilda.StartHTTP(s) // the cart and checkout, on your route
//	    if err != nil {
//	        return nilda.InitResult{}, err
//	    }
//	    return nilda.InitResult{RouteAddr: addr}, nil // Serve adds the commerce hooks for a Commerce
//	}
//	func (s *shop) HandleHook(ctx context.Context, hook string, p []byte) ([]byte, error) { return nil, nil }
//	func (s *shop) HandleEvent(ctx context.Context, event string, p []byte) error { return nil }
//
//	func (s *shop) Products(ctx context.Context, q CommerceQuery) ([]CommerceProduct, error) { … }
//	func (s *shop) Product(ctx context.Context, id string) (CommerceProduct, bool) { … }
//	func (s *shop) Endpoints(ctx context.Context) CommerceEndpoints { … }
//	func (s *shop) CountProducts(ctx context.Context, q CommerceQuery) (int, bool) { … } // optional
//
//	// The cart and checkout the shells call — the paths Endpoints names, under your route prefix.
//	func (s *shop) ServeHTTP(w http.ResponseWriter, r *http.Request) { … }
//
//	func main() { nilda.Serve(&shop{}) }
//
// A shop is a Handler as well as a Commerce because it must EMIT (EventCommerceCatalogChanged, below), and
// Emit is a method of the *Core that only Init hands out. ServeCommerce takes a bare Commerce and keeps that
// *Core to itself, so a shop built on it cannot emit at all — use it only for a catalogue nothing outside Core
// ever changes (Core's 2026-09-24 whole-plan review, S-2). Serve answers the commerce hooks for any Handler
// that is also a Commerce, exactly as ServeCommerce does.
//
// Declare it in the manifest — `events` too, because the catalogue-changed event below is not optional:
//
//	"capabilities": ["commerce", "route", "events"],
//	"route_prefix": "/shop"
//
// At most ONE plugin on an install may provide the shop. Two would each answer half the catalogue and
// neither would know it, so Core refuses the second at install time rather than at the first missing
// product.

// Commerce hook names. The Core side of this contract is core/internal/plugin/commerce.go.
const (
	HookCommerceProducts  = "commerce.products"
	HookCommerceProduct   = "commerce.product"
	HookCommerceEndpoints = "commerce.endpoints"
	HookCommerceCount     = "commerce.count"
)

// EventCommerceCatalogChanged is what you EMIT when your catalogue moves, and you must.
//
// Nilda caches a rendered page for an hour, and it drops one when something it depends on changes. Your
// catalogue is the one dependency Nilda does not own: a merchant fixing a price in your admin touches
// nothing Core can see, so without this event the storefront serves the old price for the rest of the hour
// and the merchant reports that saving is broken.
//
//	core.Emit(ctx, nilda.EventCommerceCatalogChanged, nil)
//
// Emit it after a price edit, a stock movement, a publish or unpublish, and at the end of an import — once
// for the batch, not once per row. There is no payload: the message is "something is different", and Core
// answers by dropping every page that drew any of your catalogue. Declare the `events` capability to use it.
//
// Only the plugin that provides the shop is obeyed. Core ignores it from anyone else, because a purge any
// plugin could trigger is a cache stampede one bad plugin away.
const EventCommerceCatalogChanged = "commerce.catalog.changed"

// CommerceTerm is one product category, carrying the archive URL a chip links to.
type CommerceTerm struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	// Taxonomy is which vocabulary this belongs to — "product_cat", "brand". Empty means unspecified, and
	// a widget filtering by taxonomy then matches it either way.
	Taxonomy string `json:"taxonomy,omitempty"`
	// Slug is your own key for the term. Carried because a query-string selection has no archive path in
	// it, and parsing the slug back out of a URL breaks the day the permalink scheme changes.
	Slug string `json:"slug,omitempty"`
}

// CommerceVariation is one axis of choice a shopper answers before the item is addable — "Size", with its
// options.
//
// The options are STRINGS, not priced variants: a per-combination price belongs to your pricing rules, and
// Core holding a matrix of them would be Core re-implementing your catalogue. What Core owns is asking the
// question and handing the answer back to your add-to-cart endpoint.
type CommerceVariation struct {
	// Name is what the shopper is choosing — the control's label.
	Name string `json:"name"`
	// Key is what your endpoint receives. Empty falls back to a slug of Name.
	Key string `json:"key,omitempty"`
	// Options are the answers, in the order you want them offered.
	Options []string `json:"options,omitempty"`
	// Selected opens the control on a particular option. Empty means the first.
	Selected string `json:"selected,omitempty"`
}

// CommerceProduct is a product as a WIDGET DRAWS IT.
type CommerceProduct struct {
	// ID is your own id for the item, and what comes back to you on Product and on add-to-cart.
	ID string `json:"id"`
	// Title and URL are the tile's text and its link.
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
	// Excerpt is the short description a card shows. PLAIN TEXT — a card is not the place for markup, and
	// the full description belongs to the product page.
	Excerpt string `json:"excerpt,omitempty"`
	// Image is an already-resolved URL. You know your own media; a second lookup here would be Core
	// guessing at somebody else's storage.
	Image string `json:"image,omitempty"`
	// Price and OldPrice are FORMATTED BY YOU, symbol and all. An empty OldPrice strikes nothing through.
	Price    string `json:"price,omitempty"`
	OldPrice string `json:"old_price,omitempty"`
	// InStock drives the one piece of state a storefront must show honestly.
	InStock bool `json:"in_stock,omitempty"`
	// Badge is your own word for whatever you want flagged — "Sale", "New", "Bundle".
	Badge string `json:"badge,omitempty"`
	// SKU is your code for the item, shown on a product page: somebody ordering by phone, checking a
	// parcel or matching a spreadsheet needs it, and Core cannot derive it from anything it holds.
	SKU string `json:"sku,omitempty"`
	// Rating is 0–5 and RatingCount is how many people said so. 0 means you did not answer, not "nobody
	// liked it" — leave both alone and the widget draws nothing. A NUMBER rather than your own stars,
	// because Core already draws a row of marks and two implementations disagree about half a star.
	Rating      float64 `json:"rating,omitempty"`
	RatingCount int     `json:"rating_count,omitempty"`
	// Terms are the product's categories.
	Terms []CommerceTerm `json:"terms,omitempty"`
	// Variations are the choices a shopper must make. A shop with variations and no way to express them
	// is the case that actually breaks: the button adds the default variant silently, and the shopper
	// finds out what size they bought when it arrives.
	Variations []CommerceVariation `json:"variations,omitempty"`
	// RelatedIDs are OTHER products (your own ids) a shopper is offered alongside this one because they are
	// alike — same category, "customers who viewed this also viewed", or however your catalogue decides
	// it. Core resolves each one through your own Product(ctx, id), exactly as a single-product page
	// already does, so a related tile is drawn by the SAME code that draws every other one — nothing about
	// it is invented on Core's side. Leave it empty and the Related Products row draws nothing, the way an
	// absent Badge already does.
	RelatedIDs []string `json:"related_ids,omitempty"`
	// UpsellIDs are the products YOU chose to put beside this one — "You may also like", a merchant's own
	// curated list, as distinct from RelatedIDs' algorithmic pick. Kept as a separate field rather than
	// folded into RelatedIDs because that is a real, useful distinction to a shopper and the reference
	// builder's own Elementor widgets keep it too (a "Product Related" widget and a separate "Upsells"
	// one) — collapsing the two would mean a shop that wants both cannot have them.
	UpsellIDs []string `json:"upsell_ids,omitempty"`
	// Extra is ANYTHING ELSE YOUR SHOP SELLS ON, and it is the reason this struct does not have to grow a
	// field every time somebody's catalogue is different from somebody else's.
	//
	// The fields above are the ones every shop has. Yours will have some nobody else does — "Delivery in 3
	// days", "Minimum order 5", "Serves 4", "2.4 kg", a licence term, a lead time. Put them here under
	// your own keys and an author picks one in the Product Field widget by typing the key. No Core release,
	// no capability, no permission: a key you invent this afternoon works on this afternoon's Core.
	//
	// STRINGS, formatted by you, exactly like Price. Core escapes the value and prints it, and it never
	// parses, compares, sorts or does arithmetic on anything in here — which is precisely why you may put
	// whatever you like in it without Core ever being wrong about what it means.
	//
	// Keep it to the handful an author would actually place on a page. Core sorts the keys and keeps the
	// first 20, and trims a value past 200 characters, because this is rendered into a page that is then
	// CACHED and shared: it is a place for a short fact beside a price, not for your product's full
	// description or a blob of JSON you meant to parse on the other side.
	Extra map[string]string `json:"extra,omitempty"`
}

// CommerceQuery is what a catalogue widget asks for. Deliberately small: you own querying, and every field
// here is something an AUTHOR chose in a panel rather than a filter language Core would have to define and
// keep compatible across plugins.
type CommerceQuery struct {
	// Term is a category/collection slug or name; empty means everything.
	Term   string `json:"term,omitempty"`
	Search string `json:"search,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	// Sort is "" | newest | price-asc | price-desc | name. Anything you do not support: fall back to your
	// own default order rather than returning nothing.
	Sort     string `json:"sort,omitempty"`
	Featured bool   `json:"featured,omitempty"`
	// ShowPricesIncludingTax is Core telling you HOW THE PRICE MUST READ on this site, and it is a
	// DECISION rather than a fact for you to interpret.
	//
	// EU Directive 98/6/EC says the selling price shown to a consumer is the final price including VAT.
	// Whether that binds this site is a legal question about where it operates, and Core answers it —
	// `internal/compliance` holds the one country table in the product, precisely so a second one does
	// not appear in every shop plugin and drift from it. You never learn the country and you should not
	// want to: your shipping zones are the merchant's configuration and your tax rates are matched
	// against the BUYER'S address, and neither of those answers this.
	//
	// IT ARRIVES ON THE QUERY RATHER THAN AT INIT, deliberately. An owner correcting their address must
	// change what the next page renders, not what the next RESTART renders — a value frozen at Init is
	// the same defect as a base URL read once at boot.
	//
	// Price and OldPrice are formatted by YOU, symbol and all, so honouring this means rendering the
	// gross figure in those strings. Core never parses a price and cannot do it for you. If your shop
	// genuinely cannot compute a gross figure, render what you have rather than nothing: a missing price
	// is a broken page, and a net price is at worst the gap this flag exists to close.
	ShowPricesIncludingTax bool `json:"show_prices_including_tax,omitempty"`
}

// CommerceEndpoints are the same-origin paths your plugin serves. Core renders shells that call them; it
// never proxies, parses or caches their responses.
//
// Rooted paths only ("/shop/cart"). Core drops an absolute URL and logs which one.
type CommerceEndpoints struct {
	// Cart is a GET returning this visitor's cart as an HTML fragment.
	Cart string `json:"cart,omitempty"`
	// AddToCart is a POST. It receives the product id and, when the product has them, the shopper's
	// variation answers.
	AddToCart string `json:"add_to_cart,omitempty"`
	// Checkout and Account are pages you own.
	Checkout string `json:"checkout,omitempty"`
	Account  string `json:"account,omitempty"`
}

// Commerce is the interface a shop plugin implements.
type Commerce interface {
	// Products answers a catalogue widget. The same answer for every visitor, so its output is cacheable
	// with the page — never vary it by who is asking, because you are not told and the page is shared.
	Products(ctx context.Context, q CommerceQuery) ([]CommerceProduct, error)
	// Product resolves the item a single-product page is about. found=false renders nothing rather than
	// an empty frame.
	Product(ctx context.Context, id string) (p CommerceProduct, found bool)
	// Endpoints are the URLs the client shells call.
	Endpoints(ctx context.Context) CommerceEndpoints
}

// CommerceCounter is an OPTIONAL extra: how many products a query matches, so a catalogue that pages can
// say "showing 1–12 of 240". Return ok=false if your engine cannot count cheaply — the widget loses that
// one line, which is better than a wrong total.
//
// The query you receive has no Limit or Offset: the count is about the whole result set.
type CommerceCounter interface {
	CountProducts(ctx context.Context, q CommerceQuery) (total int, ok bool)
}

// grossPricesKey carries Core's price-display decision to a hook whose Go signature cannot take it.
//
// THE PAYLOAD IS THE WIRE TRUTH and this is only the Go surface of it. Every commerce hook receives
// `show_prices_including_tax` in its JSON, because a plugin written in another language reads the payload
// and has no Go context to read; DispatchCommerceHook lifts it into the context so `Product(ctx, id)` —
// whose signature is published and may not change under existing plugins (additive only) — can still see
// it. One fact, one place on the wire, two ways to reach it in-process.
type grossPricesKey struct{}

// ShowPricesIncludingTax reports whether Core says a consumer price on this site must include tax.
//
// It answers false when nothing set it, which is the safe direction: a shop rendering net prices where
// gross is wanted is the state every install is in today.
func ShowPricesIncludingTax(ctx context.Context) bool {
	v, _ := ctx.Value(grossPricesKey{}).(bool)
	return v
}

// withGrossPrices is how the dispatcher puts it there.
func withGrossPrices(ctx context.Context, v bool) context.Context {
	if !v {
		return ctx
	}
	return context.WithValue(ctx, grossPricesKey{}, true)
}

// ServeCommerce runs a plugin whose whole job is the shop. Use Serve with your own Handler and
// DispatchCommerceHook when your plugin does other things too.
//
// YOUR CART ENDPOINTS ARE SERVED TOO when c is also an http.Handler: ServeCommerce starts it on the plugin's
// route (StartHTTP), which is what the `route` capability and route_prefix in the manifest are for. It has to
// be — Endpoints names paths under your prefix that the storefront's shells call from the browser, and
// before this, a shop built on ServeCommerce answered every product question and 502'd every "add to cart"
// (the 2026-09-23 plugin hunt's M15). A Commerce that is not an http.Handler serves no route at all.
func ServeCommerce(c Commerce) {
	Serve(&commerceOnlyHandler{c: c})
}

type commerceOnlyHandler struct {
	c    Commerce
	core *Core
}

func (h *commerceOnlyHandler) Init(ctx context.Context, core *Core) (InitResult, error) {
	h.core = core
	// Subscribed unconditionally, like the search and auth hooks: Core delivers them only to a plugin
	// granted `commerce`, so asking without the capability is harmless — and not asking WITH it would be
	// a plugin that installs cleanly and is then never asked for a single product.
	res := InitResult{Hooks: []string{
		HookCommerceProducts, HookCommerceProduct, HookCommerceEndpoints, HookCommerceCount,
	}}
	if routes, ok := h.c.(http.Handler); ok {
		addr, err := StartHTTP(routes)
		if err != nil {
			return InitResult{}, err
		}
		res.RouteAddr = addr
	}
	return res, nil
}

func (h *commerceOnlyHandler) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, handled, err := DispatchCommerceHook(ctx, h.core, h.c, hook, payload)
	if !handled {
		return nil, fmt.Errorf("unknown hook %q", hook)
	}
	return out, err
}

func (h *commerceOnlyHandler) HandleEvent(context.Context, string, []byte) error { return nil }

// DispatchCommerceHook routes one commerce hook to c. handled=false means it was not a commerce hook, so
// a plugin that does several things can pass it on rather than failing it.
func DispatchCommerceHook(ctx context.Context, _ *Core, c Commerce, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookCommerceProducts:
		var q CommerceQuery
		if err := json.Unmarshal(payload, &q); err != nil {
			return nil, true, fmt.Errorf("nilda: commerce.products payload: %w", err)
		}
		products, err := c.Products(withGrossPrices(ctx, q.ShowPricesIncludingTax), q)
		if err != nil {
			return nil, true, err
		}
		// A nil slice marshals as null, and a caller reading `.products[0]` on null is a crash in
		// whatever language reads it next. An empty catalogue is a list with nothing in it.
		if products == nil {
			products = []CommerceProduct{}
		}
		b, err := json.Marshal(map[string]any{"products": products})
		return b, true, err

	case HookCommerceProduct:
		var in struct {
			ID                     string `json:"id"`
			ShowPricesIncludingTax bool   `json:"show_prices_including_tax,omitempty"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: commerce.product payload: %w", err)
		}
		p, found := c.Product(withGrossPrices(ctx, in.ShowPricesIncludingTax), in.ID)
		if !found {
			// An explicit null, not an omitted key: "we looked and there is no such product" and "the
			// plugin did not answer" must not be the same bytes.
			return []byte(`{"product":null}`), true, nil
		}
		b, err := json.Marshal(map[string]any{"product": p})
		return b, true, err

	case HookCommerceEndpoints:
		b, err := json.Marshal(c.Endpoints(ctx))
		return b, true, err

	case HookCommerceCount:
		counter, ok := c.(CommerceCounter)
		if !ok {
			// Not an error: counting is optional, and an error here would be logged on every page of
			// every catalogue that pages.
			return []byte(`{}`), true, nil
		}
		var q CommerceQuery
		if err := json.Unmarshal(payload, &q); err != nil {
			return nil, true, fmt.Errorf("nilda: commerce.count payload: %w", err)
		}
		total, answered := counter.CountProducts(ctx, q)
		if !answered {
			return []byte(`{}`), true, nil
		}
		b, err := json.Marshal(map[string]any{"total": total})
		return b, true, err
	}
	return nil, false, nil
}
