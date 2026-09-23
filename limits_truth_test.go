package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// M48, M51 (2026-09-23 plugin hunt): how often a plugin may call into Core, as the docs tell an author — each
// number held to the constant in core's internal/plugin/calllimits.go, and "Emit returns before the
// subscribers have run" held to Core's hostservice.go, so a changed bound cannot leave a stale sentence.
func TestTheCallLimitsTheDocsNameAreCores(t *testing.T) {
	read := func(parts ...string) string {
		raw, err := os.ReadFile(filepath.Join(append([]string{"..", "core", "internal", "plugin"}, parts...)...))
		if os.IsNotExist(err) {
			t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	limits := read("calllimits.go")
	num := func(name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9]+)\b`).FindStringSubmatch(limits)
		if m == nil {
			t.Fatalf("core's calllimits.go no longer declares %s as a number — repoint this guard", name)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	flat := func(s string) string { return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n//", " ")), " ") }
	anyLang := flat(readText(t, "docs/ANY_LANGUAGE.md"))
	core := flat(readText(t, "core.go"))
	for doc, wants := range map[string][]string{
		anyLang: {
			"at most " + strconv.Itoa(num("emailsPerMinute")) + " emails a minute",
			strconv.Itoa(num("eventsPerSecond")) + " events a second (bursts of " + strconv.Itoa(num("eventBurst")) + ")",
			strconv.Itoa(num("kvOpsPerSecond")) + " key-value calls a second (bursts of " + addThousands(num("kvBurst")) + ")",
			strconv.Itoa(num("logLinesPerSecond")) + " lines of your log a second (bursts of " + strconv.Itoa(num("logBurst")),
		},
		core: {
			"At most " + strconv.Itoa(num("emailsPerMinute")) + " a minute per plugin",
			"At most " + strconv.Itoa(num("eventsPerSecond")) + " a second per plugin (bursts of " + strconv.Itoa(num("eventBurst")) + ")",
			"At most " + strconv.Itoa(num("kvOpsPerSecond")) + " calls a second per plugin (bursts of " + addThousands(num("kvBurst")) + ")",
		},
	} {
		for _, want := range wants {
			if !strings.Contains(doc, want) {
				t.Errorf("the docs do not say %q, which is Core's bound", want)
			}
		}
	}
	if !strings.Contains(read("hostservice.go"), "go (*dispatcher).EmitFromPlugin(context.WithoutCancel(ctx)") {
		t.Error("core no longer delivers an emitted event detached from the emitter's call — re-read Emit's doc")
	}
}

// M47 (2026-09-23 plugin hunt): `nilda plugin trigger` delivers only what the plugin receives, as PLUGIN_SDK.md
// says — held to core's host.go, where Call (a hook) and Dispatch (an event) both refuse a delivery the plugin
// never subscribed to. Dispatch did not, so `trigger --event` reached a plugin with no `events` grant at all.
func TestTriggerDeliversOnlyWhatThePluginReceives(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "core", "internal", "plugin", "host.go"))
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`is %w to hook %q", key, ErrNotSubscribed, hook)`,
		`is %w to event %q", key, ErrNotSubscribed, eventType)`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("core's host.go no longer refuses an unsubscribed delivery with %q — re-read the trigger paragraph", want)
		}
	}
	if doc := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " "); !strings.Contains(doc,
		"It delivers only what your plugin receives") {
		t.Error("PLUGIN_SDK.md no longer says trigger delivers only what the plugin receives")
	}
}
