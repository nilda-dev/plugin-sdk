package nilda

import (
	"net/http"
	"testing"
)

// TestProtocolForSDKDecidesCompatibility covers what `nilda plugin check` uses to tell an author their SDK
// speaks a different protocol than the Nilda they are targeting — the answer that stops a mismatch from
// first appearing as a handshake failure on somebody else's server.
//
// It had no test at all, which for version parsing is where the bugs live.
func TestProtocolForSDKDecidesCompatibility(t *testing.T) {
	for _, c := range []struct {
		in    string
		proto int
		known bool
	}{
		{"v0.1.0", 1, true}, // the retracted contract: five read-only methods
		{"0.1.1", 1, true},  // no leading v
		{"v0.2.0", 2, true}, // scoped API token, real writes
		{"v0.6.0", 2, true}, // what the scaffold pins today
		{"v0.6.0-rc1", 2, true},
		{"v0.6.0+build7", 2, true},
		{"  v0.6.0  ", 2, true},
		// v1 SPEAKS PROTOCOL 2. This row said (0, false) with the note "does not exist yet", which was the
		// right answer for an unbuilt future version and the wrong one for the very next tag: v1.0.0 would
		// have made `nilda plugin check` answer "sdk_version v1.0.0 is not a version this Nilda knows
		// about" to an author holding the SDK our own scaffold pinned for them. Changed deliberately, not
		// to make code pass — tagging v1.0.0 is a promise about the GO API, and the wire contract moves
		// when ProtocolVersion moves, which is a different number on purpose.
		{"v1.0.0", 2, true},
		{"v1.4.2", 2, true},
		{"v2.0.0", 0, false}, // still nobody's build; a check that passes for an untried pairing is worse
		{"", 0, false},
		{"v1", 0, false}, // one number is not a version
		{"vX.Y", 0, false},
		{"-1.2", 0, false}, // the suffix strip must not turn a negative into a version
	} {
		proto, known := ProtocolForSDK(c.in)
		if proto != c.proto || known != c.known {
			t.Errorf("ProtocolForSDK(%q) = (%d, %v), want (%d, %v)", c.in, proto, known, c.proto, c.known)
		}
	}
	// "The version this SDK IS must be one it claims to speak" used to be asserted here against the
	// literal "v0.6.0" — a hand-written copy of the version, which is why it went on passing while the
	// answer for the NEXT one was wrong. A module cannot read its own tag, so the check belongs where
	// both numbers are real: TestTheScaffoldedSDKVersionSpeaksThisProtocol in Core reads the version the
	// scaffold pins and asks this function about it.
}

// TestTheUpgradeHelpersDecideWhetherADataStepRuns. Getting these wrong runs somebody's one-time data
// migration twice, or never — and they had no test.
func TestTheUpgradeHelpersDecideWhetherADataStepRuns(t *testing.T) {
	fresh := &Core{}                            // first install
	upgraded := &Core{PreviousVersion: "1.0.0"} // restarted after running 1.0.0

	if !fresh.IsFirstRun() {
		t.Error("a fresh install must be a first run — this is what gates seed data")
	}
	if upgraded.IsFirstRun() {
		t.Error("an upgrade must NOT be a first run, or the seed overwrites what the owner changed")
	}
	if fresh.UpgradedFrom("1.0.0") {
		t.Error("a first run is not an upgrade from anything")
	}
	if !upgraded.UpgradedFrom("0.9.0", "1.0.0") {
		t.Error("UpgradedFrom must match any of the named versions")
	}
	if upgraded.UpgradedFrom("1.0.1") {
		t.Error("UpgradedFrom is exact-match on purpose — a range would include shapes nobody shipped")
	}
	if !upgraded.IsUpgrade("1.1.0") {
		t.Error("a different current version is an upgrade")
	}
	if upgraded.IsUpgrade("1.0.0") {
		t.Error("the SAME version is a restart, not an upgrade — this is what makes a crash-restart safe")
	}
	if fresh.IsUpgrade("1.0.0") {
		t.Error("a first run is not an upgrade")
	}
	// Every one of them is nil-safe, because a plugin holding a nil Core is a plugin mid-failure and a
	// panic there replaces a diagnosable state with a stack trace.
	var nc *Core
	if nc.IsFirstRun() || nc.UpgradedFrom("1.0.0") || nc.IsUpgrade("1.0.0") {
		t.Error("a nil Core must answer false, not panic")
	}
}

