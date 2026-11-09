package nilda

// A PAGE'S LAYOUT, AS CORE'S PAGE BUILDER STORES IT. A plugin that lays out a page for the site — a shop's cart,
// checkout and account pages, the default page of its content type (the manifest's `templates`) — writes this tree
// (PUT /content/{type}/{id}/layout, or a template's `tree`): sections hold columns, columns hold widgets, a widget is
// named by its key and set by its config. Core validates every widget's config on save and draws the page.
// layouttree_test.go holds the shape to Core's LayoutNode and the widget keys to Core's registry
// (docs/WIDGET_REGISTRY.json) when Core is beside the SDK.

// LayoutTree is a page's layout: its top-level sections, in order.
type LayoutTree struct {
	Nodes []LayoutNode `json:"nodes"`
}

// LayoutNode is one node: a section, a column or a widget (Kind), with its children.
type LayoutNode struct {
	// ID is unique within the tree; a stable one ("shop-cart") lets your own later write find the node again.
	ID string `json:"id"`
	// Kind is LayoutSection, LayoutColumn or LayoutWidget.
	Kind string `json:"kind"`
	// Widget is a widget's key (Widget… below, or any key Core's registry lists), when Kind is LayoutWidget.
	Widget string `json:"widget,omitempty"`
	// Name is what the author calls the block in the builder's layers panel; it renders nowhere.
	Name string `json:"name,omitempty"`
	// Config is a widget's settings, by the field keys its registry entry names.
	Config map[string]any `json:"config,omitempty"`
	// Settings is a section's or column's own layout settings.
	Settings map[string]any `json:"settings,omitempty"`
	Children []LayoutNode   `json:"children,omitempty"`
}

// The kinds of node.
const (
	LayoutSection = "section"
	LayoutColumn  = "column"
	LayoutWidget  = "widget"
)

// The keys of Core's widgets a shop draws with (they draw from the plugin holding the `commerce` capability), and the
// two everyday ones a shop's own pages use beside them. Core's registry has the rest, and every field each one takes.
const (
	WidgetAccount        = "account"
	WidgetAddToCart      = "add_to_cart"
	WidgetCart           = "cart"
	WidgetCartCount      = "cart_count"
	WidgetCheckout       = "checkout"
	WidgetProductField   = "product_field"
	WidgetProductGallery = "product_gallery"
	WidgetProductLoop    = "product_loop"
	WidgetProductReviews = "product_reviews"
	WidgetProductTerms   = "product_terms"
	// WidgetProductFilter is the shop's filters with their facets (commerce plan 1C, core #225/#228).
	WidgetProductFilter = "product_filter"
	// WidgetPaymentMethods is the strip of the payment marks the checkout's methods name (PaymentMethod.Marks; plan 1C).
	WidgetPaymentMethods = "payment_methods"
	// WidgetExpressCheckout is the wallet buttons (v0.58.0, EXPERIMENTAL; docs/COMMERCE.md §6): config "placement"
	// ("" a product's page, "cart", "checkout"), "product_id", "error_text".
	WidgetExpressCheckout = "express_checkout"
	WidgetButton          = "button"
	WidgetPostContent     = "post_content"
)
