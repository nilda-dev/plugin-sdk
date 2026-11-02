package nilda

import (
	"net/url"
	"strings"
)

// WHERE YOUR ADMIN PAGES ARE. A mail to the owner ("a quote is waiting") carries a button to the page it is about; the
// admin draws a plugin's pages under these addresses (Core's admin SPA: basename /admin, a page at
// /plugin/<key>/<page>, a form at .../edit/<id> or .../new). adminurls_test.go holds them to the SPA when Core is
// beside the SDK. origin is the site's public address ("https://shop.example"), with no trailing slash needed.

// AdminPageURL is one of your admin pages, at the site's address.
func AdminPageURL(origin, pluginKey, page string) string {
	return strings.TrimRight(origin, "/") + "/admin/plugin/" + url.PathEscape(pluginKey) + "/" + url.PathEscape(page)
}

// AdminFormURL is one record opened in a page's form; an empty id is the form for a new one.
func AdminFormURL(origin, pluginKey, page, id string) string {
	if id == "" {
		return AdminPageURL(origin, pluginKey, page) + "/new"
	}
	return AdminPageURL(origin, pluginKey, page) + "/edit/" + url.PathEscape(id)
}
