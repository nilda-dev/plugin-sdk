package nilda

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// coreSource reads one of Core's files, from the checkout beside plugin-sdk (parts are relative to core/). Every
// guard that holds a sentence of the SDK to Core's source reads through it.
//
// It SKIPS only when Core itself is not there — no ../core/go.mod, the SDK cloned alone, as its own CI clones it.
// Once Core is there, a missing file FAILS: Core moved or renamed what the guard reads, which is exactly the
// change the guard exists to notice. Each guard used to skip on its own file missing, so a Core refactor that
// moved filterHooks out of renderassets.go printed `ok` over the guard that holds it, on a full checkout, with the
// reason "core is not checked out".
func coreSource(t *testing.T, parts ...string) string {
	t.Helper()
	return siblingSource(t, "core", parts...)
}

// siblingSource is coreSource for any repository checked out beside plugin-sdk (`commerce`, the site's shop):
// skipped when that repository is not there, failed when it is and the file has moved.
func siblingSource(t *testing.T, repo string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", repo}, parts...)...)
	raw, err := os.ReadFile(path)
	if err == nil {
		return string(raw)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("reading %s: %v", path, err)
	}
	if _, err := os.Stat(filepath.Join("..", repo, "go.mod")); err != nil {
		t.Skip(repo + " is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	t.Fatalf("%s is checked out beside plugin-sdk but has no %s — it moved or was renamed; repoint this guard", repo, path)
	return ""
}

// from is s from the first marker on, and upTo is s before the first marker. Each FAILS the guard, naming the
// marker and where it was looked for, when the marker is not there: sliced at strings.Index's -1, a guard whose
// source moved panicked with a slice bound instead of saying what to repoint.
func from(t *testing.T, s, marker, where string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("%s no longer has %q — repoint this guard", where, marker)
	}
	return s[i:]
}

func upTo(t *testing.T, s, marker, where string) string {
	t.Helper()
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("%s no longer has %q where this guard ends its read — repoint this guard", where, marker)
	}
	return s[:i]
}

// coreCheckedOut reports whether Core's module is beside plugin-sdk.
func coreCheckedOut() bool {
	_, err := os.Stat(filepath.Join("..", "core", "go.mod"))
	return err == nil
}
