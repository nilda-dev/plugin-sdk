package nilda

import (
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// HTML AS A BODY (TextBody's richer sibling). A plugin bringing words from another system — an old shop's product
// description, a feed's article — has them as HTML, and Core keeps a body as its editor's Lexical document. HTMLBody makes that
// document from the HTML with the node vocabulary Core's own editor and renderer use (core internal/editor/lexical.go, the
// shape internal/reading renders): headings h1–h6, paragraphs with line breaks, bold, italic, underline, strikethrough, code,
// subscript and superscript, links, bullet and numbered lists (nested), quotes, preformatted code, horizontal rules, tables, and
// pictures. Author text is only ever plain text on text nodes — no markup survives into the document, so nothing can be smuggled
// through it; a link keeps only an http, https, mailto or tel address or a path on the site.
//
// A picture's address is the caller's to give (picture): a body never names a file Core does not hold, so the caller maps each
// <img> to its own copy in Core's library (an import map, an upload) or answers false and the picture is left and counted.
// What the document cannot say is left and named by kind (HTMLBodyLeft.Parts): an embedded frame or player ("embed"), a form
// ("form"), a script ("script"), a paragraph that is only a WordPress shortcode ("shortcode", which WordPress drew and nothing
// else can). Styles, classes and attributes other than a link's address and a picture's are not read.
//
// WHY HERE, AND NOT CORE'S OWN CONVERTER (D10 §1.2, SDK follow-up b, 2026-11-18). Core has an HTML-to-document converter, its
// editor's Smart Paste (internal/editor CleanPaste), and its importer runs the content reader's `body_html` through it. Neither is
// a door a plugin can use for its own write: Core's content API takes a body as a Lexical document only, the paste route is the
// admin editor's (a signed-in session at /api/v1/editor/paste, not the plugin API), and the shop is not Core's importer — D10
// says the import plugin hands the shop the description as a Core document BEFORE the call, and the shop never reads HTML. A
// Core change to open the paste route to plugin tokens would couple every plugin's write to an editor endpoint with side effects
// of its own (it uploads pasted images). The SDK already owns Core's body format for plugins (TextBody, BodyText, CheckBody), so
// the converter sits beside them, with the same node vocabulary as core internal/editor/lexical.go: the least coupled path.

// HTMLImage is one picture of an HTML body: its address, its alternative text and its class attribute (WordPress names its
// library file there, "wp-image-<id>").
type HTMLImage struct {
	Src, Alt, Class string
}

// HTMLBodyLeft is what HTMLBody could not carry: pictures the caller did not place, and the kinds of markup left.
type HTMLBodyLeft struct {
	Pictures int
	Parts    []string // sorted, each once: embed, form, script, shortcode
}

// Any says whether anything was left.
func (l HTMLBodyLeft) Any() bool { return l.Pictures > 0 || len(l.Parts) > 0 }

// HTMLBody is src as a body, and what it left. picture answers a picture's address in Core (its media item's URL), false to
// leave it; nil leaves every picture. HTML with no text and no picture is `{}`, Core's "no body yet".
func HTMLBody(src string, picture func(HTMLImage) (string, bool)) (json.RawMessage, HTMLBodyLeft) {
	c := &htmlConv{picture: picture, left: map[string]bool{}}
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	var blocks []lexNode
	if err == nil {
		blocks = c.blocks(nodes)
	}
	left := HTMLBodyLeft{Pictures: c.pictures}
	for k := range c.left {
		left.Parts = append(left.Parts, k)
	}
	slices.Sort(left.Parts)
	if len(blocks) == 0 {
		return json.RawMessage(`{}`), left
	}
	var all strings.Builder
	for _, b := range blocks {
		all.WriteString(b.text())
	}
	root := lexElement("root", blocks, textDirection(all.String()))
	raw, _ := json.Marshal(map[string]any{"root": root})
	return raw, left
}

// lexNode is one node of the document; image is set on a picture met inside inline content, which becomes a block of its own.
type lexNode map[string]any

func (n lexNode) text() string {
	if t, ok := n["text"].(string); ok {
		return t
	}
	var b strings.Builder
	if kids, ok := n["children"].([]any); ok {
		for _, k := range kids {
			if kn, ok := k.(lexNode); ok {
				b.WriteString(kn.text())
			}
		}
	}
	return b.String()
}

func lexElement(typ string, children []lexNode, dir string) lexNode {
	kids := make([]any, len(children))
	for i, c := range children {
		kids[i] = c
	}
	return lexNode{"type": typ, "version": 1, "children": kids, "direction": dir, "format": "", "indent": 0}
}

func lexText(text string, format int) lexNode {
	return lexNode{"type": "text", "version": 1, "text": text, "format": format, "detail": 0, "mode": "normal", "style": ""}
}

func lexBreak() lexNode { return lexNode{"type": "linebreak", "version": 1} }

// The text formats, as Core's editor and renderer read the bitmask.
const (
	htmlBold          = 1
	htmlItalic        = 2
	htmlStrikethrough = 4
	htmlUnderline     = 8
	htmlCode          = 16
	htmlSubscript     = 32
	htmlSuperscript   = 64
)

type htmlConv struct {
	picture  func(HTMLImage) (string, bool)
	pictures int
	left     map[string]bool
}

var htmlShortcode = regexp.MustCompile(`^\[/?[A-Za-z_][A-Za-z0-9_-]*(\s[^\]]*)?\]$`)

var htmlSpace = regexp.MustCompile(`[ \t\n\r\f]+`)

// blocks is a run of sibling nodes as blocks: an inline run between block elements is a paragraph.
func (c *htmlConv) blocks(nodes []*html.Node) []lexNode {
	var out, run []lexNode
	flush := func() {
		out = append(out, c.paragraphs("paragraph", run)...)
		run = nil
	}
	for _, n := range nodes {
		if n.Type == html.ElementNode && htmlIsBlock(n.DataAtom) {
			flush()
			out = append(out, c.block(n)...)
			continue
		}
		run = append(run, c.inline(n, 0)...)
	}
	flush()
	return out
}

func children(n *html.Node) []*html.Node {
	var out []*html.Node
	for k := n.FirstChild; k != nil; k = k.NextSibling {
		out = append(out, k)
	}
	return out
}

func htmlIsBlock(a atom.Atom) bool {
	switch a {
	case atom.P, atom.Div, atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Ul, atom.Ol, atom.Dl, atom.Li, atom.Dt, atom.Dd,
		atom.Blockquote, atom.Pre, atom.Hr, atom.Table, atom.Section, atom.Article, atom.Header, atom.Footer, atom.Main,
		atom.Aside, atom.Nav, atom.Figure, atom.Figcaption, atom.Address, atom.Center, atom.Details, atom.Summary,
		atom.Iframe, atom.Video, atom.Audio, atom.Object, atom.Embed, atom.Form, atom.Script, atom.Style, atom.Noscript,
		atom.Template, atom.Canvas, atom.Svg:
		return true
	}
	return false
}

// block is one block element as blocks.
func (c *htmlConv) block(n *html.Node) []lexNode {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		out := c.paragraphs("heading", c.inlineOf(n, 0))
		for _, b := range out {
			if b["type"] == "heading" {
				b["tag"] = n.Data
			}
		}
		return out
	case atom.P, atom.Dt, atom.Dd, atom.Figcaption, atom.Summary:
		return c.paragraphs("paragraph", c.inlineOf(n, 0))
	case atom.Ul, atom.Ol, atom.Dl:
		return c.list(n)
	case atom.Li: // an item outside a list: a bullet list of one
		return c.list(&html.Node{Type: html.ElementNode, Data: "ul", DataAtom: atom.Ul, FirstChild: n, LastChild: n})
	case atom.Blockquote:
		var kids []lexNode
		for _, b := range c.blocks(children(n)) { // a quote holds text: its paragraphs become lines of it
			if t, ok := b["children"].([]any); ok && b["type"] != "image" {
				if len(kids) > 0 {
					kids = append(kids, lexBreak())
				}
				for _, k := range t {
					if kn, ok := k.(lexNode); ok {
						kids = append(kids, kn)
					}
				}
			}
		}
		if len(kids) == 0 {
			return nil
		}
		return []lexNode{lexElement("quote", kids, textDirection(lexElement("", kids, "").text()))}
	case atom.Pre:
		code := strings.Trim(textOf(n), "\n")
		if strings.TrimSpace(code) == "" {
			return nil
		}
		node := lexElement("code", []lexNode{lexText(code, 0)}, "ltr")
		node["language"] = ""
		return []lexNode{node}
	case atom.Hr:
		return []lexNode{{"type": "horizontalrule", "version": 1}}
	case atom.Table:
		return c.table(n)
	case atom.Iframe, atom.Video, atom.Audio, atom.Object, atom.Embed, atom.Canvas, atom.Svg:
		c.left["embed"] = true
		return nil
	case atom.Form:
		c.left["form"] = true
		return nil
	case atom.Script:
		c.left["script"] = true
		return nil
	case atom.Style, atom.Noscript, atom.Template:
		return nil
	}
	return c.blocks(children(n)) // a container (div, section, figure…): its children
}

