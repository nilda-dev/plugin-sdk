package nildatest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreConformance is Core's conformance corpus, when core is checked out beside the SDK: the first-party manifests
// and Core's own drawing of each (core internal/plugin/testdata/conformance).
const coreConformance = "../../core/internal/plugin/testdata/conformance"

// The drawing here and Core's (`nilda plugin check --mobile`) are two programs; this keeps them one picture. Every
// first-party manifest Core draws, this draws the same, to the byte.
func TestMobilePreviewDrawsWhatCoreDraws(t *testing.T) {
	manifests, err := filepath.Glob(filepath.Join(coreConformance, "real", "*.json"))
	if err != nil || len(manifests) == 0 {
		t.Skip("core is not checked out beside the SDK; its conformance corpus is what this is held to")
	}
	for _, path := range manifests {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(coreConformance, "phone", name+".txt"))
			if err != nil {
				t.Fatalf("Core has no drawing of %s: %v", name, err)
			}
			got, err := renderPhone(raw)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("the SDK draws %s differently from Core.\nCore:\n%s\nSDK:\n%s", name, want, got)
			}
		})
	}
}

func TestMobilePreviewWritesThenHoldsTheGolden(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "plugin.json")
	golden := filepath.Join(dir, "testdata", "phone.txt")
	write := func(s string) {
		if err := os.WriteFile(manifest, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"key":"shop","name":"Shop","capabilities":["admin_page"],"mobile":{"icon":"shop"},
		"admin_pages":[{"key":"settings","label":"Settings","kind":"settings",
		"fields":[{"key":"mode","label":"Mode","type":"select","choices":["Live","Test"]}]}]}`)

	t.Setenv(WritePreviewEnv, "1")
	MobilePreview(t, manifest, golden)
	raw, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("the golden was not written: %v", err)
	}
	for _, want := range []string{"| [shop] Shop", "> Settings", "[full]", "( Live | Test )", "[ Save ]"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the drawing has no %q:\n%s", want, raw)
		}
	}

	t.Setenv(WritePreviewEnv, "")
	MobilePreview(t, manifest, golden) // unchanged: passes

	write(strings.Replace(string(mustRead(t, manifest)), `"Test"`, `"Staging"`, 1))
	rec := &recorder{TB: t}
	MobilePreview(rec, manifest, golden)
	if !rec.failed {
		t.Error("a changed choice did not change the drawing's verdict")
	}
}

func TestMobilePreviewDrawsOnlyPlainText(t *testing.T) {
	got, err := renderPhone([]byte(`{"key":"x","name":"\u202eShop\u001b[31m","description":"one\u200bword"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range got {
		if r == '\u202e' || r == 0x1b || r == '\u200b' {
			t.Fatalf("%U reached the drawing:\n%s", r, got)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// recorder notes a failure instead of failing the test that holds it.
type recorder struct {
	testing.TB
	failed bool
}

func (r *recorder) Errorf(string, ...any) { r.failed = true }
func (r *recorder) Fatalf(string, ...any) { r.failed = true }
func (r *recorder) Helper()               {}
