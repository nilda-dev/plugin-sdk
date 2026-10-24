package nilda

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// A REPORT ROW THAT SAYS WHAT TO DO NEXT (v0.42.0).
//
// A report page used to be read-only text: "Shipping — no zone — add one in Shipping zones". The owner read
// the advice and then had to find the screen it named. Two column types close that gap, and both stay
// DECLARED — the plugin hands back data, never markup, and Core draws the link or the button itself:
//
//   - ColumnLink: the cell is a ReportLink — one of the plugin's own declared pages, one of Core's own screens
//     from a fixed list (ReportScreens), or the Install of a plugin this one recommends (its manifest's
//     `recommends`). Anything else is drawn as its label alone, never as a link to wherever it pointed.
//   - ColumnAction: the cell is the key of one of the page's own declared Actions, or "" for no button on that
//     row. Pressing it calls your `admin.action` hook with Page = the report's key and ID = the row's "key"
//     cell (ReportRowKey), exactly as a list page's button does, and Core opens the report again after it ran.

// ReportRowKey is the cell a report row names itself by when it carries a button: the ID an action press sends.
const ReportRowKey = "key"

// The Core screens a report's link may open. A fixed list, because a link is drawn inside the administrator's
// session: a plugin naming any address it liked could send the owner anywhere with one click.
const (
	ScreenPayments = "payments" // Settings → Payments: the gateways and the methods switched on
	ScreenPlugins  = "plugins"  // Plugins: installed and available
	ScreenMenus    = "menus"    // Menus
	ScreenPages    = "pages"    // the content list (pages and posts)
	ScreenSettings = "settings" // Settings → General: the site's name, address and business details
	ScreenPrivacy  = "privacy"  // Privacy: the site's own privacy policy
	ScreenEmail    = "email"    // Settings → Email: how the site sends mail
)

// ReportScreens returns every screen a ReportLink may name, sorted.
func ReportScreens() []string {
	return []string{ScreenEmail, ScreenMenus, ScreenPages, ScreenPayments, ScreenPlugins, ScreenPrivacy, ScreenSettings}
}

// ReportLink is a ColumnLink cell: Label, and exactly one of Page, Screen and Install.
type ReportLink struct {
	Label string `json:"label"`
	// Page is the key of one of YOUR declared admin pages.
	Page string `json:"page,omitempty"`
	// Screen is one of ReportScreens.
	Screen string `json:"screen,omitempty"`
	// Install names one entry of your manifest's `recommends` — its capability or its contract — and Core draws
	// the plugins the marketplace has for it, each with an Install button.
	Install string `json:"install,omitempty"`
}

// maxReportLinkLabel bounds a link's words, as a column label is bounded.
const maxReportLinkLabel = 120

var reportTargetRe = regexp.MustCompile(`^[a-z][a-z0-9_.]*(/v[1-9][0-9]*)?$`)

// Validate checks the link's shape. Whether Page is one of your pages and Install one of your recommendations
// is Core's to check against the manifest it holds; a link that fails there is drawn as its label.
func (l ReportLink) Validate() error {
	if strings.TrimSpace(l.Label) == "" {
		return fmt.Errorf("nilda: a report link needs a label — it is the words the owner clicks")
	}
	if len([]rune(l.Label)) > maxReportLinkLabel {
		return fmt.Errorf("nilda: a report link's label is longer than %d characters", maxReportLinkLabel)
	}
	n := 0
	for _, s := range []string{l.Page, l.Screen, l.Install} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("nilda: a report link names exactly one of page, screen and install (it names %d)", n)
	}
	switch {
	case l.Screen != "":
		for _, s := range ReportScreens() {
			if s == l.Screen {
				return nil
			}
		}
		return fmt.Errorf("nilda: %q is not a screen a report may link to (ReportScreens lists them)", l.Screen)
	case l.Page != "" && !reportTargetRe.MatchString(l.Page):
		return fmt.Errorf("nilda: %q is not an admin page key", l.Page)
	case l.Install != "" && !reportTargetRe.MatchString(l.Install):
		return fmt.Errorf("nilda: %q is not a capability or a contract", l.Install)
	}
	return nil
}

// Validate checks a report's link and action cells against the page that shows it, so a report Core would draw
// wrong is refused where the author can see why — in a test — rather than in the owner's panel.
func (r AdminReportResult) Validate(page AdminPage) error {
	declared := map[string]bool{}
	for _, a := range page.Actions {
		declared[a.Key] = true
	}
	for _, c := range r.Columns {
		if c.Type != ColumnLink && c.Type != ColumnAction {
			continue
		}
		for i, row := range r.Rows {
			v, ok := row[c.Key]
			if !ok || v == nil {
				continue
			}
			if c.Type == ColumnAction {
				key, isText := v.(string)
				if !isText {
					return fmt.Errorf("nilda: row %d's %q is an action cell and holds %T, not an action key", i, c.Key, v)
				}
				if key == "" {
					continue
				}
				if !declared[key] {
					return fmt.Errorf("nilda: row %d's %q names the action %q, which page %q does not declare", i, c.Key, key, page.Key)
				}
				if id, _ := row[ReportRowKey].(string); id == "" {
					return fmt.Errorf("nilda: row %d has a button and no %q cell, so a press could not say which row it was", i, ReportRowKey)
				}
				continue
			}
			link, err := reportLinkOf(v)
			if err != nil {
				return fmt.Errorf("nilda: row %d's %q: %w", i, c.Key, err)
			}
			if err := link.Validate(); err != nil {
				return fmt.Errorf("row %d's %q: %w", i, c.Key, err)
			}
		}
	}
	return nil
}

// reportLinkOf reads a link cell as the plugin built it: a ReportLink, or the map it becomes on the wire.
func reportLinkOf(v any) (ReportLink, error) {
	if l, ok := v.(ReportLink); ok {
		return l, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ReportLink{}, err
	}
	var l ReportLink
	if err := json.Unmarshal(b, &l); err != nil {
		return ReportLink{}, fmt.Errorf("a link cell is {label, page|screen|install}, not %s", b)
	}
	return l, nil
}
