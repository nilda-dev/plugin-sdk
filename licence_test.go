package nilda

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

// licenceCore is a Core that answers the two licence routes with fixed bytes — the bytes Core's encoder writes
// (internal/pluginlimits/licence.go) — and counts what it was asked.
func licenceCore(t *testing.T, get, bind func(w http.ResponseWriter)) (*API, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/rest/v1/plugin-licence" && get != nil:
			get(w)
		case r.Method == http.MethodPost && r.URL.Path == "/api/rest/v1/plugin-licence/bind" && bind != nil:
			bind(w)
		default:
			w.Header().Set("content-type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"not found"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return newAPI(srv.URL+"/api/rest/v1", "tok", nil), &asked
}

func writeBody(status int, body string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

// AN OLDER CORE has no licence route and answers 404: the plugin holds no licence there — none, and no error, so a
// plugin built on this release runs unchanged on it and simply does not go live.
func TestLicenceIsNoneOnACoreWithoutTheRoute(t *testing.T) {
	api, asked := licenceCore(t, nil, nil)
	lic, err := api.Licence(context.Background())
	if err != nil {
		t.Fatalf("an older Core's 404 must read as no licence, not an error: %v", err)
	}
	if lic.State != LicenceNone || lic.TakesRealMoney() {
		t.Fatalf("an older Core's licence = %+v; want none, and no real money", lic)
	}
	if !reflect.DeepEqual(*asked, []string{"GET /api/rest/v1/plugin-licence"}) {
		t.Fatalf("asked %v", *asked)
	}
}

// AN ERROR IS NEVER A LICENCE. A call that did not complete — Core down, a 403, a 500, bytes that are not Core's
// answer, a plugin with no API at all — answers none AND the error: a shop that cannot read its licence does not take
// real money on a guess. And a state this release does not know (a newer Core's) takes no money either.
func TestALicenceErrorIsNeverLicensed(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		get  func(http.ResponseWriter)
	}{
		{"a 500", writeBody(http.StatusInternalServerError, `{"error":{"code":"INTERNAL","message":"internal error"}}`)},
		{"a 403", writeBody(http.StatusForbidden, `{"error":{"code":"FORBIDDEN","message":"the plugin-licence route answers plugins only"}}`)},
		{"a 503", writeBody(http.StatusServiceUnavailable, `{"error":{"code":"UNAVAILABLE","message":"down"}}`)},
		{"bytes that are not Core's answer", writeBody(http.StatusOK, `{"data":{"state":"active"`)},
	} {
		api, _ := licenceCore(t, tc.get, nil)
		lic, err := api.Licence(ctx)
		if err == nil {
			t.Errorf("%s: no error", tc.name)
		}
		if lic.State != LicenceNone || lic.TakesRealMoney() {
			t.Errorf("%s: read as %+v; an error must be none", tc.name, lic)
		}
	}

	var nilAPI *API
	lic, err := nilAPI.Licence(ctx)
	if err == nil || lic.State != LicenceNone || lic.TakesRealMoney() {
		t.Fatalf("a plugin with no API read %+v, %v; want none and an error", lic, err)
	}
	res, err := nilAPI.BindLicence(ctx)
	if err == nil || res.Bound || res.Licence.State != LicenceNone {
		t.Fatalf("a plugin with no API bound %+v, %v; want not bound and an error", res, err)
	}

	api, _ := licenceCore(t, writeBody(http.StatusOK, `{"data":{"state":"platinum"}}`), nil)
	lic, err = api.Licence(ctx)
	if err != nil || lic.TakesRealMoney() {
		t.Fatalf("a state this release does not know read %+v, %v; it must take no real money", lic, err)
	}
	api, _ = licenceCore(t, writeBody(http.StatusOK, `{"data":{}}`), nil)
	if lic, err = api.Licence(ctx); err != nil || lic.State != LicenceNone {
		t.Fatalf("an answer naming no state read %+v, %v; want none", lic, err)
	}
}

// GRACE is the time between the licence's own end (paid_until) and the day it stops (live_until): it still takes real
// money. Before paid_until it is active (trial with trial set); from live_until on it is lapsed. A time Core leaves out
// is the zero time, "no end".
func TestLicenceReadsGraceBetweenPaidAndLive(t *testing.T) {
	ctx := context.Background()
	api, _ := licenceCore(t, writeBody(http.StatusOK,
		`{"data":{"state":"grace","source":"purchase","paid_until":"2026-11-01T00:00:00Z","live_until":"2026-11-15T00:00:00Z","quantity":3}}`), nil)
	lic, err := api.Licence(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := Licence{State: LicenceGrace, Source: "purchase", Quantity: 3,
		PaidUntil: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), LiveUntil: time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)}
	if !lic.PaidUntil.Equal(want.PaidUntil) || !lic.LiveUntil.Equal(want.LiveUntil) || lic.State != want.State ||
		lic.Source != want.Source || lic.Quantity != want.Quantity || lic.Trial {
		t.Fatalf("read %+v; want %+v", lic, want)
	}
	if !lic.TakesRealMoney() {
		t.Fatal("a licence in grace must still take real money")
	}

	api, _ = licenceCore(t, writeBody(http.StatusOK, `{"data":{"state":"active","source":"plan"}}`), nil)
	if lic, err = api.Licence(ctx); err != nil || !lic.PaidUntil.IsZero() || !lic.LiveUntil.IsZero() || lic.State != LicenceActive {
		t.Fatalf("a plan's licence with no end read %+v, %v; want active and zero times", lic, err)
	}

	for state, takes := range map[LicenceState]bool{LicenceNone: false, LicenceTrial: true, LicenceActive: true,
		LicenceGrace: true, LicenceLapsed: false, "": false, "platinum": false} {
		if got := (Licence{State: state}).TakesRealMoney(); got != takes {
			t.Errorf("%q takes real money = %v; want %v", state, got, takes)
		}
	}

	// The wire round-trips in Core's shape: the times left out when zero, never "0001-01-01".
	raw, err := json.Marshal(Licence{State: LicenceActive, Source: "plan"})
	if err != nil || string(raw) != `{"state":"active","source":"plan"}` {
		t.Fatalf("a licence with no end marshalled as %s (%v)", raw, err)
	}
	raw, err = json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var back Licence
	if err := json.Unmarshal(raw, &back); err != nil || !back.PaidUntil.Equal(want.PaidUntil) ||
		!back.LiveUntil.Equal(want.LiveUntil) || back.State != want.State || back.Quantity != 3 {
		t.Fatalf("%s came back as %+v (%v)", raw, back, err)
	}
}

