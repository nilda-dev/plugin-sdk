package nilda

import (
	"context"
	"encoding/json"
	"fmt"
)

// TAKING MONEY, WITHOUT BEING A SHOP.
//
// # The question this answers, asked by the owner on 2026-09-15
//
// "Does it work without commerce? Blogs sell things too — we need payment. Commerce is for businesses
// whose whole focus is selling. Am I right?" Yes, and the code did not reflect it.
//
// Until now `PaymentGateway` was declared inside the e-commerce plugin's own repo and took that plugin's
// `Order` type. Two consequences, both bad, and neither of them anybody's decision:
//
//  1. A blog that wants to sell one ebook, take a donation or run a paid membership had to install an
//     entire shop — catalogue, cart, inventory, shipping, tax tables — to reach a payment form.
//  2. The gateway list was a hardcoded literal with one entry (`[]PaymentGateway{OfflineGateway{}}`),
//     so Stripe, PayPal or Zarinpal could not be added by ANY means, including by editing that repo:
//     there was no registration seam at all. Every gateway was a dead end before it was written.
//
// # The line, stated so it can be applied to the next feature
//
//	PAYMENT   money moved, for this thing, once. An amount, a reason, a payer, a receipt, and a way to
//	          know it succeeded.
//	COMMERCE  a catalogue somebody shops. Everything above PLUS inventory, variations, a cart holding
//	          several lines, shipping, tax by jurisdiction, fulfilment, returns.
//
// The test that decides it, for anything: DOES IT HAVE A CATALOGUE AND A BASKET? No → payment. Yes →
// commerce. Charging €9 for a download has neither. Holding three items and then checking out has both.
//
// # What a gateway is asked, and what it is never asked
//
// A gateway is handed a CHARGE — an amount in minor units, a currency, a human description and a
// reference the caller owns — and answers with somewhere to send the payer, or with the fact that it
// settled on the spot. It is never handed a cart, a product, a customer record or a tax breakdown,
// because those are the shop's concepts and a gateway that received them would become a second place
// that has to understand them.
//
// Reference is the CALLER'S id, opaque here, and it is what makes this usable by a shop and a blog at
// once: the shop puts an order id in it, a membership plugin puts a subscription id in it, and the
// gateway echoes it back at settle time so the caller knows what was paid for. Nothing in this package
// interprets it.

// Payment capability keys and hooks. Declared beside their dispatch, the way the commerce ones are, so a
// capability and the code that honours it cannot drift apart.
const (
	// HookPaymentMethods asks which payment methods this gateway offers, so a caller can show a choice
	// rather than guessing at a method name that may not be handled.
	HookPaymentMethods = "payment.methods"
	// HookPaymentStart begins one charge. The answer is a redirect, or a settled status for a method
	// that needs no redirect at all.
	HookPaymentStart = "payment.start"
	// HookPaymentStatus asks what a charge has reached RIGHT NOW. Called after a payer returns, and
	// from a webhook for a gateway that settles asynchronously.
	HookPaymentStatus = "payment.status"
	// HookPaymentRefund gives money back. The PAYMENT side only: what that means for an order, a
	// membership or a download is the caller's, deliberately.
	HookPaymentRefund = "payment.refund"
)

// Payment status values. A closed set, because a caller has to branch on them and a gateway inventing a
// sixth would land in whatever the caller's default branch happens to be.
const (
	// PaymentPending — started and not finished. The payer is at the gateway, or a bank transfer is
	// waiting for money to arrive. NOT a failure: a caller that treats it as one cancels live payments.
	PaymentPending = "pending"
	// PaymentPaid — the money is there.
	PaymentPaid = "paid"
	// PaymentFailed — the attempt ended without money. A declined card, an expired session.
	PaymentFailed = "failed"
	// PaymentCancelled — the PAYER walked away. Separate from failed because it is not a problem to
	// investigate and must not be reported to the owner as one.
	PaymentCancelled = "cancelled"
	// PaymentRefunded — money went back.
	PaymentRefunded = "refunded"
)

// Charge is one request for money.
type Charge struct {
	// Reference is the CALLER'S id for whatever is being paid for — an order, a subscription, a
	// donation. Opaque to the gateway, echoed back at status time, and the only thing that ties a
	// payment to the thing it bought.
	Reference string `json:"reference"`
	// Method is which of the gateway's methods to use, from HookPaymentMethods.
	Method string `json:"method"`
	// AmountMinor is the amount in the currency's SMALLEST unit — cents, pence, rials. Never a float:
	// money in a float is a rounding error waiting for a large enough number, and never a formatted
	// string either, because a gateway has to do arithmetic with it.
	AmountMinor int64 `json:"amount_minor"`
	// Currency is the ISO-4217 code, uppercase. Required: a gateway that guesses a currency charges the
	// wrong amount in the right number.
	Currency string `json:"currency"`
	// Description is what the payer will see on their statement and on the gateway's own page.
	Description string `json:"description,omitempty"`
	// ReturnURL is where to send the payer after they finish or abandon. Same-origin on this site; the
	// gateway appends whatever it needs to it.
	ReturnURL string `json:"return_url,omitempty"`
	// Email is the payer's, when one is known — several gateways require it and all of them use it for
	// the receipt. Optional: a donation from somebody not signed in has none.
	Email string `json:"email,omitempty"`
	// Locale is the language to present the payment page in, when the gateway supports one.
	Locale string `json:"locale,omitempty"`
}

