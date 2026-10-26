package nilda

import (
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

// A BACK-IN-STOCK REQUEST AND ITS ANSWER: what Core's "notify me" form sends to the shop's StockAlert endpoint
// (CommerceSettings.StockAlert) and what the shop answers. The shop keeps the requests itself (commerce Unit 12 B1);
// the stock.alerts/v1 contract that once handed them to a plugin of their own is gone (v0.47.0, commerce DR-19).

// What a request came to (StockAlertResult.Status).
const (
	// StockAlertSubscribed: the request is kept; a confirmation is on its way to the email.
	StockAlertSubscribed = "subscribed"
	// StockAlertAlready: this email already waits for this item; nothing changed.
	StockAlertAlready = "already"
	// StockAlertRefused: not kept — Message says why (the item is in stock, the email has too many waiting).
	StockAlertRefused = "refused"
)

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

// Validate refuses a request the shop cannot keep.
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

// StockAlertResult is what a request came to: the JSON the form reads.
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
