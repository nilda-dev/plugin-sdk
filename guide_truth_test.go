package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// M78 (2026-09-23 plugin hunt): the numbers and names the guides give an author, held to the Core source
// they describe — the time budgets, the batch cap, the translation and field-choice bounds, the storefront
// widgets, and which name is an event rather than a hook. Each was stale or missing, and each read true.
func TestTheGuidesNumbersAndNamesAreCores(t *testing.T) {
	core := func(parts ...string) string {
		raw, err := os.ReadFile(filepath.Join(append([]string{"..", "core"}, parts...)...))
		if os.IsNotExist(err) {
			t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	guide := flat(readText(t, "docs/PLUGIN_SDK.md"))
	plugins := flat(readText(t, "docs/PLUGINS.md"))
	has := func(src, file, want string) {
		t.Helper()
		if !strings.Contains(src, want) {
			t.Errorf("core's %s no longer has %q — re-read what the guides say about it", file, want)
		}
	}
	says := func(doc, name, want string) {
		t.Helper()
		if !strings.Contains(doc, want) {
			t.Errorf("%s does not say %q", name, want)
		}
	}

	// The budgets a hook runs inside.
	has(core("pkg", "config", "config.go"), "pkg/config/config.go", `parseDuration("PLUGIN_CALL_TIMEOUT", 5*time.Second)`)
	budget := core("internal", "plugin", "pagebudget", "pagebudget.go")
	has(budget, "internal/plugin/pagebudget/pagebudget.go", "const PublicPage = 1500 * time.Millisecond")
	has(budget, "internal/plugin/pagebudget/pagebudget.go", "const ExtensionPoint = 10 * time.Second")
	says(guide, "PLUGIN_SDK.md", "`PLUGIN_CALL_TIMEOUT` (5 seconds by default); every subscriber of one hook or event shares ten seconds")
	says(guide, "PLUGIN_SDK.md", "shares 1.5 seconds")
	says(plugins, "PLUGINS.md", "5 s a call (PLUGIN_CALL_TIMEOUT), 10 s for every subscriber of one hook or event together, 1.5 s for everything one public page asks of plugins")

	// The batch cap.
	has(core("pkg", "config", "config.go"), "pkg/config/config.go", `parseInt32("API_BATCH_MAX", 50)`)
	says(guide, "PLUGIN_SDK.md", "up to 50 a request (Core's API_BATCH_MAX)")

	// Translation bounds.
	manifest := core("internal", "plugin", "schema_manifest.go")
	for _, c := range []string{"maxTranslationLocales = 12", "maxTranslationStrings = 300", "maxTranslationLen     = 400", "maxTranslationBytes = 128 << 10"} {
		has(manifest, "internal/plugin/schema_manifest.go", c)
	}
	says(guide, "PLUGIN_SDK.md", "12 languages, 300 strings each, 400 characters a string — and 128 KiB for all of it together")

	// A field type's choices.
	fields := core("internal", "plugin", "fields.go")
	for _, c := range []string{"maxChoicesBytes       = 1 << 20", "maxFieldChoices       = 2000", "maxChoiceLen          = 200"} {
		has(fields, "internal/plugin/fields.go", c)
	}
	says(guide, "PLUGIN_SDK.md", "Nilda keeps the first 2,000 and cuts each value and label to 200 characters")
	says(guide, "PLUGIN_SDK.md", "an answer over 1 MiB is refused, not trimmed")

	// The storefront widgets: exactly the labels Core registers, each named once.
	widgets := core("internal", "pagebuilder", "commercewidgets.go")
	var labels []string
	for _, m := range regexp.MustCompile(`Type: "[a-z_]+", Label: "([^"]+)", Category: "(?:dynamic|navigation|commerce)"`).FindAllStringSubmatch(widgets, -1) {
		labels = append(labels, m[1])
	}
	for _, m := range regexp.MustCompile(`registerCommerceShell\("[a-z_]+", "([^"]+)"`).FindAllStringSubmatch(widgets, -1) {
		labels = append(labels, m[1])
	}
	if len(labels) != 8 {
		t.Fatalf("core registers %d storefront widgets (%v); the guide says eight — count them again", len(labels), labels)
	}
	list := regexp.MustCompile(`Nilda ships eight storefront widgets — ([^—]+) — and no commerce code at all`).FindStringSubmatch(guide)
	if list == nil {
		t.Fatal("PLUGIN_SDK.md no longer lists the storefront widgets in the sentence this guard reads")
	}
	named := map[string]int{}
	for _, n := range strings.Split(list[1], ",") {
		named[strings.TrimSpace(n)]++
	}
	for _, l := range labels {
		if named[l] != 1 {
			t.Errorf("the guide names Core's widget %q %d times, want once: %q", l, named[l], list[1])
		}
		delete(named, l)
	}
	for extra := range named {
		t.Errorf("the guide names a storefront widget Core does not register: %q", extra)
	}

	// content.published is an EVENT: the admin_page example receives it as one.
	has(core("internal", "events", "events.go"), "internal/events/events.go", `ContentPublished   Type = "content.published"`)
	if strings.Contains(core("internal", "plugin", "hookcatalog.go"), `"content.published"`) {
		t.Error("core's hook catalogue names content.published — the admin_page example's comment says it is not a hook")
	}
	says(guide, "PLUGIN_SDK.md", `return nilda.InitResult{Events: []string{"content.published"}}, nil`)
	if strings.Contains(guide, `Hooks: []string{"content.published"}`) {
		t.Error("PLUGIN_SDK.md subscribes to content.published as a hook, which Core never sends")
	}
}
