package nilda

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// A LOOK'S PRICE BEFORE ANY BASKET (v0.74.0, EXPERIMENTAL): what CommerceEndpoints.LookQuote takes and answers.
//
// A widget that sells several pieces together — a lookbook's "shop the look", a bundle builder, a "complete the set" —
// shows what the pieces cost as one basket: each line at the visitor's price, the shop's automatic discounts on that
// set, and the total, before anything is put in the basket. Only the shop can price that (its discounts, its member
// prices, its tax display), so the shop answers it on a route of its own and the widget — which may be ANOTHER
// plugin's — asks it from the visitor's browser. The widget learns the path from WidgetRenderRequest.Shop, never by
// hard-coding the shop's private one.
//
// THE DOOR. A POST of LookQuoteRequest as JSON (Content-Type: application/json), same-origin, from the visitor's
// browser, so the shop's basket cookie and the visitor's sign-in go with it and a member is priced at their terms. The
// shop answers 200 with LookQuoteAnswer, or a LookQuoteRefusal with the status LookQuoteRefusal.Status names; always
// with Cache-Control: no-store — two visitors may not see the same answer, so it is never cached shared. It changes
// nothing: no basket line, no cookie, no reservation.
//
// THE LINES ARE PRICED AS A BASKET OF THEIR OWN. The answer is what these lines would cost if they were the whole
// basket: the shop's automatic discounts are the ones THIS SET earns, with no code, no delivery and no address's tax.
// A visitor whose basket already holds other things may be charged differently at the checkout (a "spend 100" discount
// the basket reaches with them, a "one per order" deal already used), so a caller shows a saving — "the look saves
// you €26" — only when the visitor's basket holds nothing else, and otherwise shows the lines and the total without a
// promise. The checkout's own figures are the ones that bind.
//
// THE ANSWER IS PLAIN TEXT. A SKU, a discount's label and the currency are words to draw, not markup: insert them as
// text (textContent, never innerHTML) and format the amounts yourself in the page's language (the figures are minor
// units of Currency, as the page must show them: tax included when PricesIncludeTax).

// The bounds of a look quote — the shop's door's own (plugin-commerce's lookquote.go).
const (
	// MaxLookQuoteLines is the most pieces one look is quoted for; MaxLookQuoteQty the most units of one.
	MaxLookQuoteLines = 12
	MaxLookQuoteQty   = 10
	// MaxLookQuoteBody bounds the request body in bytes: twelve lines of the longest SKU fit well inside it.
	MaxLookQuoteBody = 4 << 10
	// MaxLookQuoteDiscounts bounds the discounts an answer names; MaxLookQuoteLabel a discount's label, in characters.
	MaxLookQuoteDiscounts = 20
	MaxLookQuoteLabel     = 200
)

// AvailabilityShort is a look quote's availability for a line asking more units than the shop can sell now: the basket
// would refuse that quantity. Only a look quote says it; a catalogue feed never does.
const AvailabilityShort = "short"

// The codes a look quote is refused with (LookQuoteRefusal.Code).
const (
	// LookQuoteUnavailable — a piece the shop does not sell here (no such SKU, a draft, a product sold elsewhere or
	// only as a group); the same answer for each, so a draft is never told apart. 409.
	LookQuoteUnavailable = "unavailable"
	// LookQuoteChoiceRequired — a product's own SKU where a choice (a size, a colour) must be made first. 409.
	LookQuoteChoiceRequired = "choice_required"
	// LookQuoteMixedCurrency — pieces in more than one currency; one basket holds one. 409.
	LookQuoteMixedCurrency = "mixed_currency"
	// LookQuoteBadRequest — a body that is not a LookQuoteRequest within its bounds. 400.
	LookQuoteBadRequest = "bad_request"
	// LookQuoteTooManyRequests — this visitor asked too often; ask again later. 429.
	LookQuoteTooManyRequests = "too_many_requests"
	// LookQuoteError — the shop could not price it now. 500.
	LookQuoteError = "error"
)

// LookQuoteLine is one piece of a look: a variant's SKU, as the shop's catalogue names it, and how many.
type LookQuoteLine struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// LookQuoteRequest is the LookQuote endpoint's body: 1 to MaxLookQuoteLines lines, no SKU twice.
type LookQuoteRequest struct {
	Lines []LookQuoteLine `json:"lines"`
}

// Validate refuses a request the shop's door refuses: no lines or more than MaxLookQuoteLines, a SKU that is empty,
// padded, past MaxPaymentLineSKU bytes or holds a control character, a quantity outside 1 to MaxLookQuoteQty, or a SKU
// named twice (letter case aside).
func (r LookQuoteRequest) Validate() error {
	if len(r.Lines) == 0 || len(r.Lines) > MaxLookQuoteLines {
		return fmt.Errorf("nilda: a look quote asks for 1 to %d lines, not %d", MaxLookQuoteLines, len(r.Lines))
	}
	seen := map[string]bool{}
	for _, l := range r.Lines {
		if err := lookSKU(l.SKU); err != nil {
			return err
		}
		if l.Qty < 1 || l.Qty > MaxLookQuoteQty {
			return fmt.Errorf("nilda: a look quote asks for 1 to %d of %q, not %d", MaxLookQuoteQty, l.SKU, l.Qty)
		}
		k := strings.ToLower(l.SKU)
		if seen[k] {
			return fmt.Errorf("nilda: a look quote names %q twice", l.SKU)
		}
		seen[k] = true
	}
	return nil
}