// list is a list element: its items, an item's own list nested in an item of its own (Lexical's shape).
func (c *htmlConv) list(n *html.Node) []lexNode {
	var items []lexNode
	for _, k := range children(n) {
		if k.Type != html.ElementNode {
			continue
		}
		switch k.DataAtom {
		case atom.Li, atom.Dt, atom.Dd:
			var inline []lexNode
			var nested []lexNode
			for _, kk := range children(k) {
				if kk.Type == html.ElementNode && (kk.DataAtom == atom.Ul || kk.DataAtom == atom.Ol) {
					nested = append(nested, c.list(kk)...)
					continue
				}
				if kk.Type == html.ElementNode && htmlIsBlock(kk.DataAtom) {
					for _, b := range c.block(kk) {
						if b["type"] == "image" || b["type"] == "list" {
							nested = append(nested, b)
							continue
						}
						if t, ok := b["children"].([]any); ok {
							if len(inline) > 0 {
								inline = append(inline, lexBreak())
							}
							for _, x := range t {
								inline = append(inline, x.(lexNode))
							}
						}
					}
					continue
				}
				inline = append(inline, c.inline(kk, 0)...)
			}
			for _, b := range c.paragraphs("listitem", inline) {
				if b["type"] == "listitem" {
					b["value"] = len(items) + 1
					items = append(items, b)
				}
			}
			for _, sub := range nested {
				if sub["type"] == "list" {
					item := lexElement("listitem", []lexNode{sub}, "ltr")
					item["value"] = len(items) + 1
					items = append(items, item)
				}
			}
		case atom.Ul, atom.Ol: // a list written straight inside a list
			for _, sub := range c.list(k) {
				item := lexElement("listitem", []lexNode{sub}, "ltr")
				item["value"] = len(items) + 1
				items = append(items, item)
			}
		}
	}
	if len(items) == 0 {
		return nil
	}
	var all strings.Builder
	for _, it := range items {
		all.WriteString(it.text())
	}
	l := lexElement("list", items, textDirection(all.String()))
	if n.DataAtom == atom.Ol {
		l["listType"], l["tag"] = "number", "ol"
	} else {
		l["listType"], l["tag"] = "bullet", "ul"
	}
	l["start"] = 1
	return []lexNode{l}
}

