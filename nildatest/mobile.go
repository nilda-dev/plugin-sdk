package nildatest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// WritePreviewEnv is the environment variable that makes MobilePreview write its golden instead of failing.
const WritePreviewEnv = "NILDA_WRITE_PREVIEW"

// MobilePreview draws your plugin as the Nilda app shows it on the owner's phone — its row on the Apps tab, its
// home, then each page, each with how it reaches the phone (full, basic) — and holds the drawing to a golden file,
// so a change to plugin.json that changes the phone shows up in your review as a diff of the picture.
//
//	func TestThePhone(t *testing.T) {
//		nildatest.MobilePreview(t, "plugin.json", "testdata/phone.txt")
//	}
//
// Run once with NILDA_WRITE_PREVIEW=1 to write the golden. The drawing is the one `nilda plugin check --mobile`
// prints: Core's own test keeps its drawings of the first-party plugins, and this package's test holds this one to
// them, so the two cannot drift apart. `nilda plugin check` also says what would make each page read better.
func MobilePreview(t testing.TB, manifestPath, goldenPath string) {
	t.Helper()
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	got, err := renderPhone(raw)
	if err != nil {
		t.Fatalf("%s: %v", manifestPath, err)
	}
	if os.Getenv(WritePreviewEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("no preview at %s: run the test once with %s=1 (%v)", goldenPath, WritePreviewEnv, err)
	}
	if string(want) != got {
		t.Errorf("the phone changed. If that is meant, run with %s=1 and review the diff.\nwant:\n%s\ngot:\n%s",
			WritePreviewEnv, want, got)
	}
}

// ---- what the app receives: Core's resolution (core internal/plugin apppages.go), read from plugin.json ----

// previewManifest is the part of plugin.json the app's resolution reads.
type previewManifest struct {
	Key          string                       `json:"key"`
	Name         string                       `json:"name"`
	Description  string                       `json:"description"`
	Capabilities []string                     `json:"capabilities"`
	Tables       []previewTable               `json:"tables"`
	AdminPages   []nilda.AdminPage            `json:"admin_pages"`
	Mobile       *nilda.PluginMobile          `json:"mobile"`
	Abilities    []nilda.Ability              `json:"abilities"`
	Account      *previewAccount              `json:"account"`
	Translations map[string]map[string]string `json:"translations"`
}

type previewTable struct {
	Name    string `json:"name"`
	Columns []struct {
		Name       string `json:"name"`
		PrimaryKey bool   `json:"primary_key"`
	} `json:"columns"`
}

type previewAccount struct {
	Name string `json:"name"`
	Host string `json:"host"`
}

// The bounds Core puts on what a plugin shows (core internal/plugin plaintext.go, schema_mobile.go).
const (
	shownLabel   = 80
	shownProse   = 500
	shownConfirm = 300
	shownDesc    = 300
	shownAsks    = 6
)

var (
	shownColumnTypes = map[string]bool{"text": true, "number": true, "date": true, "datetime": true,
		"boolean": true, "money": true, "badge": true}
	shownFormats = map[string]bool{"number": true, "money": true, "percent": true, "text": true}
)

type phonePlugin struct {
	name, icon, description, shape string
	account                        *previewAccount
	pages                          []phonePage
	asks                           []string
}

type phoneField struct {
	label, typ, help string
	secret           bool
	choices          []string
}

type phoneColumn struct{ key, label, typ string }

type phoneAction struct{ label, confirm string }

type phoneMetric struct{ label, format, currency string }

type phonePage struct {
	key, label, block, help, mode string
	fields                        []phoneField
	columns                       []phoneColumn
	row                           *nilda.MobileRow
	actions                       []phoneAction
	searchable, pin               bool
	metrics                       []phoneMetric
	form                          *phoneForm
}

// phoneForm is a list page's form as the app draws it: New, and its cards in the app's one-column order.
type phoneForm struct {
	create, edit bool
	createLabel  string
	cards        []phoneCard
}

