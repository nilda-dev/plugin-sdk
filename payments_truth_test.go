package nilda

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
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

// The numbers PAYMENTS.md gives an author: fifteen seconds a gateway hook, the ordinary five for a consumer's,
// a day's life for a session its gateway gives no expiry.
func TestThePaymentNumbersInTheGuideAreCores(t *testing.T) {
	doc := strings.Join(strings.Fields(readText(t, "docs/PAYMENTS.md")), " ")
	for file, want := range map[string]string{
		"internal/plugin/payments.go":  "const PaymentHookBudget = 15 * time.Second",
		"internal/payments/service.go": "const sessionLifetime = 24 * time.Hour",
		"pkg/config/config.go":         `parseDuration("PLUGIN_CALL_TIMEOUT", 5*time.Second)`,
	} {
		if !strings.Contains(corePaymentsSource(t, strings.Split(file, "/")...), want) {
			t.Errorf("core's %s no longer has %q — re-read what PAYMENTS.md says about it", file, want)
		}
	}
	for _, want := range []string{
		"**15 seconds** for each payment hook, instead of the ordinary plugin call's 5",
		"| a gateway's hook | 15 seconds |",
		"| a session nothing decided | 24 hours, unless the gateway says |",
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

// The routes: every call PAYMENTS.md §6 lists is one Core registers, every route Core registers is listed, and
// every path the SDK's client builds is one of them.
func TestThePaymentRoutesAreCores(t *testing.T) {
	src := corePaymentsSource(t, "internal", "payments", "rest.go")
	core := map[string]bool{}
	for _, m := range regexp.MustCompile(`g\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"`).FindAllStringSubmatch(src, -1) {
		core[m[1]+" /payments"+m[2]] = true
	}
	if len(core) < 10 {
		t.Fatalf("found %d routes in core's rest.go — the parse is broken, repoint this guard", len(core))
	}
	doc := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| `(GET|POST) (/payments/[^` ?]+)").FindAllStringSubmatch(readText(t, "docs/PAYMENTS.md"), -1) {
		doc[m[1]+" "+strings.ReplaceAll(m[2], "{id}", ":id")] = true
	}
	for r := range doc {
		if !core[r] {
			t.Errorf("PAYMENTS.md lists %s; core registers no such route", r)
		}
	}
	for r := range core {
		if !doc[r] {
			t.Errorf("core registers %s; PAYMENTS.md does not list it", r)
		}
	}
	// A client path is a literal ("/payments/methods") or a prefix, an id and a suffix; a suffix held in a
	// variable (the refund verbs) must still lead to a route under that prefix.
	client := readText(t, "paymentapi.go")
	built := 0
	for _, m := range regexp.MustCompile(`"(/payments/[a-z/]*)"(?:, \w+, (?:"(/?[a-z]*)"|(\w+)))?`).FindAllStringSubmatch(client, -1) {
		path, exact := m[1], true
		if strings.HasSuffix(path, "/") {
			path += ":id"
			if m[3] != "" {
				exact = false
			} else {
				path += m[2]
			}
		}
		built++
		found := false
		for r := range core {
			route := r[strings.Index(r, " ")+1:]
			if (exact && route == path) || (!exact && strings.HasPrefix(route, path+"/")) {
				found = true
			}
		}
		if !found {
			t.Errorf("paymentapi.go builds %s; core registers no such route", path)
		}
	}
	if built < 9 {
		t.Fatalf("found %d paths in paymentapi.go — the parse is broken, repoint this guard", built)
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
