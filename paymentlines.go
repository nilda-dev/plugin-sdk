package nilda

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// WHAT THE PAYER IS BUYING, LINE BY LINE (commerce plan 10.15, "Stripe item lines"; v0.58.0; PAYMENTS.md §5 "Item lines").
//
// A session used to be one amount: the processor's page said "Order NLD-1042 — 84.90 €". With Lines it shows what the
// payer is buying — each item's name, quantity and price, the delivery, the discount, the tax — as Stripe Checkout's
// line_items and PayPal's purchase_units[].items show them. The lines describe the amount; they never set it: AmountMinor
// stays the one figure charged, and Validate holds the lines to it exactly, so a gateway can hand them on without
// re-adding anything.

// The bounds of the item lines: Stripe Checkout takes at most 100 line items in payment mode, PayPal's item name is cut
// past 127 characters and its SKU is at most 127 (docs.stripe.com/api/checkout/sessions/create; PayPal orders v2 schema
// 2.36, item_request; both read 2026-11-05).
const (
	MaxPaymentLines        = 100
	MaxPaymentLineName     = 127
	MaxPaymentLineSKU      = 127
	MaxPaymentLineQuantity = 999_999
	maxPaymentLineURL      = 2048
)

// PaymentLine is one thing the payer is buying.
type PaymentLine struct {
	// Name is what the payer reads: one plain line of 1 to MaxPaymentLineName characters.
	Name string `json:"name"`
	// Quantity is how many, 1 to MaxPaymentLineQuantity.
	Quantity int64 `json:"quantity"`
	// UnitAmountMinor is the price of one, and TotalMinor exactly UnitAmountMinor × Quantity — both as the shop shows
	// them (with tax when Breakdown.TaxIncluded). A line discount that does not divide by the quantity goes into
	// Breakdown.DiscountMinor, never into a unit price.
	UnitAmountMinor int64 `json:"unit_amount_minor"`
	TotalMinor      int64 `json:"total_minor"`
	// TaxMinor is the tax in (or on) this line's total; at most the line's total when included.
	TaxMinor int64 `json:"tax_minor,omitempty"`
	// SKU, URL and ImageURL are optional: the item's code, its page and its picture (absolute http(s) URLs).
	SKU      string `json:"sku,omitempty"`
	URL      string `json:"url,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	// Fulfilment is FulfilmentPhysical, FulfilmentDigital or FulfilmentNone (PayPal's PHYSICAL_GOODS, DIGITAL_GOODS,
	// DONATION); empty when not said.
	Fulfilment string `json:"fulfilment,omitempty"`
}

// PaymentBreakdown is what lies between the lines' sum and the amount, required with Lines:
//
//	sum(lines' TotalMinor) + ShippingMinor − DiscountMinor + (TaxMinor unless TaxIncluded) = AmountMinor
//
// TaxMinor is all the tax in the amount — the lines' and the delivery's; TaxIncluded says the lines and the delivery
// already hold it (prices shown with tax, as an EU shop's are), so it is not added again.
type PaymentBreakdown struct {
	ShippingMinor int64 `json:"shipping_minor"`
	DiscountMinor int64 `json:"discount_minor"`
	TaxMinor      int64 `json:"tax_minor"`
	TaxIncluded   bool  `json:"tax_included"`
}

// validLines holds lines and their breakdown to the amount they describe.
func validLines(amount int64, lines []PaymentLine, b *PaymentBreakdown) error {
	switch {
	case len(lines) == 0 && b == nil:
		return nil
	case len(lines) == 0 || b == nil:
		return errors.New("nilda: item lines come with their breakdown, and a breakdown only with lines")
	case len(lines) > MaxPaymentLines:
		return fmt.Errorf("nilda: a payment shows at most %d item lines, got %d", MaxPaymentLines, len(lines))
	case b.ShippingMinor < 0 || b.DiscountMinor < 0 || b.TaxMinor < 0:
		return errors.New("nilda: a breakdown's delivery, discount and tax are never negative")
	}
	var sum, lineTax int64
	for i, l := range lines {
		if err := l.validate(); err != nil {
			return fmt.Errorf("nilda: item line %d: %w", i+1, err)
		}
		if b.TaxIncluded && l.TaxMinor > l.TotalMinor {
			return fmt.Errorf("nilda: item line %d: its tax is past its total, which holds it", i+1)
		}
		if sum > math.MaxInt64-l.TotalMinor || lineTax > math.MaxInt64-l.TaxMinor {
			return errors.New("nilda: the item lines add up past what an amount can hold")
		}
		sum, lineTax = sum+l.TotalMinor, lineTax+l.TaxMinor
	}
	if lineTax > b.TaxMinor {
		return fmt.Errorf("nilda: the item lines' tax (%d) is past the breakdown's tax_minor (%d), which holds all of it", lineTax, b.TaxMinor)
	}
	total := sum
	for _, add := range []int64{b.ShippingMinor, onTop(b)} {
		if total > math.MaxInt64-add {
			return errors.New("nilda: the item lines add up past what an amount can hold")
		}
		total += add
	}
	if total-b.DiscountMinor != amount {
		return fmt.Errorf("nilda: the item lines do not add up to the amount: %d of lines + %d delivery − %d discount + %d tax on top = %d, and the amount is %d",
			sum, b.ShippingMinor, b.DiscountMinor, onTop(b), total-b.DiscountMinor, amount)
	}
	return nil
}

// onTop is the tax the breakdown adds to the lines and the delivery: none when they hold it.
func onTop(b *PaymentBreakdown) int64 {
	if b.TaxIncluded {
		return 0
	}
	return b.TaxMinor
}

// validate holds one line to its bounds and its arithmetic.
func (l PaymentLine) validate() error {
	switch {
	case !noNUL(l.Name, l.SKU, l.URL, l.ImageURL):
		return errNUL()
	case strings.TrimSpace(l.Name) == "" || utf8.RuneCountInString(l.Name) > MaxPaymentLineName || !plainLine(l.Name):
		return fmt.Errorf("its name is one plain line of 1 to %d characters", MaxPaymentLineName)
	case l.Quantity < 1 || l.Quantity > MaxPaymentLineQuantity:
		return fmt.Errorf("its quantity is 1 to %d, got %d", MaxPaymentLineQuantity, l.Quantity)
	case l.UnitAmountMinor < 0 || l.TotalMinor < 0 || l.TaxMinor < 0:
		return errors.New("its amounts are never negative")
	case l.UnitAmountMinor > math.MaxInt64/l.Quantity || l.UnitAmountMinor*l.Quantity != l.TotalMinor:
		return fmt.Errorf("its total_minor (%d) is not its unit_amount_minor (%d) times its quantity (%d); a discount that does not divide goes into the breakdown's discount_minor",
			l.TotalMinor, l.UnitAmountMinor, l.Quantity)
	case utf8.RuneCountInString(l.SKU) > MaxPaymentLineSKU || !plainLine(l.SKU):
		return fmt.Errorf("its sku is one plain line of at most %d characters", MaxPaymentLineSKU)
	}
	for field, u := range map[string]string{"url": l.URL, "image_url": l.ImageURL} {
		if u == "" {
			continue
		}
		if err := validPayerURL(field, u, false); err != nil || len(u) > maxPaymentLineURL {
			return fmt.Errorf("its %s is an absolute http(s) URL of at most %d bytes", field, maxPaymentLineURL)
		}
	}
	return validFulfilment(l.Fulfilment)
}
