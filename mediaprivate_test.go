package nilda

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A SOLD FILE IS PRIVATE FROM THE INSERT (Core's POST /media `private=true`, internal/api/rest uploadMedia; D10 §1.2: an import
// plugin copies a product's downloads before the call). Uploading it public and making it private afterwards leaves a moment in
// which a paid file has a public address, so the upload carries `private` itself. Read the way Core reads it (FormFile,
// PostForm), the request is the file, its name and private=true — no alt — and a retried attempt sends the same bytes.
func TestAPluginCanUploadAFilePrivateFromTheInsert(t *testing.T) {
	attempts := 0
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		r.Body = io.NopCloser(strings.NewReader(string(raw)))
		file, fh, err := r.FormFile("file")
		if err != nil {
			http.Error(w, `{"error":{"code":"VALIDATION","message":"a multipart file field is required"}}`, http.StatusUnprocessableEntity)
			return
		}
		body, _ := io.ReadAll(file)
		if r.URL.Path != "/media" || fh.Filename != "pattern.pdf" || string(body) != "%PDF-1.7" || r.PostFormValue("private") != "true" ||
			r.PostFormValue("alt") != "" {
			t.Errorf("the upload carried %s %q %q private=%q alt=%q", r.URL.Path, fh.Filename, body, r.PostFormValue("private"),
				r.PostFormValue("alt"))
		}
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{"id":"m9","private":true}}`))
	}))
	defer srv.Close()
	api := newAPI(srv.URL, "tok", []string{"write:media"}).WithIdempotencyKey("download-9")
	var out struct {
		Data MediaItem `json:"data"`
	}
	if err := api.UploadPrivateMedia(context.Background(), "pattern.pdf", strings.NewReader("%PDF-1.7"), &out); err != nil {
		t.Fatal(err)
	}
	if out.Data.ID != "m9" || !out.Data.Private || attempts != 2 || bodies[0] != bodies[1] {
		t.Fatalf("answered %+v after %d attempts (same bytes: %v)", out.Data, attempts, bodies[0] == bodies[1])
	}
	// The same key and file uploaded public is another request: private is part of what the boundary is made from.
	var public string
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		public = string(raw)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"data":{}}`))
	}))
	defer pub.Close()
	if err := newAPI(pub.URL, "tok", []string{"write:media"}).WithIdempotencyKey("download-9").
		UploadMedia(context.Background(), "pattern.pdf", strings.NewReader("%PDF-1.7"), "", nil); err != nil {
		t.Fatal(err)
	}
	if public == bodies[0] {
		t.Error("a public and a private upload of one key sent the same request")
	}
}

func TestAPrivateUploadWithoutAnAPIIsRefused(t *testing.T) {
	var api *API
	if err := api.UploadPrivateMedia(context.Background(), "a.pdf", strings.NewReader("x"), nil); err == nil {
		t.Error("a nil API uploaded")
	}
}
