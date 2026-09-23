package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// M64–M76 (2026-09-23 plugin hunt): what PLUGIN_SDK.md tells an author a manifest may declare — each number
// held to the constant core's validators use, and each "Core does X" to the line in core that does it — so a
// bound Core changes cannot leave a sentence here that an author, or their agent, builds against.
func TestTheManifestRulesTheDocsNameAreCores(t *testing.T) {
	read := func(parts ...string) string {
		raw, err := os.ReadFile(filepath.Join(append([]string{"..", "core", "internal"}, parts...)...))
		if os.IsNotExist(err) {
			t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	// num reads `name = N` or `name = N << S` from one of core's files.
	num := func(src, file, name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9]+)(?:\s*<<\s*([0-9]+))?\b`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("core's %s no longer declares %s as a number — repoint this guard", file, name)
		}
		n, _ := strconv.Atoi(m[1])
		if m[2] != "" {
			s, _ := strconv.Atoi(m[2])
			n <<= s
		}
		return n
	}
	widgets := read("plugin", "widgets.go")
	network := read("plugin", "schema_network.go")
	tables := read("plugin", "schema_tables.go")
	taxonomies := read("plugin", "schema_taxonomies.go")
	contentTypes := read("plugin", "schema_contenttypes.go")
	n := func(src, file, name string) string { return strconv.Itoa(num(src, file, name)) }

	doc := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " ")
	if b := num(widgets, "widgets.go", "maxWidgetDescribeBytes"); b != 1<<20 {
		t.Errorf("core bounds a describe answer at %d bytes; the docs say 1 MiB", b)
	}
	wants := []string{
		"at most " + n(widgets, "widgets.go", "maxWidgetFieldsTotal") + " fields — counted at every level",
		"at most " + n(widgets, "widgets.go", "maxWidgetChoices") + " choices and " + n(widgets, "widgets.go", "maxWidgetRows") + " rows in any one field",
		"external hosts the plugin may reach — at most " + n(network, "schema_network.go", "maxNetworkHosts"),
		n(tables, "schema_tables.go", "maxIndexesPerTable") + " indexes a table and " + n(tables, "schema_tables.go", "maxIndexColumns") + " columns an index",
		"at most " + n(contentTypes, "schema_contenttypes.go", "maxPluginContentTypes") + " content types of at most " +
			n(contentTypes, "schema_contenttypes.go", "maxPluginContentTypeFields") + " fields each, and " +
			n(taxonomies, "schema_taxonomies.go", "maxPluginTaxonomies") + " taxonomies, each attached to between 1 and " +
			n(taxonomies, "schema_taxonomies.go", "maxTaxonomyAppliesTo") + " content types",
	}
	for _, want := range wants {
		if !strings.Contains(doc, want) {
			t.Errorf("PLUGIN_SDK.md does not say %q, which is Core's bound", want)
		}
	}

	// Bounds written as a literal where Core checks them.
	for _, c := range []struct{ doc, core, file string }{
		{"at most 64 tables, 64 columns a table", "len(tables) > 64", "schema_tables.go"},
		{"a taxonomy key at most 60", "len(s) > 60", "schema_contenttypes.go"},
	} {
		src := map[string]string{"schema_tables.go": tables, "schema_contenttypes.go": contentTypes}[c.file]
		if !strings.Contains(src, c.core) || !strings.Contains(doc, c.doc) {
			t.Errorf("the docs' %q is not held by core's %s check %q", c.doc, c.file, c.core)
		}
	}
	if !strings.Contains(tables, "len(t.Columns) > 64") {
		t.Error("core's schema_tables.go no longer bounds a table's columns at 64 — re-read the docs' limit")
	}
	// A content type key is bounded by the content-type registry's own pattern, not the manifest's.
	m := regexp.MustCompile(`reTypeKey\s*=\s*regexp\.MustCompile\(` + "`" + `\^\[a-z\]\[a-z0-9_\]\{0,([0-9]+)\}\$` + "`").
		FindStringSubmatch(read("contenttype", "registry.go"))
	if m == nil {
		t.Fatal("core's contenttype reTypeKey is no longer `^[a-z][a-z0-9_]{0,N}$` — repoint this guard")
	}
	if max, _ := strconv.Atoi(m[1]); !strings.Contains(doc, "A content type key is at most "+strconv.Itoa(max+1)+" characters") {
		t.Errorf("the docs do not say a content type key is at most %d characters, which is the registry's bound", max+1)
	}
	// Every prefix the route paragraph names as Core's is one Core reserves.
	manifest := read("plugin", "schema_manifest.go")
	for _, p := range []string{"/api", "/admin", "/feed", "/themes", "/privacy"} {
		if !strings.Contains(manifest, `"`+p+`": true`) || !strings.Contains(doc, "`"+p+"`") {
			t.Errorf("the route paragraph names %s as Core's, and core's reservedRoutePrefixes does not reserve it", p)
		}
	}
	for _, name := range []string{"tableoid", "xmin", "cmin", "xmax", "cmax", "ctid"} {
		if !strings.Contains(tables, `"`+name+`": true`) || !strings.Contains(doc, "`"+name+"`") {
			t.Errorf("the system column %s is not both refused by core and named by the docs", name)
		}
	}

	// What the docs say Core DOES, held to the line that does it.
	for _, c := range []struct{ claim, file, line string }{
		{"Core refuses a manifest that declares `render.assets` without it", "plugin/schema_manifest.go", "seen[CapRenderAssets] && !hasRoute"},
		{"builds an index `CONCURRENTLY`", "plugin/tables.go", "INDEX CONCURRENTLY IF NOT EXISTS"},
		{"Core adds only the columns a table does not have yet", "plugin/tables.go", "c.PrimaryKey || have[c.Name]"},
		{"`1.09.0` is refused", "plugin/schema_semver.go", "len(p) > 1 && p[0] == '0'"},
		{"the owner is told which fields were ignored, by path at any depth", "plugin/handlers.go", `body["unknown_manifest_fields"]`},
		{"an update that marks a different column `primary_key` is refused before anything changes", "plugin/datastore.go", "update cannot change it"},
		{"would put two existing rows on one value", "plugin/datastore.go", "func refuseSharedValues("},
		{"Core refuses one it answers on itself", "plugin/schema_manifest.go", `" is reserved by Core"`},
	} {
		if !strings.Contains(doc, c.claim) {
			t.Errorf("PLUGIN_SDK.md no longer says %q", c.claim)
		}
		if !strings.Contains(read(strings.Split(c.file, "/")...), c.line) {
			t.Errorf("core's %s no longer has %q, which the docs' %q describes", c.file, c.line, c.claim)
		}
	}
}