func lookSKU(sku string) error {
	if strings.TrimSpace(sku) == "" || strings.TrimSpace(sku) != sku || len(sku) > MaxPaymentLineSKU || !plainLine(sku) {
		return fmt.Errorf("nilda: a look quote's SKU is 1 to %d bytes of plain text, got %q", MaxPaymentLineSKU, sku)
	}
	return nil
}

// DecodeLookQuoteRequest reads the body of a call to the LookQuote endpoint as the shop's door reads it — at most
// MaxLookQuoteBody bytes, one JSON object of known fields and nothing after it, each SKU trimmed — and validates it.
// Answer an error with LookQuoteRefusal{Code: LookQuoteBadRequest}.
func DecodeLookQuoteRequest(body io.Reader) (LookQuoteRequest, error) {
	raw, err := io.ReadAll(io.LimitReader(body, MaxLookQuoteBody+1))
	if err != nil {
		return LookQuoteRequest{}, err
	}
	if len(raw) > MaxLookQuoteBody {
		return LookQuoteRequest{}, fmt.Errorf("nilda: a look quote request is at most %d bytes", MaxLookQuoteBody)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var r LookQuoteRequest
	if err := dec.Decode(&r); err != nil {
		return LookQuoteRequest{}, fmt.Errorf("nilda: a look quote request: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return LookQuoteRequest{}, errors.New("nilda: a look quote request is one JSON object and nothing after it")
	}
	for i := range r.Lines {
		r.Lines[i].SKU = strings.TrimSpace(r.Lines[i].SKU)
	}
	return r, r.Validate()
}

// LookQuoteAnswerLine is one line of the quote: UnitMinor one unit at the visitor's price, LineMinor the line after its
// own deals (a member's price), both as the page must show them (LookQuoteAnswer.PricesIncludeTax). Availability is
// AvailabilityInStock, AvailabilityOutOfStock, AvailabilityBackorder, AvailabilityPreOrder, or AvailabilityShort when
// the shop holds fewer than Qty: a sold-out piece is quoted and says so, rather than refusing the look.
type LookQuoteAnswerLine struct {
	SKU          string `json:"sku"`
	Qty          int64  `json:"qty"`
	UnitMinor    int64  `json:"unit_minor"`
	LineMinor    int64  `json:"line_minor"`
	Availability string `json:"availability"`
}

// LookQuoteDiscount is one of the shop's automatic discounts on the look, in the shop's words, and what it takes off.
type LookQuoteDiscount struct {
	Label       string `json:"label"`
	AmountMinor int64  `json:"amount_minor"`
}

// LookQuoteAnswer is the LookQuote endpoint's 200 answer: what the lines cost as a basket of their own, in minor units
// of Currency. SubtotalMinor is the sum of the lines; TotalMinor is SubtotalMinor less every discount.
type LookQuoteAnswer struct {
	Currency         string                `json:"currency"`
	PricesIncludeTax bool                  `json:"prices_include_tax"`
	Lines            []LookQuoteAnswerLine `json:"lines"`
	SubtotalMinor    int64                 `json:"subtotal_minor"`
	Discounts        []LookQuoteDiscount   `json:"discounts"`
	TotalMinor       int64                 `json:"total_minor"`
}

// Validate refuses an answer a caller could not draw truthfully: a currency ISO 4217 gives no minor unit to, no lines
// or more than MaxLookQuoteLines, a SKU that is not one or is named twice, a quantity outside 1 to MaxLookQuoteQty, an
// availability that is not one of the SDK's, more than MaxLookQuoteDiscounts discounts or a label that is empty, past
// MaxLookQuoteLabel characters or not plain text, a negative figure, or figures that do not add up.
func (a LookQuoteAnswer) Validate() error {
	if _, ok := MinorUnits(a.Currency); !ok || !currencyCodeRe.MatchString(a.Currency) {
		return fmt.Errorf("nilda: a look quote's currency is an ISO 4217 code (\"EUR\") with a minor unit, got %q", a.Currency)
	}
	if len(a.Lines) == 0 || len(a.Lines) > MaxLookQuoteLines {
		return fmt.Errorf("nilda: a look quote answers 1 to %d lines, not %d", MaxLookQuoteLines, len(a.Lines))
	}
	seen := map[string]bool{}
	var subtotal int64
	for _, l := range a.Lines {
		if err := lookSKU(l.SKU); err != nil {
			return err
		}
		if seen[strings.ToLower(l.SKU)] {
			return fmt.Errorf("nilda: a look quote answers %q twice", l.SKU)
		}
		seen[strings.ToLower(l.SKU)] = true
		if l.Qty < 1 || l.Qty > MaxLookQuoteQty {
			return fmt.Errorf("nilda: a look quote's line %q is for 1 to %d, not %d", l.SKU, MaxLookQuoteQty, l.Qty)
		}
		switch l.Availability {
		case AvailabilityInStock, AvailabilityOutOfStock, AvailabilityBackorder, AvailabilityPreOrder, AvailabilityShort:
		default:
			return fmt.Errorf("nilda: a look quote's line %q has availability %q, not one of the SDK's", l.SKU, l.Availability)
		}
		if l.UnitMinor < 0 || l.LineMinor < 0 {
			return fmt.Errorf("nilda: a look quote's line %q has a negative price", l.SKU)
		}
		var ok bool
		if subtotal, ok = addMinor(subtotal, l.LineMinor); !ok {
			return errors.New("nilda: a look quote's lines add up past what an amount holds")
		}
	}
	if a.SubtotalMinor != subtotal {
		return fmt.Errorf("nilda: a look quote's subtotal is its lines, %d, not %d", subtotal, a.SubtotalMinor)
	}
	if len(a.Discounts) > MaxLookQuoteDiscounts {
		return fmt.Errorf("nilda: a look quote names at most %d discounts, not %d", MaxLookQuoteDiscounts, len(a.Discounts))
	}
	total := subtotal
	for _, d := range a.Discounts {
		if strings.TrimSpace(d.Label) == "" || utf8.RuneCountInString(d.Label) > MaxLookQuoteLabel || !plainLine(d.Label) {
			return fmt.Errorf("nilda: a look quote's discount label is one line of 1 to %d characters of plain text", MaxLookQuoteLabel)
		}
		if d.AmountMinor < 0 {
			return fmt.Errorf("nilda: the discount %q takes off a negative amount", d.Label)
		}
		total -= d.AmountMinor // both non-negative: cannot overflow
	}
	if total < 0 {
		return errors.New("nilda: a look quote's discounts take off more than its subtotal")
	}
	if a.TotalMinor != total {
		return fmt.Errorf("nilda: a look quote's total is its subtotal less its discounts, %d, not %d", total, a.TotalMinor)
	}
	return nil
}

// ValidateFor is Validate, and the answer prices exactly the lines r asked for: each SKU once (letter case aside, as
// the shop answers its catalogue's own spelling), at the quantity asked.
func (a LookQuoteAnswer) ValidateFor(r LookQuoteRequest) error {
	if err := a.Validate(); err != nil {
		return err
	}
	asked := map[string]int64{}
	for _, l := range r.Lines {
		asked[strings.ToLower(l.SKU)] = l.Qty
	}
	if len(a.Lines) != len(asked) {
		return fmt.Errorf("nilda: a look quote answers %d lines for %d asked", len(a.Lines), len(asked))
	}
	for _, l := range a.Lines {
		q, ok := asked[strings.ToLower(l.SKU)]
		if !ok {
			return fmt.Errorf("nilda: a look quote answers %q, which was not asked", l.SKU)
		}
		if q != l.Qty {
			return fmt.Errorf("nilda: a look quote answers %d of %q for %d asked", l.Qty, l.SKU, q)
		}
	}
	return nil
}

// LookQuoteRefusal is the LookQuote endpoint's answer when it prices nothing: Code one of the LookQuote… codes, and SKU
// the piece it is about (the caller's own word, sent back), when there is one.
type LookQuoteRefusal struct {
	Code string `json:"code"`
	SKU  string `json:"sku,omitempty"`
}

// Validate refuses a code that is not one of the door's, or a SKU that is not one.
func (r LookQuoteRefusal) Validate() error {
	if r.Status() == 0 {
		return fmt.Errorf("nilda: a look quote is refused with one of the door's codes, not %q", r.Code)
	}
	if r.SKU != "" && (len(r.SKU) > MaxPaymentLineSKU || !plainLine(r.SKU)) {
		return fmt.Errorf("nilda: a look quote refusal's SKU is at most %d bytes of plain text", MaxPaymentLineSKU)
	}
	return nil
}

// Status is the HTTP status the refusal is answered with, 0 for a code that is not the door's.
func (r LookQuoteRefusal) Status() int {
	switch r.Code {
	case LookQuoteUnavailable, LookQuoteChoiceRequired, LookQuoteMixedCurrency:
		return http.StatusConflict
	case LookQuoteBadRequest:
		return http.StatusBadRequest
	case LookQuoteTooManyRequests:
		return http.StatusTooManyRequests
	case LookQuoteError:
		return http.StatusInternalServerError
	}
	return 0
}

// addMinor adds two non-negative amounts, false past what an int64 holds.
func addMinor(a, b int64) (int64, bool) {
	if b > 0 && a > (1<<63-1)-b {
		return 0, false
	}
	return a + b, true
}
