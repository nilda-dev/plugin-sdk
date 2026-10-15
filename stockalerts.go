package nilda

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// BACK-IN-STOCK REQUESTS, KEPT BY A PLUGIN OF THEIR OWN (STOCK_ALERTS.md; commerce unit 10).
//
// A shopper on a sold-out product leaves an email and is told, once, when it can be bought again. The button and the
// form are the shop's product page (Core's add-to-cart widget, the shop's CommerceEndpoints.StockAlert); keeping the
// requests, confirming them, watching the stock and sending the news are a plugin's — the way Shopify, BigCommerce
// and WooCommerce ship it, as an app or an extension beside the shop. The plugin PROVIDES stock.alerts/v1; the shop
// USES it, and shows the form only while a provider says it is collecting.

// ContractStockAlerts is the contract's name.
const ContractStockAlerts = "stock.alerts/v1"

// The contract's methods.
const (
	StockAlertsDescribe = "describe"
	StockAlertSubscribe = "subscribe"
)

// What a subscribe came to (StockAlertResult.Status).
const (
	// StockAlertSubscribed: the request is kept; a confirmation is on its way to the email.
	StockAlertSubscribed = "subscribed"
	// StockAlertAlready: this email already waits for this item; nothing changed.
	StockAlertAlready = "already"
	// StockAlertRefused: not kept — Message says why (the item is in stock, the email has too many waiting).
	StockAlertRefused = "refused"
)

// StockAlertsDescription is whether the plugin takes requests now, and the sentence the form shows under the email
// box — how the address is used, in the shop's words (the shop hands it to Core as CommerceEndpoints.StockAlertConsent;
// empty, Core says it in its own words).
type StockAlertsDescription struct {
	Collecting  bool   `json:"collecting"`
	ConsentText string `json:"consent_text,omitempty"`
}

// Validate accepts any description.
func (StockAlertsDescription) Validate() error { return nil }

// StockAlertRequest is one shopper asking to be told when one item is back.
type StockAlertRequest struct {
	// SKU is the variant's — the item the shopper chose; ProductSKU its product's.
	SKU        string `json:"sku"`
	ProductSKU string `json:"product_sku,omitempty"`
	// Title is what the mails call it ("Linen shirt — Blue / M"); URL its page, absolute.
	Title string `json:"title"`
	URL   string `json:"url"`
	Email string `json:"email"`
	// Locale is the language the shopper read the page in; the mails are written in it.
	Locale string `json:"locale,omitempty"`
	// ConsentText is the sentence the form showed under the email box, as the shopper read it — keep it, the locale
	// and the time as the record of what they agreed to. From v0.23.0.
	ConsentText string `json:"consent_text,omitempty"`
	// Requester is the shop's VisitorKey for whoever sent the form: opaque, the same all day, never an address — what a
	// plugin limits requests by, so a script cannot make it mail a thousand strangers. From v0.23.0.
	Requester string `json:"requester,omitempty"`
}

// Validate refuses a request the plugin cannot keep.
func (r StockAlertRequest) Validate() error {
	if strings.TrimSpace(r.SKU) == "" || strings.TrimSpace(r.Title) == "" {
		return errors.New("nilda: a back-in-stock request names the item's SKU and title")
	}
	if !strings.HasPrefix(r.URL, "https://") && !strings.HasPrefix(r.URL, "http://") {
		return errors.New("nilda: a back-in-stock request carries the item's page, absolute")
	}
	e := strings.TrimSpace(r.Email)
	if len(r.ConsentText) > 1000 || len(r.Requester) > 64 {
		return errors.New("nilda: a back-in-stock request's consent text or requester is too long")
	}
	if len(e) > 254 {
		return errors.New("nilda: that email address is too long")
	}
	if a, err := mail.ParseAddress(e); err != nil || a.Address != e || !strings.Contains(e[strings.LastIndex(e, "@")+1:], ".") {
		return errors.New("nilda: that is not an email address")
	}
	return nil
}

// StockAlertResult is what a subscribe came to.
type StockAlertResult struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Validate checks the status.
func (r StockAlertResult) Validate() error {
	switch r.Status {
	case StockAlertSubscribed, StockAlertAlready, StockAlertRefused:
		return nil
	}
	return fmt.Errorf("nilda: a back-in-stock answer with status %q", r.Status)
}

// ---- the plugin's side -------------------------------------------------------------------------------------

// StockAlertProvider keeps the requests. Serve finds it by itself; declare "provides": ["stock.alerts/v1"] with
// service_provider.
type StockAlertProvider interface {
	DescribeStockAlerts(ctx context.Context) (StockAlertsDescription, error)
	SubscribeStockAlert(ctx context.Context, req StockAlertRequest) (StockAlertResult, error)
}

// StockAlertMethods are the provider's contract methods; a request the checks refuse is answered as refused, with
// the reason the shopper reads.
func StockAlertMethods(p StockAlertProvider) []ServiceMethod {
	return []ServiceMethod{
		Serves(ContractStockAlerts, StockAlertsDescribe, func(ctx context.Context, _ struct{}) (StockAlertsDescription, error) {
			return checked(p.DescribeStockAlerts(ctx))
		}),
		Serves(ContractStockAlerts, StockAlertSubscribe, func(ctx context.Context, req StockAlertRequest) (StockAlertResult, error) {
			if err := req.Validate(); err != nil {
				return StockAlertResult{Status: StockAlertRefused, Message: strings.TrimPrefix(err.Error(), "nilda: ")}, nil
			}
			return checked(p.SubscribeStockAlert(ctx, req))
		}),
	}
}

// ---- the shop's side ---------------------------------------------------------------------------------------

// StockAlertsDescription asks whether a plugin takes back-in-stock requests now. ok is false only when none is
// installed and on — the shop then shows no form. A plugin that is there and did not answer (a timeout, an error, an
// answer the contract refuses) is an error, not "nobody": the shop cannot tell whether it collects, and a form hidden
// on that would stay hidden for as long as the shop's answer is kept.
func (s *Services) StockAlertsDescription(ctx context.Context) (StockAlertsDescription, bool, error) {
	res, err := s.Call(ctx, ContractStockAlerts, StockAlertsDescribe, struct{}{}, ServiceCallOptions{})
	if err != nil {
		return StockAlertsDescription{}, false, err
	}
	if len(res.Answers) == 0 {
		if len(res.Missed) > 0 {
			return StockAlertsDescription{}, false, fmt.Errorf("nilda: %s did not answer %s %s (%s)", res.Missed[0].Provider,
				ContractStockAlerts, StockAlertsDescribe, res.Missed[0].Reason)
		}
		return StockAlertsDescription{}, false, nil
	}
	var d StockAlertsDescription
	if err := res.Answers[0].Decode(&d); err != nil {
		return StockAlertsDescription{}, false, err
	}
	return d, true, nil
}

// SubscribeStockAlert hands one shopper's request to the plugin that keeps them.
func (s *Services) SubscribeStockAlert(ctx context.Context, req StockAlertRequest) (StockAlertResult, error) {
	res, err := s.Call(ctx, ContractStockAlerts, StockAlertSubscribe, req, ServiceCallOptions{})
	if err != nil {
		return StockAlertResult{}, err
	}
	if len(res.Answers) == 0 {
		return StockAlertResult{}, fmt.Errorf("nilda: nothing answered %s %s", ContractStockAlerts, StockAlertSubscribe)
	}
	var r StockAlertResult
	if err := res.Answers[0].Decode(&r); err != nil {
		return StockAlertResult{}, err
	}
	return r, r.Validate()
}
