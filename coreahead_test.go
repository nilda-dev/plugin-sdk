package nilda

import (
	"strings"
	"testing"
)

// SURFACE THIS SDK PUBLISHES BEFORE CORE SERVES IT. The truth tests hold the SDK to Core's working source (../core), so
// a contract change made SDK-first — the plan's order: the SDK step, then Core's — would fail them until Core lands it.
// Rather than weaken a guard, each guard skips exactly the items listed here, and the list says what Core must add and
// in which step. TestTheSurfaceAheadOfCoreIsStillAhead fails the moment Core has an item, so the entry is removed in
// the change that lands it: an SDK-first step adds an entry, Core's step takes it out, and the list is empty once Core
// has caught up.
//
// Each item is the guard's own word for it ("hook payment.reprice", "route POST /payments/sessions/:id/reprice",
// "session client_action", "bridge shown_total"), with the source Core will have when it has it. The list may be empty:
// then every guard holds the whole surface, and this test checks nothing because nothing is ahead.
var aheadOfCore = map[string]struct {
	step  string   // the plan's step that lands it in Core
	file  []string // where in Core it will be
	probe string   // what that file says once it is there
}{
	// Core landed X2b (the reprice hook and route, the session's client_action) in core #178 and the bridge's
	// shown_total, shipping_option and press in the same PR (core ba8a428bf).

	// Core landed the session's risk (plan 10.3 F1) in core #181.

	// Commerce plan 10.15, "Stripe item lines": Core keeps a session's item lines and their breakdown as the consumer
	// sent them, checks them as PaymentSessionParams.Validate does, hands them to the gateway in payment.start's session,
	// and drops them when a re-price moves the amount.
	"session lines":     {step: "10.15 (Core)", file: []string{"internal", "payments", "types.go"}, probe: `json:"lines`},
	"session breakdown": {step: "10.15 (Core)", file: []string{"internal", "payments", "types.go"}, probe: `json:"breakdown`},
}

// coreLacks reports whether item is SDK surface Core does not serve yet (aheadOfCore).
func coreLacks(item string) bool {
	_, ok := aheadOfCore[item]
	return ok
}

func TestTheSurfaceAheadOfCoreIsStillAhead(t *testing.T) {
	for item, a := range aheadOfCore {
		if strings.Contains(coreSource(t, a.file...), a.probe) {
			t.Errorf("core now has %s (%s says %s): remove it from aheadOfCore so every guard holds it again", item,
				strings.Join(a.file, "/"), a.probe)
		}
	}
}
