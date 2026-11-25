package nilda

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// THE VISITOR'S BASKET, AS JSON (v0.75.0, EXPERIMENTAL): what CommerceEndpoints.Basket answers.
//
// A widget that puts things in the basket — a lookbook's "shop the look", a bundle builder, another plugin's — posts to
// CommerceEndpoints.AddToCart, and the shop answers each add on its own terms (a size sold out, a basket full). Cart is an
// HTML fragment to draw, not data to read. Basket is the data: the lines the visitor's basket holds now, by SKU and
// quantity, so the widget can
//
//   - RECONCILE ITS ADDS: after posting a look's pieces, ask Basket and see which pieces are in and how many, rather
//     than trusting a run of answers it may have lost (a refused add, a tab closed half-way, a second tab), and
//   - KNOW WHETHER THE BASKET HOLDS ONLY A LOOK: a LookQuoteAnswer prices its lines as a basket of their own, so a
//     caller shows the look's saving only while the basket holds nothing else (BasketAnswer.HoldsOnly).
//
// THE DOOR. A GET, same-origin, from the visitor's browser, so the shop's basket cookie and the visitor's sign-in go with
// it. The shop answers 200 with a JSON object whose "lines" is an array — [] for an empty basket, never null — of objects
// carrying at least "sku" (the variant's SKU, as the catalogue names it) and "qty" (a whole number of units), spelled
// exactly so: a browser's script reads line.sku, and JavaScript does not forgive "SKU". A shop may put more keys beside
// them (a title, a price, a subtotal); a caller reads them at its own risk, as they are the shop's and not the SDK's. A
// SKU may stand on more than one line (a one-time line and a subscription line of the same variant). Always with
// Cache-Control: no-store — it is one visitor's basket. It changes nothing: asking it adds no line and sets no cookie a
// visitor without a basket did not have.
//
// The answer is plain text: draw a SKU with textContent, never innerHTML. Modelled on the shop's own door
// (plugin-commerce's GET /shop/api/cart), and its bounds are the shop's: at most MaxBasketLines lines, each of 1 to
// MaxBasketLineQty units.
const (
	// MaxBasketLines is the most different lines a basket holds; MaxBasketLineQty the most units of one line.
	MaxBasketLines   = 100
	MaxBasketLineQty = 9999
	// MaxBasketBody bounds a Basket answer in bytes, the shop's own keys beside sku and qty included.
	MaxBasketBody = 256 << 10
)

// BasketLine is one line of the visitor's basket: a variant's SKU and how many units of it.
type BasketLine struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// BasketAnswer is the Basket endpoint's 200 answer: the lines the visitor's basket holds now, in the shop's order.
type BasketAnswer struct {
	Lines []BasketLine `json:"lines"`
}

// Validate refuses an answer a caller could not read truthfully: more than MaxBasketLines lines, a SKU that is empty,
// padded, past MaxPaymentLineSKU bytes or not plain text, or a quantity outside 1 to MaxBasketLineQty. An empty basket
// is an answer; a SKU on two lines is too.
func (a BasketAnswer) Validate() error {
	if len(a.Lines) > MaxBasketLines {
		return fmt.Errorf("nilda: a basket holds at most %d lines, not %d", MaxBasketLines, len(a.Lines))
	}
	for _, l := range a.Lines {
		if strings.TrimSpace(l.SKU) == "" || strings.TrimSpace(l.SKU) != l.SKU || len(l.SKU) > MaxPaymentLineSKU || !plainLine(l.SKU) {
			return fmt.Errorf("nilda: a basket line's SKU is 1 to %d bytes of plain text, got %q", MaxPaymentLineSKU, l.SKU)
		}
		if l.Qty < 1 || l.Qty > MaxBasketLineQty {
			return fmt.Errorf("nilda: a basket line holds 1 to %d of %q, not %d", MaxBasketLineQty, l.SKU, l.Qty)
		}
	}
	return nil
}

// HoldsOnly reports whether the basket holds exactly the look and nothing else: every line is one of the look's SKUs
// (letter case aside, as a shop answers its catalogue's own spelling) and each SKU's units, summed over its lines, are
// the quantity the look asks. An empty basket holds no look. Only then is a LookQuoteAnswer's saving the one the
// checkout gives, so only then does a caller show it.
func (a BasketAnswer) HoldsOnly(look LookQuoteRequest) bool {
	if len(a.Lines) == 0 {
		return false
	}
	held := map[string]int64{}
	for _, l := range a.Lines {
		held[strings.ToLower(l.SKU)] += l.Qty
	}
	if len(held) != len(look.Lines) {
		return false
	}
	for _, l := range look.Lines {
		if held[strings.ToLower(l.SKU)] != l.Qty {
			return false
		}
	}
	return true
}

// DecodeBasketAnswer reads a Basket endpoint's 200 body as a widget's script reads it — at most MaxBasketBody bytes, one
// JSON object and nothing after it, "lines" an array of objects whose "sku" is a string and "qty" a whole number, the keys
// spelled exactly so (Go's own decoding forgives "SKU"; a browser does not) — and validates it. Keys beside those are
// the shop's and are ignored.
func DecodeBasketAnswer(body io.Reader) (BasketAnswer, error) {
	raw, err := io.ReadAll(io.LimitReader(body, MaxBasketBody+1))
	if err != nil {
		return BasketAnswer{}, err
	}
	if len(raw) > MaxBasketBody {
		return BasketAnswer{}, fmt.Errorf("nilda: a basket answer is at most %d bytes", MaxBasketBody)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var top map[string]json.RawMessage
	if err := dec.Decode(&top); err != nil {
		return BasketAnswer{}, fmt.Errorf("nilda: a basket answer is one JSON object: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return BasketAnswer{}, errors.New("nilda: a basket answer is one JSON object and nothing after it")
	}
	var lines []map[string]json.RawMessage
	if err := json.Unmarshal(top["lines"], &lines); err != nil || lines == nil {
		return BasketAnswer{}, errors.New(`nilda: a basket answer's "lines" is an array ([] for an empty basket)`)
	}
	a := BasketAnswer{Lines: make([]BasketLine, 0, len(lines))}
	for i, l := range lines {
		var line BasketLine
		if err := json.Unmarshal(l["sku"], &line.SKU); err != nil {
			return BasketAnswer{}, fmt.Errorf(`nilda: a basket answer's line %d has no "sku" string (spelled so)`, i)
		}
		var qty json.Number // a JSON number: json.Number would also take the string "1", which a script reads as text
		if q := bytes.TrimSpace(l["qty"]); len(q) == 0 || q[0] == '"' || json.Unmarshal(q, &qty) != nil {
			return BasketAnswer{}, fmt.Errorf(`nilda: a basket answer's line %d has no "qty" number (spelled so)`, i)
		}
		if line.Qty, err = qty.Int64(); err != nil {
			return BasketAnswer{}, fmt.Errorf("nilda: a basket answer's line %d holds %s units, not a whole number", i, qty)
		}
		a.Lines = append(a.Lines, line)
	}
	return a, a.Validate()
}
