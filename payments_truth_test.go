package nilda

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// THE PAYMENT CONTRACT, HELD TO CORE (D-84). payment.go, paymentapi.go, nildatest.Payments and
// docs/PAYMENTS.md describe what Core does with a payment; each guard below reads the Core source that does it,
// so a change on either side fails here rather than in a gateway author's first live checkout. Core holds the
// same contract from its side against the tag it builds on (core/internal/payments/sdk_mirror_test.go); this
// file sees Core's WORKING source, so it also catches a Core change before Core moves to a new tag.

func corePaymentsSource(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "core"}, parts...)...))
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
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
	body := src[start:]
	body = body[:strings.Index(body, "\n\t}")]
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

	// The failure codes: the same seven, and the same five a gateway may report.
	all := []string{PaymentFailDeclined, PaymentFailCancelled, PaymentFailProviderUnavailable, PaymentFailInvalidRequest,
		PaymentFailExpired, PaymentFailAmountMismatch, PaymentFailConsumerRefused}
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
	block := src[strings.Index(src, "gatewayFailureCodes = map[string]bool{"):]
	block = block[:strings.Index(block, "}")]
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
	sdk := []string{HookPaymentDescribe, HookPaymentStart, HookPaymentRefund, HookPaymentConfirm,
		HookPaymentSessionUpdated, HookPaymentRefundUpdated}
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
	service, module := flat("internal", "payments", "service.go"), flat("internal", "payments", "module.go")
	config := flat("pkg", "config", "config.go")

	gateway := number(plugin, "internal/plugin/payments.go", `const PaymentHookBudget = ([0-9]+) \* time\.Second`)
	ordinary := number(config, "pkg/config/config.go", `parseDuration\("PLUGIN_CALL_TIMEOUT", ([0-9]+)\*time\.Second\)`)
	lifetime := number(service, "internal/payments/service.go", `const sessionLifetime = ([0-9]+) \* time\.Hour`)
	sweep := number(module, "internal/payments/module.go", `\{Spec: "@every ([0-9]+)m", JobType: JobExpire\}`)

	// Where each is applied: every hook call takes its budget from hookBudget, which gives the gateway's three
	// hooks PaymentHookBudget only when it is the longer — and nothing else; a session's expiry starts at
	// sessionLifetime from its creation.
	for _, c := range []struct{ src, file, want string }{
		{host, "internal/plugin/host.go", "callCtx, cancel := context.WithTimeout(ctx, hookBudget(hook, h.cfg.CallTimeout))"},
		{plugin, "internal/plugin/payments.go", "func hookBudget(hook string, ordinary time.Duration) time.Duration { " +
			"switch hook { case HookPaymentDescribe, HookPaymentStart, HookPaymentRefund: " +
			"if PaymentHookBudget > ordinary { return PaymentHookBudget } } return ordinary }"},
		{service, "internal/payments/service.go", "ExpiresAt: s.now().Add(sessionLifetime),"},
	} {
		if !strings.Contains(c.src, c.want) {
			t.Errorf("core's %s no longer has %q — re-read what PAYMENTS.md says about it", c.file, c.want)
		}
	}
	words := map[string]string{"5": "five", "10": "ten", "15": "fifteen"}
	for _, want := range []string{
		"**" + gateway + " seconds** for each payment hook, instead of the ordinary plugin call's " + ordinary,
		"the longer of " + gateway + " seconds and the site's `PLUGIN_CALL_TIMEOUT`",
		"| a gateway's hook | " + gateway + " seconds, or `PLUGIN_CALL_TIMEOUT` if that is longer |",
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
	for where, want := range map[string]struct{ src, text string }{
		"payments: the setting first":           {service, `if u := strings.TrimSpace(s.siteURL(ctx)); u != "" { return u }`},
		"payments: then APP_BASE_URL":           {service, "return s.appURL }"},
		"main.go: the setting is site.base_url": {main, "SiteURL: a.settingsSvc.SiteBaseURL"},
		"main.go: the fallback is the env":      {main, "AppBaseURL: a.cfg.AppBaseURL"},
		"main.go: GET /site reads the setting": {main, `stableAPI := stable.NewService(a.store, func(ctx context.Context) stable.Site { ` +
			`return stable.Site{ Title: a.settingsSvc.GetString(ctx, "site.title"), ` +
			`Description: a.settingsSvc.GetString(ctx, "site.description"), BaseURL: a.settingsSvc.GetString(ctx, "site.base_url"),`},
		"main.go: that is the REST API's": {main, "a.srv.Mount(rest.PathV1, stable.NewHandlers(stableAPI,"},
		"settings: SiteBaseURL's key":     {flat("internal", "settings", "browser.go"), `const keySiteBaseURL = "site.base_url"`},
		"stable: the field is base_url":   {flat("internal", "api", "stable", "contract.go"), "BaseURL string `json:\"base_url\"`"},
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
		fn := src[strings.Index(src, "func (h *RESTHandlers) "+m[3]+"(c *gin.Context) {"):]
		c := regexp.MustCompile(`caller\(c, apistandards\.(\w+), (side\w+)\)`).FindStringSubmatch(fn[:strings.Index(fn, "\n}")])
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
	for _, m := range regexp.MustCompile("(?m)^\\| `(GET|POST) (/payments/[^` ?]+)[^|]*\\| ([^|]+) \\|").FindAllStringSubmatch(docText, -1) {
		doc[m[1]+" "+strings.ReplaceAll(m[2], "{id}", ":id")] = strings.TrimSpace(m[3])
	}
	for r, who := range doc {
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
		path = strings.NewReplacer("/sid/", "/:id/", "/rid/", "/:id/").Replace(path + "/")
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
		"Methods": func() { _, _ = pay.Methods(ctx, "EUR") },
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
		if _, ok := core[r]; !ok {
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
		body := src[start:]
		body = body[:strings.Index(body, "\n}")]
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
		list := doc[i+len(marker):]
		list = list[:strings.Index(list, "}")]
		var listed []string
		for _, f := range strings.Split(list, ",") {
			listed = append(listed, strings.Trim(strings.TrimSpace(f), `"`))
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
