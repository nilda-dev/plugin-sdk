package siteimport

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// SNAPSHOT VERSION 5, ADDITION THREE: an item's modified date (blog data ledger A13). WordPress keeps when a post was last edited
// (WXR post_modified_gmt, REST modified_gmt). The field is optional, so an item without a date writes exactly the bytes it wrote before.

func TestAnItemCarriesItsModifiedDateThroughTheCodec(t *testing.T) {
	when := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	site := Site{Items: []Item{{SourceID: "old.example#1", Type: "post", Title: "T", SourceStatus: "published", ModifiedAt: &when}}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	if want := `"modified_at":"2021-06-07T08:09:10Z"`; !strings.Contains(b.String(), want) {
		t.Fatalf("the encoding lacks %s: %s", want, b.String())
	}
	var got Site
	if err := Decode(&b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ModifiedAt == nil || !got.Items[0].ModifiedAt.Equal(when) {
		t.Fatalf("decoded %+v", got.Items)
	}
}

func TestAnItemWithoutAModifiedDateWritesTheBytesItWroteBefore(t *testing.T) {
	site := Site{Items: []Item{{SourceID: "old.example#1", Type: "post", Title: "T", SourceStatus: "published"}}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "modified_at") {
		t.Fatalf("an item without a date wrote the new member: %s", b.String())
	}
	var got Site
	if err := Decode(strings.NewReader(`{"items":[{"source_id":"old.example#1","type":"post","title":"T","source_status":"published"}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ModifiedAt != nil {
		t.Fatalf("an older snapshot decoded as %+v", got.Items)
	}
}
