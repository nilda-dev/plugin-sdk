package nilda

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Each name of the wire is the one Core's browser script sends or reads.
func TestTheStorefrontWireIsCoresScript(t *testing.T) {
	src := coreSource(t, "internal", "pagebuilder", "commercewidgets.go")
	for _, want := range []string{"b={product:", "b.quantity=n", "b.variations[k]=", "b.attribution=JSON.parse(f)",
		"o.ref=", "o.path=", "'utm_source','utm_medium','utm_campaign','utm_term','utm_content'",
		"r.status!==409", "j.code!=='" + CommerceOutOfStockCode + "'"} {
		if !strings.Contains(src, want) {
			t.Errorf("Core's add-to-cart script no longer has %q", want)
		}
	}
	if !strings.Contains(src, "r.headers.get('"+CommerceRedirectHeader+"')") {
		t.Errorf("Core's shell no longer reads %s", CommerceRedirectHeader)
	}
}

func TestAnAddToCartBodyDecodes(t *testing.T) {
	var in AddToCartRequest
	body := `{"product":"mug","quantity":2,"variations":{"Colour":"Red"},"attribution":{"ref":"news.example","path":"/shop","utm_source":"letter"}}`
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatal(err)
	}
	if in.Product != "mug" || in.Quantity != 2 || in.Variations["Colour"] != "Red" || in.Attribution == nil ||
		in.Attribution.Ref != "news.example" || in.Attribution.UTMSource != "letter" {
		t.Errorf("decoded %+v %+v", in, in.Attribution)
	}
	w := httptest.NewRecorder()
	CommerceOutOfStock(w, "Sold out")
	var out map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if w.Code != http.StatusConflict || out["code"] != "out_of_stock" || !strings.Contains(w.Header().Get("Content-Type"), "json") {
		t.Errorf("the refusal: %d %v %v", w.Code, out, w.Header())
	}
}
