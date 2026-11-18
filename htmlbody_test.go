package nilda

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// node is a decoded body node, for the assertions.
type testNode struct {
	Type      string          `json:"type"`
	Tag       string          `json:"tag"`
	Text      string          `json:"text"`
	Format    json.RawMessage `json:"format"`
	URL       string          `json:"url"`
	Src       string          `json:"src"`
	AltText   string          `json:"altText"`
	ListType  string          `json:"listType"`
	Direction string          `json:"direction"`
	Header    int             `json:"headerState"`
	Children  []testNode      `json:"children"`
}

func decodeBody(t *testing.T, raw json.RawMessage) []testNode {
	t.Helper()
	if err := CheckBody(raw); err != nil {
		t.Fatalf("Core would refuse the body: %v", err)
	}
	var doc struct {
		Root testNode `json:"root"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Root.Type != "root" {
		t.Fatalf("root %+v", doc.Root)
	}
	return doc.Root.Children
}

// shape is a block list as "type[/tag]" names, children in brackets: what the document SAYS, without its envelope.
func shape(ns []testNode) string {
	var parts []string
	for _, n := range ns {
		s := n.Type
		if n.Tag != "" {
			s += "/" + n.Tag
		}
		if n.Type == "text" {
			s = strconv.Quote(n.Text)
			if f := string(n.Format); f != "0" && f != "" {
				s = "<*" + f + ">" + s
			}
		}
		if n.Type == "link" {
			s += "(" + n.URL + ")"
		}
		if n.Type == "image" {
			s += "(" + n.Src + "|" + n.AltText + ")"
		}
		if len(n.Children) > 0 {
			s += "[" + shape(n.Children) + "]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " ")
}

// A product description keeps what it says: headings, paragraphs and their line breaks, bold and italic, links, bullet and
// numbered lists (a nested one inside its item), a quote, a table with its header, a rule, and its pictures at the addresses the
// caller gave — a picture inside a link stays, a block of its own.
func TestHTMLBodyKeepsHeadingsLinksListsAndPictures(t *testing.T) {
	src := `<h2>The linen <em>apron</em></h2>
<p>Washed <strong>twice</strong>,<br> hemmed by hand. <a href="https://shop.example/care">Care</a>.</p>
<ul><li>100 % linen</li><li>Two pockets<ul><li>deep</li></ul></li></ul>
<ol><li>Wash</li><li>Dry</li></ol>
<p><a href="https://shop.example/big.jpg"><img class="alignnone wp-image-68" src="https://shop.example/a.jpg" alt="The apron"></a></p>
<blockquote><p>Best apron.</p></blockquote>
<table><thead><tr><th>Size</th></tr></thead><tbody><tr><td>M</td></tr></tbody></table>
<hr>`
	var asked []HTMLImage
	raw, left := HTMLBody(src, func(img HTMLImage) (string, bool) {
		asked = append(asked, img)
		return "/api/v1/media/0b9f/file", true
	})
	if left.Any() {
		t.Errorf("left %+v", left)
	}
	got := shape(decodeBody(t, raw))
	want := `heading/h2["The linen " <*2>"apron"] ` +
		`paragraph["Washed " <*1>"twice" "," linebreak "hemmed by hand. " link(https://shop.example/care)["Care"] "."] ` +
		`list/ul[listitem["100 % linen"] listitem["Two pockets"] listitem[list/ul[listitem["deep"]]]] ` +
		`list/ol[listitem["Wash"] listitem["Dry"]] ` +
		`image(/api/v1/media/0b9f/file|The apron) ` +
		`quote["Best apron."] ` +
		`table[tablerow[tablecell[paragraph["Size"]]] tablerow[tablecell[paragraph["M"]]]] ` +
		`horizontalrule`
	if got != want {
		t.Errorf("the document says\n%s\nwant\n%s", got, want)
	}
	if len(asked) != 1 || asked[0].Class != "alignnone wp-image-68" || asked[0].Src != "https://shop.example/a.jpg" {
		t.Errorf("the picture asked: %+v", asked)
	}
	if text := BodyText(raw); !strings.Contains(text, "Washed twice,\nhemmed by hand. Care.") || !strings.Contains(text, "deep") {
		t.Errorf("BodyText reads %q", text)
	}
	var doc struct {
		Root struct {
			Children []testNode `json:"children"`
		} `json:"root"`
	}
	_ = json.Unmarshal(raw, &doc)
	if th := doc.Root.Children[6].Children[0].Children[0]; th.Header != 1 {
		t.Errorf("the header cell %+v", th)
	}
}

// Nothing unsafe gets through: a script, a javascript: or data: link (its text stays), an embedded frame and a form are left and
// named; a paragraph that is only a shortcode is named; a picture the caller does not place, or places at an address no body may
// name, is counted; text is only ever text.
func TestHTMLBodyLeavesWhatItCannotSayAndNamesIt(t *testing.T) {
	src := `<p>Hi <a href="javascript:alert(1)">there</a> <a href="data:text/html,x">you</a></p>
<script>alert(1)</script><iframe src="https://video.example/1"></iframe>
<form><input name="q"></form>
<p>[gallery ids="1,2"]</p>
<p><img src="https://shop.example/gone.jpg"><img src="https://shop.example/evil.jpg"></p>
<p>&lt;b&gt;not bold&lt;/b&gt;</p>`
	raw, left := HTMLBody(src, func(img HTMLImage) (string, bool) {
		if strings.Contains(img.Src, "evil") {
			return "javascript:alert(1)", true
		}
		return "", false
	})
	if got := shape(decodeBody(t, raw)); got != `paragraph["Hi " "there" " " "you"] paragraph["<b>not bold</b>"]` {
		t.Errorf("the document says %s", got)
	}
	if want := (HTMLBodyLeft{Pictures: 2, Parts: []string{"embed", "form", "script", "shortcode"}}); !reflect.DeepEqual(left, want) {
		t.Errorf("left %+v, want %+v", left, want)
	}
	if _, left := HTMLBody(`<p><img src="https://shop.example/a.jpg"></p>`, nil); left.Pictures != 1 {
		t.Errorf("with no picture answer, left %+v", left)
	}
}

// Empty HTML, or HTML with no text, is Core's "no body yet"; right-to-left words are marked so, block by block.
func TestHTMLBodyOfNothingIsNoBodyAndPersianReadsRightToLeft(t *testing.T) {
	for _, src := range []string{"", "   ", "<p> </p><br>", "<style>p{}</style>"} {
		if raw, _ := HTMLBody(src, nil); string(raw) != `{}` {
			t.Errorf("HTMLBody(%q) = %s, want {}", src, raw)
		}
	}
	raw, _ := HTMLBody(`<p>پیش‌بند کتانی</p><p>Linen</p>`, nil)
	ns := decodeBody(t, raw)
	if ns[0].Direction != "rtl" || ns[1].Direction != "ltr" {
		t.Errorf("directions %q %q", ns[0].Direction, ns[1].Direction)
	}
}