type phoneCard struct {
	label  string
	fields []phoneFormField
}

type phoneFormField struct {
	label, typ string
	fields     []phoneFormField
}

// resolveForm is Core's appForm and formColumn: the fields naming no card first, then the cards in order, the side
// ones after the main ones; a card with no field is left out.
func resolveForm(f nilda.PageForm, tr func(string, int) string) *phoneForm {
	out := &phoneForm{create: f.Create, edit: f.Edit, createLabel: tr(f.CreateLabel, shownLabel)}
	field := func(fl nilda.FormField) phoneFormField {
		pf := phoneFormField{label: tr(fl.Label, shownLabel), typ: fl.Type}
		for _, sub := range fl.Fields {
			pf.fields = append(pf.fields, phoneFormField{label: tr(sub.Label, shownLabel), typ: sub.Type})
		}
		return pf
	}
	known := map[string]bool{}
	for _, sc := range f.Sections {
		known[sc.Key] = true
	}
	var loose []phoneFormField
	for _, fl := range f.Fields {
		if !known[fl.Section] {
			loose = append(loose, field(fl))
		}
	}
	if len(loose) > 0 {
		out.cards = append(out.cards, phoneCard{fields: loose})
	}
	for _, side := range []bool{false, true} {
		for _, sc := range f.Sections {
			if sc.Side != side {
				continue
			}
			card := phoneCard{label: tr(sc.Label, shownLabel)}
			for _, fl := range f.Fields {
				if fl.Section == sc.Key {
					card.fields = append(card.fields, field(fl))
				}
			}
			if len(card.fields) > 0 {
				out.cards = append(out.cards, card)
			}
		}
	}
	return out
}

func renderPhone(raw []byte) (string, error) {
	var m previewManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("not a manifest: %w", err)
	}
	return drawPhone(resolve(m)), nil
}

func has(caps []string, want ...string) bool {
	for _, c := range caps {
		for _, w := range want {
			if c == w {
				return true
			}
		}
	}
	return false
}

func resolve(m previewManifest) phonePlugin {
	table := m.Translations["en"]
	tr := func(s string, max int) string {
		if t, ok := table[s]; ok && strings.TrimSpace(t) != "" {
			s = t
		}
		if max == 0 {
			return s
		}
		return plain(s, max, false)
	}
	p := phonePlugin{name: tr(m.Name, shownLabel), description: plain(tr(m.Description, 0), shownDesc, true),
		icon: "puzzle"}
	if m.Account != nil {
		p.account = &previewAccount{Name: tr(m.Account.Name, shownLabel), Host: m.Account.Host}
	}
	if m.Mobile != nil {
		for _, i := range nilda.MobileIcons() {
			if i == m.Mobile.Icon {
				p.icon = i
			}
		}
	}
	if has(m.Capabilities, "admin_page") {
		for _, pg := range m.AdminPages {
			if pg.Mobile != nil && pg.Mobile.Hide {
				continue
			}
			if page, ok := resolvePage(m, pg, tr); ok {
				p.pages = append(p.pages, page)
			}
		}
	}
	if has(m.Capabilities, "abilities") {
		for _, a := range m.Abilities {
			if len(p.asks) == shownAsks {
				break
			}
			if l := tr(a.Label, shownLabel); l != "" {
				p.asks = append(p.asks, l)
			}
		}
	}
	switch {
	case len(p.pages) > 0:
		p.shape = "screens"
	case has(m.Capabilities, "abilities"):
		p.shape = "assistant"
	case has(m.Capabilities, "route", "site_files", "visitor_tools", "widget", "render_assets", "field"):
		p.shape = "site"
	default:
		p.shape = "background"
	}
	return p
}

