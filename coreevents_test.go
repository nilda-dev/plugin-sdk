package nilda

import (
	"encoding/json"
	"regexp"
	"sort"
	"testing"
)

// The constants are the names Core publishes, all of them: read from Core's KnownEvents when Core is beside the SDK.
func TestCoreEventNamesAreCoresOwn(t *testing.T) {
	src := coreSource(t, "internal", "events", "events.go")
	consts := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s+(\w+)\s+Type = "([a-z_.]+)"`).FindAllStringSubmatch(src, -1) {
		consts[m[1]] = m[2]
	}
	known := from(t, src, "func KnownEvents() []string {", "core internal/events/events.go")
	var core []string
	for _, m := range regexp.MustCompile(`string\((\w+)\)`).FindAllStringSubmatch(upTo(t, known, "\n}", "KnownEvents"), -1) {
		core = append(core, consts[m[1]])
	}
	ours := []string{EventContentCreated, EventContentUpdated, EventContentPublished, EventContentUnpublished,
		EventContentTrashed, EventContentRestored, EventContentDeleted, EventContentImported, EventFormSubmitted,
		EventFormSubscribe, EventUserEmailVerified, EventUserErased}
	sort.Strings(core)
	sort.Strings(ours)
	if len(core) == 0 || len(core) != len(ours) {
		t.Fatalf("Core publishes %v; the SDK names %v", core, ours)
	}
	for i := range core {
		if core[i] != ours[i] {
			t.Errorf("Core publishes %q where the SDK names %q", core[i], ours[i])
		}
	}
}

// The data decodes from the flat payload Core hands a plugin (core internal/events Payload, plus the event's own).
func TestCoreEventDataDecodesCoresPayload(t *testing.T) {
	raw := `{"entity_id":"c-1","entity_kind":"content","occurred_at":"2026-11-02T10:00:00Z","automation_hop":0,` +
		`"event_id":"e-1","type":"page","slug":"about"}`
	var d CoreEventData
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if d.EntityID != "c-1" || d.EntityKind != "content" || d.Type != "page" || d.Slug != "about" || d.OccurredAt.IsZero() {
		t.Errorf("decoded %+v", d)
	}
	payload := coreSource(t, "internal", "events", "events.go")
	for _, key := range []string{`"entity_id":`, `"entity_kind":`, `"occurred_at":`} {
		if !regexp.MustCompile(regexp.QuoteMeta(key)).MatchString(from(t, payload, "func Payload(e Envelope)", "core Payload")) {
			t.Errorf("Core's Payload no longer sets %s", key)
		}
	}
}
