package siteimport

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The golden sites are Core's (internal/importer/testdata/golden, card K6a): for every fixture, the Site Core's reader produces
// in the snapshot encoding. This package must read each one and write it back to the same bytes — the proof that its types
// and codec are Core's. Copied with Core's MANIFEST.sha256; TestTheGoldensAreCoresGoldens fails if a file changed on this side.

const goldenDir = "testdata/golden"

func goldenFiles(t *testing.T) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(goldenDir, "*.site.json"))
	if err != nil || len(m) < 9 {
		t.Fatalf("golden sites: %v, found %d, want at least 9", err, len(m))
	}
	return m
}

func TestEveryGoldenSiteReadsAndWritesBackToTheSameBytes(t *testing.T) {
	for _, path := range goldenFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var site Site
			if err := Decode(bytes.NewReader(want), &site); err != nil {
				t.Fatalf("Decode: %v", err)
			}
			var raw bytes.Buffer
			if err := Encode(&raw, &site); err != nil {
				t.Fatalf("Encode: %v", err)
			}
			var pretty bytes.Buffer
			if err := json.Indent(&pretty, raw.Bytes(), "", "  "); err != nil {
				t.Fatalf("Encode wrote something that is not JSON: %v", err)
			}
			pretty.WriteByte('\n')
			if !bytes.Equal(pretty.Bytes(), want) {
				t.Fatalf("%s: written back as %d bytes, golden is %d — the model or the codec is not Core's", path, pretty.Len(), len(want))
			}
		})
	}
}

func TestTheGoldensAreCoresGoldens(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join(goldenDir, "MANIFEST.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		sum, name, ok := strings.Cut(line, "  ")
		if !ok {
			t.Fatalf("manifest line %q", line)
		}
		b, err := os.ReadFile(filepath.Join(goldenDir, name))
		if err != nil {
			t.Fatal(err)
		}
		got := sha256.Sum256(b)
		if hex.EncodeToString(got[:]) != sum {
			t.Errorf("%s differs from Core's golden (manifest %s): copy Core's file and manifest together, never edit one here", name, sum[:12])
		}
	}
}

func TestADecoderIgnoresWhatItDoesNotKnow(t *testing.T) {
	var site Site
	if err := Decode(strings.NewReader(`{"site_key":"a.example","from_the_future":{"x":[1,2,3]},"items":[{"source_id":"a#1"}]}`), &site); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if site.SiteKey != "a.example" || len(site.Items) != 1 || site.Items[0].SourceID != "a#1" {
		t.Fatalf("decoded %+v", site)
	}
}
