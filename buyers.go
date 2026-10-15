package nilda

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"
)

// BUSINESS BUYERS: THEIR PRICES, TERMS AND QUOTES (BUYERS.md; commerce unit 10, the B2B starter, plan 14).
//
// The wholesale starter the market ships (Shopify B2B, BigCommerce B2B Edition, the WooCommerce wholesale suites):
// customer groups with their own prices — a percent off the catalogue, or a fixed price per item — minimum quantities
// and a minimum order, paying by invoice on net terms, and quotes the owner prices. The groups, members, prices and
// quotes are a plugin's; the shop asks for a signed-in buyer's terms and applies them through its own pricing, so tax,
// invoices and refunds treat a group price as any other discount. The plugin PROVIDES shop.buyers/v1; the shop USES it.

// ContractShopBuyers is the contract's name.
const ContractShopBuyers = "shop.buyers/v1"

// Its methods.
const (
	BuyerTermsGet = "buyer.terms"
	QuoteRequest  = "quote.request"
	QuoteOpen     = "quote.open"
)

// BuyerTermsRequest asks a signed-in buyer's terms, and their prices for some items.
type BuyerTermsRequest struct {
	// UserID is Core's id for the signed-in buyer (CurrentUser).
	UserID string `json:"user_id"`
	// SKUs are the items whose prices are wanted — the basket's; at most 500.
	SKUs []string `json:"skus"`
}

// Validate refuses a request with no buyer.
func (r BuyerTermsRequest) Validate() error {
	if strings.TrimSpace(r.UserID) == "" || len(r.UserID) > 128 {
		return errors.New("nilda: buyer terms are asked for a signed-in buyer")
	}
	if len(r.SKUs) > 500 {
		return errors.New("nilda: buyer terms are asked for at most 500 items")
	}
	return nil
}

// BuyerTerms is what a buyer is sold at. Member false: an ordinary shopper, and nothing else is read.
type BuyerTerms struct {
	Member bool `json:"member"`
	// Group names the buyer's group (its id and the owner's name for it).
	Group     string `json:"group,omitempty"`
	GroupName string `json:"group_name,omitempty"`
	// PercentOff is the group's discount on every item without a price of its own, 0 to 100.
	PercentOff int `json:"percent_off,omitempty"`
	// Prices are the buyer's own prices for asked items — the group's price list, or a quote they accepted.
	Prices []BuyerPrice `json:"prices,omitempty"`
	// MinQty are the least quantities the buyer may order of asked items; MinOrderMinor the least an order of theirs
	// comes to, after their prices, in the shop's currency.
	MinQty        []BuyerMinimum `json:"min_qty,omitempty"`
	MinOrderMinor int64          `json:"min_order_minor,omitempty"`
	// PayByInvoice lets the buyer pay by invoice, due NetDays after the order (0 is due on receipt).
	PayByInvoice bool `json:"pay_by_invoice,omitempty"`
	NetDays      int  `json:"net_days,omitempty"`
	// Quotes lets the buyer ask for a quote from their basket.
	Quotes bool `json:"quotes,omitempty"`
}

// Validate refuses terms the shop could not apply.
func (t BuyerTerms) Validate() error {
	if !t.Member {
		return nil
	}
	if t.PercentOff < 0 || t.PercentOff > 100 || t.NetDays < 0 || t.NetDays > 365 || t.MinOrderMinor < 0 {
		return errors.New("nilda: buyer terms with a discount outside 0–100%, net days outside 0–365 or a negative minimum")
	}
	for _, p := range t.Prices {
		if strings.TrimSpace(p.SKU) == "" || p.PriceMinor < 0 || p.MaxQty < 0 || (p.QuoteID != "" && p.MaxQty == 0) {
			return errors.New("nilda: a buyer price names its item and is not negative")
		}
		if err := validCurrency("a buyer price's currency", p.Currency); err != nil {
			return err
		}
	}
	for _, m := range t.MinQty {
		if strings.TrimSpace(m.SKU) == "" || m.Qty < 1 {
			return errors.New("nilda: a buyer minimum names its item and is at least 1")
		}
	}
	return nil
}

