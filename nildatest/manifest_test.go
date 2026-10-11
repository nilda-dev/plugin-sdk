package nildatest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// fakeT records a failure instead of stopping the real test.
type fakeT struct {
	testing.TB
	failed string
}

func (f *fakeT) Helper()                   {}
func (f *fakeT) Logf(string, ...any)       {}
func (f *fakeT) Fatalf(s string, a ...any) { f.failed = fmt.Sprintf(s, a...); panic(f) }

func run(tb testing.TB, path string, list []nilda.Ability) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	ManifestAbilities(f, path, list)
	return ""
}

var shopAbilities = []nilda.Ability{
	{Name: "create_product", Label: "Create a product", Description: "Add a product.", Class: nilda.ClassWrite},
	{Name: "list_orders", Label: "List orders", Description: "List the orders.", Class: nilda.ClassRead},
}

// WP15: the manifest's abilities are written by the SDK, not typed — and a manifest that disagrees with Init fails.
func TestManifestAbilitiesWritesThenHolds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plugin.json")
	orig := "{\n  \"key\": \"shop\",\n  \"capabilities\": [\"abilities\"]\n}\n"
	if err := os.WriteFile(path, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if failed := run(t, path, shopAbilities); !strings.Contains(failed, WriteManifestEnv) {
		t.Fatalf("a manifest with no abilities passed: %q", failed)
	}
	t.Setenv(WriteManifestEnv, "1")
	if failed := run(t, path, shopAbilities); failed != "" {
		t.Fatalf("writing failed: %s", failed)
	}
	got, _ := os.ReadFile(path)
	if !strings.HasPrefix(string(got), "{\n  \"key\": \"shop\",\n  \"capabilities\": [\"abilities\"],\n  \"abilities\": [") {
		t.Fatalf("the rest of the file was not kept as it was:\n%s", got)
	}
	t.Setenv(WriteManifestEnv, "")
	if failed := run(t, path, shopAbilities); failed != "" {
		t.Fatalf("the written manifest does not hold: %s", failed)
	}
	// A changed description fails until the manifest is written again, and the rewrite replaces in place.
	changed := append([]nilda.Ability{}, shopAbilities...)
	changed[1].Description = "List the orders, newest first."
	if failed := run(t, path, changed); !strings.Contains(failed, "list_orders differs") {
		t.Fatalf("a changed description passed: %q", failed)
	}
	t.Setenv(WriteManifestEnv, "1")
	_ = run(t, path, changed[:1])
	got, _ = os.ReadFile(path)
	if strings.Contains(string(got), "list_orders") || strings.Count(string(got), "\"abilities\"") != 2 {
		t.Fatalf("the rewrite did not replace the old list in place:\n%s", got)
	}
}
