package nildatest

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ONE KEY, ONE SPELLING, as Core's service desk refuses it (Core internal/servicedesk, reason key_given_twice; the whole
// desk, every contract).
//
// encoding/json fills a field from a key in ANY case, and when an object holds two such keys the LAST one wins, while a
// schema validator reads each key as written: {"import": {...}, "IMPORT": {...}} would pass the schema on the first and be
// acted on by the second. So the desk refuses, with a 422 whose details say reason key_given_twice and at = the JSON
// path of the object, before the schema is asked and before any provider sees the request:
//
//   - an object, at any depth, that names one key twice exactly as written;
//   - an object whose schema declares `properties` (a record, a settings object…) naming two keys equal under Unicode
//     simple case folding (strings.EqualFold: "name"/"NAME", "kind"/"Kind" with the Kelvin sign).
//
// Inside a FREE-FORM MAP — an object whose schema gives only `additionalProperties: {schema}`, such as shop.import/v1's
// `sku_pairs` or `tax_class_rates` — the keys are the caller's data, and "abc" and "ABC" are two distinct keys: only an
// exact repeat is refused there. The order is Core's: the grant, then the keys, then the schema.

// ErrKeyGivenTwice is the desk's refusal of a request whose object names one key twice (KeyGivenTwice).
var ErrKeyGivenTwice = errors.New("the request names one key twice in one object: send each once")

// KeyGivenTwice is the refusal of a request that names one key twice; At is the JSON path of the object ("$.records[0].data").
type KeyGivenTwice struct{ At string }

func (e *KeyGivenTwice) Error() string {
	return "the request names one key twice in the object at " + e.At + " (in a record, a key is one key in any spelling " +
		"of its case): send each once"
}

func (e *KeyGivenTwice) Unwrap() error { return ErrKeyGivenTwice }

// maxPathShown bounds the path the refusal names: its keys are the caller's own text.
const maxPathShown = 200

// refuseKeyGivenTwice answers the refusal when an object of input names one key twice, schema names the method's input
// schema; a request that is not JSON is left to the schema's refusal.
func refuseKeyGivenTwice(input, schema []byte) error {
	var root any
	if len(schema) > 0 {
		_ = json.Unmarshal(schema, &root)
	}
	at := keyGivenTwice(input, root)
	if at == "" {
		return nil
	}
	if len(at) > maxPathShown {
		at = at[:maxPathShown] + "…"
	}
	return &KeyGivenTwice{At: at}
}

// schemaAt is the schemas a value is checked against at one place of the request: a node and everything allOf, anyOf,
// oneOf and if/then/else add to it (all branches, whichever the data takes — the stricter reading).
type schemaAt []map[string]any

func expand(nodes ...any) schemaAt {
	var out schemaAt
	var add func(n any, depth int)
	add = func(n any, depth int) {
		m, ok := n.(map[string]any)
		if !ok || depth > 32 {
			return
		}
		out = append(out, m)
		for _, k := range []string{"allOf", "anyOf", "oneOf"} {
			if list, ok := m[k].([]any); ok {
				for _, sub := range list {
					add(sub, depth+1)
				}
			}
		}
		for _, k := range []string{"if", "then", "else"} {
			add(m[k], depth+1)
		}
	}
	for _, n := range nodes {
		add(n, 0)
	}
	return out
}

// declared says the object here has a declared shape (`properties`): a record whose keys are names, folded as Go reads them.
func (s schemaAt) declared() bool {
	for _, m := range s {
		if p, ok := m["properties"].(map[string]any); ok && len(p) > 0 {
			return true
		}
	}
	return false
}

// member is the schema of an object's member k: its declared property, else the map's additionalProperties schema.
func (s schemaAt) member(k string) schemaAt {
	var subs []any
	for _, m := range s {
		if p, ok := m["properties"].(map[string]any); ok {
			if sub, ok := p[k]; ok {
				subs = append(subs, sub)
				continue
			}
		}
		if ap, ok := m["additionalProperties"].(map[string]any); ok {
			subs = append(subs, ap)
		}
	}
	return expand(subs...)
}

// element is the schema of an array's element.
func (s schemaAt) element() schemaAt {
	var subs []any
	for _, m := range s {
		if it, ok := m["items"].(map[string]any); ok {
			subs = append(subs, it)
		}
	}
	return expand(subs...)
}

// keyGivenTwice is the path of the first object of input that names one key twice, "" when none does.
func keyGivenTwice(input []byte, schema any) string {
	type frame struct {
		object  bool
		wantKey bool            // an object between members: the next token is a key or its end
		fold    bool            // the object's schema declares properties: keys compare under case folding
		keys    map[string]bool // an object's keys, as compared
		key     string          // the member being read, in an object
		index   int             // the element being read, in an array
		schema  schemaAt
	}
	var stack []*frame
	ended := func() {
		if len(stack) == 0 {
			return
		}
		if top := stack[len(stack)-1]; top.object {
			top.wantKey = true
		} else {
			top.index++
		}
	}
	path := func() string {
		var b strings.Builder
		b.WriteString("$")
		for _, f := range stack {
			switch {
			case f.object && !f.wantKey:
				b.WriteString("." + f.key)
			case !f.object:
				b.WriteString("[" + strconv.Itoa(f.index) + "]")
			}
		}
		return b.String()
	}
	// here is the schema of the value about to be read.
	here := func() schemaAt {
		if len(stack) == 0 {
			return expand(schema)
		}
		top := stack[len(stack)-1]
		if top.object {
			return top.schema.member(top.key)
		}
		return top.schema.element()
	}
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	for {
		tok, err := dec.Token()
		if err != nil {
			return "" // the end, or not JSON: the schema answers for it
		}
		if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].wantKey {
			top := stack[n-1]
			if tok == json.Delim('}') {
				stack = stack[:n-1]
				ended()
				continue
			}
			k, _ := tok.(string)
			seen := k
			if top.fold {
				seen = foldKey(k)
			}
			if top.keys[seen] {
				return path()
			}
			top.keys[seen], top.key, top.wantKey = true, k, false
			continue
		}
		switch tok {
		case json.Delim('{'):
			s := here()
			stack = append(stack, &frame{object: true, wantKey: true, fold: s.declared(), keys: map[string]bool{}, schema: s})
		case json.Delim('['):
			stack = append(stack, &frame{schema: here()})
		case json.Delim(']'):
			stack = stack[:len(stack)-1]
			ended()
		default:
			ended()
		}
	}
}

// foldKey is a key as encoding/json matches it: each rune replaced by the smallest of its case-folding orbit, so two keys
// strings.EqualFold calls equal fold to one string ("K", "k" and the Kelvin sign alike).
func foldKey(k string) string {
	var b strings.Builder
	b.Grow(len(k))
	for _, r := range k {
		if r == utf8.RuneError {
			b.WriteRune(r)
			continue
		}
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			least = min(least, f)
		}
		b.WriteRune(least)
	}
	return b.String()
}
