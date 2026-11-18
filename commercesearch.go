package nilda

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// THE SHOP'S SEARCH SUGGESTIONS (EXPERIMENTAL, v0.69.0): what CommerceEndpoints.SearchSuggest answers.
//
// Core's search box draws what its own search engine finds as the visitor types — and on a site without the heavy
// engine (Meilisearch) that search forgives no typo: "candel" finds no candle. A shop knows its own catalogue's words,
// so it can: SearchSuggest answers the products the typed words match (each word may be off by a typo or two), the
// searches they complete to, and the search the shop ran instead when it corrected one. Shopify's predictive search
// answers the same three things (products, queries, and a corrected search through its typo tolerance).
//
// THE PRICES ARE THE VIEWER'S. The endpoint is a request to your own route, so it carries the visitor's signed-in
// account and language (CurrentUser, VisitorLocale): price a hit as your product page would for them, and answer
// with Cache-Control: no-store, because two visitors may not see the same answer.

// MaxCommerceSearchSuggestions bounds the searches an answer completes to; MaxCommerceSearchHits the products it names.
const (
	MaxCommerceSearchSuggestions = 8
	MaxCommerceSearchHits        = 20
	// MaxCommerceSearchText bounds the query, a suggestion and the corrected search, in characters.
	MaxCommerceSearchText = 100
)

// CommerceSearchAnswer is the SearchSuggest endpoint's JSON.
type CommerceSearchAnswer struct {
	// Query is the search as the shop read it (trimmed).
	Query string `json:"query"`
	// Corrected is the search the shop ran instead, when a word of Query matched nothing and was read as a typo of a
	// word of the catalogue ("candel" → "candle"); empty when Query was searched as typed. Core says "Showing results
	// for <Corrected>".
	Corrected string `json:"corrected,omitempty"`
	// Suggestions are whole searches the typed words complete to ("blue bo" → "blue bowl"), best first, none equal to
	// Query; at most MaxCommerceSearchSuggestions.
	Suggestions []string `json:"suggestions"`
	// Products are the items found, best first; at most MaxCommerceSearchHits.
	Products []CommerceSearchHit `json:"products"`
}

// CommerceSearchHit is one product a search found, as the box draws it: FORMATTED BY YOU, as CommerceProduct is.
type CommerceSearchHit struct {
	// ID is your own id, as CommerceProduct.ID.
	ID    string `json:"id"`
	Title string `json:"title"`
	// URL is the product's page; a hit without one is not drawn.
	URL   string `json:"url"`
	Image string `json:"image,omitempty"`
	Price string `json:"price,omitempty"`
	// OldPrice is the struck-through price, under the same rule as CommerceProduct.OldPrice.
	OldPrice  string `json:"old_price,omitempty"`
	PriceFrom bool   `json:"price_from,omitempty"`
	UnitPrice string `json:"unit_price,omitempty"`
	// Badge is the product's own word, as CommerceProduct.Badge ("Sale", "Sold out").
	Badge   string `json:"badge,omitempty"`
	InStock bool   `json:"in_stock,omitempty"`
}

// Validate holds an answer to what the box draws: bounded lists and words, a title and a rooted or https page for
// every hit, and no suggestion repeating the query.
func (a CommerceSearchAnswer) Validate() error {
	if err := searchText("query", a.Query); err != nil {
		return err
	}
	if err := searchText("corrected", a.Corrected); err != nil {
		return err
	}
	if len(a.Suggestions) > MaxCommerceSearchSuggestions {
		return fmt.Errorf("nilda: a search answer completes to at most %d searches, not %d", MaxCommerceSearchSuggestions, len(a.Suggestions))
	}
	seen := map[string]bool{}
	for _, s := range a.Suggestions {
		if strings.TrimSpace(s) == "" {
			return errors.New("nilda: a search suggestion is empty")
		}
		if err := searchText("suggestion", s); err != nil {
			return err
		}
		k := strings.ToLower(strings.TrimSpace(s))
		if k == strings.ToLower(strings.TrimSpace(a.Query)) || seen[k] {
			return fmt.Errorf("nilda: the search suggestion %q repeats the query or another suggestion", s)
		}
		seen[k] = true
	}
	if len(a.Products) > MaxCommerceSearchHits {
		return fmt.Errorf("nilda: a search answer names at most %d products, not %d", MaxCommerceSearchHits, len(a.Products))
	}
	for _, h := range a.Products {
		if strings.TrimSpace(h.ID) == "" || strings.TrimSpace(h.Title) == "" {
			return errors.New("nilda: a search hit needs its id and its title")
		}
		if !strings.HasPrefix(h.URL, "/") && !strings.HasPrefix(h.URL, "https://") || strings.HasPrefix(h.URL, "//") {
			return fmt.Errorf("nilda: the search hit %q needs a page on this site (a rooted path or https), not %q", h.ID, h.URL)
		}
	}
	return nil
}

func searchText(what, s string) error {
	if utf8.RuneCountInString(s) > MaxCommerceSearchText {
		return fmt.Errorf("nilda: a search answer's %s is at most %d characters", what, MaxCommerceSearchText)
	}
	return nil
}