// ChargeResult is what starting a charge produced.
type ChargeResult struct {
	// RedirectURL is where the payer must be sent. Empty means this method settled without leaving the
	// site, and Status says what it settled to.
	RedirectURL string `json:"redirect_url,omitempty"`
	// Status is one of the Payment* constants.
	Status string `json:"status"`
	// GatewayRef is the gateway's OWN id for this attempt, kept so a human can find it in the gateway's
	// dashboard when something has to be reconciled by hand.
	GatewayRef string `json:"gateway_ref,omitempty"`
	// Message is for the PAYER when something went wrong — "card declined", not a stack trace.
	Message string `json:"message,omitempty"`
}

// PaymentMethod is one way this gateway can be paid.
type PaymentMethod struct {
	// Key is what goes in Charge.Method.
	Key string `json:"key"`
	// Label is what a payer reads.
	Label string `json:"label"`
	// Description is the one line under it — "you will be redirected to your bank".
	Description string `json:"description,omitempty"`
	// Currencies this method can take, as ISO-4217 codes. Empty means "any this account is set up for",
	// which is the honest answer for a gateway that cannot enumerate them.
	Currencies []string `json:"currencies,omitempty"`
	// Offline marks a method where no processor is involved and the money arrives outside this software
	// — cash on delivery, a bank transfer to the owner's own account. A caller shows those differently
	// because nothing will confirm them automatically.
	Offline bool `json:"offline,omitempty"`
}

// Gateway is the seam a payment provider implements.
//
// FOUR METHODS, and the shape is the one every real processor already has: enumerate, start, ask, refund.
// It is deliberately the same shape the e-commerce plugin's own gateway interface had — that one was
// right about the protocol and wrong only about where it lived and what it took.
type Gateway interface {
	// Methods lists what this gateway can be paid by.
	Methods(ctx context.Context) ([]PaymentMethod, error)
	// Start begins a charge.
	Start(ctx context.Context, c Charge) (ChargeResult, error)
	// Status reports where a charge stands now, by the caller's own reference.
	Status(ctx context.Context, reference string) (ChargeResult, error)
	// Refund returns money. amountMinor of 0 means the whole charge — the common case, and one a caller
	// should not have to look up an amount to express.
	Refund(ctx context.Context, reference string, amountMinor int64) (ChargeResult, error)
}

// ServePayment runs a plugin whose whole job is being a payment gateway.
func ServePayment(g Gateway) { Serve(&paymentOnlyHandler{g: g}) }

type paymentOnlyHandler struct {
	g    Gateway
	core *Core
}

func (h *paymentOnlyHandler) Init(_ context.Context, core *Core) (InitResult, error) {
	h.core = core
	return InitResult{}, nil
}

func (h *paymentOnlyHandler) HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error) {
	out, handled, err := DispatchPaymentHook(ctx, h.core, h.g, hook, payload)
	if !handled {
		return nil, fmt.Errorf("nilda: unknown hook %q", hook)
	}
	return out, err
}

func (h *paymentOnlyHandler) HandleEvent(context.Context, string, []byte) error { return nil }

// DispatchPaymentHook routes one payment hook to g. handled=false means it was not a payment hook, so a
// plugin that does several things can pass it on rather than failing it — the same contract
// DispatchCommerceHook has, so a plugin can be a shop AND its own gateway by chaining the two.
func DispatchPaymentHook(ctx context.Context, _ *Core, g Gateway, hook string, payload []byte) (out []byte, handled bool, err error) {
	switch hook {
	case HookPaymentMethods:
		methods, err := g.Methods(ctx)
		if err != nil {
			return nil, true, err
		}
		// A nil slice marshals as null, and a caller reading `.methods[0]` on null crashes in whatever
		// language reads it next. A gateway with nothing configured offers an EMPTY list.
		if methods == nil {
			methods = []PaymentMethod{}
		}
		b, err := json.Marshal(map[string]any{"methods": methods})
		return b, true, err

	case HookPaymentStart:
		var c Charge
		if err := json.Unmarshal(payload, &c); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.start payload: %w", err)
		}
		// REFUSED HERE RATHER THAN SENT ON, because a gateway asked to charge zero, a negative amount or
		// an unnamed currency will do something — decline, error differently, or in the worst case
		// charge something — and each gateway will do a different one. The contract is checked once, on
		// this side, so every provider sees the same guarantee.
		if err := c.validate(); err != nil {
			return nil, true, err
		}
		res, err := g.Start(ctx, c)
		if err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookPaymentStatus:
		var in struct {
			Reference string `json:"reference"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.status payload: %w", err)
		}
		res, err := g.Status(ctx, in.Reference)
		if err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err

	case HookPaymentRefund:
		var in struct {
			Reference   string `json:"reference"`
			AmountMinor int64  `json:"amount_minor"`
		}
		if err := json.Unmarshal(payload, &in); err != nil {
			return nil, true, fmt.Errorf("nilda: payment.refund payload: %w", err)
		}
		if in.AmountMinor < 0 {
			return nil, true, fmt.Errorf("nilda: payment.refund: a negative refund is a charge")
		}
		res, err := g.Refund(ctx, in.Reference, in.AmountMinor)
		if err != nil {
			return nil, true, err
		}
		b, err := json.Marshal(res)
		return b, true, err
	}
	return nil, false, nil
}

// validate rejects a charge no gateway should be asked to make.
func (c Charge) validate() error {
	switch {
	case c.Reference == "":
		return fmt.Errorf("nilda: payment.start: a charge with no reference cannot be matched to what it paid for")
	case c.AmountMinor <= 0:
		// Zero included: a "free" purchase is not a payment, and sending one to a processor produces a
		// different error from every provider.
		return fmt.Errorf("nilda: payment.start: amount_minor must be positive, got %d", c.AmountMinor)
	case len(c.Currency) != 3:
		return fmt.Errorf("nilda: payment.start: currency must be a 3-letter ISO-4217 code, got %q", c.Currency)
	}
	return nil
}
