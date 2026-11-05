package siteimport

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
)

// ErrTooLarge is the error a capped writer returns for the write that would go past its limit.
var ErrTooLarge = fmt.Errorf("site: over its size cap")

// cappedWriter counts what passes and refuses the write that would go over limit.
type cappedWriter struct {
	w        io.Writer
	n, limit int64
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.n+int64(len(p)) > c.limit {
		return 0, ErrTooLarge
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// snapshotWriter writes JSON a piece at a time; the first error sticks and every later call does nothing.
type snapshotWriter struct {
	w   io.Writer
	enc *json.Encoder
	err error
}

func (sw *snapshotWriter) raw(s string) {
	if sw.err == nil {
		_, sw.err = io.WriteString(sw.w, s)
	}
}

// value writes one element. Encode appends a newline, which is whitespace between JSON tokens.
func (sw *snapshotWriter) value(v any) {
	if sw.err == nil {
		sw.err = sw.enc.Encode(v)
	}
}

// object writes one JSON object member by member: each member is preceded by a comma unless it is the first.
type objectWriter struct {
	sw    *snapshotWriter
	empty bool
}

func (sw *snapshotWriter) object() *objectWriter {
	sw.raw("{")
	return &objectWriter{sw: sw, empty: true}
}

// key writes the member name and the colon; the value follows.
func (o *objectWriter) key(k string) {
	if !o.empty {
		o.sw.raw(",")
	}
	o.empty = false
	o.sw.value(k)
	o.sw.raw(":")
}

func (o *objectWriter) field(k string, v any) {
	o.key(k)
	o.sw.value(v)
}

func (o *objectWriter) close() { o.sw.raw("}") }

// writeArray writes the member k as a list, one element at a time. A nil list is left out — or written as null when
// the member is always present (authors, items) — so that what is read back is exactly what was written.
func writeArray[T any](o *objectWriter, k string, list []T, always bool) {
	if list == nil && !always {
		return
	}
	o.key(k)
	if list == nil {
		o.sw.raw("null")
		return
	}
	o.sw.raw("[")
	for i := range list {
		if i > 0 {
			o.sw.raw(",")
		}
		o.sw.value(list[i])
	}
	o.sw.raw("]")
}

// writeSite writes the Site in the shape its json tags give it, each list element by element.
func writeSite(sw *snapshotWriter, site *Site) {
	o := sw.object()
	if site.BaseURL != "" {
		o.field("base_url", site.BaseURL)
	}
	if site.SiteKey != "" {
		o.field("site_key", site.SiteKey)
	}
	if site.Identity != nil {
		o.field("identity", site.Identity)
	}
	o.key("authors")
	if site.Authors == nil {
		sw.raw("null")
	} else {
		authors := sw.object()
		for _, k := range slices.Sorted(maps.Keys(site.Authors)) {
			authors.field(k, site.Authors[k])
		}
		authors.close()
	}
	writeArray(o, "vocabularies", site.Vocabularies, false)
	writeArray(o, "terms", site.Terms, false)
	writeArray(o, "media", site.Media, false)
	if p := site.MediaPolicy; p != nil {
		o.key("media_policy")
		policy := sw.object()
		writeArray(policy, "own_hosts", p.OwnHosts, false)
		writeArray(policy, "library", p.Library, false)
		if p.Authenticated {
			policy.field("authenticated", true)
		}
		policy.close()
	}
	writeArray(o, "types", site.Types, false)
	writeArray(o, "field_groups", site.FieldGroups, false)
	writeArray(o, "menus", site.Menus, false)
	writeArray(o, "menu_locations", site.Locations, false)
	writeArray(o, "redirects", site.Redirects, false)
	if site.Settings != nil {
		o.field("settings", site.Settings)
	}
	writeArray(o, "plugins", site.Plugins, false)
	writeArray(o, "sidebars", site.Sidebars, false)
	writeArray(o, "blocks", site.Blocks, false)
	if site.CustomCSS != nil {
		o.field("custom_css", site.CustomCSS)
	}
	if site.Theme != nil {
		o.field("theme", site.Theme)
	}
	if sh := site.Shop; sh != nil {
		// The shop's records one at a time, like the items: a catalogue of 50,000 products is the snapshot's largest list.
		o.key("shop")
		shop := sw.object()
		shop.field("settings", sh.Settings)
		writeArray(shop, "records", sh.Records, true)
		shop.field("pages", sh.Pages)
		shop.close()
	}
	writeArray(o, "items", site.Items, true)
	writeArray(o, "not_carried", site.NotCarried, false)
	writeArray(o, "warnings", site.Warnings, false)
	o.close()
}

func decodeSite(dec *json.Decoder, site *Site) error {
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		key, err := memberKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "base_url":
			err = dec.Decode(&site.BaseURL)
		case "site_key":
			err = dec.Decode(&site.SiteKey)
		case "identity":
			err = dec.Decode(&site.Identity)
		case "authors":
			err = decodeAuthors(dec, &site.Authors)
		case "vocabularies":
			err = decodeArray(dec, &site.Vocabularies)
		case "terms":
			err = decodeArray(dec, &site.Terms)
		case "media":
			err = decodeArray(dec, &site.Media)
		case "media_policy":
			site.MediaPolicy = &MediaPolicy{}
			err = decodeMediaPolicy(dec, site.MediaPolicy)
		case "types":
			err = decodeArray(dec, &site.Types)
		case "field_groups":
			err = decodeArray(dec, &site.FieldGroups)
		case "menus":
			err = decodeArray(dec, &site.Menus)
		case "menu_locations":
			err = decodeArray(dec, &site.Locations)
		case "redirects":
			err = decodeArray(dec, &site.Redirects)
		case "settings":
			err = dec.Decode(&site.Settings)
		case "plugins":
			err = decodeArray(dec, &site.Plugins)
		case "sidebars":
			err = decodeArray(dec, &site.Sidebars)
		case "blocks":
			err = decodeArray(dec, &site.Blocks)
		case "custom_css":
			err = dec.Decode(&site.CustomCSS)
		case "theme":
			err = dec.Decode(&site.Theme)
		case "shop":
			site.Shop = &SourceShop{}
			err = decodeShop(dec, site.Shop)
		case "items":
			err = decodeArray(dec, &site.Items)
		case "not_carried":
			err = decodeArray(dec, &site.NotCarried)
		case "warnings":
			err = decodeArray(dec, &site.Warnings)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

func decodeAuthors(dec *json.Decoder, into *map[string]Author) error {
	tok, err := dec.Token()
	if err != nil || tok == nil {
		return err // null: no registry
	}
	if tok != json.Delim('{') {
		return fmt.Errorf("live read snapshot: authors is %v, not an object", tok)
	}
	*into = map[string]Author{}
	for dec.More() {
		key, err := memberKey(dec)
		if err != nil {
			return err
		}
		var a Author
		if err := dec.Decode(&a); err != nil {
			return err
		}
		(*into)[key] = a
	}
	return expectDelim(dec, '}')
}

func decodeMediaPolicy(dec *json.Decoder, p *MediaPolicy) error {
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		key, err := memberKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "own_hosts":
			err = decodeArray(dec, &p.OwnHosts)
		case "library":
			err = decodeArray(dec, &p.Library)
		case "authenticated":
			err = dec.Decode(&p.Authenticated)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

func decodeShop(dec *json.Decoder, shop *SourceShop) error {
	if err := expectDelim(dec, '{'); err != nil {
		return err
	}
	for dec.More() {
		key, err := memberKey(dec)
		if err != nil {
			return err
		}
		switch key {
		case "settings":
			err = dec.Decode(&shop.Settings)
		case "records":
			err = decodeArray(dec, &shop.Records)
		case "pages":
			err = dec.Decode(&shop.Pages)
		default:
			err = skipValue(dec)
		}
		if err != nil {
			return err
		}
	}
	return expectDelim(dec, '}')
}

// decodeArray reads a JSON list one element at a time: null leaves the list nil, [] makes it empty.
func decodeArray[T any](dec *json.Decoder, into *[]T) error {
	tok, err := dec.Token()
	if err != nil || tok == nil {
		return err
	}
	if tok != json.Delim('[') {
		return fmt.Errorf("live read snapshot: %v is not a list", tok)
	}
	*into = []T{}
	for dec.More() {
		var el T
		if err := dec.Decode(&el); err != nil {
			return err
		}
		*into = append(*into, el)
	}
	return expectDelim(dec, ']')
}

// skipValue reads past one value this version does not know.
func skipValue(dec *json.Decoder) error {
	var skip json.RawMessage
	return dec.Decode(&skip)
}

func memberKey(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	if err != nil {
		return "", err
	}
	key, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("live read snapshot: %v is not a member name", tok)
	}
	return key, nil
}

func expectDelim(dec *json.Decoder, want json.Delim) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != want {
		return fmt.Errorf("live read snapshot: %v where %v belongs", tok, want)
	}
	return nil
}

// Encode writes site in Core's snapshot encoding: one JSON object, every list element by element so the largest buffer is the
// largest single element, never the whole site. HTML is not escaped.
func Encode(w io.Writer, site *Site) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	sw := &snapshotWriter{w: w, enc: enc}
	writeSite(sw, site)
	return sw.err
}

// Decode reads the encoding Encode writes into site, element by element, ignoring members it does not know (a newer
// writer's), so a reader and Core of different ages still meet on the members they share.
func Decode(r io.Reader, site *Site) error {
	return decodeSite(json.NewDecoder(r), site)
}