func resolvePage(m previewManifest, pg nilda.AdminPage, tr func(string, int) string) (phonePage, bool) {
	out := phonePage{key: pg.Key, label: tr(pg.Label, shownLabel), block: pg.Kind,
		help: plain(tr(pg.Help, 0), shownProse, true)}
	field := func(f nilda.SettingField) phoneField {
		pf := phoneField{label: tr(f.Label, shownLabel), typ: f.Type, help: plain(tr(f.Help, 0), shownProse, true),
			secret: f.Secret}
		for _, c := range f.Choices {
			pf.choices = append(pf.choices, plain(c, shownLabel, false))
		}
		return pf
	}
	switch pg.Kind {
	case nilda.AdminPageSettings:
		if len(pg.Fields) == 0 {
			return out, false
		}
		out.mode = "full"
		for _, f := range pg.Fields {
			out.fields = append(out.fields, field(f))
		}
	case nilda.AdminPageReport:
		out.mode = "basic"
		if len(pg.Metrics) > 0 {
			out.mode = "full"
		}
		for _, f := range pg.Fields {
			f.Secret = false // a report's fields are filter inputs
			out.fields = append(out.fields, field(f))
		}
		for _, mt := range pg.Metrics {
			format := mt.Format
			switch {
			case format == "":
				format = "number"
			case !shownFormats[format]:
				format = "text"
			}
			out.metrics = append(out.metrics, phoneMetric{label: tr(mt.Label, shownLabel), format: format,
				currency: mt.Currency})
		}
	case nilda.AdminPageList:
		var t *previewTable
		for i := range m.Tables {
			if m.Tables[i].Name == pg.Table {
				t = &m.Tables[i]
			}
		}
		if t == nil {
			return out, false
		}
		known, idKey := map[string]bool{}, ""
		for _, c := range t.Columns {
			known[c.Name] = true
			if c.PrimaryKey && idKey == "" {
				idKey = c.Name
			}
		}
		for _, c := range pg.Columns {
			if !known[c.Key] || (c.Type == "money" && c.CurrencyColumn != "" && !known[c.CurrencyColumn]) {
				return out, false
			}
		}
		for _, c := range pg.Search {
			if !known[c] {
				return out, false
			}
		}
		if pg.OrderBy != "" && !known[pg.OrderBy] {
			return out, false
		}
		out.mode = "basic"
		for _, c := range pg.Columns {
			typ := c.Type
			if !shownColumnTypes[typ] {
				typ = "text"
			}
			out.columns = append(out.columns, phoneColumn{key: c.Key, label: tr(c.Label, shownLabel), typ: typ})
		}
		for _, a := range pg.Actions {
			out.actions = append(out.actions, phoneAction{label: tr(a.Label, shownLabel),
				confirm: plain(tr(a.Confirm, 0), shownConfirm, false)})
		}
		out.searchable = len(pg.Search) > 0
		if pg.Form != nil {
			out.form = resolveForm(*pg.Form, tr)
		}
		if pg.Mobile != nil {
			out.row = pg.Mobile.Row
			out.pin = pg.Mobile.Pin && idKey != ""
			if out.row != nil {
				out.mode = "full"
			}
		}
	default:
		return out, false
	}
	return out, true
}

// printable is Core's Printable: every control and format character but the line break removed — what a terminal
// or a diff would obey — and nothing else changed, so the drawing keeps its spacing.
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\u200C' || r == '\u200D' || r == '\n':
			return r
		case r == utf8.RuneError, unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r),
			r == '\u2028', r == '\u2029', r >= '\uFFF9' && r <= '\uFFFC':
			return -1
		}
		return r
	}, s)
}

