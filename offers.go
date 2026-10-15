package nilda

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// OFFERS AT THE CART, THE CHECKOUT AND AFTER THE PURCHASE (OFFERS.md; commerce unit 10, plan 14).
//
// The conversion layer every leading shop sells as an app: a suggestion on the cart page, an "order bump" — one more
// item, often at a discount, added with one tap on the checkout — and an offer after the purchase. The owner's offers
// and their numbers are a plugin's; the shop draws them, adds the item, and applies the discount through its own
// pricing, so tax, refunds and invoices treat it as any other discount. The plugin PROVIDES shop.offers/v1; the shop
// USES it.
//
// Two questions. offers.for: which offers to show at this stage, for this basket (or the order just paid). And
// offers.confirm: which of the offers already on basket lines still stand — asked whenever the basket is priced, so an
// offer the owner switched off, or whose trigger the shopper took out of the basket, gives no discount at the
// checkout.

// ContractShopOffers is the contract's name.
const ContractShopOffers = "shop.offers/v1"

// Its methods.
const (
	OffersFor     = "offers.for"
	OffersConfirm = "offers.confirm"
)

// Where an offer is shown.
const (
	// OfferStageCart: on the cart page, beside the basket ("you may also like", with or without a discount).
	OfferStageCart = "cart"
	// OfferStageCheckout: on the checkout, one tap adds it (an order bump).
	OfferStageCheckout = "checkout"
	// OfferStageAfterPurchase: on the page after the order is placed or paid.
	OfferStageAfterPurchase = "after_purchase"
)

// OfferPercentMax is the largest discount an offer may give, in percent.
const OfferPercentMax = 90

// OfferLine is one basket or order line as an offers plugin sees it.
type OfferLine struct {
	SKU            string `json:"sku"`
	Qty            int64  `json:"qty"`
	UnitPriceMinor int64  `json:"unit_price_minor"`
	// OfferID is the offer this line was added through, "" for a line the shopper added themselves.
	OfferID string `json:"offer_id,omitempty"`
}

// OffersRequest asks which offers to show.
type OffersRequest struct {
	Stage string      `json:"stage"`
	Lines []OfferLine `json:"lines"`
	// Currency is the basket's (ISO 4217); SubtotalMinor its goods' total before discounts.
	Currency      string `json:"currency"`
	SubtotalMinor int64  `json:"subtotal_minor"`
	// Locale is the language of the page; a headline is written in it.
	Locale string `json:"locale,omitempty"`
	// OrderID is the order just placed, for OfferStageAfterPurchase.
	OrderID string `json:"order_id,omitempty"`
}

// Validate refuses a request no offer could be chosen for.
func (r OffersRequest) Validate() error {
	switch r.Stage {
	case OfferStageCart, OfferStageCheckout, OfferStageAfterPurchase:
	default:
		return fmt.Errorf("nilda: an offer stage %q", r.Stage)
	}
	if err := validCurrency("the basket's currency", r.Currency); err != nil {
		return err
	}
	return validOfferLines(r.Lines)
}

func validOfferLines(lines []OfferLine) error {
	if len(lines) > 500 {
		return errors.New("nilda: an offers request holds at most 500 lines")
	}
	for _, l := range lines {
		if strings.TrimSpace(l.SKU) == "" || l.Qty < 1 || l.UnitPriceMinor < 0 {
			return errors.New("nilda: an offer line has a SKU, a quantity and a price")
		}
	}
	return nil
}

// OffersResult is the offers to show, the first first.
type OffersResult struct {
	Offers []Offer `json:"offers"`
}