// table is a table's rows and cells, a header cell marked as Core's table nodes mark it.
func (c *htmlConv) table(n *html.Node) []lexNode {
	var rows []lexNode
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		for _, k := range children(p) {
			if k.Type != html.ElementNode {
				continue
			}
			switch k.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(k)
			case atom.Tr:
				var cells []lexNode
				for _, cell := range children(k) {
					if cell.Type != html.ElementNode || (cell.DataAtom != atom.Td && cell.DataAtom != atom.Th) {
						continue
					}
					paras := c.blocks(children(cell))
					if len(paras) == 0 {
						paras = []lexNode{lexElement("paragraph", nil, "ltr")}
					}
					tc := lexElement("tablecell", paras, "ltr")
					tc["headerState"], tc["colSpan"], tc["rowSpan"] = 0, 1, 1
					if cell.DataAtom == atom.Th {
						tc["headerState"] = 1
					}
					cells = append(cells, tc)
				}
				if len(cells) > 0 {
					row := lexElement("tablerow", cells, "ltr")
					row["height"] = nil
					rows = append(rows, row)
				}
			}
		}
	}
	walk(n)
	if len(rows) == 0 {
		return nil
	}
	return []lexNode{lexElement("table", rows, "ltr")}
}

// inlineOf is n's children as inline content.
func (c *htmlConv) inlineOf(n *html.Node, format int) []lexNode {
	var out []lexNode
	for _, k := range children(n) {
		if k.Type == html.ElementNode && htmlIsBlock(k.DataAtom) { // a block inside a paragraph: its text, on a line of its own
			for _, b := range c.block(k) {
				if b["type"] == "image" {
					out = append(out, b)
					continue
				}
				if len(out) > 0 {
					out = append(out, lexBreak())
				}
				out = append(out, lexNode{"type": "text", "text": b.text(), "version": 1, "format": format, "detail": 0,
					"mode": "normal", "style": ""})
			}
			continue
		}
		out = append(out, c.inline(k, format)...)
	}
	return out
}