// plain is Core's PlainText: NFC, no control or format character (the zero-width joiner and non-joiner are
// spelling and stay), at most max runes with an ellipsis, line breaks kept only where prose is allowed.
func plain(s string, max int, keepLines bool) string {
	if s == "" {
		return ""
	}
	s = norm.NFC.String(s)
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		switch {
		case r == '\n' && keepLines:
			b.WriteRune('\n')
			lastSpace = false
			continue
		case r == '\n' || r == '\r' || r == '\t':
			r = ' '
		case r == '\u200C' || r == '\u200D':
		case r == utf8.RuneError, unicode.Is(unicode.Cc, r), unicode.Is(unicode.Cf, r),
			r == '\u2028', r == '\u2029', r >= '\uFFF9' && r <= '\uFFFC':
			continue
		}
		if r == ' ' {
			if lastSpace {
				continue
			}
			lastSpace = true
		} else {
			lastSpace = false
		}
		b.WriteRune(r)
	}
	out := strings.TrimSpace(b.String())
	if max > 0 && utf8.RuneCountInString(out) > max {
		out = strings.TrimSpace(string([]rune(out)[:max-1])) + "…"
	}
	return out
}

// ---- the drawing: Core's RenderAppPhone (core internal/plugin appphone.go) ----

const phoneWidth = 38

func drawPhone(p phonePlugin) string {
	var b phone
	b.rule()
	b.line("Apps")
	b.line(fmt.Sprintf("[%s] %s", p.icon, p.name))
	if p.description != "" {
		b.wrap(p.description, "     ")
	}
	b.rule()
	b.line("< Apps")
	b.line(fmt.Sprintf("[%s] %s", p.icon, p.name))
	if p.description != "" {
		b.wrap(p.description, "")
	}
	if len(p.pages) == 0 {
		b.wrap(map[string]string{
			"assistant":  "Works through the assistant: ask in Chat.",
			"site":       "Works on your site, for your visitors.",
			"background": "Works in the background.",
		}[p.shape], "")
	}
	if p.account != nil {
		b.blank()
		b.wrap(fmt.Sprintf("Connect %s (%s)", p.account.Name, p.account.Host), "")
	}
	if len(p.pages) > 0 {
		b.blank()
		for _, pg := range p.pages {
			b.pair("> "+pg.label, pg.block)
		}
	}
	if len(p.asks) > 0 {
		b.blank()
		b.line("Ask the assistant")
		for _, a := range p.asks {
			b.wrap("· "+a, "  ")
		}
	}
	b.rule()
	for _, pg := range p.pages {
		b.line("< " + p.name)
		b.pair(pg.label, "["+pg.mode+"]")
		if pg.help != "" {
			b.wrap(pg.help, "")
		}
		switch pg.block {
		case nilda.AdminPageSettings:
			drawSettings(&b, pg)
		case nilda.AdminPageList:
			drawList(&b, pg)
		case nilda.AdminPageReport:
			drawReport(&b, pg)
		}
		b.rule()
	}
	return b.String()
}

func drawSettings(b *phone, pg phonePage) {
	for _, f := range pg.fields {
		b.blank()
		b.wrap(f.label, "")
		switch {
		case f.secret:
			b.line("  [ secret, not stored ]")
		case f.typ == "boolean":
			b.line("  [ on / off ]")
		case len(f.choices) > 0:
			b.wrap("( "+strings.Join(f.choices, " | ")+" )", "  ")
		case f.typ == "" || f.typ == "text":
			b.line("  [ text ]")
		default:
			b.line("  [ " + f.typ + " ]")
		}
		if f.help != "" {
			b.wrap(f.help, "  ")
		}
	}
	b.blank()
	b.line("[ Save ]")
}

