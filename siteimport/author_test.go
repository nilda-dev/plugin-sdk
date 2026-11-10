package siteimport

import (
	"bytes"
	"strings"
	"testing"
)

// SNAPSHOT VERSION 5, ADDITION ONE: an author's bio and profile link (blog data ledger A12). The live WordPress REST user holds a
// description and a website; the file export holds neither, so both are optional and an author without them writes exactly the
// bytes version 4 wrote.

func TestAnAuthorCarriesABioAndALinkThroughTheCodec(t *testing.T) {
	site := Site{SiteKey: "old.example", Authors: map[string]Author{
		"7": {Login: "ada", Name: "Ada", SourceID: "old.example#author:7", Bio: "Writes about engines.", Link: "https://ada.example/about"},
	}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"bio":"Writes about engines."`, `"link":"https://ada.example/about"`} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("the encoding lacks %s: %s", want, b.String())
		}
	}
	var got Site
	if err := Decode(&b, &got); err != nil {
		t.Fatal(err)
	}
	a := got.Authors["7"]
	if a.Bio != "Writes about engines." || a.Link != "https://ada.example/about" || a.Name != "Ada" {
		t.Fatalf("decoded %+v", a)
	}
}

// An author with neither field writes no bio and no link: a version 4 snapshot and a version 5 snapshot of the same author are the
// same bytes, so every golden that was written before this field still reproduces.
func TestAnAuthorWithoutABioWritesTheBytesVersionFourWrote(t *testing.T) {
	site := Site{Authors: map[string]Author{"7": {Login: "ada", Name: "Ada", SourceID: "old.example#author:7"}}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), `"bio"`) || strings.Contains(b.String(), `"link"`) {
		t.Fatalf("an author without a bio wrote the new members: %s", b.String())
	}
	var got Site
	if err := Decode(strings.NewReader(`{"authors":{"7":{"login":"ada","name":"Ada","source_id":"old.example#author:7"}}}`), &got); err != nil {
		t.Fatal(err)
	}
	if a := got.Authors["7"]; a.Bio != "" || a.Link != "" || a.Name != "Ada" {
		t.Fatalf("a version 4 author decoded as %+v", a)
	}
}