// inline is one node as inline content; a picture comes as an image block, which paragraphs lifts out.
func (c *htmlConv) inline(n *html.Node, format int) []lexNode {
	switch n.Type {
	case html.TextNode:
		t := htmlSpace.ReplaceAllString(n.Data, " ")
		if t == "" {
			return nil
		}
		return []lexNode{lexText(t, format)}
	case html.ElementNode:
	default:
		return nil
	}
	switch n.DataAtom {
	case atom.Br:
		return []lexNode{lexBreak()}
	case atom.Img:
		return c.image(n)
	case atom.Strong, atom.B:
		return c.inlineOf(n, format|htmlBold)
	case atom.Em, atom.I, atom.Cite, atom.Dfn, atom.Var:
		return c.inlineOf(n, format|htmlItalic)
	case atom.S, atom.Del, atom.Strike:
		return c.inlineOf(n, format|htmlStrikethrough)
	case atom.U, atom.Ins:
		return c.inlineOf(n, format|htmlUnderline)
	case atom.Code, atom.Kbd, atom.Samp, atom.Tt:
		return c.inlineOf(n, format|htmlCode)
	case atom.Sub:
		return c.inlineOf(n, format|htmlSubscript)
	case atom.Sup:
		return c.inlineOf(n, format|htmlSuperscript)
	case atom.A:
		inner := c.inlineOf(n, format)
		href := safeHref(attr(n, "href"))
		if href == "" {
			return inner
		}
		// A link around a picture keeps the picture (a block of its own) and links the text beside it.
		var out, run []lexNode
		flush := func() {
			if len(run) > 0 {
				l := lexElement("link", run, "ltr")
				l["url"], l["rel"], l["target"], l["title"] = href, "noopener nofollow", nil, nil
				out = append(out, l)
				run = nil
			}
		}
		for _, k := range inner {
			if k["type"] == "image" {
				flush()
				out = append(out, k)
				continue
			}
			run = append(run, k)
		}
		flush()
		return out
	case atom.Iframe, atom.Video, atom.Audio, atom.Object, atom.Embed, atom.Canvas, atom.Svg:
		c.left["embed"] = true
		return nil
	case atom.Input, atom.Select, atom.Textarea, atom.Button:
		c.left["form"] = true
		return nil
	case atom.Script:
		c.left["script"] = true
		return nil
	case atom.Style, atom.Noscript, atom.Template:
		return nil
	}
	return c.inlineOf(n, format) // span, font, mark, small, abbr…: their text
}

