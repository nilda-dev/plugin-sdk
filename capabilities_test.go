package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The SDK names exactly the capabilities Core registers (core internal/plugin schema_capabilities.go's init list, each
// key's value read from the const that declares it, wherever in internal/plugin that is).
func TestTheCapabilitiesAreCoresOwn(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "schema_capabilities.go")
	list := upTo(t, from(t, src, "func init() {", "core schema_capabilities.go"), "\n}", "its init")
	names := map[string]bool{}
	for _, m := range regexp.MustCompile(`\{(Cap[A-Za-z]+),`).FindAllStringSubmatch(list, -1) {
		names[m[1]] = true
	}
	files, _ := filepath.Glob(filepath.Join("..", "core", "internal", "plugin", "*.go"))
	values := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range regexp.MustCompile(`\b(Cap[A-Za-z]+)\s*=\s*"([a-z_.]+)"`).FindAllStringSubmatch(string(raw), -1) {
			values[m[1]] = m[2]
		}
	}
	var core []string
	for n := range names {
		if values[n] == "" {
			t.Fatalf("Core registers %s, whose value this guard cannot find", n)
		}
		core = append(core, values[n])
	}
	ours := []string{CapAbilities, CapAdminPage, CapAuthProvider, CapCommerce, CapContentRead, CapContentWrite,
		CapDatastore, CapEmail, CapEvents, CapField, CapHooks, CapKV, CapMediaRead, CapMediaWrite, CapMenusRead,
		CapMenusWrite, CapPaymentGateway, CapPaymentSession, CapPersonalData, CapRenderAssets, CapRoute, CapSchedule,
		CapSearchProvider, CapSecrets, CapServiceConsumer, CapServiceProvider, CapSiteFiles, CapTaxonomyRead,
		CapTaxonomyWrite, CapTrash, CapUsersRead, CapVisitorTools, CapWidget}
	sort.Strings(core)
	sort.Strings(ours)
	if strings.Join(core, " ") != strings.Join(ours, " ") {
		t.Errorf("Core registers\n  %v\nthe SDK names\n  %v", core, ours)
	}
}
