package nildatest

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"
)

// The fake refuses the event names Core refuses, with Core's codes — an author whose plugin emits another
// plugin's event finds out in `go test`, not on somebody's site where the call is refused and the event lost.
func TestTheFakeRefusesTheEventNamesCoreRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		key     string
		granted []string
		event   string
		want    codes.Code
	}{
		{"its own key", "booking", []string{"events"}, "booking.confirmed", codes.OK},
		{"another plugin's key", "booking", []string{"events"}, "shop.order_paid", codes.PermissionDenied},
		{"the shop's orders, from the shop", "nilda_ecommerce", []string{"events", "commerce"}, "ecommerce.order_paid", codes.OK},
		{"the shop's catalogue, from the shop", "nilda_ecommerce", []string{"events", "commerce"}, nilda.EventCommerceCatalogChanged, codes.OK},
		{"the shop's orders, from a plugin that is not the shop", "booking", []string{"events"}, "ecommerce.order_paid", codes.PermissionDenied},
		{"a plugin KEYED commerce is still not the shop", "commerce", []string{"events"}, "commerce.catalog.changed", codes.PermissionDenied},
		{"no namespace", "booking", []string{"events"}, "confirmed", codes.InvalidArgument},
		{"no capability at all", "booking", nil, "booking.confirmed", codes.PermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, host := New(tc.key, tc.granted...)
			err := core.Emit(ctx, tc.event, nil)
			if got := status.Code(err); got != tc.want {
				t.Fatalf("plugin %q holding %v emitting %q: %v (%v), want %v", tc.key, tc.granted, tc.event, got, err, tc.want)
			}
			if recorded := len(host.Events()); (tc.want == codes.OK) != (recorded == 1) {
				t.Fatalf("recorded %d events for a call answered %v", recorded, tc.want)
			}
		})
	}
}

// TestTheFakesEventTableIsCores holds capabilityEventNamespaces equal to Core's, read from Core's source when
// Core is checked out beside this repository. A copy nothing compares is a fake that drifts, and a drifted
// fake passes a plugin's tests that real Core then refuses.
func TestTheFakesEventTableIsCores(t *testing.T) {
	dir := filepath.Join("..", "..", "core", "internal", "plugin")
	raw, err := os.ReadFile(filepath.Join(dir, "eventprovenance.go"))
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk, so its event table cannot be read from here — " +
			"run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	body := regexp.MustCompile(`(?s)var capabilityEventNamespaces = map\[string\]string\{(.*?)\n\}`).FindSubmatch(raw)
	if body == nil {
		t.Fatal("core's eventprovenance.go no longer declares capabilityEventNamespaces as a map literal — repoint this guard")
	}
	consts := coreStringConstants(t, dir)
	core := map[string]string{}
	for _, m := range regexp.MustCompile(`"([^"]+)":\s*(\w+|"[^"]*")`).FindAllStringSubmatch(string(body[1]), -1) {
		v := strings.Trim(m[2], `"`)
		if !strings.HasPrefix(m[2], `"`) {
			resolved, ok := consts[m[2]]
			if !ok {
				t.Fatalf("core's table names %s, a constant this guard cannot find in %s", m[2], dir)
			}
			v = resolved
		}
		core[m[1]] = v
	}
	if len(core) == 0 {
		t.Fatal("read no entries from core's capabilityEventNamespaces — repoint this guard")
	}
	for ns, capability := range core {
		if capabilityEventNamespaces[ns] != capability {
			t.Errorf("core binds %s.* to %q; this fake binds it to %q", ns, capability, capabilityEventNamespaces[ns])
		}
	}
	for ns := range capabilityEventNamespaces {
		if _, ok := core[ns]; !ok {
			t.Errorf("this fake binds %s.* to a capability; core does not", ns)
		}
	}
}

// The fake refuses a KV value Core refuses, with Core's code — and its cap is Core's, read from Core's source.
func TestTheFakesKVValueCapIsCores(t *testing.T) {
	core, _ := New("kvcap", "kv")
	if err := core.KVSet(context.Background(), "big", strings.Repeat("x", maxKVValueBytes+1), 0); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("a value past the cap answered %v, want InvalidArgument", err)
	}
	if err := core.KVSet(context.Background(), "ok", strings.Repeat("x", maxKVValueBytes), 0); err != nil {
		t.Fatalf("a value at the cap was refused: %v", err)
	}

	got, found := coreIntConstant(t, "kvquota.go", "maxKVValueBytes")
	if !found {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if got != maxKVValueBytes {
		t.Fatalf("core caps a KV value at %d bytes; this fake at %d", got, maxKVValueBytes)
	}
}

// coreIntConstant reads `name = <int>` or `name = <a> << <b>` from core's internal/plugin/<file>.
func coreIntConstant(t *testing.T, file, name string) (int, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "core", "internal", "plugin", file))
	if os.IsNotExist(err) {
		return 0, false
	}
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9_]+)(?:\s*<<\s*([0-9]+))?`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatalf("core's %s no longer declares %s as a number — repoint this guard", file, name)
	}
	n, err := strconv.Atoi(strings.ReplaceAll(m[1], "_", ""))
	if err != nil {
		t.Fatal(err)
	}
	if m[2] != "" {
		shift, _ := strconv.Atoi(m[2])
		n <<= shift
	}
	return n, true
}

// coreStringConstants is every `Name = "value"` string constant in core's non-test sources in dir.
func coreStringConstants(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s*=\s*"([^"]*)"`)
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range decl.FindAllStringSubmatch(string(raw), -1) {
			out[m[1]] = m[2]
		}
	}
	return out
}