// A BIND passes Core's answer through: bound with the licence the plugin now reads, or not bound with Central's (or
// Core's) reason, word for word. A bind that failed is an error, never bound and never a reason.
func TestBindPassesCentralsReasons(t *testing.T) {
	ctx := context.Background()
	api, asked := licenceCore(t, nil, writeBody(http.StatusOK,
		`{"data":{"bound":true,"licence":{"state":"trial","source":"trial","paid_until":"2026-12-06T00:00:00Z","live_until":"2026-12-06T00:00:00Z","trial":true}}}`))
	res, err := api.BindLicence(ctx)
	if err != nil || !res.Bound || res.Reason != "" || res.Licence.State != LicenceTrial || !res.Licence.Trial ||
		!res.Licence.TakesRealMoney() {
		t.Fatalf("a bound answer read %+v, %v", res, err)
	}
	if !reflect.DeepEqual(*asked, []string{"POST /api/rest/v1/plugin-licence/bind"}) {
		t.Fatalf("asked %v", *asked)
	}

	for _, reason := range BindReasons() {
		api, _ := licenceCore(t, nil, writeBody(http.StatusOK,
			`{"data":{"bound":false,"reason":"`+reason+`","licence":{"state":"none"}}}`))
		res, err := api.BindLicence(ctx)
		if err != nil || res.Bound || res.Reason != reason || res.Licence.State != LicenceNone {
			t.Errorf("%s read %+v, %v", reason, res, err)
		}
	}
	want := []string{BindReasonNoneFree, BindReasonNotConnected, BindReasonHosted, BindReasonNoSiteLicenceLeft,
		BindReasonAddressNotActivatable, BindReasonAccountBlocked}
	if !reflect.DeepEqual(BindReasons(), want) {
		t.Fatalf("BindReasons() = %v; want %v", BindReasons(), want)
	}

	for _, tc := range []struct {
		name string
		bind func(http.ResponseWriter)
	}{
		{"Central unreachable (502)", writeBody(http.StatusBadGateway, `{"error":{"code":"UPSTREAM","message":"nilda.dev did not answer"}}`)},
		{"an older Core (404)", nil},
		{"bytes that are not Core's answer", writeBody(http.StatusOK, `{"data":`)},
	} {
		api, _ := licenceCore(t, nil, tc.bind)
		res, err := api.BindLicence(ctx)
		if err == nil || res.Bound || res.Reason != "" || res.Licence.State != LicenceNone {
			t.Errorf("%s read %+v, %v; want an error, not bound, no reason, none", tc.name, res, err)
		}
	}
}