// BuyerPrice is one item's price for the buyer, in minor units of its currency.
type BuyerPrice struct {
	SKU        string `json:"sku"`
	PriceMinor int64  `json:"price_minor"`
	Currency   string `json:"currency"`
	// QuoteID names the quote the price comes from, "" for the group's own: a quote's price is the buyer's only on the
	// basket lines the quote filled (QuoteOpenResult), for at most MaxQty units, in one order.
	QuoteID string `json:"quote_id,omitempty"`
	MaxQty  int64  `json:"max_qty,omitempty"`
}

// BuyerMinimum is the least quantity of one item.
type BuyerMinimum struct {
	SKU string `json:"sku"`
	Qty int64  `json:"qty"`
}

// QuoteLine is one item of a quote: what the buyer asked for, and — once the owner priced it — at what price.
type QuoteLine struct {
	SKU   string `json:"sku"`
	Title string `json:"title,omitempty"`
	Qty   int64  `json:"qty"`
	// UnitPriceMinor is the price per unit: the list price in a request, the quoted price in an opened quote.
	UnitPriceMinor int64 `json:"unit_price_minor"`
}

func validQuoteLines(lines []QuoteLine) error {
	if len(lines) == 0 || len(lines) > 200 {
		return errors.New("nilda: a quote holds 1 to 200 lines")
	}
	for _, l := range lines {
		if strings.TrimSpace(l.SKU) == "" || l.Qty < 1 || l.UnitPriceMinor < 0 {
			return errors.New("nilda: a quote line has an item, a quantity and a price")
		}
	}
	return nil
}

// QuoteRequestInput is a buyer asking for a quote for their basket.
type QuoteRequestInput struct {
	UserID string `json:"user_id"`
	// Email is where the priced quote is sent; Locale the language it is written in.
	Email    string      `json:"email"`
	Locale   string      `json:"locale,omitempty"`
	Currency string      `json:"currency"`
	Lines    []QuoteLine `json:"lines"`
	Note     string      `json:"note,omitempty"`
	// AcceptURL is the shop's page that opens a priced quote, absolute; the plugin adds ?t=<token> to it.
	AcceptURL string `json:"accept_url"`
}

// Validate refuses a request the plugin could not keep.
func (r QuoteRequestInput) Validate() error {
	if strings.TrimSpace(r.UserID) == "" {
		return errors.New("nilda: a quote is asked for by a signed-in buyer")
	}
	if a, err := mail.ParseAddress(r.Email); err != nil || a.Address != r.Email {
		return errors.New("nilda: a quote request carries the buyer's email address")
	}
	if !strings.HasPrefix(r.AcceptURL, "https://") && !strings.HasPrefix(r.AcceptURL, "http://") {
		return errors.New("nilda: a quote request carries the shop's page that opens it, absolute")
	}
	if len(r.Note) > 2000 {
		return errors.New("nilda: a quote request's note is at most 2000 characters")
	}
	if err := validCurrency("the quote's currency", r.Currency); err != nil {
		return err
	}
	return validQuoteLines(r.Lines)
}

// QuoteRequestResult is the request kept, and the sentence the buyer reads.
type QuoteRequestResult struct {
	QuoteID string `json:"quote_id"`
	Message string `json:"message,omitempty"`
}

// Validate requires the quote's id.
func (r QuoteRequestResult) Validate() error {
	if strings.TrimSpace(r.QuoteID) == "" {
		return errors.New("nilda: a kept quote request has an id")
	}
	return nil
}

// QuoteOpenRequest opens a priced quote by the token in its link, for the signed-in buyer it was made for.
type QuoteOpenRequest struct {
	Token  string `json:"token"`
	UserID string `json:"user_id"`
}

// Validate refuses a request with no token or buyer.
func (r QuoteOpenRequest) Validate() error {
	if strings.TrimSpace(r.Token) == "" || len(r.Token) > 128 || strings.TrimSpace(r.UserID) == "" {
		return errors.New("nilda: a quote is opened by its token, for a signed-in buyer")
	}
	return nil
}

// QuoteOpenResult is the quote, or Found false (unknown, another buyer's, not priced yet, or expired).
type QuoteOpenResult struct {
	Found bool   `json:"found"`
	Quote *Quote `json:"quote,omitempty"`
}

// Validate checks the quote.
func (r QuoteOpenResult) Validate() error {
	if r.Found != (r.Quote != nil) {
		return errors.New("nilda: an opened quote carries the quote, and only then")
	}
	if r.Quote != nil {
		if err := validCurrency("the quote's currency", r.Quote.Currency); err != nil {
			return err
		}
		return validQuoteLines(r.Quote.Lines)
	}
	return nil
}

