package nilda

import (
	"strings"
	"testing"
)

// SURFACE THIS SDK PUBLISHES BEFORE CORE SERVES IT. The truth tests hold the SDK to Core's working source (../core), so
// a contract change made SDK-first — the plan's order: the SDK step, then Core's — would fail them until Core lands it.
// Rather than weaken a guard, each guard skips exactly the items listed here, and the list says what Core must add and
// in which step. TestTheSurfaceAheadOfCoreIsStillAhead fails the moment Core has an item, so the entry is removed in
// the change that lands it: the list only shrinks, and it is empty once Core has caught up.
//
// Each item is the guard's own word for it ("hook payment.reprice", "route POST /payments/sessions/:id/reprice",
// "session client_action"), with the source Core will have when it has it.
var aheadOfCore = map[string]struct {
	step  string   // the plan's step that lands it in Core
	file  []string // where in Core it will be
	probe string   // what that file says once it is there
}{
	// Commerce plan 10.2 (express wallet buttons), X1 here; Core's X2b.
	"hook " + HookPaymentReprice: {"plan 10.2 X2b: send payment.reprice to a PaymentRepricer, admitted by payment_gateway, " +
		"15 s, errors are failures (nothing changed)", []string{"internal", "plugin", "payments.go"}, `"payment.reprice"`},
	"route POST /payments/sessions/:id/reprice": {"plan 10.2 X2b: the consumer's re-price — its own session, created or " +
		"redirected, not yet confirmed, no credential/kept method/reader, its method listing reprice; asked under the " +
		"session's lock; 409 on the processor's refusal, 503 when unanswered", []string{"internal", "payments", "rest.go"},
		`"/sessions/:id/reprice"`},
	"session client_action": {"plan 10.2 X2b: keep a start's client_action on the session while it is redirected (a " +
		"redirected start with only a client_action is valid), drop it after, never log it", []string{"internal", "payments", "types.go"},
		"json:\"client_action"},
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