// TestTheSettingsAccessorsReadWhatCoreStores. Core stores a `number` field as a float and a `boolean` as a
// bool (internal/plugin/adminpages.go settingType), so these are the shapes that actually arrive.
func TestTheSettingsAccessorsReadWhatCoreStores(t *testing.T) {
	c := &Core{}
	SetSettingsForTest(c, map[string]any{
		"api_key": "sk_live_1", "empty": "", "retries": float64(5), "ratio": 0.5,
		"enabled": true, "disabled": false, "on_as_text": "true", "one": "1",
	})

	if c.Setting("api_key") != "sk_live_1" {
		t.Errorf("Setting = %q", c.Setting("api_key"))
	}
	if c.Setting("missing") != "" || c.Setting("retries") != "" {
		t.Error("a missing key, and one of another type, must read as empty rather than panicking")
	}
	if c.SettingNumber("retries") != 5 || c.SettingNumber("ratio") != 0.5 {
		t.Errorf("SettingNumber: %v / %v", c.SettingNumber("retries"), c.SettingNumber("ratio"))
	}
	if c.SettingNumber("api_key") != 0 || c.SettingNumber("missing") != 0 {
		t.Error("a non-number reads as 0")
	}
	if !c.SettingBool("enabled") || c.SettingBool("disabled") {
		t.Error("SettingBool on real bools")
	}
	if !c.SettingBool("on_as_text") || !c.SettingBool("one") {
		t.Error("SettingBool also accepts the string forms a hand-written manifest default can carry")
	}

	// HasSetting is the "is this configured yet" question, so an EMPTY string must be false — otherwise a
	// plugin logs "calling the gateway" every minute with no key.
	if !c.HasSetting("api_key") || c.HasSetting("empty") || c.HasSetting("missing") {
		t.Error("HasSetting: a blank value is not configured")
	}
	if !c.HasSetting("disabled") {
		t.Error("HasSetting on a false BOOL is true — it holds a value, and the value is false")
	}

	var nc *Core
	if nc.Setting("k") != "" || nc.SettingBool("k") || nc.SettingNumber("k") != 0 || nc.HasSetting("k") {
		t.Error("a nil Core must answer zero values, not panic")
	}
}

// TestRouteBuildsThePathCoreActuallyProxies — Core forwards the FULL path, so a handler registered at the
// bare name is never reached, and this helper is the only thing standing between an author and a route
// that 404s with no explanation.
func TestRouteBuildsThePathCoreActuallyProxies(t *testing.T) {
	c := &Core{RoutePrefix: "/sso"}
	if got := c.Route("/backchannel-logout"); got != "/sso/backchannel-logout" {
		t.Errorf("Route = %q", got)
	}
	if got := c.Route("backchannel-logout"); got != "/sso/backchannel-logout" {
		t.Errorf("a missing leading slash must be added, got %q", got)
	}
	if got := (&Core{RoutePrefix: "/sso/"}).Route("/x"); got != "/sso/x" {
		t.Errorf("a trailing slash on the prefix must not double up: %q", got)
	}
	// No prefix means no `route` capability: returning the path unchanged keeps a mux built at Init from
	// panicking on an empty pattern.
	if got := (&Core{}).Route("/x"); got != "/x" {
		t.Errorf("with no prefix the path is unchanged, got %q", got)
	}
	var nc *Core
	if got := nc.Route("/x"); got != "/x" {
		t.Errorf("nil Core: %q", got)
	}
}

// TestAPIErrorAnswersWhatTheAuthorAsks — these four decide whether an author retries, re-reads, or gives
// up, and they are on every error path in a plugin.
func TestAPIErrorAnswersWhatTheAuthorAsks(t *testing.T) {
	for _, c := range []struct {
		status                              int
		notFound, conflict, rate, retryable bool
	}{
		{http.StatusNotFound, true, false, false, false},
		{http.StatusConflict, false, true, false, false},
		{http.StatusTooManyRequests, false, false, true, true},
		{http.StatusInternalServerError, false, false, false, true},
		{http.StatusBadGateway, false, false, false, true},
		{http.StatusForbidden, false, false, false, false},
		{http.StatusBadRequest, false, false, false, false},
	} {
		e := &APIError{Status: c.status, Method: "GET", Path: "/content", Code: "X", Body: "m"}
		if e.NotFound() != c.notFound || e.Conflict() != c.conflict || e.RateLimited() != c.rate {
			t.Errorf("%d: notFound=%v conflict=%v rate=%v", c.status, e.NotFound(), e.Conflict(), e.RateLimited())
		}
		if e.Retryable() != c.retryable {
			t.Errorf("%d: Retryable=%v, want %v — this decides whether an author loops", c.status, e.Retryable(), c.retryable)
		}
		if e.Error() == "" {
			t.Errorf("%d: an error with no message tells nobody anything", c.status)
		}
	}
	// Forbidden is the one an author must NOT retry: a backoff will not grow a capability.
	if (&APIError{Status: http.StatusForbidden}).Retryable() {
		t.Error("403 must not be retryable — the manifest is missing a capability and waiting will not fix it")
	}
}