func drawList(b *phone, pg phonePage) {
	label := func(key string) string {
		for _, c := range pg.columns {
			if c.key == key {
				return "<" + c.label + ">"
			}
		}
		return ""
	}
	if pg.form != nil && pg.form.create {
		createLabel := pg.form.createLabel
		if createLabel == "" {
			createLabel = "New"
		}
		b.wrap("[ + "+createLabel+" ]", "")
	}
	if pg.searchable {
		b.line("[ Search " + pg.label + " ]")
	}
	title, trailing, badge := "", "", ""
	var under []string
	if pg.row != nil {
		title, trailing, badge = label(pg.row.Title), label(pg.row.Trailing), label(pg.row.Badge)
		for _, k := range pg.row.Subtitle {
			under = append(under, label(k))
		}
	} else if len(pg.columns) > 0 {
		title = "<" + pg.columns[0].label + ">"
		for _, c := range pg.columns[1:min(3, len(pg.columns))] {
			under = append(under, "<"+c.label+">")
		}
	}
	b.blank()
	for i := 0; i < 2; i++ {
		b.pair(title, trailing)
		if len(under) > 0 || badge != "" {
			b.pair("  "+strings.Join(under, " · "), badge)
		}
	}
	b.line("  …")
	// A row of a list with a form opens in the form, its actions under it; any other row opens a sheet.
	opensForm := pg.form != nil && pg.form.edit
	if !opensForm && (len(pg.actions) > 0 || len(pg.columns) > 0) {
		b.blank()
		b.line("A row's sheet:")
		for _, c := range pg.columns {
			b.pair("  "+c.label, c.typ)
		}
		drawRowActions(b, pg)
	}
	if pg.form != nil {
		b.blank()
		if pg.form.edit {
			b.line("A row opens in its form:")
		} else {
			b.line("New opens its form:")
		}
		for _, card := range pg.form.cards {
			if card.label != "" {
				b.wrap(card.label, "  ")
			}
			for _, fl := range card.fields {
				b.pair("    "+fl.label, fl.typ)
				for _, sub := range fl.fields {
					b.pair("      "+sub.label, sub.typ)
				}
			}
		}
		if opensForm {
			drawRowActions(b, pg)
		}
	}
}

func drawRowActions(b *phone, pg phonePage) {
	for _, a := range pg.actions {
		b.wrap("[ "+a.label+" ]", "  ")
		if a.confirm != "" {
			b.wrap("asks: “"+a.confirm+"”", "    ")
		}
	}
	if pg.pin {
		b.line("  [ Ask in Chat ]")
	}
}

func drawReport(b *phone, pg phonePage) {
	for _, f := range pg.fields {
		b.wrap("[ "+f.label+" ]", "")
	}
	for _, m := range pg.metrics {
		kind := m.format
		if m.format == "money" && m.currency != "" {
			kind = "money " + m.currency
		}
		b.pair(m.label, kind)
	}
	b.blank()
	b.line("<the rows the report returns>")
}

type phone struct{ strings.Builder }

func (b *phone) rule() { b.WriteString("+" + strings.Repeat("-", phoneWidth+2) + "+\n") }

func (b *phone) blank() { b.line("") }

func (b *phone) line(s string) {
	s = printable(s)
	if n := utf8.RuneCountInString(s); n > phoneWidth {
		s = string([]rune(s)[:phoneWidth-1]) + "…"
	}
	b.WriteString("| " + s + strings.Repeat(" ", phoneWidth-utf8.RuneCountInString(s)) + " |\n")
}

func (b *phone) pair(left, right string) {
	l, r := utf8.RuneCountInString(left), utf8.RuneCountInString(right)
	if right == "" {
		b.wrap(left, "")
		return
	}
	if l+1+r <= phoneWidth {
		b.line(left + strings.Repeat(" ", phoneWidth-l-r) + right)
		return
	}
	b.wrap(left, "")
	b.line(strings.Repeat(" ", max(0, phoneWidth-r)) + right)
}

func (b *phone) wrap(s, indent string) {
	for _, para := range strings.Split(s, "\n") {
		room := phoneWidth - utf8.RuneCountInString(indent)
		line := ""
		flush := func() {
			b.line(indent + line)
			line = ""
		}
		for _, w := range strings.Fields(para) {
			for utf8.RuneCountInString(w) > room {
				if line != "" {
					flush()
				}
				rs := []rune(w)
				line = string(rs[:room])
				flush()
				w = string(rs[room:])
			}
			switch {
			case line == "":
				line = w
			case utf8.RuneCountInString(line)+1+utf8.RuneCountInString(w) <= room:
				line += " " + w
			default:
				flush()
				line = w
			}
		}
		if line != "" || para == "" {
			flush()
		}
	}
}
