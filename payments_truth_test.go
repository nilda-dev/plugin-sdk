package nilda

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// THE PAYMENT CONTRACT, HELD TO CORE (D-84). payment.go, paymentapi.go, nildatest.Payments and
// docs/PAYMENTS.md describe what Core does with a payment; each guard below reads the Core source that does it,
// so a change on either side fails here rather than in a gateway author's first live checkout. Core holds the
// same contract from its side against the tag it builds on (core/internal/payments/sdk_mirror_test.go); this
// file sees Core's WORKING source, so it also catches a Core change before Core moves to a new tag.

func corePaymentsSource(t *testing.T, parts ...string) string {
	t.Helper()
	return coreSource(t, parts...)
}

// constValues maps each `Name = "value"` constant in src to its value.
func constValues(src string) map[string]string {
	out := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"([^"]*)"`).FindAllStringSubmatch(src, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// movesIn reads a `var name = map[string][]string{ Key: {A, B}, … }` table out of Go source, naming each status
// by its constant's value.
func movesIn(t *testing.T, src, name string, consts map[string]string) map[string][]string {
	t.Helper()
	start := strings.Index(src, name+" = map[string][]string{")
	if start < 0 {
		t.Fatalf("no %s table in core's machine.go — repoint this guard", name)
	}
	body := upTo(t, src[start:], "\n\t}", "core's "+name)
	out := map[string][]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(\w+):\s*\{([^}]*)\}`).FindAllStringSubmatch(body, -1) {
		from, ok := consts[m[1]]
		if !ok {
			t.Fatalf("core's %s names %s, which is no constant of machine.go", name, m[1])
		}
		for _, to := range strings.Split(m[2], ",") {
			v, ok := consts[strings.TrimSpace(to)]
			if !ok {
				t.Fatalf("core's %s moves to %q, which is no constant of machine.go", name, to)
			}
			out[from] = append(out[from], v)
		}
	}
	return out
}

func sortedMoves(m map[string][]string) string {
	var lines []string
	for from, tos := range m {
		for _, to := range tos {
			lines = append(lines, from+">"+to)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, " ")
}

func TestCoresPaymentMachineIsTheSDKs(t *testing.T) {
	src := corePaymentsSource(t, "internal", "payments", "machine.go")
	consts := constValues(src)
	if got, want := sortedMoves(movesIn(t, src, "sessionMoves", consts)), sortedMoves(paymentMoves); got != want {
		t.Errorf("a session's moves differ.\n core: %s\n  sdk: %s", got, want)
	}
	if got, want := sortedMoves(movesIn(t, src, "refundMoves", consts)), sortedMoves(refundMoves); got != want {
		t.Errorf("a refund's moves differ.\n core: %s\n  sdk: %s", got, want)
	}

	// The failure codes: the same nine, and the same six a gateway may report.
	all := []string{PaymentFailDeclined, PaymentFailCancelled, PaymentFailProviderUnavailable, PaymentFailInvalidRequest,
		PaymentFailExpired, PaymentFailAuthenticationRequired, PaymentFailAmountMismatch, PaymentFailConsumerRefused,
		PaymentFailVoided}
	var core []string
	for name, v := range consts {
		if strings.HasPrefix(name, "Fail") {
			core = append(core, v)
		}
	}
	sort.Strings(core)
	sort.Strings(all)
	if strings.Join(core, ",") != strings.Join(all, ",") {
		t.Errorf("core's failure codes are %v; the SDK's are %v", core, all)
	}
	block := upTo(t, from(t, src, "gatewayFailureCodes = map[string]bool{", "core's validate.go"), "}", "core's gatewayFailureCodes")
	var coreGateway []string
	for _, m := range regexp.MustCompile(`(\w+):\s*true`).FindAllStringSubmatch(block, -1) {
		coreGateway = append(coreGateway, consts[m[1]])
	}
	sdkGateway := append([]string(nil), gatewayFailureCodes...)
	sort.Strings(coreGateway)
	sort.Strings(sdkGateway)
	if strings.Join(coreGateway, ",") != strings.Join(sdkGateway, ",") {
		t.Errorf("core lets a gateway report %v; the SDK says %v", coreGateway, sdkGateway)
	}
}

func TestThePaymentHooksAreCores(t *testing.T) {
	src := corePaymentsSource(t, "internal", "plugin", "payments.go")
	var core []string
	for _, m := range regexp.MustCompile(`(?m)^\s*Hook\w+\s*=\s*"(payment\.[a-z.]+)"`).FindAllStringSubmatch(src, -1) {
		core = append(core, m[1])
	}
	var sdk []string
	for _, h := range []string{HookPaymentDescribe, HookPaymentStart, HookPaymentRefund, HookPaymentSync, HookPaymentForget,
		HookPaymentCapture, HookPaymentVoid, HookPaymentReprice, HookPaymentConfirm, HookPaymentSessionUpdated,
		HookPaymentRefundUpdated, HookPaymentDisputeUpdated} {
		if !coreLacks("hook " + h) { // SDK-first surface Core has not landed yet (coreahead_test.go)
			sdk = append(sdk, h)
		}
	}
	sort.Strings(core)
	sort.Strings(sdk)
	if strings.Join(core, ",") != strings.Join(sdk, ",") {
		t.Fatalf("core's payment hooks are %v; the SDK's are %v", core, sdk)
	}
}

// The bounds Validate checks are the bounds Core checks, and the ones PAYMENTS.md §7 prints.
func TestThePaymentBoundsAreCores(t *testing.T) {
	src := corePaymentsSource(t, "internal", "payments", "validate.go")
	for core, sdk := range map[string]int{
		"maxReference": maxPaymentReference, "maxText": maxPaymentText, "maxProviderRef": maxProviderRef,
		"maxURL": maxPayerURL, "maxEmail": maxEmail, "maxLocale": maxLocale,
	} {
		want := regexp.MustCompile(`(?m)^\s*` + core + `\s*=\s*(\d+)\s*$`)
		m := want.FindStringSubmatch(src)
		if m == nil {
			t.Errorf("core's validate.go has no %s — repoint this guard", core)
			continue
		}
		if m[1] != itoa(sdk) {
			t.Errorf("core's %s is %s; the SDK's is %d", core, m[1], sdk)
		}
	}
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	for _, row := range []string{
		"| `reference` | " + itoa(maxPaymentReference) + " characters |",
		"| `description`, refund `reason`, `failure_message` | " + itoa(maxPaymentText) + " characters |",
		"| `provider_ref` | " + itoa(maxProviderRef) + " bytes |",
		"| `return_url`, `cancel_url`, `redirect_url` | 2,048 bytes, absolute http(s) |",
		"| `email` | " + itoa(maxEmail) + " bytes, a bare address |",
		"| `locale` | " + itoa(maxLocale) + " characters, a language tag |",
	} {
		if !strings.Contains(doc, row) {
			t.Errorf("PAYMENTS.md §7 does not say %q", row)
		}
	}
	if maxPayerURL != 2048 {
		t.Errorf("maxPayerURL is %d; PAYMENTS.md §7 says 2,048", maxPayerURL)
	}
}

// The numbers PAYMENTS.md gives an author: fifteen seconds a gateway hook (or the ordinary budget if that is
// longer), the ordinary five for a consumer's, a day's life for a session its gateway gives no expiry, and the
// five-minute sweep that closes it. Each number is read from Core, and each is held where Core APPLIES it —
// a constant nothing uses would leave every sentence here green and false.
func TestThePaymentNumbersInTheGuideAreCores(t *testing.T) {
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	flat := func(parts ...string) string {
		return strings.Join(strings.Fields(corePaymentsSource(t, parts...)), " ")
	}
	number := func(src, file string, re string) string {
		t.Helper()
		m := regexp.MustCompile(re).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("core's %s no longer matches %s — repoint this guard", file, re)
		}
		return m[1]
	}
	plugin, host := flat("internal", "plugin", "payments.go"), flat("internal", "plugin", "host.go")
	calls := flat("internal", "plugin", "calltable.go")
	service, module := flat("internal", "payments", "service.go"), flat("internal", "payments", "module.go")
	config := flat("pkg", "config", "config.go")
	queries := flat("internal", "db", "queries", "payments.sql")

	gateway := number(plugin, "internal/plugin/payments.go", `const PaymentHookBudget = ([0-9]+) \* time\.Second`)
	ordinary := number(config, "pkg/config/config.go", `parseDuration\("PLUGIN_CALL_TIMEOUT", ([0-9]+)\*time\.Second\)`)
	lifetime := number(service, "internal/payments/service.go", `const sessionLifetime = ([0-9]+) \* time\.Hour`)
	sweep := number(module, "internal/payments/module.go", `\{Spec: "@every ([0-9]+)m", JobType: JobExpire\}`)

	// Where each is applied: every hook call takes its budget from hookBudget, which gives a hook its call-table
	// row's budget only when it is the longer — and only the gateway's four hooks have PaymentHookBudget there; a session's expiry starts at
	// sessionLifetime from its creation, counted on the DATABASE's clock (Core's whole-plan review, P-m1: the
	// sweep that closes it reads that clock).
	for _, c := range []struct{ src, file, want string }{
		{host, "internal/plugin/host.go", "callCtx, cancel := context.WithTimeout(ctx, hookBudget(hook, h.cfg.CallTimeout))"},
		{calls, "internal/plugin/calltable.go", "func hookBudget(hook string, ordinary time.Duration) time.Duration { " +
			"if r, _ := ruleFor(hook); r.budget > ordinary { return r.budget } return ordinary }"},
		{calls, "internal/plugin/calltable.go", "gatewayCall = hookRule{budget: PaymentHookBudget}"},
		{calls, "internal/plugin/calltable.go", "HookPaymentDescribe: gatewayCall, HookPaymentStart: gatewayCall,"},
		{calls, "internal/plugin/calltable.go", "HookPaymentRefund: {budget: PaymentHookBudget, errorIsAnswer: true}, " +
			"HookPaymentSync: {budget: PaymentHookBudget, errorIsAnswer: true},"},
		{calls, "internal/plugin/calltable.go", "HookPaymentForget: {budget: PaymentHookBudget, errorIsAnswer: true},"},
		{calls, "internal/plugin/calltable.go", "HookPaymentCapture: {budget: PaymentHookBudget, errorIsAnswer: true}, " +
			"HookPaymentVoid: {budget: PaymentHookBudget, errorIsAnswer: true},"},
		{service, "internal/payments/service.go", "Lifetime: sessionLifetime,"},
		// Core's 625bc5d33 (2026-10-30) added credential_type before expires_at, its migration 0377 (v0.58.0) capture_mode
		// after it, and 0378 (door D5) off_session and reader; the expiry is still the database's now() plus the lifetime.
		{queries, "internal/db/queries/payments.sql", "save, charged_saved_id, credential_type, expires_at, capture_mode, off_session, reader, lines, breakdown ) VALUES ( $1, $2, $3, $4, $5, $6, $7, " +
			"'created', $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, sqlc.narg('save'), sqlc.narg('charged_saved_id'), " +
			"sqlc.arg('credential_type'), now() + sqlc.arg('lifetime')::interval,"},
	} {
		if !strings.Contains(c.src, c.want) {
			t.Errorf("core's %s no longer has %q — re-read what PAYMENTS.md says about it", c.file, c.want)
		}
	}
	// "and nothing else": those four places are the only rows that name the payment budget.
	if n := strings.Count(calls, "PaymentHookBudget"); n != 6 {
		t.Errorf("core's call table names PaymentHookBudget %d times, want the gateway row kind and the refund, sync, forget, "+
			"capture and void rows — "+
			"re-read which hooks PAYMENTS.md says get 15 seconds", n)
	}
	// "At most": start and describe run on the asking request's context, which a WithTimeout inside the call
	// keeps (a child context never outlives its parent's deadline).
	for _, want := range []string{
		"res, callErr := s.start(ctx, row, p.Credential)",
		"out, err := s.plugins.Call(ctx, row.GatewayKey, plugin.HookPaymentStart, payload)",
		"cat, err := s.catalogue(ctx)",
		"out, err := s.plugins.Call(ctx, gateway, plugin.HookPaymentDescribe, []byte(`{}`))",
	} {
		if !strings.Contains(service, want) {
			t.Errorf("core's internal/payments/service.go no longer has %q — re-read PAYMENTS.md's \"At most\"", want)
		}
	}
	words := map[string]string{"5": "five", "10": "ten", "15": "fifteen"}
	if words[sweep] == "" {
		t.Fatalf("core's expiry sweep runs every %s minutes, which this guard cannot spell — teach it the word", sweep)
	}
	for _, want := range []string{
		"**" + gateway + " seconds** for each payment hook, instead of the ordinary plugin call's " + ordinary,
		"at most the longer of " + gateway + " seconds and the site's `PLUGIN_CALL_TIMEOUT`",
		"`payment.start` and `payment.describe` run inside the request that asked — the payer is waiting — and keep its deadline",
		"| a gateway's hook | at most " + gateway + " seconds, or `PLUGIN_CALL_TIMEOUT` if that is longer |",
		"| a consumer's hook | " + ordinary + " seconds, the default of `PLUGIN_CALL_TIMEOUT` |",
		"| a session nothing decided | " + lifetime + " hours, unless the gateway says |",
		"(" + lifetime + " hours after it was created if you say nothing",
		"a sweep that runs every " + words[sweep] + " minutes, so a session can stay open up to " + words[sweep] + " minutes past its time",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("PAYMENTS.md does not say %q", want)
		}
	}
}

// "This site", as PAYMENTS.md defines it for return addresses: the owner's site.base_url, else APP_BASE_URL —
// while GET /site answers base_url from the setting alone. Each half read where Core does it.
func TestTheSiteThePaymentsAreHeldToIsCores(t *testing.T) {
	flat := func(parts ...string) string {
		return strings.Join(strings.Fields(corePaymentsSource(t, parts...)), " ")
	}
	service := flat("internal", "payments", "service.go")
	main := flat("cmd", "server", "main.go")
	// stable.NewService also takes the translation service now (Core's 96aee2586, 2026-10-27: the D-49 shared values
	// became a NewService parameter); the site the REST API answers from is stable.SiteSource over the settings (Core's
	// #320 moved the adapter out of cmd/server).
	for where, want := range map[string]struct{ src, text string }{
		"payments: the setting first":            {service, `if u := strings.TrimSpace(s.siteURL(ctx)); u != "" { return u }`},
		"payments: then APP_BASE_URL":            {service, "return s.appURL }"},
		"main.go: the setting is site.base_url":  {main, "SiteURL: a.settingsSvc.SiteBaseURL"},
		"main.go: the fallback is the env":       {main, "AppBaseURL: a.cfg.AppBaseURL"},
		"main.go: GET /site reads the setting":   {main, "stable.SiteSource(a.settingsSvc, a.cfg.I18NDefault)"},
		"stable: the source reads the settings":  {flat("internal", "api", "stable", "site.go"), "return func(ctx context.Context) Site { return SiteFromSettings(ctx, settings, locale) }"},
		"stable: it reads site.base_url":         {flat("internal", "api", "stable", "site.go"), `BaseURL: settings.GetString(ctx, "site.base_url"),`},
		"main.go: the REST API is built from it": {main, "stableAPI := stable.NewService(a.store, stable.SiteSource(a.settingsSvc, a.cfg.I18NDefault), a.translationSvc, a.log)"},
		"main.go: that is the REST API's":        {main, "a.srv.Mount(rest.PathV1, stable.NewHandlers(stableAPI,"},
		"settings: SiteBaseURL's key":            {flat("internal", "settings", "browser.go"), `const keySiteBaseURL = "site.base_url"`},
		"stable: the field is base_url":          {flat("internal", "api", "stable", "contract.go"), "BaseURL string `json:\"base_url\"`"},
	} {
		if !strings.Contains(want.src, want.text) {
			t.Errorf("%s: core no longer has %q — re-read PAYMENTS.md's \"this site\"", where, want.text)
		}
	}
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	for _, want := range []string{
		"the owner's Settings → General address (`site.base_url`), or the server's `APP_BASE_URL` when none is set",
		"`GET /site` answers `base_url` from the setting alone",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("PAYMENTS.md does not say %q", want)
		}
	}
}

