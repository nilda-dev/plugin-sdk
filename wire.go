package nilda

import (
	"bytes"
	"encoding/json"
)

// encodeWire writes v as it goes on the wire, in either direction the SDK encodes JSON for Core: a provider's answer to a service
// hook (DispatchServiceHook) and a request this plugin sends (Services.Call's input, every API body). It is json.Marshal's encoding —
// compact, map keys sorted, a json.RawMessage checked and compacted, a Marshaler's output used — with ONE difference: "<", ">" and
// "&" are the characters themselves, not six-byte escapes. Core measures the bytes it reads at its bounds (site.reader/v1: a piece,
// an element, the whole read; a request body's cap), so escaping them cut HTML-heavy content at a fraction of its size (block HTML
// ~1.8 bytes a byte, tag-dense HTML up to six). The value decodes to the same values with any JSON decoder, old or new, so a Core
// and a plugin of different ages still meet; a reader that puts it inside HTML escapes it there.
//
// U+2028 and U+2029 stay escaped, as json.Marshal escapes them: the Encoder does so in strings, but a json.RawMessage is only
// compacted once escaping is off, so they are escaped here. In valid compact JSON either can stand only inside a string, where
// the escape is the same character.
func encodeWire(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	raw := bytes.TrimSuffix(b.Bytes(), []byte{'\n'}) // Encode ends with a newline; json.Marshal does not
	raw = bytes.ReplaceAll(raw, []byte(string(rune(0x2028))), []byte(`\`+"u2028"))
	return bytes.ReplaceAll(raw, []byte(string(rune(0x2029))), []byte(`\`+"u2029")), nil
}