// image is an <img> as an image block at the address the caller gave, or nothing (counted).
func (c *htmlConv) image(n *html.Node) []lexNode {
	img := HTMLImage{Src: strings.TrimSpace(attr(n, "src")), Alt: strings.TrimSpace(attr(n, "alt")), Class: attr(n, "class")}
	if img.Src == "" {
		return nil
	}
	if c.picture == nil {
		c.pictures++
		return nil
	}
	src, ok := c.picture(img)
	if !ok || safeHref(src) == "" {
		c.pictures++
		return nil
	}
	return []lexNode{{"type": "image", "version": 1, "src": src, "altText": img.Alt}}
}

// paragraphs is an inline run as blocks of typ: its pictures lifted out as blocks of their own, in order, each run of text
// around them trimmed; a run with no text is none, and one that is only a shortcode is left.
func (c *htmlConv) paragraphs(typ string, inline []lexNode) []lexNode {
	var out, run []lexNode
	flush := func() {
		run = trimRun(run)
		if len(run) == 0 {
			return
		}
		el := lexElement(typ, run, "ltr")
		t := el.text()
		if strings.TrimSpace(t) == "" {
			run = nil
			return
		}
		if htmlShortcode.MatchString(strings.TrimSpace(t)) {
			c.left["shortcode"] = true
			run = nil
			return
		}
		el["direction"] = textDirection(t)
		out = append(out, el)
		run = nil
	}
	for _, k := range inline {
		if k["type"] == "image" {
			flush()
			out = append(out, k)
			continue
		}
		run = append(run, k)
	}
	flush()
	return out
}

// trimRun drops the spaces HTML would not show: at the run's two ends and around its line breaks; text nodes left empty go.
func trimRun(run []lexNode) []lexNode {
	var out []lexNode
	for i, k := range run {
		if k["type"] != "text" {
			out = append(out, k)
			continue
		}
		t := k["text"].(string)
		if i == 0 || run[i-1]["type"] == "linebreak" || (len(out) > 0 && endsWithSpace(out[len(out)-1])) {
			t = strings.TrimLeft(t, " ")
		}
		if i == len(run)-1 || run[i+1]["type"] == "linebreak" {
			t = strings.TrimRight(t, " ")
		}
		if t == "" {
			continue
		}
		k["text"] = t
		out = append(out, k)
	}
	for len(out) > 0 && out[0]["type"] == "linebreak" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1]["type"] == "linebreak" {
		out = out[:len(out)-1]
	}
	return out
}

func endsWithSpace(n lexNode) bool {
	t, ok := n["text"].(string)
	return ok && strings.HasSuffix(t, " ")
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Namespace == "" && strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(p *html.Node) {
		if p.Type == html.TextNode {
			b.WriteString(p.Data)
		}
		if p.Type == html.ElementNode && p.DataAtom == atom.Br {
			b.WriteString("\n")
		}
		for k := p.FirstChild; k != nil; k = k.NextSibling {
			walk(k)
		}
	}
	walk(n)
	return b.String()
}

// safeHref is an address a body may link to: http, https, mailto or tel, or a path on the site; "" for any other.
func safeHref(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, "\x00\r\n\t") {
		return ""
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		if u.Host == "" {
			return ""
		}
		return s
	case "mailto", "tel":
		return s
	}
	return ""
}