// THE TRUTH TEST: the route paths, the JSON names, the states and the reasons this client reads are Core's own, read
// from Core's source beside this module (internal/pluginlimits, internal/license, internal/license/connect).
func TestTheLicenceClientMatchesCoresSource(t *testing.T) {
	rest := coreSource(t, "internal", "pluginlimits", "rest.go")
	for _, want := range []string{`rg.Group("/plugin-licence"`, `lic.GET("", h.licence)`, `lic.POST("/bind", h.bind)`} {
		if !strings.Contains(rest, want) {
			t.Errorf("Core's internal/pluginlimits/rest.go no longer mounts %s — %s and %s read it", want,
				licencePath, licenceBindPath)
		}
	}
	if licencePath != "/plugin-licence" || licenceBindPath != "/plugin-licence/bind" {
		t.Errorf("the client asks %s and %s", licencePath, licenceBindPath)
	}
	handler := coreSource(t, "internal", "pluginlimits", "licence.go")
	for _, want := range []string{`gin.H{"data": h.licences.PluginLicence(`, `gin.H{"data": BindAnswer{`,
		"Licence license.PluginLicenceStanding `json:\"licence\"`"} {
		if !strings.Contains(handler, want) {
			t.Errorf("Core's internal/pluginlimits/licence.go no longer has %s", want)
		}
	}

	lic := coreSource(t, "internal", "license", "pluginlicence.go")
	standing := upTo(t, from(t, lic, "type PluginLicenceStanding struct", "pluginlicence.go"), "\n}", "pluginlicence.go")
	if got, want := sourceJSONNames(standing), wireNames(reflect.TypeOf(licenceWire{})); !reflect.DeepEqual(got, want) {
		t.Errorf("Core's PluginLicenceStanding names %v; this client reads %v", got, want)
	}
	states := upTo(t, from(t, lic, "PluginLicenceNone ", "pluginlicence.go"), ")", "pluginlicence.go")
	var core []string
	for _, m := range regexp.MustCompile(`PluginLicence\w+\s+PluginLicenceState = "(\w+)"`).FindAllStringSubmatch(states, -1) {
		core = append(core, m[1])
	}
	var sdk []string
	for _, s := range LicenceStates() {
		sdk = append(sdk, string(s))
	}
	if !reflect.DeepEqual(core, sdk) {
		t.Errorf("Core's plugin licence states are %v; LicenceStates() is %v", core, sdk)
	}

	bind := coreSource(t, "internal", "license", "connect", "pluginbind.go")
	outcome := upTo(t, from(t, bind, "type BindOutcome struct", "pluginbind.go"), "\n}", "pluginbind.go")
	result := wireNames(reflect.TypeOf(BindResult{}))
	if got := append(sourceJSONNames(outcome), "licence"); !reflect.DeepEqual(sorted(got), result) {
		t.Errorf("Core's bind answer names %v (with BindAnswer's licence); BindResult reads %v", got, result)
	}
	var reasons []string
	for _, m := range regexp.MustCompile(`(?m)^\s*BindReason\w+ = "(\w+)"`).FindAllStringSubmatch(bind, -1) {
		reasons = append(reasons, m[1])
	}
	if !reflect.DeepEqual(sorted(reasons), sorted(BindReasons())) {
		t.Errorf("Core's bind reasons are %v; BindReasons() is %v", reasons, BindReasons())
	}
}

// sourceJSONNames is every json name a Go struct's source declares, "-" left out, sorted.
func sourceJSONNames(src string) []string {
	var out []string
	for _, m := range regexp.MustCompile("json:\"([a-z_]+)").FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return sorted(out)
}

// wireNames is every json name a struct type carries, sorted.
func wireNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return sorted(out)
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
