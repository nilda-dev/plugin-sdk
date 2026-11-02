package nilda

import (
	"encoding/json"
	"net/http"
)

// THE STOREFRONT'S WIRE — what Core's commerce widgets send a shop's endpoints and read back, so a shop decodes and
// answers them with the SDK's types instead of copying Core's browser script. commercewire_test.go holds each name to
// Core's script (internal/pagebuilder/commercewidgets.go) when Core is beside the SDK.

// CommerceRedirectHeader is the header a fragment's form answer carries to send the shopper to another page (a
// payment processor's): answer 200 with it set to an absolute https URL; the shell follows it (see "Forms in your
// fragments" in commerce.go).
const CommerceRedirectHeader = "Nilda-Redirect"

// AddToCartRequest is the JSON body Core's add-to-cart button POSTs to CommerceEndpoints.AddToCart.
type AddToCartRequest struct {
	// Product is the button's data-product: the product id or SKU the shop gave the widget.
	Product string `json:"product"`
	// Quantity is the widget's quantity box, when it offers one and it holds a positive number; 0 when not sent.
	Quantity int64 `json:"quantity,omitempty"`
	// Variations are the row's variation answers, axis key to the value chosen; nil when the product has none.
	Variations map[string]string `json:"variations,omitempty"`
	// Attribution is the visit's first touch, sent only while the visitor allows analytics; nil otherwise.
	Attribution *FirstTouch `json:"attribution,omitempty"`
}

// FirstTouch is where a visit first came from, as Core's page records it once per browser session: the referring
// host when it was another site, the landing path, and the campaign tags of the landing address. Each value is at
// most 256 characters; any may be empty.
type FirstTouch struct {
	Ref         string `json:"ref,omitempty"`
	Path        string `json:"path,omitempty"`
	UTMSource   string `json:"utm_source,omitempty"`
	UTMMedium   string `json:"utm_medium,omitempty"`
	UTMCampaign string `json:"utm_campaign,omitempty"`
	UTMTerm     string `json:"utm_term,omitempty"`
	UTMContent  string `json:"utm_content,omitempty"`
}

// CommerceOutOfStockCode is the code of a sold-out refusal: an add-to-cart answered 409 with it opens the row's
// back-in-stock form (CommerceSettings.StockAlert), when the page has one.
const CommerceOutOfStockCode = "out_of_stock"

// CommerceOutOfStock answers an add-to-cart for a choice that is sold out: 409, JSON {"code":"out_of_stock",
// "message": message}. Any other 409 (a basket in another currency) is a refusal like any other.
func CommerceOutOfStock(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": CommerceOutOfStockCode, "message": message})
}