// Quote is a priced quote: its lines at the quoted prices, until it expires.
type Quote struct {
	ID        string      `json:"id"`
	Currency  string      `json:"currency"`
	ExpiresAt time.Time   `json:"expires_at"`
	Lines     []QuoteLine `json:"lines"`
}

// ---- the plugin's side -------------------------------------------------------------------------------------

// BuyersProvider is a plugin that keeps business buyers. Serve finds it by itself; declare
// "provides": ["shop.buyers/v1"] with service_provider.
type BuyersProvider interface {
	BuyerTerms(ctx context.Context, req BuyerTermsRequest) (BuyerTerms, error)
	RequestQuote(ctx context.Context, req QuoteRequestInput) (QuoteRequestResult, error)
	OpenQuote(ctx context.Context, req QuoteOpenRequest) (QuoteOpenResult, error)
}

// BuyersMethods are the provider's contract methods.
func BuyersMethods(p BuyersProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopBuyers, BuyerTermsGet, func(ctx context.Context, req BuyerTermsRequest) (BuyerTerms, error) {
			if err := req.Validate(); err != nil {
				return BuyerTerms{}, err
			}
			return checked(p.BuyerTerms(ctx, req))
		}),
		Serves(ContractShopBuyers, QuoteRequest, func(ctx context.Context, req QuoteRequestInput) (QuoteRequestResult, error) {
			if err := req.Validate(); err != nil {
				return QuoteRequestResult{}, err
			}
			return checked(p.RequestQuote(ctx, req))
		}),
		Serves(ContractShopBuyers, QuoteOpen, func(ctx context.Context, req QuoteOpenRequest) (QuoteOpenResult, error) {
			if err := req.Validate(); err != nil {
				return QuoteOpenResult{}, err
			}
			return checked(p.OpenQuote(ctx, req))
		}),
	}
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// buyersAnswer asks the one plugin that keeps buyers. ok is false when none is installed; one that is there and did
// not answer is an error — a member's basket priced as a stranger's would be the wrong price.
func (s *Services) buyersAnswer(ctx context.Context, method string, req any, into validator) (bool, error) {
	res, err := s.Call(ctx, ContractShopBuyers, method, req, ServiceCallOptions{})
	if err != nil {
		return false, err
	}
	if len(res.Answers) == 0 {
		if len(res.Missed) > 0 {
			return false, fmt.Errorf("nilda: %s did not answer %s %s (%s)", res.Missed[0].Provider, ContractShopBuyers, method,
				res.Missed[0].Reason)
		}
		return false, nil
	}
	if err := res.Answers[0].Decode(into); err != nil {
		return false, err
	}
	return true, into.Validate()
}

// BuyerTerms reads a signed-in buyer's terms. No plugin keeping buyers is an ordinary shopper.
func (s *Services) BuyerTerms(ctx context.Context, req BuyerTermsRequest) (BuyerTerms, error) {
	if err := req.Validate(); err != nil {
		return BuyerTerms{}, err
	}
	var t BuyerTerms
	if _, err := s.buyersAnswer(ctx, BuyerTermsGet, req, &t); err != nil {
		return BuyerTerms{}, err
	}
	return t, nil
}

// RequestQuote hands a buyer's quote request to the plugin.
func (s *Services) RequestQuote(ctx context.Context, req QuoteRequestInput) (QuoteRequestResult, error) {
	if err := req.Validate(); err != nil {
		return QuoteRequestResult{}, err
	}
	var r QuoteRequestResult
	ok, err := s.buyersAnswer(ctx, QuoteRequest, req, &r)
	if err == nil && !ok {
		err = fmt.Errorf("nilda: nothing answered %s %s", ContractShopBuyers, QuoteRequest)
	}
	return r, err
}

// OpenQuote opens a priced quote for the signed-in buyer it was made for.
func (s *Services) OpenQuote(ctx context.Context, req QuoteOpenRequest) (QuoteOpenResult, error) {
	if err := req.Validate(); err != nil {
		return QuoteOpenResult{}, err
	}
	var r QuoteOpenResult
	if _, err := s.buyersAnswer(ctx, QuoteOpen, req, &r); err != nil {
		return QuoteOpenResult{}, err
	}
	return r, nil
}