// Validate checks every offer.
func (r OffersResult) Validate() error {
	for _, o := range r.Offers {
		if err := o.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// Offer is one offer: an item, and what it costs taken this way.
type Offer struct {
	// ID is the plugin's own, carried on the basket line the offer adds and on the order line after it.
	ID  string `json:"id"`
	SKU string `json:"sku"`
	// Headline is the owner's sentence above it ("Complete the set"), in the page's language; "" lets the shop say it.
	Headline string `json:"headline,omitempty"`
	// PercentOff is the discount on the item taken through the offer, 0 to OfferPercentMax; MaxUnits how many units
	// of it get the discount (at least 1).
	PercentOff int `json:"percent_off"`
	MaxUnits   int `json:"max_units"`
}

// Validate refuses an offer the shop could not honour.
func (o Offer) Validate() error {
	if strings.TrimSpace(o.ID) == "" || len(o.ID) > 64 || strings.TrimSpace(o.SKU) == "" {
		return errors.New("nilda: an offer has an id and a SKU")
	}
	if o.PercentOff < 0 || o.PercentOff > OfferPercentMax {
		return fmt.Errorf("nilda: offer %s takes %d%% off; an offer takes 0 to %d%%", o.ID, o.PercentOff, OfferPercentMax)
	}
	if o.MaxUnits < 1 || o.MaxUnits > 100 {
		return fmt.Errorf("nilda: offer %s discounts %d units; an offer discounts 1 to 100", o.ID, o.MaxUnits)
	}
	if len(o.Headline) > 200 {
		return fmt.Errorf("nilda: offer %s has a headline over 200 characters", o.ID)
	}
	return nil
}

// OffersConfirmRequest asks which offers on the basket's lines still stand.
type OffersConfirmRequest struct {
	Lines         []OfferLine `json:"lines"`
	Currency      string      `json:"currency"`
	SubtotalMinor int64       `json:"subtotal_minor"`
}

// Validate refuses a request with no line carrying an offer.
func (r OffersConfirmRequest) Validate() error {
	if err := validCurrency("the basket's currency", r.Currency); err != nil {
		return err
	}
	return validOfferLines(r.Lines)
}

// OffersConfirmResult is the offers that still stand, each with its terms now.
type OffersConfirmResult struct {
	Accepted []Offer `json:"accepted"`
}

// Validate checks every offer.
func (r OffersConfirmResult) Validate() error {
	return OffersResult{Offers: r.Accepted}.Validate()
}

// ---- the plugin's side -------------------------------------------------------------------------------------

// OffersProvider is a plugin that keeps the owner's offers. Serve finds it by itself; declare
// "provides": ["shop.offers/v1"] with service_provider.
type OffersProvider interface {
	OffersFor(ctx context.Context, req OffersRequest) (OffersResult, error)
	ConfirmOffers(ctx context.Context, req OffersConfirmRequest) (OffersConfirmResult, error)
}

// OffersMethods are the provider's contract methods.
func OffersMethods(p OffersProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractShopOffers, OffersFor, func(ctx context.Context, req OffersRequest) (OffersResult, error) {
			if err := req.Validate(); err != nil {
				return OffersResult{}, err
			}
			r, err := p.OffersFor(ctx, req)
			if r.Offers == nil {
				r.Offers = []Offer{} // none is an empty list on the wire, never null
			}
			return checked(r, err)
		}),
		Serves(ContractShopOffers, OffersConfirm, func(ctx context.Context, req OffersConfirmRequest) (OffersConfirmResult, error) {
			if err := req.Validate(); err != nil {
				return OffersConfirmResult{}, err
			}
			r, err := p.ConfirmOffers(ctx, req)
			if r.Accepted == nil {
				r.Accepted = []Offer{}
			}
			return checked(r, err)
		}),
	}
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// Offers is every offer every offers plugin makes for this stage, in the plugins' order. A plugin that did not answer
// is left out: an offer is a suggestion, and a page is drawn without it.
func (s *Services) Offers(ctx context.Context, req OffersRequest) ([]Offer, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	res, err := s.Call(ctx, ContractShopOffers, OffersFor, req, ServiceCallOptions{})
	if err != nil {
		return nil, err
	}
	var out []Offer
	for _, a := range res.Answers {
		var r OffersResult
		if a.Decode(&r) != nil || r.Validate() != nil {
			continue
		}
		out = append(out, r.Offers...)
	}
	return out, nil
}

// ConfirmOffers is the offers on the basket's lines that still stand, by offer id. An offer whose plugin did not
// answer does not stand — the basket is priced without its discount, and the checkout says so before anything is
// paid.
func (s *Services) ConfirmOffers(ctx context.Context, req OffersConfirmRequest) (map[string]Offer, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	res, err := s.Call(ctx, ContractShopOffers, OffersConfirm, req, ServiceCallOptions{})
	if err != nil {
		return nil, err
	}
	out := map[string]Offer{}
	for _, a := range res.Answers {
		var r OffersConfirmResult
		if a.Decode(&r) != nil || r.Validate() != nil {
			continue
		}
		for _, o := range r.Accepted {
			out[o.ID] = o
		}
	}
	return out, nil
}
