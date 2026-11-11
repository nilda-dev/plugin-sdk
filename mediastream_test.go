package nilda

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestAMediaFileIsNotCutByTheAPIClientsTimeout is the 2026-11-11 review's finding 8: the 30-second client's timeout
// covers the body, so a paid download read slowly stopped at 30 seconds. Here the API client's timeout is shortened to
// make the same thing visible in milliseconds.
func TestAMediaFileIsNotCutByTheAPIClientsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/media/m1" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"id":"m1","filename":"big.zip","size_bytes":6}}`)
			return
		}
		w.Header().Set("Content-Length", "6")
		for i := 0; i < 6; i++ {
			_, _ = w.Write([]byte("x"))
			w.(http.Flusher).Flush()
			time.Sleep(60 * time.Millisecond)
		}
	}))
	defer srv.Close()
	api := newAPI(srv.URL, "tok", nil)
	api.http.Timeout = 100 * time.Millisecond
	f, err := api.MediaFile(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || len(b) != 6 {
		t.Fatalf("read %d bytes, %v: the download was cut", len(b), err)
	}
	if api.stream.Timeout != 0 {
		t.Fatalf("the stream client has a total timeout of %s", api.stream.Timeout)
	}
}
