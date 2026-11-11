package siteimport

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// SNAPSHOT VERSION 5, ADDITION TWO: a media file's upload date (blog data ledger A20). WordPress keeps the day a file was uploaded
// (WXR post_date_gmt of the attachment, REST date_gmt); Nilda's library row has a creation time the importer may now set for a file
// it adds. The field is optional, so a file without a date writes exactly the bytes version 4 wrote.

func TestAMediaFileCarriesItsUploadDateThroughTheCodec(t *testing.T) {
	when := time.Date(2019, 3, 4, 5, 6, 7, 0, time.UTC)
	site := Site{Media: []MediaItem{{SourceID: "old.example#media:5", URL: "https://old.example/a.png", UploadedAt: &when}}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	if want := `"uploaded_at":"2019-03-04T05:06:07Z"`; !strings.Contains(b.String(), want) {
		t.Fatalf("the encoding lacks %s: %s", want, b.String())
	}
	var got Site
	if err := Decode(&b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Media) != 1 || got.Media[0].UploadedAt == nil || !got.Media[0].UploadedAt.Equal(when) {
		t.Fatalf("decoded %+v", got.Media)
	}
}

func TestAMediaFileWithoutADateWritesTheBytesVersionFourWrote(t *testing.T) {
	site := Site{Media: []MediaItem{{SourceID: "old.example#media:5", URL: "https://old.example/a.png"}}}
	var b bytes.Buffer
	if err := Encode(&b, &site); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "uploaded_at") {
		t.Fatalf("a file without a date wrote the new member: %s", b.String())
	}
	var got Site
	if err := Decode(strings.NewReader(`{"media":[{"source_id":"old.example#media:5","url":"https://old.example/a.png"}]}`), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Media) != 1 || got.Media[0].UploadedAt != nil {
		t.Fatalf("a version 4 file decoded as %+v", got.Media)
	}
}
