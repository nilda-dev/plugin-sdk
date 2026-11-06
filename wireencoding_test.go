package nilda

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// THE DISPATCH WRITES AN ANSWER'S CHARACTERS THEMSELVES (owner decision, option A). Core measures the bytes on the wire at every
// bound of site.reader/v1 (a piece 4 MiB, an element 1 MiB, the whole read), so an answer written with json.Marshal — "<", ">" and
// "&" six bytes each — cut HTML-heavy content at a fraction of its real size. The answer is still JSON, decodes to the same values,
// is compact, and keeps json.Marshal's U+2028/U+2029 escapes (JSON allows them raw; a script embedding the answer does not).

// The two separators and a JSON escape are built, never typed, so no editor or tool can turn the escape into the character.
const (
	lineSep = rune(0x2028)
	paraSep = rune(0x2029)
)

// esc is the six-byte JSON escape of the character with this hex code ("2028" → backslash, u, 2028).
func esc(hex string) string { return `\` + "u" + hex }

type wireReader struct{ answer SiteReaderPiece }

func (w wireReader) ServiceMethods() []ServiceMethod {
	return []ServiceMethod{Serves(ContractSiteReader, SiteReaderNext, func(context.Context, SiteReaderNextRequest) (any, error) {
		return w.answer, nil
	})}
}

func dispatchNext(t *testing.T, answer SiteReaderPiece) ([]byte, error) {
	t.Helper()
	out, ok, err := DispatchServiceHook(context.Background(), wireReader{answer}, ServiceHook(ContractSiteReader, SiteReaderNext),
		[]byte(`{"read_id":"r1","seq":0}`))
	if !ok {
		t.Fatalf("the dispatch did not take the call: %v", err)
	}
	return out, err
}

// sameValues fails unless a and b decode to the same JSON values.
func sameValues(t *testing.T, a, b []byte) {
	t.Helper()
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		t.Fatalf("not JSON: %v: %s", err, a)
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		t.Fatalf("not JSON: %v: %s", err, b)
	}
	if !reflect.DeepEqual(va, vb) {
		t.Errorf("the values differ:\n %s\n %s", a, b)
	}
}

func TestTheDispatchWritesAnAnswersCharactersThemselves(t *testing.T) {
	ls, ps := string(lineSep), string(paraSep)
	answer := SiteReaderPiece{Seq: 0, SnapshotVersion: SiteReaderSnapshotVersion, Last: true,
		Piece: map[string]json.RawMessage{
			"site_key": json.RawMessage(`"old.example"`),
			// A raw member holds the separators as characters: json.Marshal escapes them there too, and so must the dispatch.
			"items": json.RawMessage(`[{"title":"Fish & chips","body":"<p>a<b>bold</b> line` + ls + `sep` + ps + `end</p>"}]`),
		},
		Progress: &SiteReaderProgress{Phase: "<items> & " + ls + ps, Done: 1, Total: 1}}
	out, err := dispatchNext(t, answer)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"seq":0,"snapshot_version":4,"last":true,"piece":{"items":[{"title":"Fish & chips","body":"<p>a<b>bold</b> line` +
		esc("2028") + `sep` + esc("2029") + `end</p>"}],"site_key":"old.example"},"progress":{"phase":"<items> & ` +
		esc("2028") + esc("2029") + `","done":1,"total":1}}`
	if string(out) != want {
		t.Errorf("the dispatch wrote\n %s\nwant\n %s", out, want)
	}
	for _, hex := range []string{"003c", "003e", "0026"} {
		if bytes.Contains(out, []byte(esc(hex))) {
			t.Errorf("the answer still escapes a character to %s: %s", esc(hex), out)
		}
	}
	if bytes.ContainsRune(out, lineSep) || bytes.ContainsRune(out, paraSep) {
		t.Errorf("the answer carries a raw U+2028 or U+2029, which json.Marshal escapes: %q", out)
	}
	marshalled, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	sameValues(t, out, marshalled)
	if len(out) >= len(marshalled) {
		t.Errorf("the answer is %d bytes on the wire, json.Marshal's %d: nothing was regained", len(out), len(marshalled))
	}
	var back SiteReaderPiece
	if err := json.Unmarshal(out, &back); err != nil || back.Progress == nil || back.Progress.Phase != answer.Progress.Phase {
		t.Fatalf("the answer did not decode back: %+v %v", back, err)
	}
}

// A raw member is still compacted and still valid, as json.Marshal leaves it: white space goes, every escape it holds stays as it
// was written (an escape is not decoded — a "<" the provider wrote as six bytes is still six bytes), a raw "<" stays one byte.
func TestARawMemberIsStillCompactedAndValid(t *testing.T) {
	member := "{\n  \"title\" : \"Caf" + esc("00e9") + ` \"quoted\" \\ back\nslash" ,` + "\n\t" +
		`"html": "` + esc("003c") + "b" + esc("003e") + `<i>&amp;" ,` + "\n  " + `"list" : [ 1 , 2.50 , true , null ]` + "\n}"
	answer := SiteReaderPiece{SnapshotVersion: SiteReaderSnapshotVersion, Piece: map[string]json.RawMessage{"items": json.RawMessage(`[` + member + `]`)}}
	out, err := dispatchNext(t, answer)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"seq":0,"snapshot_version":4,"last":false,"piece":{"items":[{"title":"Caf` + esc("00e9") +
		` \"quoted\" \\ back\nslash","html":"` + esc("003c") + "b" + esc("003e") + `<i>&amp;","list":[1,2.50,true,null]}]}}`
	if string(out) != want {
		t.Errorf("the dispatch wrote\n %s\nwant\n %s", out, want)
	}
	if strings.ContainsAny(string(out), "\n\t") || !json.Valid(out) {
		t.Fatalf("the answer is not compact, valid JSON: %q", out)
	}
	marshalled, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	sameValues(t, out, marshalled)
	m, _ := siteReaderContract(t).Method(SiteReaderNext)
	if err := m.ValidateOutput(out); err != nil {
		t.Errorf("the contract refuses the answer as written: %v", err)
	}
}

// An answer that cannot be JSON is still refused, as before: a broken raw member is the provider's defect, never sent.
func TestABrokenRawMemberIsStillRefusedByTheDispatch(t *testing.T) {
	answer := SiteReaderPiece{SnapshotVersion: SiteReaderSnapshotVersion, Piece: map[string]json.RawMessage{"items": json.RawMessage(`[{"a":]`)}}
	if out, err := dispatchNext(t, answer); err == nil || !strings.Contains(err.Error(), "does not encode") {
		t.Fatalf("a broken raw member was not refused as an answer that does not encode: %s %v", out, err)
	}
}
