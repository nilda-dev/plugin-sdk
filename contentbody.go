package nilda

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"golang.org/x/text/language"
)

// A CONTENT ITEM'S BODY, AS CORE STORES IT. Core's editor keeps a body as a Lexical document,
// `{"root": {"type": "root", "children": [ … ]}}`, and refuses any other shape on every write (an empty body, `{}`,
// is an item with no body yet). A plugin that writes plain words — a product description, a FAQ answer — and reads
// them back used to rebuild this format by hand; these do it the one way Core reads and searches. Formatting a
// person adds in Core's editor (bold, a link, a heading) stays on the page; BodyText reads it as plain text.

// TextBody is text as a body: one paragraph per block the text separates with a blank line, each paragraph's line
// breaks kept. Empty (or only spaces) text is `{}`, Core's "no body yet" — an empty paragraph would give a page a
// blank line and a sitemap something to list. The paragraphs read right to left when their words do (Arabic,
// Hebrew…), as Core's editor would mark them.
func TextBody(text string) json.RawMessage {
	paras := bodyParagraphs(text)
	if len(paras) == 0 {
		return json.RawMessage(`{}`)
	}
	children := make([]any, 0, len(paras))
	for _, p := range paras {
		inline := []any{}
		for i, line := range strings.Split(p, "\n") {
			if i > 0 {
				inline = append(inline, map[string]any{"type": "linebreak", "version": 1})
			}
			if line != "" {
				inline = append(inline, map[string]any{"type": "text", "text": line, "format": 0, "style": "",
					"mode": "normal", "detail": 0, "version": 1})
			}
		}
		children = append(children, map[string]any{"type": "paragraph", "format": "", "indent": 0, "version": 1,
			"direction": textDirection(p), "children": inline})
	}
	raw, _ := json.Marshal(map[string]any{"root": map[string]any{"type": "root", "format": "", "indent": 0,
		"version": 1, "direction": textDirection(text), "children": children}})
	return raw
}

// BodyText is a body read as plain text: each block (a paragraph, a heading, a quote, a list item, a table cell) on
// its own, blocks separated by a blank line, a line break kept as a line break. "" for an empty or unreadable body.
// BodyText(TextBody(s)) is s with its blank lines normalised.
func BodyText(body json.RawMessage) string {
	var doc struct {
		Root bodyNode `json:"root"`
	}
	if len(body) == 0 || json.Unmarshal(body, &doc) != nil {
		return ""
	}
	var blocks []string
	var walk func(n bodyNode)
	walk = func(n bodyNode) {
		if n.holdsBlocks() {
			for _, c := range n.Children {
				walk(c)
			}
			return
		}
		if t := strings.TrimSpace(n.inline()); t != "" {
			blocks = append(blocks, t)
		}
	}
	for _, c := range doc.Root.Children {
		walk(c)
	}
	return strings.Join(blocks, "\n\n")
}

// ErrNotABody is CheckBody's answer for a body Core would refuse.
var ErrNotABody = errors.New(`nilda: a body is {} or a Lexical document {"root": {"type": "root", "children": [ … ]}}`)

// CheckBody answers whether Core would take body on a content write: absent, `{}`, or an object with a root object.
// The same rule Core's content service applies, for a plugin's own tests and before a write.
func CheckBody(body json.RawMessage) error {
	if len(body) == 0 {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		return ErrNotABody
	}
	if len(doc) == 0 {
		return nil
	}
	var root map[string]json.RawMessage
	if raw, ok := doc["root"]; !ok || json.Unmarshal(raw, &root) != nil || root == nil {
		return ErrNotABody
	}
	return nil
}

// LocaleDirection is a locale's text direction, "rtl" or "ltr": read off the script the language is written in
// (Persian, Arabic, Hebrew, Urdu… are right to left; Kurdish in Arabic script is, in Latin script it is not), as
// Core decides it. "ltr" for a code that names no language.
func LocaleDirection(locale string) string {
	tag, err := language.Parse(strings.TrimSpace(locale))
	if err != nil {
		return "ltr"
	}
	if s, _ := tag.Script(); rtlScripts[s.String()] {
		return "rtl"
	}
	return "ltr"
}

// rtlScripts are the right-to-left scripts (Core's internal/i18n list).
var rtlScripts = map[string]bool{
	"Adlm": true, "Arab": true, "Aran": true, "Hebr": true, "Mand": true, "Mend": true,
	"Nkoo": true, "Rohg": true, "Samr": true, "Syrc": true, "Thaa": true, "Yezi": true,
}

var bodyBlankLine = regexp.MustCompile(`\n[ \t]*\n\s*`)

func bodyParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var out []string
	for _, p := range bodyBlankLine.Split(text, -1) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// textDirection is "rtl" when the first letter that has a direction is a right-to-left one.
func textDirection(s string) string {
	for _, r := range s {
		switch {
		case r >= 0x0590 && r <= 0x08FF, r >= 0xFB1D && r <= 0xFDFF, r >= 0xFE70 && r <= 0xFEFF:
			return "rtl"
		case (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= 0x00C0 && r <= 0x024F) || (r >= 0x0370 && r <= 0x052F):
			return "ltr"
		}
	}
	return "ltr"
}

type bodyNode struct {
	Type     string     `json:"type"`
	Text     string     `json:"text"`
	Children []bodyNode `json:"children"`
}

// holdsBlocks says whether a node holds blocks rather than text: a list holds items, a table rows, a layout columns.
func (n bodyNode) holdsBlocks() bool {
	for _, c := range n.Children {
		switch c.Type {
		case "listitem", "list", "paragraph", "heading", "quote", "tablerow", "tablecell", "layoutitem":
			return true
		}
	}
	return false
}

func (n bodyNode) inline() string {
	switch n.Type {
	case "text":
		return n.Text
	case "linebreak":
		return "\n"
	}
	var b strings.Builder
	for _, c := range n.Children {
		b.WriteString(c.inline())
	}
	return b.String()
}
