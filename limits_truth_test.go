package nilda

import (
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
		return coreSource(t, append([]string{"internal", "plugin"}, parts...)...)
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
	// The email bucket is a burst of emailsPerMinute refilling at one every minute/emailsPerMinute: a flat
	// "N a minute" undersells the burst, so the docs name both halves — and only while the bucket is built so.
	if !strings.Contains(limits, "rate.NewLimiter(rate.Every(time.Minute/emailsPerMinute), emailsPerMinute)") {
		t.Error("core's email limiter is no longer a burst of emailsPerMinute refilling evenly over the minute — re-read the docs' email bound")
	}
	emails := strconv.Itoa(num("emailsPerMinute"))
	refill := "one a second"
	if n := num("emailsPerMinute"); n != 60 {
		// Only a whole number of seconds a message is a sentence this guard can write; any other bound would have
		// it demand "one every 0 seconds" of the docs.
		if n > 60 || 60%n != 0 {
			t.Fatalf("core's email bound is %d a minute, which is not a whole number of seconds a message — teach "+
				"this guard the sentence, then the docs", n)
		}
		refill = "one every " + strconv.Itoa(60/n) + " seconds"
	}
	for doc, wants := range map[string][]string{
		anyLang: {
			emails + " emails at once, then " + refill + " as the allowance refills (" + emails + " a minute sustained)",
			strconv.Itoa(num("eventsPerSecond")) + " events a second (bursts of " + strconv.Itoa(num("eventBurst")) + ")",
			strconv.Itoa(num("kvOpsPerSecond")) + " key-value calls a second (bursts of " + addThousands(num("kvBurst")) + ")",
			strconv.Itoa(num("logLinesPerSecond")) + " lines of your log a second (bursts of " + strconv.Itoa(num("logBurst")),
		},
		core: {
			emails + " may leave at once, and the allowance refills at " + refill + " — " + emails + " a minute sustained",
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
	// "Slow down" and "full" told apart (the 2026-09-24 review's L-F13): a rate refusal carries a RetryInfo, a
	// full kv namespace answers the same code with none.
	if !strings.Contains(limits, "st.WithDetails(&errdetails.RetryInfo{") {
		t.Error("core's overLimit no longer attaches a RetryInfo — re-read the docs' ResourceExhausted sentences")
	}
	// …and each of the three bounds refuses THROUGH it, so every one of core.go's three sentences is true.
	hostservice := read("hostservice.go")
	for _, call := range []string{
		`overLimit(h.info.Key, "using its key-value store", h.limits.kv)`,
		`overLimit(h.info.Key, "sending email", h.limits.email)`,
		`overLimit(h.info.Key, "emitting events", h.limits.emit)`,
	} {
		if !strings.Contains(hostservice, "return nil, "+call) {
			t.Errorf("core's hostservice.go no longer refuses with %s — re-read core.go's RetryInfo sentence for it", call)
		}
	}
	if !strings.Contains(hostservice, "case errors.Is(err, ErrKVFull):\n\t\treturn status.Error(codes.ResourceExhausted, err.Error())") {
		t.Error("core's full-namespace refusal changed shape — re-read the docs' \"no RetryInfo\" sentence")
	}
	// Once for each of SendEmail, the kv block and Emit.
	if n := strings.Count(core, "answers codes.ResourceExhausted at once, with a google.rpc.RetryInfo detail saying when there is room again"); n != 3 {
		t.Errorf("core.go says a rate refusal carries a RetryInfo %d times, want 3 — SendEmail, kv and Emit", n)
	}
	if !strings.Contains(core, "ResourceExhausted too, with no RetryInfo: waiting alone makes room there only as your keys with a TTL expire") {
		t.Error("core.go no longer says a full kv namespace answers with no RetryInfo, which core's hostservice.go makes true")
	}
	guideRow := flat(readText(t, "docs/PLUGIN_SDK.md"))
	for doc, want := range map[string]string{
		"PLUGIN_SDK.md's kv row": "with no `google.rpc.RetryInfo` (a call past the kv rate limit carries one; waiting does not empty a full namespace)",
		"ANY_LANGUAGE.md (rate)": "`RESOURCE_EXHAUSTED` at once rather than queueing, with a `google.rpc.RetryInfo` detail",
		"ANY_LANGUAGE.md (full)": "answers `RESOURCE_EXHAUSTED` too, with NO `RetryInfo`: retrying does not make room there",
	} {
		text := anyLang
		if strings.HasPrefix(doc, "PLUGIN_SDK.md") {
			text = guideRow
		}
		if !strings.Contains(text, want) {
			t.Errorf("%s no longer says %q, which core's calllimits.go and hostservice.go make true", doc, want)
		}
	}
	// Emit's two promises about delivery, each held both ways: Core runs the fan-out on a goroutine of its
	// own, detached from the emitter's call — which is what makes "returns before the subscribers have run"
	// true, and also what makes two emits' order unreliable — and core.go and the guide say both.
	if !strings.Contains(read("hostservice.go"), "go (*dispatcher).EmitFromPlugin(context.WithoutCancel(ctx)") {
		t.Error("core no longer delivers an emitted event detached from the emitter's call — re-read Emit's doc")
	}
	guide := flat(readText(t, "docs/PLUGIN_SDK.md"))
	for doc, wants := range map[string][]string{
		"core.go (Emit)": {"Emit returns before the subscribers have run", "events are not ordered: Core delivers each emit on its own goroutine"},
		"PLUGIN_SDK.md":  {"`Emit` returns before any subscriber has run, and Core delivers each emit on its own goroutine"},
	} {
		text := core
		if doc == "PLUGIN_SDK.md" {
			text = guide
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s no longer says %q, which core's hostservice.go makes true", doc, want)
			}
		}
	}
}

// M47 (2026-09-23 plugin hunt): `nilda plugin trigger` delivers only what the plugin receives, as PLUGIN_SDK.md
// says — held to core's host.go, where Call (a hook) and Dispatch (an event) both refuse a delivery the plugin
// never subscribed to. Dispatch did not, so `trigger --event` reached a plugin with no `events` grant at all.
func TestTriggerDeliversOnlyWhatThePluginReceives(t *testing.T) {
	raw := coreSource(t, "internal", "plugin", "host.go")
	for _, want := range []string{
		`is %w to hook %q", key, ErrNotSubscribed, hook)`,
		`is %w to event %q", key, ErrNotSubscribed, eventType)`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("core's host.go no longer refuses an unsubscribed delivery with %q — re-read the trigger paragraph", want)
		}
	}
	if doc := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " "); !strings.Contains(doc,
		"It delivers only what your plugin receives") {
		t.Error("PLUGIN_SDK.md no longer says trigger delivers only what the plugin receives")
	}
}
