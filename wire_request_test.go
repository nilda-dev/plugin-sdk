package nilda

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// ONE ENCODER FOR EVERYTHING THE SDK SENDS CORE (wire.go). The dispatch's answer was moved to it first (wireencoding_test.go); these
// tests hold the other direction — a request body and a service call's input — and the claim both rest on: the bytes differ from
// json.Marshal's, the values never do, so a Core and a plugin of different ages still meet.

// htmlPayload is a page body the way a WordPress import hands it over: tags, entities, quotes, an ampersand in a URL, the two line
// separators and a character outside the Basic Multilingual Plane.
func htmlPayload() map[string]any {
	body := strings.Repeat(`<p class="a">Fish &amp; chips <a href="/x?a=1&b=2">go</a> <b>bold</b> `+string(lineSep)+` x `+string(paraSep)+` y `+"\U0001F41F"+` < typed</p>`, 40)
	return map[string]any{"title": "<Tom & Jerry>", "content": body, "n": 3, "nested": map[string]any{"list": []any{"<", ">", "&", 1.5, nil, true}}}
}

func TestEncodeWireRoundTripsAndIsSmallerThanMarshal(t *testing.T) {
	in := htmlPayload()
	got, err := encodeWire(in)
	if err != nil {
		t.Fatal(err)
	}
	old, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) >= len(old) {
		t.Errorf("the wire form is %d bytes, json.Marshal's %d: nothing was regained", len(got), len(old))
	}
	for _, hex := range []string{"003c", "003e", "0026"} {
		if bytes.Contains(got, []byte(esc(hex))) {
			t.Errorf("the wire form still escapes a character to %s", esc(hex))
		}
	}
	// U+2028/2029 are still escaped (Go's own safety rule for a script embedding JSON); that is the same character to a decoder.
	if bytes.ContainsRune(got, lineSep) || bytes.ContainsRune(got, paraSep) || !bytes.Contains(got, []byte(esc("2028"))) || !bytes.Contains(got, []byte(esc("2029"))) {
		t.Errorf("U+2028/U+2029 must stay six-byte escapes")
	}
	if bytes.HasSuffix(got, []byte("\n")) || !json.Valid(got) {
		t.Errorf("the wire form must be valid JSON with no trailing newline (json.Marshal has none): %q", got[len(got)-8:])
	}
	// Round trip: what comes out is what went in, including the character outside the BMP.
	var back map[string]any
	if err := json.Unmarshal(got, &back); err != nil {
		t.Fatal(err)
	}
	var want map[string]any
	if err := json.Unmarshal(old, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, want) || back["content"] != in["content"] || back["title"] != in["title"] {
		t.Errorf("the wire form decodes to other values than json.Marshal's")
	}
}

// Old and new bytes are the same JSON to every decoder: whole-value, struct, number-preserving, strict and token by token. This is the
// compatibility claim: an old Core reading a new plugin, and a new Core reading an old plugin, use only encoding/json's decoding.
func TestOldAndNewFormsDecodeIdentically(t *testing.T) {
	type page struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		N       int    `json:"n"`
	}
	in := htmlPayload()
	newForm, err := encodeWire(in)
	if err != nil {
		t.Fatal(err)
	}
	oldForm, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(newForm, oldForm) {
		t.Fatal("the test payload does not tell the two forms apart")
	}
	var a, b page
	if err := json.Unmarshal(oldForm, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(newForm, &b); err != nil || a != b {
		t.Errorf("struct decode differs: %v", err)
	}
	for name, form := range map[string][]byte{"old": oldForm, "new": newForm} {
		dec := json.NewDecoder(bytes.NewReader(form))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("%s form: %v", name, err)
		}
		if dec.More() {
			t.Errorf("%s form carries more than one value", name)
		}
	}
	tokens := func(form []byte) []string {
		dec := json.NewDecoder(bytes.NewReader(form))
		var out []string
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				return out
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%T:%v", tok, tok))
		}
	}
	if !reflect.DeepEqual(tokens(oldForm), tokens(newForm)) {
		t.Errorf("the two forms tokenise differently")
	}
	// The SDK's own consuming code reads both: a service answer in either form decodes to the same output.
	testQuoteContract(t, "test.quote/v1")
	for _, form := range [][]byte{oldForm, newForm} {
		answer := ServiceAnswer{Provider: "p", Output: form}
		var got page
		if err := answer.Decode(&got); err != nil || got != a {
			t.Errorf("a service answer in this form decodes to %+v %v", got, err)
		}
	}
}

// A request body goes out with its characters themselves. The server a plugin talks to reads it with encoding/json, as Core does.
func TestAnAPIRequestBodyIsSentUnescaped(t *testing.T) {
	var seen []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	core := NewCoreForTest("importer", []string{"content.write"}, nil, srv.URL, "t", nil)
	in := htmlPayload()
	if err := core.API().JSON(context.Background(), http.MethodPost, "/content", in, nil); err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(in)
	if len(seen) >= len(old) || bytes.Contains(seen, []byte(esc("003c")+"p")) {
		t.Errorf("the body went out %d bytes against json.Marshal's %d, escapes kept", len(seen), len(old))
	}
	var back map[string]any
	if err := json.Unmarshal(seen, &back); err != nil || back["content"] != in["content"] {
		t.Errorf("Core would read other content than the plugin wrote: %v", err)
	}
	// An io.Reader and a []byte body are the caller's own bytes: never re-encoded.
	if got, _ := encodeBody([]byte(`{"a":"` + esc("003c") + `"}`)); string(got) != `{"a":"`+esc("003c")+`"}` {
		t.Errorf("a caller's own bytes were changed: %s", got)
	}
}

// A service call's input is encoded once for the contract's check and nested raw into the request: both stay unescaped, so what the
// desk reads is what the plugin wrote at its real size.
func TestAServiceCallInputIsSentUnescaped(t *testing.T) {
	testQuoteContract(t, "test.quote/v1")
	var seen []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"contract": "test.quote/v1", "method": "quote", "answers": []any{}}})
	}))
	defer srv.Close()
	core := NewCoreForTest("shop", []string{"service_consumer"}, nil, srv.URL, "t", []string{"use:test.quote/v1"})
	in := htmlPayload()
	if _, err := core.API().Services().Call(context.Background(), "test.quote/v1", "quote", in, ServiceCallOptions{}); err != nil {
		t.Fatal(err)
	}
	oldInput, _ := json.Marshal(in)
	if len(seen) >= len(oldInput) || bytes.Contains(seen, []byte(esc("003c")+"p")) || bytes.Contains(seen, []byte(esc("0026"))) {
		t.Errorf("the call went out %d bytes against the escaped input's %d: %.80s", len(seen), len(oldInput), seen)
	}
	var req struct {
		Input map[string]any `json:"input"`
	}
	if err := json.Unmarshal(seen, &req); err != nil || req.Input["content"] != in["content"] || req.Input["title"] != in["title"] {
		t.Errorf("the desk would read other input than the plugin wrote: %v", err)
	}
}

// Text that merely looks like an escape (a backslash, "u003c") is data, not an escape: it survives the trip and is not turned into a
// "<" on the way, which a replace-after approach to un-escaping would do.
func TestTypedEscapeTextIsNotUnescaped(t *testing.T) {
	typed := `a \u003c b \\u0026 c ` + esc("003c")
	raw, err := encodeWire(map[string]string{"s": typed})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]string
	if err := json.Unmarshal(raw, &back); err != nil || back["s"] != typed {
		t.Fatalf("typed escape text changed: %q %v", back["s"], err)
	}
}