// Each capability's token carries the scopes the guide names and nildatest grants — Core's grant is the truth.
func TestThePaymentScopesAreCores(t *testing.T) {
	src := strings.Join(strings.Fields(corePaymentsSource(t, "internal", "plugin", "identity.go")), " ")
	for _, want := range []string{
		`CapPaymentSession: {"read:payments", "write:payments"}`,
		`CapPaymentGateway: {"read:payments", "report:payments"}`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("core's identity.go does not grant %s", want)
		}
	}
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	if !strings.Contains(doc, "`payment_session` → `read:payments write:payments`, `payment_gateway` → `read:payments report:payments`") {
		t.Error("PAYMENTS.md §6 no longer names each capability's scopes")
	}
	fake := strings.Join(strings.Fields(readText(t, "nildatest/nildatest.go")), " ")
	for _, want := range []string{`"payment_session": {"read:payments", "write:payments"}`, `"payment_gateway": {"read:payments", "report:payments"}`} {
		if !strings.Contains(fake, want) {
			t.Errorf("nildatest does not grant %s", want)
		}
	}
}

// The routes: where they are mounted, every call PAYMENTS.md §6 lists is one Core registers and every route
// Core registers is listed — with the side that may call it — and every request the SDK's client sends, by
// method and full path, is one of them.
func TestThePaymentRoutesAreCores(t *testing.T) {
	src := corePaymentsSource(t, "internal", "payments", "rest.go")
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	// Where: the module mounts the handlers under a prefix, rest.go groups them, and the prefix is the path a
	// plugin's API client is handed as its base (PLUGIN_API_BASE_URL's default). Nothing here is typed.
	mount := regexp.MustCompile(`\{Prefix: "([^"]+)", Mounter: NewRESTHandlers\(`).FindStringSubmatch(
		corePaymentsSource(t, "internal", "payments", "module.go"))
	group := regexp.MustCompile(`g := rg\.Group\("([^"]+)"`).FindStringSubmatch(src)
	base := regexp.MustCompile(`getEnv\("PLUGIN_API_BASE_URL", fmt\.Sprintf\("http://127\.0\.0\.1:%d([^"]+)"`).FindStringSubmatch(
		flat(corePaymentsSource(t, "pkg", "config", "config.go")))
	if mount == nil || group == nil || base == nil {
		t.Fatal("core no longer mounts the payment routes as `{Prefix: …, Mounter: NewRESTHandlers(…)}` under " +
			"`rg.Group(…)`, or no longer defaults PLUGIN_API_BASE_URL to a local path — repoint this guard")
	}
	if mount[1] != base[1] {
		t.Errorf("core mounts the payment routes under %s, and hands a plugin %s as its API base", mount[1], base[1])
	}
	docText := readText(t, "docs/PAYMENTS.md")
	if !strings.Contains(flat(docText), "Calls into Core, under `"+mount[1]+"`") {
		t.Errorf("PAYMENTS.md §6 does not say the calls are under `%s`", mount[1])
	}

	// Who: each route's handler asks for one scope, naming one side. The side is what §6's "who" column
	// says, and the scope is one only that side's capability grants (or both, for "either side").
	sides := map[string]string{"sideConsumer": "consumer", "sideGateway": "gateway", "sideEither": "either side of it"}
	scopes := constValues(corePaymentsSource(t, "internal", "apistandards", "scopes.go"))
	identity := flat(corePaymentsSource(t, "internal", "plugin", "identity.go"))
	grants := func(cap string) map[string]bool {
		m := regexp.MustCompile(cap + `: \{([^}]*)\}`).FindStringSubmatch(identity)
		if m == nil {
			t.Fatalf("core's identity.go no longer grants %s scopes as a list — repoint this guard", cap)
		}
		out := map[string]bool{}
		for _, s := range strings.Split(m[1], ",") {
			out[strings.Trim(strings.TrimSpace(s), `"`)] = true
		}
		return out
	}
	consumer, gateway := grants("CapPaymentSession"), grants("CapPaymentGateway")
	core := map[string]string{} // "METHOD /payments/…" -> who
	for _, m := range regexp.MustCompile(`g\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)", h\.(\w+)\)`).FindAllStringSubmatch(src, -1) {
		fn := from(t, src, "func (h *RESTHandlers) "+m[3]+"(c *gin.Context) {", "core's rest.go")
		c := regexp.MustCompile(`caller\(c, apistandards\.(\w+), (side\w+)\)`).FindStringSubmatch(upTo(t, fn, "\n}", "core's "+m[3]))
		if c == nil {
			t.Fatalf("core's %s handler no longer asks caller() for a scope and a side — repoint this guard", m[3])
		}
		who, scope := sides[c[2]], scopes[c[1]]
		if who == "" || scope == "" {
			t.Fatalf("core's %s handler names %s / %s, which this guard cannot read", m[3], c[1], c[2])
		}
		ok := map[string]bool{
			"consumer":          consumer[scope] && !gateway[scope],
			"gateway":           gateway[scope] && !consumer[scope],
			"either side of it": consumer[scope] && gateway[scope],
		}[who]
		if !ok {
			t.Errorf("core's %s %s is for the %s, and asks for %q, which is not a scope only that side holds", m[1], m[2], who, scope)
		}
		core[m[1]+" "+group[1]+m[2]] = who
	}
	if len(core) < 10 {
		t.Fatalf("found %d routes in core's rest.go — the parse is broken, repoint this guard", len(core))
	}
	doc := map[string]string{}
	for _, m := range regexp.MustCompile("(?m)^\\| `(GET|POST|PUT) (/payments/[^` ?]+)[^|]*\\| ([^|]+) \\|").FindAllStringSubmatch(docText, -1) {
		doc[m[1]+" "+strings.NewReplacer("{id}", ":id", "{name}", ":name").Replace(m[2])] = strings.TrimSpace(m[3])
	}
	for r, who := range doc {
		if coreLacks("route " + r) {
			continue // SDK-first: PAYMENTS.md lists it, Core lands it in its own step (coreahead_test.go)
		}
		if coreWho, ok := core[r]; !ok {
			t.Errorf("PAYMENTS.md lists %s; core registers no such route", r)
		} else if coreWho != who {
			t.Errorf("PAYMENTS.md says %s is for %q; core lets the %s call it", r, who, coreWho)
		}
	}
	for r := range core {
		if _, ok := doc[r]; !ok {
			t.Errorf("core registers %s; PAYMENTS.md does not list it", r)
		}
	}

	// What the client SENDS: every method of *Payments, driven against a server that records the method and
	// the full path, must hit a route Core registers — exactly, by method — and together they must hit all.
	var mu sync.Mutex
	sent := map[string]bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, mount[1])
		path = strings.NewReplacer("/sid/", "/:id/", "/rid/", "/:id/", "/did/", "/:id/", "/smid/", "/:id/", "/proof_secret/", "/:name/").Replace(path + "/")
		mu.Lock()
		sent[r.Method+" "+strings.TrimSuffix(path, "/")] = true
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":null}`)
	}))
	defer srv.Close()
	pay := newAPI(srv.URL+mount[1], "tok", nil).Payments()
	ctx := context.Background()
	calls := map[string]func(){
		"Methods":   func() { _, _ = pay.Methods(ctx, "EUR") },
		"Readiness": func() { _, _ = pay.Readiness(ctx, "EUR") },
		"CreateSession": func() {
			_, _ = pay.CreateSession(ctx, "k1", PaymentSessionParams{Gateway: "g", Method: "m", Reference: "r",
				AmountMinor: 1, Currency: "EUR", ReturnURL: "https://site.test/x"})
		},
		"GetSession":    func() { _, _ = pay.GetSession(ctx, "sid") },
		"CreateRefund":  func() { _, _ = pay.CreateRefund(ctx, "k2", "sid", PaymentRefundParams{AmountMinor: 1}) },
		"GetRefund":     func() { _, _ = pay.GetRefund(ctx, "rid") },
		"Resolve":       func() { _, _ = pay.Resolve(ctx, "sid", PaymentResolution{AmountMinor: 1, Currency: "EUR"}) },
		"Reject":        func() { _, _ = pay.Reject(ctx, "sid", PaymentRejection{FailureCode: PaymentFailDeclined}) },
		"MarkPending":   func() { _, _ = pay.MarkPending(ctx, "sid", "") },
		"Confirm":       func() { _, _ = pay.Confirm(ctx, "sid") },
		"ResolveRefund": func() { _, _ = pay.ResolveRefund(ctx, "rid", "") },
		"RejectRefund":  func() { _, _ = pay.RejectRefund(ctx, "rid", PaymentRejection{FailureCode: PaymentFailDeclined}) },
		"ReverseRefund": func() { _, _ = pay.ReverseRefund(ctx, "rid", PaymentRejection{FailureCode: PaymentFailDeclined}) },
		"ReportExternalRefund": func() {
			_, _ = pay.ReportExternalRefund(ctx, "sid", PaymentExternalRefund{AmountMinor: 1, Currency: "EUR", ProviderRef: "re_1"})
		},
		"ReportDispute": func() {
			_, _ = pay.ReportDispute(ctx, "sid", PaymentDisputeReport{ProviderRef: "dp_1", Stage: DisputeChargeback,
				Status: DisputeNeedsResponse, AmountMinor: 1, Currency: "EUR"})
		},
		"ListDisputes": func() { _, _ = pay.ListDisputes(ctx, "sid") },
		"GetDispute":   func() { _, _ = pay.GetDispute(ctx, "did") },
		"SetSecret":    func() { _ = pay.SetSecret(ctx, "proof_secret", "v") },
		"GetSecret":    func() { _, _, _ = pay.GetSecret(ctx, "proof_secret") },
		"ChargeSaved": func() {
			_, _ = pay.ChargeSaved(ctx, "k3", PaymentChargeParams{SavedMethod: "smid", Reference: "r", AmountMinor: 1, Currency: "EUR"})
		},
		"GetSavedMethod": func() { _, _ = pay.GetSavedMethod(ctx, "smid") },
		"Authorize": func() {
			_, _ = pay.Authorize(ctx, "sid", PaymentAuthorization{AmountMinor: 1, Currency: "EUR"})
		},
		"Capture":           func() { _, _ = pay.Capture(ctx, "k4", "sid", 1) },
		"Reprice":           func() { _, _ = pay.Reprice(ctx, "k6", "sid", PaymentReprice{AmountMinor: 1}) },
		"Void":              func() { _, _ = pay.Void(ctx, "k5", "sid") },
		"ForgetSavedMethod": func() { _, _ = pay.ForgetSavedMethod(ctx, "smid") },
	}
	methods := reflect.TypeOf(pay)
	for i := 0; i < methods.NumMethod(); i++ {
		if _, ok := calls[methods.Method(i).Name]; !ok {
			t.Errorf("*Payments has a method %s this guard does not drive — add it to calls", methods.Method(i).Name)
		}
	}
	for _, call := range calls {
		call()
	}
	for r := range sent {
		if _, ok := core[r]; !ok && !coreLacks("route "+r) {
			t.Errorf("the SDK's client sends %s; core registers no such route", r)
		}
	}
	for r := range core {
		if !sent[r] {
			t.Errorf("core registers %s and no method of the SDK's client sends it", r)
		}
	}
}

// The shapes a plugin reads: the field names PAYMENTS.md lists for a session and a refund are the json tags of
// Core's Session and Refund.
func TestThePaymentShapesAreCores(t *testing.T) {
	src := corePaymentsSource(t, "internal", "payments", "types.go")
	tags := func(typ string) []string {
		start := strings.Index(src, "type "+typ+" struct {")
		if start < 0 {
			t.Fatalf("no %s in core's types.go — repoint this guard", typ)
		}
		body := upTo(t, src[start:], "\n}", "core's "+typ)
		var out []string
		for _, m := range regexp.MustCompile("json:\"([a-z_]+)").FindAllStringSubmatch(body, -1) {
			out = append(out, m[1])
		}
		return out
	}
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	for typ, marker := range map[string]string{"Session": "A session is `{", "Refund": "a refund is `{"} {
		i := strings.Index(doc, marker)
		if i < 0 {
			t.Errorf("PAYMENTS.md no longer lists %q", marker)
			continue
		}
		list := upTo(t, doc[i+len(marker):], "}", "PAYMENTS.md's "+marker)
		var listed []string
		for _, f := range strings.Split(list, ",") {
			f = strings.Trim(strings.TrimSpace(f), `"`)
			if !coreLacks(strings.ToLower(typ) + " " + f) { // SDK-first (coreahead_test.go)
				listed = append(listed, f)
			}
		}
		if got, want := strings.Join(tags(typ), ","), strings.Join(listed, ","); got != want {
			t.Errorf("core's %s carries %s; PAYMENTS.md lists %s", typ, got, want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// TestTheKitsDecisionIsCores holds nildatest's Payments.Decide to Core's Decide (internal/payments/admin.go),
// rule by rule: the words a decision takes, only a pending payment, a held mismatch resolved at the processor's
// amount and refused in another currency, and a rejection's reason and message. A consumer tests what it is
// told after a person decides; told something else than Core tells it, the test proves nothing.
func TestTheKitsDecisionIsCores(t *testing.T) {
	core := strings.Join(strings.Fields(coreSource(t, "internal", "payments", "admin.go")), " ")
	kit := strings.Join(strings.Fields(readText(t, "nildatest/payments.go")), " ")
	for _, pair := range []struct{ core, kit string }{
		{`DecisionResolve = "resolve" DecisionReject = "reject"`, `if decision != "resolve" && decision != "reject" {`},
		// Core's d6f8fed90 (2026-10-30) moved the two refusals about the payment itself into decisionRefused, one home
		// for the assistant's preview and the apply; Decide's apply asks it first, as before.
		{`if err := decisionRefused(decision, cur.Status, cur.FailureCode, cur.Currency, cur.ReportedAmountMinor, cur.ReportedCurrency); err != nil { return false, err }`,
			`if s.Status != nilda.PaymentPending {`},
		{`func decisionRefused(decision, status, failureCode, currency string, reported *int64, reportedCurrency string) error { ` +
			`if status != StatusPending { return apperr.Conflict("only a payment that is pending can be decided by a person")`,
			`if s.Status != nilda.PaymentPending { return nilda.PaymentSession{}, fmt.Errorf("nildatest: only a payment that is pending can be decided by a person;`},
		{`if decision == DecisionResolve && failureCode == FailAmountMismatch && reported != nil && reportedCurrency != currency {`,
			`if decision == "resolve" { if held { if took.currency != s.Currency {`},
		{`if cur.FailureCode == FailAmountMismatch && cur.ReportedAmountMinor != nil { next.AmountMinor = *cur.ReportedAmountMinor`,
			`held = held && s.FailureCode == nilda.PaymentFailAmountMismatch`},
		{`next.AmountMinor = *cur.ReportedAmountMinor`, `s.AmountMinor = took.amount`},
		{`next.Status, next.FailureCode, next.FailureMessage = StatusResolved, "", ""`, `s.FailureCode, s.FailureMessage = "", "" p.move(s, nilda.PaymentResolved)`},
		{`code := FailCancelled if cur.FailureCode == FailAmountMismatch { code = FailAmountMismatch }`,
			`code := nilda.PaymentFailCancelled if s.FailureCode == nilda.PaymentFailAmountMismatch { code = nilda.PaymentFailAmountMismatch }`},
		{`next.Status, next.FailureCode, next.FailureMessage = StatusRejected, code, note`, `s.FailureCode, s.FailureMessage = code, note p.move(s, nilda.PaymentRejected)`},
		{`if utf8.RuneCountInString(note) > maxText {`, `if utf8.RuneCountInString(note) > maxPaymentText {`},
		// …and a note holding a NUL is refused (Core's d6f8fed90, 2026-10-30), in Core's words.
		{`if !noNUL(note) { return errNUL() }`, `if strings.IndexByte(note, 0) >= 0 { return nilda.PaymentSession{}, fmt.Errorf("nildatest: text in a payment may not hold a NUL character")`},
	} {
		if !strings.Contains(core, pair.core) {
			t.Errorf("core's Decide no longer says %q — re-read nildatest's Decide against it", pair.core)
		}
		if !strings.Contains(kit, pair.kit) {
			t.Errorf("nildatest's Decide no longer says %q, the kit's half of Core's %q", pair.kit, pair.core)
		}
	}
}

// TestTheNULRuleIsCores holds every place the SDK and nildatest refuse a NUL in payment text to the Core line that
// refuses the same field (Core's validate.go, cd898a911, review 2026-10-30): the rule itself and its words, then each
// validator Core says mirrors the SDK's — read function by function on both sides, so a NUL check moved to another
// field, or dropped from one, fails here.
func TestTheNULRuleIsCores(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	body := func(src, decl, where string) string {
		t.Helper()
		b := from(t, src, decl, where)
		return flat(upTo(t, b, "\n}\n", where+"'s "+decl))
	}
	core := corePaymentsSource(t, "internal", "payments", "validate.go")
	sdk := readText(t, "payment.go") + readText(t, "paymentmore.go") + readText(t, "paymentsaved.go") + readText(t, "paymentapi.go")
	kit := readText(t, "nildatest/payments.go")
	const cv, sv = "core's internal/payments/validate.go", "the SDK's payment files"

	// The rule, and the words a plugin is told.
	if c, s := body(core, "func noNUL(vals ...string) bool {", cv), body(sdk, "func noNUL(vals ...string) bool {", sv); c != s {
		t.Errorf("the SDK's noNUL is not Core's:\n core: %s\n  sdk: %s", c, s)
	}
	if !strings.Contains(flat(core), `return apperr.ValidationMsg("text in a payment may not hold a NUL character", nil)`) ||
		!strings.Contains(flat(sdk), `return fmt.Errorf("nilda: text in a payment may not hold a NUL character")`) {
		t.Error("the SDK's errNUL no longer says what Core's does")
	}

	for _, c := range []struct{ coreDecl, coreWant, sdkDecl, sdkWant string }{
		{"func validateSessionParams(", "case !noNUL(p.Gateway, p.Method, p.Reference, p.Description, p.Email): return errNUL()",
			"func (p PaymentSessionParams) Validate() error {", "case !noNUL(p.Gateway, p.Method, p.Reference, p.Description, p.Email): return errNUL()"},
		{"func validateSessionParams(", "if !noNUL(p.Save.Text) { return errNUL() }",
			"func (p PaymentSessionParams) Validate() error {", "if !noNUL(p.Save.Text) { return errNUL() }"},
		// A credential's type is a token: Core refuses a NUL in it with the credential's own sentence.
		{"func validateSessionParams(", `strings.ContainsAny(c.Type, " \t\n,") || !noNUL(c.Type)`,
			"func (c PaymentCredential) Validate() error {", `strings.ContainsAny(c.Type, " \t\n,\x00")`},
		{"func validateAddress(a *Address) error {", "if !noNUL(a.Name, a.Line1, a.Line2, a.City, a.PostalCode, a.Region) { return errNUL() }",
			"func (a PaymentAddress) Validate() error {", "if !noNUL(a.Name, a.Line1, a.Line2, a.City, a.PostalCode, a.Region) { return errNUL() }"},
		{"func validateDisputeReport(r DisputeReport) error {", "case !noNUL(r.Reason): return errNUL()",
			"func (r PaymentDisputeReport) Validate() error {", "if !noNUL(r.Reason) { return errNUL() }"},
		{"func validateExternalRefund(r ExternalRefund) error {", "if !noNUL(r.Reason) { return errNUL() } return validateProviderRef(r.ProviderRef)",
			"func (r PaymentExternalRefund) Validate() error {", "if !noNUL(r.Reason) { return errNUL() } return validProviderRef(r.ProviderRef)"},
		{"func validateChargeParams(p ChargeParams) error {", "if !noNUL(p.Reference, p.Description) { return errNUL() }",
			"func (p PaymentChargeParams) Validate() error {", "if !noNUL(p.Reference, p.Description) { return errNUL() }"},
		{"func validateRefundParams(p RefundParams) error {", "if !noNUL(p.Reason) { return errNUL() }",
			"func (p PaymentRefundParams) Validate() error {", "if !noNUL(p.Reason) { return errNUL() }"},
		{"func validateResolution(r Resolution) error {", "if !noNUL(r.Saved.Label) { return errNUL() }",
			"func (r PaymentResolution) Validate() error {", "if !noNUL(r.Saved.Label) { return errNUL() }"},
		{"func validateRejection(r Rejection) error {", "if !noNUL(r.FailureMessage) { return errNUL() } return validateProviderRef(r.ProviderRef)",
			"func (r PaymentRejection) Validate() error {", "if !noNUL(r.FailureMessage) { return errNUL() } return validProviderRef(r.ProviderRef)"},
		{"func validateProviderRef(ref string) error {", "if !noNUL(ref) { return errNUL() }",
			"func validProviderRef(ref string) error {", "if !noNUL(ref) { return errNUL() }"},
		{"func validateIdempotencyKey(key string) error {", "if !noNUL(key) { return errNUL() }",
			"func validIdempotencyKey(key string) error {", "case !noNUL(key): return errNUL()"},
		// What a gateway answers: Core refuses the whole answer, the dispatch says which field.
		{"func validMethods(methods []Method) bool {", "!noNUL(m.Label, m.Description)",
			"func validMethods(methods []PaymentMethod) error {", "case !noNUL(m.Key, m.Label, m.Description):"},
		{"func validMethods(methods []Method) bool {", "!noNUL(hint)",
			"func validMethods(methods []PaymentMethod) error {", "if !noNUL(hint) {"},
		{"func validMethods(methods []Method) bool {", "for _, ct := range m.CredentialTypes { if !token(ct) {",
			"func validMethods(methods []PaymentMethod) error {", "for _, ct := range m.CredentialTypes { if !noNUL(ct) {"},
		{"func validMethods(methods []Method) bool {", "for _, sup := range m.Supports { if !token(sup) {",
			"func validMethods(methods []PaymentMethod) error {", `strings.ContainsAny(sup, " \t\n,\x00")`},
		{"func validStart(r startResult) bool {", "noNUL(r.FailureMessage, r.ProviderRef)",
			"func validStart(r PaymentStartResult) error {", "if !noNUL(r.FailureMessage) { return errNUL() } return validProviderRef(r.ProviderRef)"},
		{"func validRefundResult(r refundResult) bool {", "noNUL(r.FailureMessage, r.ProviderRef)",
			"func validRefundResult(r PaymentRefundResult) error {", "if !noNUL(r.FailureMessage) { return errNUL() } return validProviderRef(r.ProviderRef)"},
	} {
		if !strings.Contains(body(core, c.coreDecl, cv), c.coreWant) {
			t.Errorf("core's %s no longer has %q — re-read the SDK's %s", c.coreDecl, c.coreWant, c.sdkDecl)
		}
		if !strings.Contains(body(sdk, c.sdkDecl, sv), c.sdkWant) {
			t.Errorf("the SDK's %s no longer has %q, its half of Core's %q", c.sdkDecl, c.sdkWant, c.coreWant)
		}
	}
	// Core's token(), which its methods' key, capabilities and credential types go through, holds no NUL either.
	if !strings.Contains(body(core, "func token(v string) bool {", cv), `!strings.ContainsAny(v, " \t\n,\x00")`) {
		t.Error("core's token() no longer refuses a NUL — re-read the SDK's validMethods")
	}
	// nildatest: a pending or refund-resolve report's provider_ref (Core's validateProviderRef, through MarkPending);
	// every other body goes through the SDK's Validate above.
	if !strings.Contains(body(kit, "func checkReport(verb string, body []byte) error {", "nildatest/payments.go"),
		`if strings.IndexByte(r.ProviderRef, 0) >= 0 { return invalid("text in a payment may not hold a NUL character") }`) {
		t.Error("nildatest's checkReport no longer refuses a NUL in a provider_ref, as Core's validateProviderRef does")
	}
	doc := flat(readText(t, "docs/PAYMENTS.md"))
	if !strings.Contains(doc, "a NUL character in any text") {
		t.Error("PAYMENTS.md no longer says Validate refuses a NUL character in any text, as Core does")
	}
}

// TestTheHoldNumbersAreCores holds a hold's numbers and keys (v0.58.0) to Core's capture.go, where Core applies them:
// seven days when the gateway names no deadline, thirty at most, three days' wait for a gateway that cannot look, and
// the idempotency keys a capture and a void go out under — and to nildatest and PAYMENTS.md, which say the same.
func TestTheHoldNumbersAreCores(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	core := flat(corePaymentsSource(t, "internal", "payments", "capture.go"))
	kit := flat(readText(t, "nildatest/paymentcapture.go"))
	for _, want := range []string{"holdLifetime = 7 * 24 * time.Hour", "maxHoldLifetime = 30 * 24 * time.Hour"} {
		if !strings.Contains(core, want) {
			t.Errorf("core's capture.go no longer has %q — re-read PAYMENTS.md §7", want)
		}
		if !strings.Contains(kit, want) {
			t.Errorf("nildatest's paymentcapture.go no longer has %q, Core's", want)
		}
	}
	if !strings.Contains(core, "holdGrace = 3 * 24 * time.Hour") {
		t.Error("core's capture.go no longer waits three days for a gateway that cannot look — re-read PAYMENTS.md §7")
	}
	if maxHoldLifetime != 30*24*time.Hour {
		t.Errorf("the SDK's maxHoldLifetime is %v; Core's is thirty days", maxHoldLifetime)
	}
	consts := constValues(corePaymentsSource(t, "internal", "payments", "capture.go"))
	if consts["captureKeyPrefix"] != CaptureKeyPrefix || consts["voidKeyPrefix"] != VoidKeyPrefix {
		t.Errorf("core asks under %q and %q; the SDK tells a gateway %q and %q", consts["captureKeyPrefix"],
			consts["voidKeyPrefix"], CaptureKeyPrefix, VoidKeyPrefix)
	}
	doc := flat(readText(t, "docs/PAYMENTS.md"))
	for _, want := range []string{
		"| a hold the gateway gives no deadline | 7 days |",
		"| a hold's deadline | at most 30 days |",
		"| a hold past its deadline whose gateway cannot look | 3 days, then closed `expired` |",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("PAYMENTS.md §7 does not say %q", want)
		}
	}
}

// TestAHoldReportsRulesAreCores holds a hold report's rules (the P2 review) to Core's Authorize in capture.go, to
// nildatest's authorize and to PAYMENTS.md: a deadline already past is now; a capture or void asked decides the hold; a
// session held for a person is refused; the same hold again keeps a deadline it names.
func TestAHoldReportsRulesAreCores(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	core := flat(corePaymentsSource(t, "internal", "payments", "capture.go"))
	kit := flat(readText(t, "nildatest/paymentcapture.go"))
	for _, c := range []struct{ core, kit, rule string }{
		{"case !at.After(now): return now", "case !at.After(now): return now", "a deadline already past is kept as now"},
		{"if cur.CaptureRequestedMinor != nil || cur.VoidRequested { if cur.Status == StatusAuthorized { return false, nil",
			"if s.CaptureRequestedMinor > 0 || s.VoidRequested { if s.Status == nilda.PaymentAuthorized { return *s, nil",
			"a capture or void asked decides the hold"},
		{"if held(cur.Status, cur.FailureCode) { return false, apperr.Conflict(",
			"if s.Status == nilda.PaymentPending && s.FailureCode == nilda.PaymentFailAmountMismatch { return nil, &apiError{http.StatusConflict",
			"a session held for a person refuses a hold"},
		{"if a.ExpiresAt == nil || !newDeadline(now, cur.AuthorizationExpiresAt, deadline) { return false, nil",
			"if a.ExpiresAt == nil || !newDeadline(now, s.AuthorizationExpiresAt, deadline) { return *s, nil",
			"the same hold again keeps a deadline it names"},
	} {
		if !strings.Contains(core, c.core) {
			t.Errorf("core's capture.go no longer says %s (%q) — re-read PAYMENTS.md §4", c.rule, c.core)
		}
		if !strings.Contains(kit, c.kit) {
			t.Errorf("nildatest's paymentcapture.go no longer says %s, as Core does", c.rule)
		}
	}
	doc := flat(readText(t, "docs/PAYMENTS.md"))
	for _, want := range []string{
		"now when the deadline you give has already passed",
		"on a session already `authorized` as on one not yet held",
		"**The same hold again** is a no-op — unless it names a deadline other than the one Core keeps and no capture or void was asked yet",
		"**Once a capture or void was asked, a hold report moves nothing**",
		"**A session held for a person** (`pending`, `amount_mismatch`: the processor reported taking another amount) is money taken: a hold on it is refused (409)",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("PAYMENTS.md §4 does not say %q", want)
		}
	}
}

// Door D5 (v0.58.0) held to Core: the two kinds of merchant-initiated charge and the rule that picks one from the
// mandate, stamped by Core on every charge of a kept method; the in-person token and the readers' bounds; and what a void
// may let go — read where Core decides each (internal/payments/inperson.go, saved.go, capture.go), with the kit's half.
func TestDoorD5sRulesAreCores(t *testing.T) {
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	inperson := flat(corePaymentsSource(t, "internal", "payments", "inperson.go"))
	saved := flat(corePaymentsSource(t, "internal", "payments", "saved.go"))
	capture := flat(corePaymentsSource(t, "internal", "payments", "capture.go"))
	kit := flat(readText(t, "nildatest/paymentinperson.go"))
	kitSaved := flat(readText(t, "nildatest/paymentsaved.go"))
	kitCapture := flat(readText(t, "nildatest/paymentcapture.go"))
	sdkSaved := flat(readText(t, "paymentsaved.go"))
	sdkInPerson := flat(readText(t, "paymentinperson.go"))
	kind := "case MandateSubscription: return OffSessionRecurring case MandatePreOrder: return OffSessionUnscheduled } " +
		"if replaced == OffSessionRecurring || replaced == OffSessionUnscheduled { return replaced } " +
		"if m.Interval != \"\" { return OffSessionRecurring } return OffSessionUnscheduled"
	plain := `return unicode.IsControl(r) || (r >= '\u202a' && r <= '\u202e') || (r >= '\u2066' && r <= '\u2069')`
	for _, c := range []struct{ src, file, want string }{
		{inperson, "inperson.go", `OffSessionRecurring = "` + OffSessionRecurring + `" OffSessionUnscheduled = "` + OffSessionUnscheduled + `"`},
		// The kind is the mandate's purpose (review 2026-11-05), the SDK's rule word for word.
		{inperson, "inperson.go", strings.NewReplacer("MandateSubscription", "mandateSubscription", "MandatePreOrder", "mandatePreOrder").Replace(kind)},
		{sdkSaved, "paymentsaved.go", kind},
		// A new mandate cannot steer it: the same refusals, in the same words.
		{inperson, "inperson.go", `case m.Purpose == mandatePreOrder && (m.Interval != "" || m.IntervalCount != 0): return apperr.ValidationMsg("a pre-order's agreement is one charge when it ships; it names no interval", nil)`},
		{sdkSaved, "paymentsaved.go", `case m.Purpose == MandatePreOrder && (m.Interval != "" || m.IntervalCount != 0): return fmt.Errorf("nilda: a pre-order's agreement is one charge when it ships; it names no interval")`},
		{inperson, "inperson.go", `return apperr.ValidationMsg("an update names the kept payment method whose agreement it replaces", nil)`},
		{sdkSaved, "paymentsaved.go", `return fmt.Errorf("nilda: an update names the kept payment method whose agreement it replaces")`},
		{inperson, "inperson.go", `return apperr.ValidationMsg("only an update replaces a kept payment method's agreement", nil)`},
		{sdkSaved, "paymentsaved.go", `return fmt.Errorf("nilda: only an update replaces a kept payment method's agreement")`},
		{inperson, "inperson.go", "if (m.Interval != \"\") != (kind == OffSessionRecurring) {"},
		{kit, "nildatest/paymentinperson.go", "if (m.Interval != \"\") != (p.chargeKind(replaced) == nilda.OffSessionRecurring) {"},
		{saved, "saved.go", "kind, err := chargeKind(ctx, q, kept)"},
		{saved, "saved.go", "OffSession: kind,"},
		{kitSaved, "nildatest/paymentsaved.go", "kind := p.chargeKind(m)"},
		{kitSaved, "nildatest/paymentsaved.go", "OffSession: kind,"},
		{inperson, "inperson.go", "maxReplaced = 16"},
		{kit, "nildatest/paymentinperson.go", "maxReplaced = 16"},
		{inperson, "inperson.go", `const SupportCardPresent = "` + SupportCardPresent + `"`},
		{inperson, "inperson.go", "maxReaders = " + itoa(maxReaders) + " maxReaderLabel = " + itoa(maxReaderLabel)},
		// A reader's name is one plain line, on both sides.
		{inperson, "inperson.go", "return strings.TrimSpace(id) != \"\" && len(id) <= maxProviderRef && plainLine(id)"},
		{inperson, "inperson.go", plain},
		{sdkInPerson, "paymentinperson.go", plain},
		{sdkInPerson, "paymentinperson.go", "if strings.TrimSpace(id) == \"\" || len(id) > maxProviderRef || !plainLine(id) {"},
		{inperson, "inperson.go", "utf8.RuneCountInString(r.Label) > maxReaderLabel || !plainLine(r.Label) {"},
		{sdkInPerson, "paymentinperson.go", "utf8.RuneCountInString(r.Label) > maxReaderLabel || !plainLine(r.Label) {"},
		// What a void may let go, and when it is no longer owed.
		{inperson, "inperson.go", "return cur.Status == StatusAuthorized || (cur.Status == StatusPending && cur.Reader != \"\" && cur.FailureCode != FailAmountMismatch)"},
		{capture, "capture.go", "case !voidable(cur): return false, conflict(cur.Status)"},
		{capture, "capture.go", "return voidable(cur) && cur.VoidRequested &&"},
		{capture, "capture.go", "if cur.Status != StatusAuthorized && !(cur.VoidRequested && voidable(cur)) {"},
		{kitCapture, "nildatest/paymentcapture.go", "if s.Status == nilda.PaymentAuthorized || (s.VoidRequested && voidable(*s)) { return false }"},
		{capture, "capture.go", "case cur.Status == StatusCreated && cur.Reader != \"\":"},
		{kitCapture, "nildatest/paymentcapture.go", "case s.Status == nilda.PaymentCreated && s.Reader != \"\":"},
		{kit, "nildatest/paymentinperson.go", "return s.Status == nilda.PaymentAuthorized || (s.Status == nilda.PaymentPending && s.Reader != \"\" && s.FailureCode != nilda.PaymentFailAmountMismatch)"},
		// A reader left waiting: Core's figures, the kit's too.
		{inperson, "inperson.go", "readerWait = 5 * time.Minute"},
		{kit, "nildatest/paymentinperson.go", "readerWait = 5 * time.Minute"},
		{inperson, "inperson.go", "maxReaderWait = 30 * time.Minute"},
		{kit, "nildatest/paymentinperson.go", "maxReaderWait = 30 * time.Minute"},
		{inperson, "inperson.go", "maxReaderVoidAsks = 8"},
		{kit, "nildatest/paymentinperson.go", "maxReaderVoidAsks = 8"},
		{capture, "capture.go", "next.HoldAskAttempts >= maxReaderVoidAsks"},
		{kitCapture, "nildatest/paymentcapture.go", "p.holdAsks[id] >= maxReaderVoidAsks"},
	} {
		if !strings.Contains(c.src, c.want) {
			t.Errorf("%s no longer says %q — re-read door D5 on both sides", c.file, c.want)
		}
	}
	// The SDK's rule is the one Core's words state: the purpose decides; an update is the kind it replaces.
	if OffSessionOf(PaymentMandate{Purpose: MandatePreOrder, Interval: IntervalWeek}, "") != OffSessionUnscheduled ||
		OffSessionOf(PaymentMandate{Purpose: MandateSubscription}, "") != OffSessionRecurring ||
		OffSessionOf(PaymentMandate{Purpose: MandateUpdate}, OffSessionRecurring) != OffSessionRecurring {
		t.Error("OffSessionOf no longer reads the mandate's purpose as Core does")
	}
	if maxProviderRef != 255 {
		t.Errorf("a reader's id is bounded by maxProviderRef, now %d; PAYMENTS.md §7 says 255 bytes", maxProviderRef)
	}
	doc := flat(readText(t, "docs/PAYMENTS.md"))
	if want := "| a method's `readers` | at most " + itoa(maxReaders) + ", each id at most 255 bytes, each label 1–" +
		itoa(maxReaderLabel) + " characters |"; !strings.Contains(doc, want) {
		t.Errorf("PAYMENTS.md §7 does not say %q", want)
	}
}

// The page bridge PAYMENTS.md teaches a gateway's express script is Core's as built (internal/pagebuilder/
// expresswidget.go, pbExpressRuntime): where the script registers, the event that tells the bridge it did, and every
// member of the host its mount is handed — a member the guide names and Core's host lacks is a call that throws on the
// payer's page. What the bridge forwards to the shop with complete is held too: a field the SDK asks for that the bridge
// drops reaches no shop (the one Core has not landed is in coreahead_test.go).
func TestTheExpressBridgeIsCores(t *testing.T) {
	// The bridge is one Go string written as many: join its pieces back into the script the page runs.
	src := regexp.MustCompile("`\\s*\\+\\s*`").ReplaceAllString(coreSource(t, "internal", "pagebuilder", "expresswidget.go"), "")
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	for _, s := range []string{"window.nilda.express.gateways", "nilda:express-gateway"} {
		if !strings.Contains(doc, s) {
			t.Errorf("PAYMENTS.md does not teach %s", s)
		}
	}
	if !strings.Contains(src, "N=window.nilda=window.nilda||{},X=N.express=N.express||{},G=X.gateways=X.gateways||{}") {
		t.Error("core's bridge no longer reads window.nilda.express.gateways — repoint this guard and the guide")
	}
	if !strings.Contains(src, "document.addEventListener('nilda:express-gateway'") {
		t.Error("core's bridge no longer listens for nilda:express-gateway — repoint this guard and the guide")
	}
	block := upTo(t, src[strings.Index(src, "return {element:"):], "function load(", "core's bridge host")
	var core []string
	for _, m := range regexp.MustCompile(`(?:\{|,)(\w+):`).FindAllStringSubmatch(block, -1) {
		core = append(core, m[1])
	}
	const marker = "The host is `{"
	i := strings.Index(doc, marker)
	if i < 0 {
		t.Fatalf("PAYMENTS.md no longer lists %q", marker)
	}
	var listed []string
	for _, f := range strings.Split(upTo(t, doc[i+len(marker):], "}", "PAYMENTS.md's host"), ",") {
		listed = append(listed, strings.TrimSpace(f))
	}
	if got, want := strings.Join(core, ","), strings.Join(listed, ","); got != want {
		t.Errorf("core's bridge hands a mount %s; PAYMENTS.md lists %s", got, want)
	}
	for _, f := range []string{"shipping_option", "shown_total"} {
		if !strings.Contains(src, f) && !coreLacks("bridge "+f) {
			t.Errorf("core's bridge never sends %s to the shop", f)
		}
	}
}
