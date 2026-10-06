package nilda

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// THE DOCUMENTATION A PLUGIN AUTHOR — OR THEIR CODING AGENT — COPIES MUST NOT CLAIM WHAT THE CODE DOES NOT DO.
//
// The 2026-09-23 hunt confirmed six defects of one shape in this repository (C30–C35): a sentence or a snippet
// in a document that AGENTS.md sends every agent to, saying something false. A method that never existed, a
// manifest file the installer never reads, a signature entry the installer refuses, a sandbox nobody built, a
// shop walkthrough that leaves out the one event a shop cannot do without. Each read perfectly; none compiled
// against the truth.
//
// Every guard here reads the SOURCE of the truth it compares against — the *Core method set, Core's own package
// constants, the SDK's own commerce event — and never a literal copied from the document it checks, because a
// guard that copies what it checks agrees with the document by construction.

// mdGoBlock is a fenced Go code block in markdown.
var mdGoBlock = regexp.MustCompile("(?s)```go\\n(.*?)```")

// coreCall is a call through the conventional `core` variable — core.Emit(…), core.API() — which is how every
// example in this repository names the *Core a plugin is handed.
var coreCall = regexp.MustCompile(`\bcore\.([A-Z][A-Za-z0-9_]*)\(`)

// TestEveryDocumentedCoreMethodExists is C31.
//
// commerce.go's doc comment told every shop author to call core.EmitEvent — a method that never existed; it is
// Emit. TestDocumentedIdentifiersExist could not see it for two reasons at once: it checks `nilda.X`, and it
// reads markdown code blocks only, while this one sat in a Go doc comment. So this reads both: Go code blocks in
// every document, and the indented example lines of every doc comment in the package.
func TestEveryDocumentedCoreMethodExists(t *testing.T) {
	coreType := reflect.TypeOf((*Core)(nil))
	for _, src := range documentedCode(t) {
		for _, m := range coreCall.FindAllStringSubmatch(src.code, -1) {
			if _, ok := coreType.MethodByName(m[1]); !ok {
				t.Errorf("%s shows core.%s(…), and *Core has no method %s — an author copying it writes code "+
					"that does not compile, or deletes the call and loses what it was for", src.where, m[1], m[1])
			}
		}
	}
}

// TestNothingCallsFaultIsolationASecuritySandbox is C30.
//
// A plugin is a separate process running as the SAME operating-system user as Core: fault isolation, not a
// security boundary (NILDA_DESIGN_DECISIONS D-9; docs/PLUGIN_SDK.md §6). handshake.go and ANY_LANGUAGE.md still
// said "the sandbox is" the security, and a reader told that skips the capability scoping they would otherwise
// write, or tells a site owner a stranger's plugin is safe "because it runs in a sandbox". The word may appear
// only where it is being denied.
func TestNothingCallsFaultIsolationASecuritySandbox(t *testing.T) {
	denies := regexp.MustCompile(`(?i)not a security sandbox|older word`)
	// Every file an author or an agent reads, not only the root: the generated contract and its .proto, the
	// test kit, and the examples an author copies whole.
	files := sdkTextFiles(t)
	for _, pattern := range []string{"contract/*.proto", "contract/*.go", "nildatest/*.go", "_examples/*/*.go",
		"_examples/*/*.json", ".github/workflows/*.yml"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			t.Fatalf("%s matches nothing — the layout moved; repoint this guard", pattern)
		}
		files = append(files, matches...)
	}
	for _, f := range files {
		for i, line := range strings.Split(readText(t, f), "\n") {
			if strings.Contains(strings.ToLower(line), "sandbox") && !denies.MatchString(line) {
				t.Errorf("%s:%d says %q — there is no security sandbox; a plugin runs as the same OS user as "+
					"Core, and only a sentence denying the sandbox may use the word", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}

// TestThePackagingTheDocsDescribeIsTheOneCoreInstalls is C32 and C34.
//
// ANY_LANGUAGE.md told non-Go authors to ship `manifest.json`; PLUGIN_SDK.md said it three more times and
// described a `signature` entry INSIDE the archive. Core's installer reads exactly two entries and refuses
// anything else, and the signature travels beside the file. The truth is Core's own constants, read from its
// source when Core is checked out beside this repository — the same arrangement pins_test.go uses.
func TestThePackagingTheDocsDescribeIsTheOneCoreInstalls(t *testing.T) {
	path := filepath.Join("..", "core", "internal", "plugin", "schema_package.go")
	raw := coreSource(t, "internal", "plugin", "schema_package.go")
	constant := func(name string) string {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("%s no longer declares %s — repoint this guard rather than deleting it", path, name)
		}
		return m[1]
	}
	manifest, binary := constant("ManifestEntry"), constant("BinaryEntry")
	sigExt, pkgExt := constant("SignatureExt"), constant("PackageExt")
	signatureEntry := regexp.MustCompile(`(?m)^signature\s{2,}`) // the refused listing: "signature   the detached…"

	for _, doc := range []string{"README.md", "AGENTS.md", "docs/PLUGIN_SDK.md", "docs/ANY_LANGUAGE.md"} {
		text := readText(t, doc)
		for _, want := range []string{manifest, binary, pkgExt, sigExt} {
			if !strings.Contains(text, want) {
				t.Errorf("%s describes a plugin package without naming %q, which Core's installer requires", doc, want)
			}
		}
		if manifest != "manifest.json" && strings.Contains(text, "manifest.json") {
			t.Errorf("%s names `manifest.json`; Core's installer reads %q and nothing else", doc, manifest)
		}
		if signatureEntry.MatchString(text) {
			t.Errorf("%s lists a `signature` entry inside the archive; Core refuses any entry but %s and %s, "+
				"and the signature travels beside the file as <file>%s%s", doc, manifest, binary, pkgExt, sigExt)
		}
	}
}

// TestTheShopWalkthroughEmitsTheCatalogueEvent is C35.
//
// Nilda caches a page for an hour and cannot see a shop's catalogue change, so a shop MUST emit
// EventCommerceCatalogChanged — commerce.go says so. PLUGIN_SDK.md's shop walkthrough never mentioned it, and
// its manifest example could not have sent it: emitting needs `events`, and the example granted only
// ["commerce", "route"]. Both walkthroughs are held here: the markdown one and commerce.go's own doc comment.
func TestTheShopWalkthroughEmitsTheCatalogueEvent(t *testing.T) {
	const event = "EventCommerceCatalogChanged" // the identifier TestDocumentedIdentifiersExist proves exists
	caps := regexp.MustCompile(`"capabilities":\s*\[([^\]]*)\]`)

	section := mdSection(t, "docs/PLUGIN_SDK.md", "### Being the site's shop")

	for _, walk := range []struct{ where, text string }{
		{"docs/PLUGIN_SDK.md (shop walkthrough)", section},
		{"commerce.go (doc comment)", readText(t, "commerce.go")},
	} {
		if !strings.Contains(walk.text, event) {
			t.Errorf("%s never tells a shop author to emit %s — the storefront then serves stale prices "+
				"for up to an hour after every edit", walk.where, event)
		}
		found := caps.FindAllStringSubmatch(walk.text, -1)
		if len(found) == 0 {
			t.Errorf("%s shows no capabilities example — repoint this guard", walk.where)
		}
		for _, c := range found {
			if !strings.Contains(c[1], `"events"`) {
				t.Errorf("%s's manifest example grants [%s] without \"events\", so the catalogue event it "+
					"requires is refused with PermissionDenied", walk.where, c[1])
			}
		}
	}
}

// TestAKeyHoldsAcrossARestartOnlyUnderTheSameScopes holds the guide and api.go to Core's idempotency layer (its
// 2026-09-24 whole-plan review, I-11): a repeat is replayed only under the scopes the first request had, because
// Core writes the token's scopes into the fingerprint.
func TestAKeyHoldsAcrossARestartOnlyUnderTheSameScopes(t *testing.T) {
	raw := coreSource(t, "internal", "apistandards", "idempotency.go")
	if !strings.Contains(raw, `io.WriteString(h, "scopes "+strings.Join(sorted, " ")+"\n")`) {
		t.Error("core's idempotency fingerprint no longer carries the token's scopes — re-read \"under the same scopes\"")
	}
	for doc, want := range map[string]string{
		"docs/PLUGIN_SDK.md": "so they survive a restart — under the same scopes: a repeat made with different ones",
		"api.go":             "so they hold across a restart — under the same scopes: a repeat made with different ones is a different request (422)",
	} {
		text := strings.Join(strings.Fields(strings.ReplaceAll(readText(t, doc), "\n//", " ")), " ")
		if !strings.Contains(text, want) {
			t.Errorf("%s no longer says %q, which core's idempotency.go makes true", doc, want)
		}
	}
}

// TestTheGuideSaysAnAbilityIsNotToldWhoAsked holds the other half of C10 (Core's 2026-09-24 whole-plan review,
// I-8): the call carries the hook's name and its input and nothing about the person — the wire's own shape, so a
// field added to carry the person turns this red until the sentence is rewritten.
func TestTheGuideSaysAnAbilityIsNotToldWhoAsked(t *testing.T) {
	var fields []string
	rt := reflect.TypeOf(contract.HookRequest{})
	for i := range rt.NumField() {
		if f := rt.Field(i); f.IsExported() {
			fields = append(fields, f.Name)
		}
	}
	if strings.Join(fields, ",") != "Hook,Payload" {
		t.Fatalf("contract.HookRequest carries %v now, not only the hook and its input — re-read the guide's "+
			"\"What you are NOT told is who that person was\"", fields)
	}
	// …and Core sends only that: the ability's input as its payload, the request built from the two, and no gRPC
	// metadata beside it — the other way the person could travel without the wire's shape changing.
	hostGo := coreSource(t, "internal", "plugin", "host.go")
	for _, want := range []string{
		"return h.Call(ctx, key, AbilityHookPrefix+name, args)",
		"handle.Plugin.HandleHook(callCtx, &contract.HookRequest{Hook: hook, Payload: payload})",
	} {
		if !strings.Contains(hostGo, want) {
			t.Errorf("core's host.go no longer has %q — re-read the guide's \"What you are NOT told is who that person was\"", want)
		}
	}
	if strings.Contains(hostGo, "OutgoingContext") {
		t.Error("core's host.go puts gRPC metadata on a call — re-read whether a plugin is told who asked")
	}
	if doc := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " "); !strings.Contains(doc,
		"What you are NOT told is who that person was: the call carries the hook's name and its input, and nothing about the person") {
		t.Error("the guide no longer says an ability is not told who asked, which contract.HookRequest makes true")
	}
}

// TestTheAbilityGateTheDocsNameIsTheOneCoreApplies is the documentation half of the hunt's C10.
//
// Core runs a plugin's ability only for a person who holds one of plugin.ConfigurePermissions — the list the
// plugin's own admin section is gated on — and the abilities section tells an author who that is. The truth is
// Core's source, read when Core is checked out beside this repository, so a Core that changes the gate turns this
// red instead of leaving the sentence behind.
func TestTheAbilityGateTheDocsNameIsTheOneCoreApplies(t *testing.T) {
	pages := filepath.Join("..", "core", "internal", "plugin", "adminpages.go")
	src := coreSource(t, "internal", "plugin", "adminpages.go")

	// func AdminPermission(pluginKey string) string { return "plugin." + pluginKey + ".configure" }
	adm := regexp.MustCompile(`func AdminPermission\(\w+ string\) string \{\s*return "([^"]*)" \+ \w+ \+ "([^"]*)"\s*\}`).
		FindStringSubmatch(src)
	if adm == nil {
		t.Fatalf("%s no longer spells AdminPermission as prefix + key + suffix — repoint this guard", pages)
	}
	list := regexp.MustCompile(`(?s)func ConfigurePermissions\([^)]*\) \[\]string \{\s*return \[\]string\{([^}]*)\}\s*\}`).
		FindStringSubmatch(src)
	if list == nil {
		t.Fatalf("%s no longer returns ConfigurePermissions as one list literal — repoint this guard", pages)
	}
	perms := filepath.Join("..", "core", "internal", "authz", "permissions.go")
	manage := regexp.MustCompile(`\bPermPluginManage\s*=\s*"([^"]+)"`).FindStringSubmatch(coreSource(t, "internal", "authz", "permissions.go"))
	if manage == nil {
		t.Fatalf("%s no longer declares PermPluginManage — repoint this guard", perms)
	}

	var want []string
	for _, el := range strings.Split(list[1], ",") {
		switch el = strings.TrimSpace(el); {
		case el == "":
		case strings.HasPrefix(el, "AdminPermission("):
			want = append(want, adm[1]+"<key>"+adm[2])
		case el == "authz.PermPluginManage":
			want = append(want, manage[1])
		default:
			t.Fatalf("ConfigurePermissions now returns %s, which this guard cannot translate — teach it, and "+
				"then the abilities section", el)
		}
	}
	section := mdSection(t, "docs/PLUGIN_SDK.md", "### Telling the AI agent what you can do")
	for _, p := range want {
		if !strings.Contains(section, "`"+p+"`") {
			t.Errorf("the abilities section never names `%s`, which Core accepts as leave to run an ability", p)
		}
	}
}

// TestTheHookGrantsInitResultNamesAreCores holds InitResult.Hooks' doc comment — which grant admits which
// hook — to the switch in core's filterHooks (internal/plugin/renderassets.go), pair by pair. The comment
// once listed every grant but the two payment ones, and an author reading it could not learn that
// `payment_session`, not `hooks`, admits a consumer's hooks.
func TestTheHookGrantsInitResultNamesAreCores(t *testing.T) {
	dir := filepath.Join("..", "core", "internal", "plugin")
	src := coreSource(t, "internal", "plugin", "renderassets.go")
	// Every constant core's plugin package declares, so a case can be read by value.
	values := map[string]string{}
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^\s*(?:const\s+)?(\w+)\s*=\s*"([^"]*)"`)
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		for _, m := range decl.FindAllStringSubmatch(readText(t, f), -1) {
			values[m[1]] = m[2]
		}
	}
	body := upTo(t, from(t, src, "func filterHooks(", "core's renderassets.go"), "\n}\n", "core's filterHooks")
	if !strings.Contains(body, "allowed := contains(grants, CapHooks)") {
		t.Fatal("core's filterHooks no longer defaults to the `hooks` grant — re-read InitResult.Hooks")
	}
	grantOf := map[string]string{} // hook value -> capability value, from core
	// The assignment must END after its one contains(): a case that became `contains(grants, CapA) &&
	// contains(grants, CapB)` would otherwise read as CapA alone admitting the hook.
	caseRE := regexp.MustCompile(`(?s)^([A-Za-z0-9_,\s]+):.*?\n\s*allowed = contains\(grants, (Cap\w+)\)\n`)
	for _, chunk := range regexp.MustCompile(`(?m)^\t\tcase `).Split(body, -1)[1:] {
		m := caseRE.FindStringSubmatch(chunk)
		if m == nil || strings.Count(chunk, "allowed =") != 1 {
			t.Fatalf("a case of core's filterHooks does not read as `case Hooks…: allowed = contains(grants, Cap…)` — teach this guard:\n%s", chunk)
		}
		capability, ok := values[m[2]]
		if !ok {
			t.Fatalf("core's filterHooks grants by %s, which no constant in internal/plugin names", m[2])
		}
		for _, name := range strings.Split(m[1], ",") {
			hook, ok := values[strings.TrimSpace(name)]
			if !ok {
				t.Fatalf("core's filterHooks names %s, which no constant in internal/plugin names", name)
			}
			grantOf[hook] = capability
		}
	}
	if len(grantOf) < 20 {
		t.Fatalf("read %d hooks from core's filterHooks — the parse is broken, repoint this guard", len(grantOf))
	}

	// The comment's pairs: "<hooks> by `<grant>`", where <hooks> is a name, a family ("commerce.*") or names
	// joined by "/".
	serve := readText(t, "serve.go")
	c := upTo(t, from(t, serve, "// Hooks are the hook names to receive.", "serve.go"), "Hooks  []string", "serve.go's InitResult")
	c = strings.Join(strings.Fields(strings.ReplaceAll(c, "//", " ")), " ")
	said := map[string]string{} // pattern -> capability
	for _, m := range regexp.MustCompile("([a-z][a-z._*/]*) by `([a-z_.]+)`").FindAllStringSubmatch(c, -1) {
		for _, p := range strings.Split(m[1], "/") {
			said[p] = m[2]
		}
	}
	matches := func(pattern, hook string) bool {
		if fam, ok := strings.CutSuffix(pattern, ".*"); ok {
			return strings.HasPrefix(hook, fam+".")
		}
		return pattern == hook
	}
	for hook, capability := range grantOf {
		found := false
		for pattern, sdk := range said {
			if !matches(pattern, hook) {
				continue
			}
			found = true
			if sdk != capability {
				t.Errorf("core admits %s by `%s`; InitResult.Hooks says `%s`", hook, capability, sdk)
			}
		}
		if !found {
			t.Errorf("core admits %s by `%s`, and InitResult.Hooks never says which grant admits it", hook, capability)
		}
	}
	for pattern := range said {
		used := false
		for hook := range grantOf {
			used = used || matches(pattern, hook)
		}
		if !used {
			t.Errorf("InitResult.Hooks names %s, which core's filterHooks has no case for", pattern)
		}
	}
	if !strings.Contains(c, "require `hooks`") {
		t.Error("InitResult.Hooks no longer says the content-lifecycle hooks require `hooks`, core's default")
	}
}

// TestTheKVLimitsTheDocsNameAreCores holds the capability table's `kv` row to Core's own caps (the 2026-09-23
// plugin hunt's C19), read from core's internal/plugin/kvquota.go.
func TestTheKVLimitsTheDocsNameAreCores(t *testing.T) {
	path := filepath.Join("..", "core", "internal", "plugin", "kvquota.go")
	raw := coreSource(t, "internal", "plugin", "kvquota.go")
	num := func(name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9_]+)(?:\s*<<\s*([0-9]+))?`).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("%s no longer declares %s as a number — repoint this guard", path, name)
		}
		n, _ := strconv.Atoi(strings.ReplaceAll(m[1], "_", ""))
		if m[2] != "" {
			shift, _ := strconv.Atoi(m[2])
			n <<= shift
		}
		return n
	}
	var row string
	for _, line := range strings.Split(readText(t, "docs/PLUGIN_SDK.md"), "\n") {
		if strings.HasPrefix(line, "| `kv` |") {
			row = line
		}
	}
	if row == "" {
		t.Fatal("docs/PLUGIN_SDK.md has no `kv` row in its capability table — repoint this guard")
	}
	for _, want := range []string{
		strconv.Itoa(num("maxKVValueBytes")>>10) + " KiB",
		addThousands(num("maxKVKeysPerPlugin")) + " keys",
		strconv.Itoa(num("maxKVBytesPerPlugin")>>20) + " MiB",
	} {
		if !strings.Contains(row, want) {
			t.Errorf("the kv row does not say %q, which is Core's cap:\n%s", want, row)
		}
	}
	// WHERE they hold: every install since Core keeps the count on Dragonfly too (its kvshared.go; the 2026-09-24
	// review's B-F5). The row said "on a Lite install", and a plugin on Dragonfly met ResourceExhausted it was
	// told it would not.
	// Held to the call that keeps them there, not to the file existing (the review of 384e49c): Set on
	// Dragonfly admits the write against the caps before it writes. And the one way past them, said as Core says it.
	shared := strings.Join(strings.Fields(strings.ReplaceAll(coreSource(t, "internal", "plugin", "kvshared.go"), "\n//", "\n")), " ")
	if !strings.Contains(shared, "old, err := k.admitShared(ctx, name, size)") ||
		!strings.Contains(shared, "keys > maxKVKeysPerPlugin || bytes > maxKVBytesPerPlugin { return 0, ErrKVFull") {
		t.Error("core's Dragonfly kv no longer admits a write against the key and byte caps — re-read the kv row's \"on every install\"")
	}
	if !strings.Contains(row, "on every install") {
		t.Errorf("the kv row does not say the key and byte caps hold on every install, as Core keeps them:\n%s", row)
	}
	if !strings.Contains(shared, "writes racing each other at the very edge can pass it together") ||
		!strings.Contains(row, "where writes racing each other at the very edge can pass it together") {
		t.Errorf("the kv row and core's kvshared.go no longer say the same about writes racing at the edge:\n%s", row)
	}
}

// addThousands writes 10000 as 10,000.
func addThousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// mdSection is one `### ` section of a markdown document, heading included, up to the next `### `.
func mdSection(t *testing.T, path, heading string) string {
	t.Helper()
	doc := readText(t, path)
	start := strings.Index(doc, heading)
	if start < 0 {
		t.Fatalf("%s has no %q section — repoint this guard", path, heading)
	}
	section := doc[start:]
	if next := strings.Index(section[1:], "\n### "); next >= 0 {
		section = section[:next+1]
	}
	return section
}

type codeSample struct{ where, code string }

// documentedCode is every piece of example code a reader is shown: the Go blocks of every markdown document,
// and the indented example lines of every doc comment in this package.
func documentedCode(t *testing.T) []codeSample {
	t.Helper()
	var out []codeSample
	for _, f := range sdkTextFiles(t) {
		text := readText(t, f)
		if strings.HasSuffix(f, ".md") {
			for _, b := range mdGoBlock.FindAllStringSubmatch(text, -1) {
				out = append(out, codeSample{f, b[1]})
			}
			continue
		}
		var lines []string
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "//\t") {
				lines = append(lines, strings.TrimPrefix(line, "//\t"))
			}
		}
		out = append(out, codeSample{f, strings.Join(lines, "\n")})
	}
	return out
}

// sdkTextFiles is every document and every non-test Go source file at the root of this module, plus docs/.
func sdkTextFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, pattern := range []string{"*.md", "docs/*.md", "*.go"} {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				out = append(out, m)
			}
		}
	}
	sort.Strings(out)
	if len(out) < 10 {
		t.Fatalf("found only %d files to read — the layout moved; repoint this guard", len(out))
	}
	return out
}

func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// M20 (2026-09-23 plugin hunt): the SDK told authors Core strips `class` and `data-*` from a widget's HTML,
// while Core renders plugin widgets with its COMPONENT policy, which keeps both on purpose. An author who
// believed the doc stopped using the two hooks the policy exists to preserve. Held to Core's source: the call
// in Render, AND the policy lines that make the sentence true — a policy that stopped allowing `class` or
// `data-*`, or started allowing a style or a script, would leave the call in place and the doc wrong.
func TestTheWidgetSanitizerTheDocsDescribeIsCores(t *testing.T) {
	raw := coreSource(t, "internal", "plugin", "widgets.go")
	if !strings.Contains(raw, "security.SanitizeComponentHTML(res.HTML)") {
		t.Fatal("core's WidgetCatalog.Render no longer sanitizes with the component policy — re-read what the docs say is kept")
	}
	policy := readText(t, filepath.Join("..", "core", "pkg", "security", "sanitize.go"))
	for _, want := range []string{
		"componentPolicy = newComponentPolicy()",
		"func SanitizeComponentHTML(html string) string { return componentPolicy.Sanitize(html) }",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("core's sanitize.go no longer has %q — SanitizeComponentHTML may not be the policy the docs describe", want)
		}
	}
	body := func(fn string) string {
		start := strings.Index(policy, "func "+fn+"() *bluemonday.Policy {")
		if start < 0 {
			t.Fatalf("core's sanitize.go no longer declares %s — repoint this guard", fn)
		}
		return upTo(t, policy[start:], "\n}", "core's "+fn)
	}
	component, rich := body("newComponentPolicy"), body("newRichPolicy")
	for _, want := range []string{"p := newRichPolicy()", `p.AllowAttrs("class").Globally()`, "p.AllowDataAttributes()"} {
		if !strings.Contains(component, want) {
			t.Errorf("core's component policy no longer has %q — the docs say `class` and `data-*` are kept", want)
		}
	}
	if !strings.Contains(rich, "p := bluemonday.UGCPolicy()") {
		t.Error("core's rich policy no longer starts from bluemonday's UGC policy — re-read what the docs say is removed")
	}
	for _, refused := range []string{"AllowStyles", "AllowUnsafe", `"style"`, `"script"`, `"iframe"`, `AllowAttrs("on`} {
		if strings.Contains(component+rich, refused) {
			t.Errorf("core's widget policy now contains %s — the docs say scripts, iframes, on* handlers and style are removed", refused)
		}
	}
	for _, file := range []string{"widgets.go", "docs/PLUGIN_SDK.md"} {
		text := readText(t, file)
		if !strings.Contains(text, "`class` and `data-*` are") || strings.Contains(text, "`data-*` are stripped") ||
			strings.Contains(text, "and data-* are\n\t// stripped") {
			t.Errorf("%s does not say that `class` and `data-*` are kept", file)
		}
	}
}

// M14 (2026-09-23 plugin hunt): field.go told authors a long choices list is "trimmed rather than refused".
// Core trims only what it has already read, and refuses an answer past its byte ceiling outright. The numbers
// the doc names are held to Core's constants here.
func TestTheChoicesBoundsTheDocsNameAreCores(t *testing.T) {
	path := filepath.Join("..", "core", "internal", "plugin", "fields.go")
	raw := coreSource(t, "internal", "plugin", "fields.go")
	num := func(name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9_]+)(?:\s*<<\s*([0-9]+))?`).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("%s no longer declares %s as a number — repoint this guard", path, name)
		}
		n, _ := strconv.Atoi(strings.ReplaceAll(m[1], "_", ""))
		if m[2] != "" {
			shift, _ := strconv.Atoi(m[2])
			n <<= shift
		}
		return n
	}
	doc := readText(t, "field.go")
	for _, want := range []string{
		"first " + addThousands(num("maxFieldChoices")),
		strconv.Itoa(num("maxChoiceLen")) + "\n\t// characters",
		strconv.Itoa(num("maxChoicesBytes")>>20) + " MiB is REFUSED",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("field.go's Choices doc does not say %q, which is what core's fields.go enforces", want)
		}
	}
}

// M17 (2026-09-23 plugin hunt): the SDK now names Core's two time budgets where an author meets them — an
// ability's Run, a search engine's calls, an upgrade step in Init. Each number is held to the default Core's
// config parses, so the sentence cannot outlive a changed default.
func TestTheTimeBudgetsTheDocsNameAreCores(t *testing.T) {
	raw := coreSource(t, "pkg", "config", "config.go")
	seconds := func(env string) string {
		m := regexp.MustCompile(`parseDuration\("` + env + `",\s*([0-9]+)\s*\*\s*time\.Second\)`).FindStringSubmatch(raw)
		if m == nil {
			t.Fatalf("core's config.go no longer defaults %s in seconds — repoint this guard", env)
		}
		return m[1]
	}
	call, init := seconds("PLUGIN_CALL_TIMEOUT"), seconds("PLUGIN_INIT_TIMEOUT")
	for file, want := range map[string]string{
		"abilities.go": call + " seconds by default (the site's PLUGIN_CALL_TIMEOUT)",
		"search.go":    call + " seconds by default (PLUGIN_CALL_TIMEOUT)",
		"upgrade.go":   init + " seconds by default, the site's PLUGIN_INIT_TIMEOUT",
	} {
		if !strings.Contains(strings.ReplaceAll(readText(t, file), "\n\t// ", " "), want) {
			t.Errorf("%s does not say %q, which is Core's default", file, want)
		}
	}
}

// M42 (2026-09-23 plugin hunt): the egress proxy bounds each plugin's connections and closes idle tunnels. The
// two numbers the network section names are held to Core's, read from core's resilience.go.
func TestTheEgressLimitsTheDocsNameAreCores(t *testing.T) {
	raw := coreSource(t, "internal", "plugin", "resilience.go")
	conns := regexp.MustCompile(`maxEgressPerPlugin\s*=\s*([0-9]+)\b`).FindStringSubmatch(raw)
	idle := regexp.MustCompile(`egressIdleTimeout\s*=\s*([0-9]+)\s*\*\s*time\.Minute`).FindStringSubmatch(raw)
	if conns == nil || idle == nil {
		t.Fatal("core's resilience.go no longer declares maxEgressPerPlugin / egressIdleTimeout as numbers — repoint this guard")
	}
	section := strings.Join(strings.Fields(mdSection(t, "docs/PLUGIN_SDK.md", "### 5.1 The package")), " ")
	for _, want := range []string{"at most " + conns[1] + " connections", "for " + idle[1] + " minutes"} {
		if !strings.Contains(section, want) {
			t.Errorf("the network section does not say %q, which is Core's bound", want)
		}
	}
}

// Core's 2026-09-24 whole-plan review, A-10: a request that names no plugin is challenged — 407 with a Basic
// Proxy-Authenticate — and both guides say so, where they said such a call was refused as "not declared".
func TestTheDocsSayAnUnnamedRequestIsChallenged(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "egress.go")
	if !strings.Contains(src, `w.Header().Set("Proxy-Authenticate", `+"`"+`Basic realm=`) || !strings.Contains(src, "http.StatusProxyAuthRequired") {
		t.Error("core's egress proxy no longer challenges a request that names no plugin — re-read the guides' 407 sentence")
	}
	for _, doc := range []string{"docs/PLUGIN_SDK.md", "docs/ANY_LANGUAGE.md"} {
		text := strings.Join(strings.Fields(readText(t, doc)), " ")
		if !strings.Contains(text, "`407 Proxy Authentication Required` with a Basic challenge") {
			t.Errorf("%s no longer says an unnamed request is challenged with 407, which core's egress.go does", doc)
		}
	}
}

// M13 (2026-09-23 plugin hunt): a field type's choices and validate requests now carry Locale. Core pins this
// SDK by tag, so its own mirror test cannot name the new field until the pin moves; until then this side
// holds Core to it — the field on Core's copy of both requests, under the same JSON name, and Core filling it
// from the request's resolved locale on both calls.
func TestTheFieldRequestsCarryCoresLocale(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "fields.go")
	for _, typ := range []string{"FieldChoicesRequest", "FieldValidateRequest"} {
		start := strings.Index(src, "type "+typ+" struct {")
		if start < 0 {
			t.Fatalf("core's fields.go no longer declares %s — repoint this guard", typ)
		}
		body := upTo(t, src[start:], "\n}", "core's "+typ)
		if !strings.Contains(body, "Locale string `json:\"locale,omitempty\"`") {
			t.Errorf("core's %s carries no Locale under the SDK's JSON name", typ)
		}
	}
	if n := strings.Count(src, "Locale: logger.LocaleFromContext(ctx)"); n != 2 {
		t.Errorf("core fills Locale on %d of its 2 field requests (choices, validate)", n)
	}
	for _, sdkType := range []any{FieldChoicesRequest{}, FieldValidateRequest{}} {
		if f, ok := reflect.TypeOf(sdkType).FieldByName("Locale"); !ok || f.Tag.Get("json") != "locale,omitempty" {
			t.Errorf("the SDK's %T has no Locale under json:\"locale,omitempty\"", sdkType)
		}
	}
}

// M23–M34 (2026-09-23 plugin hunt): what the admin_page section tells an author Core does and refuses — each
// sentence beside the Core source that makes it true, and each number held to Core's constant, so the section
// cannot keep a rule Core dropped or a bound Core moved.
func TestTheAdminPageRulesTheDocsNameAreCores(t *testing.T) {
	read := func(file string) string {
		return coreSource(t, "internal", "plugin", file)
	}
	manifest, report, list, tables := read("schema_manifest.go"), read("adminreport.go"), read("adminlist.go"), read("tables.go")
	num := func(src, name string) string {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9]+)\b`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("core no longer declares %s as a number — repoint this guard", name)
		}
		return m[1]
	}
	// Each marker is what DOES the thing the sentence says, not a name that survives its removal: the
	// primary key appears in the list's SELECT too, the reason constant outlives the branch that returns it,
	// and a function nothing calls creates no index. So the tie-break is held in the ORDER BY, the reason in
	// the timeout branch, and the index helper at the call that provisions the tables.
	flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	service := read("service.go")
	section := strings.Join(strings.Fields(mdSection(t, "docs/PLUGIN_SDK.md", "### Your own section of the admin")), " ")
	for _, c := range []struct {
		src    string
		marker *regexp.Regexp
		says   string
	}{
		{manifest, regexp.MustCompile(regexp.QuoteMeta("the 'admin_page' capability is declared but no admin page is")), "and the capability without a page"},
		{manifest, regexp.MustCompile(regexp.QuoteMeta("admin pages are declared but the 'admin_page' capability is not")), "`admin_pages` without the `admin_page` capability"},
		{manifest, regexp.MustCompile(regexp.QuoteMeta("is also a field of the settings page")), "One field key on two settings pages"},
		{flat(list), regexp.MustCompile(`if actionOutcomeUnknown\(err\) \{ ` +
			`return nil, apperr\.New\(apperr\.CodeConflict, [^{}]*\)\. WithDetails\(map\[string\]string\{"reason": ReasonActionOutcomeUnknown\}\) \}`),
			"Core cannot tell \"never started\" from \"finished, answer lost\""},
		// Which answers count as lost: the call's time running out, and the process going away mid-call
		// (Core's 2026-09-24 whole-plan review, A-4).
		{flat(list), regexp.MustCompile(`func actionOutcomeUnknown\(err error\) bool \{ if errors\.Is\(err, context\.DeadlineExceeded\) \|\| ` +
			`status\.Code\(err\) == codes\.DeadlineExceeded \{ return true \}.*case codes\.Unavailable, codes\.Canceled: return true`),
			"the call budget (`PLUGIN_CALL_TIMEOUT`) ran out, or your process stopped mid-call"},
		{flat(list), regexp.MustCompile(regexp.QuoteMeta(`if pk := primaryKeyOf(table); pk != "" && pk != page.OrderBy { ` +
			`order = append(order, pgx.Identifier{pk}.Sanitize()+" "+dir) }`)), "then by the table's primary key"},
		{flat(service), regexp.MustCompile(regexp.QuoteMeta("man.Tables = TablesWithListIndexes(man.Tables, man.AdminPages)") +
			`.*` + regexp.QuoteMeta("ProvisionDatastore(ctx, p.Key, p.Publisher, man.Tables,")), "Core creates one with your tables"},
	} {
		if !c.marker.MatchString(c.src) {
			t.Errorf("core no longer has %s — re-read the admin_page section's sentence %q", c.marker, c.says)
		}
		if !strings.Contains(section, c.says) {
			t.Errorf("the admin_page section no longer says %q, which core's %s makes true", c.says, c.marker)
		}
	}
	if !strings.Contains(tables, "func TablesWithListIndexes(") {
		t.Error("core's tables.go no longer declares TablesWithListIndexes — repoint this guard")
	}
	for _, want := range []string{
		"More than " + num(manifest, "maxAdminPages") + " pages",
		num(manifest, "maxPageFields") + " fields on a settings page",
		num(report, "maxReportParams") + " filters on a report page",
		num(manifest, "maxListColumns") + " columns or " + num(manifest, "maxListColumns") + " search columns",
		num(manifest, "maxRowActions") + " row actions",
		num(manifest, "maxSettingChoices") + " choices on a field",
		"longer than " + num(manifest, "maxDeclaredLabelLen") + " characters",
		// The strings M34 left unbounded (Core's 2026-09-24 whole-plan review, A-15).
		"help longer than " + num(manifest, "maxDeclaredHelpLen") + " characters",
		"and so is a placeholder, a choice or a page's icon",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the admin_page section does not say %q, which is Core's bound", want)
		}
	}
	// Each string the bounds sentences name is CHECKED — the constant alone held nothing: a placeholder, a
	// choice, a page's icon and help, a field's help; and a command's group and icon (the editor_commands
	// section says the same 120).
	for _, check := range []string{
		`tooLong(fw+".placeholder", fl.Placeholder)`,
		`tooLong(fw+".choices["+strconv.Itoa(k)+"]", ch)`,
		`tooLong(where+".icon", pg.Icon)`,
		`helpTooLong(where+".help", pg.Help)`,
		`helpTooLong(fw+".help", fl.Help)`,
		`tooLong(where+".group", c.Group)`,
		`tooLong(where+".icon", c.Icon)`,
		`if utf8.RuneCountInString(s) > maxDeclaredHelpLen {`,
	} {
		if !strings.Contains(manifest, check) {
			t.Errorf("core's schema_manifest.go no longer checks %s — re-read the guide's bound for it", check)
		}
	}
	guide := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " ")
	if !strings.Contains(guide, "one longer than "+num(manifest, "maxDeclaredLabelLen")+" characters (a `group`, an `icon`") {
		t.Error("the editor_commands section no longer says a command's group and icon are bounded as a label is")
	}
}

// S-6 (Core's 2026-09-24 whole-plan review): "an error you return reaches the owner as the reason the action
// did not run" was false — Core passed the plugin's error on raw, the owner read "internal error", and each
// refusal counted toward switching the plugin off. Held to Core's source both ways: the row action turns the
// plugin's own error into its words (bounded by the same 300 the sentence names), and the failure count asks
// refusalIsAnAnswer for the three calls a person makes.
func TestARowActionsRefusalIsTheOwnersReasonAndNotAFailure(t *testing.T) {
	read := func(name string) string {
		return coreSource(t, "internal", "plugin", name)
	}
	list, host, fields := read("adminlist.go"), read("host.go"), read("fields.go")
	for _, want := range []string{
		"if said := pluginRefusal(err); said != \"\" {\n\t\t\t\treturn nil, apperr.New(apperr.CodeConflict, \"the plugin did not run this action: \"+said).\n" +
			"\t\t\t\t\tWithDetails(map[string]string{\"reason\": ReasonActionRefused, \"said\": said})",
		"return trimTo(strings.TrimSpace(st.GRPCStatus().Message()), maxValidationMsgLen)",
		"if hook != AdminActionHook && hook != AdminReportHook && !strings.HasPrefix(hook, AbilityHookPrefix) {",
		"return callCtx.Err() == nil && status.Code(err) == codes.Unknown",
	} {
		if !strings.Contains(list, want) {
			t.Errorf("core's adminlist.go no longer has %q — re-read DispatchAdminAction's error sentence and ANY_LANGUAGE's failure count", want)
		}
	}
	if !strings.Contains(host, "askAgainIsAnAnswer(callCtx, hook, err) || refusalIsAnAnswer(callCtx, hook, err)") {
		t.Error("core's host.go no longer spares a person's refusal from the failure count — re-read ANY_LANGUAGE's sentence")
	}
	// The code Core reads as an answer, for the consumer's hooks as for a person's: Unknown and nothing else —
	// which is why Serve sends every handler error as Unknown and a panic as Internal (serve.go's asAnswer and
	// recoverCall, held by TestAHandlersErrorReachesCoreAsItsAnswer and TestAPanicInTheAuthorsCodeFailsTheCallNotTheProcess).
	if !strings.Contains(read("payments.go"), "return callCtx.Err() == nil && status.Code(callErr) == codes.Unknown") {
		t.Error("core's askAgainIsAnAnswer no longer reads Unknown as the consumer's answer — re-read serve.go's asAnswer and ANY_LANGUAGE's code")
	}
	if !regexp.MustCompile(`\bmaxValidationMsgLen\s*=\s*300\b`).MatchString(fields) {
		t.Error("core's refusal bound is no longer 300 — re-read DispatchAdminAction's \"first 300 characters\"")
	}
	// "through plugin.json's translations": the admin looks the bare words up as it does a success Message.
	screen := coreSource(t, "web", "admin", "src", "screens", "PluginPage.tsx")
	if !strings.Contains(screen, "details?.reason === REASON_ACTION_REFUSED && details.said ? pt(details.said)") {
		t.Error("core's admin no longer translates a refusal through the plugin's translations — re-read DispatchAdminAction's sentence")
	}
	sdk := strings.Join(strings.Fields(strings.ReplaceAll(readText(t, "adminpage.go"), "\n//", "\n")), " ")
	for _, want := range []string{
		"An error you return reaches the owner as the reason the action did not run — its first 300 characters, through plugin.json's translations like Message",
		"refusing an action, however often, never counts toward switching the plugin off",
		"Serve sends every error your handler returns to Core as that answer (gRPC Unknown), whatever it wraps",
		"Running out of time, crashing, or panicking is not an answer, and does count.",
	} {
		if !strings.Contains(sdk, want) {
			t.Errorf("adminpage.go no longer says %q, which core makes true", want)
		}
	}
	anyLang := strings.Join(strings.Fields(readText(t, "docs/ANY_LANGUAGE.md")), " ")
	for _, want := range []string{
		"An error you give a PERSON is not a failed call: a row action, a report or an ability that answers with an error has answered",
		"**Answer with gRPC status `UNKNOWN` (code 2)**",
		"`INVALID_ARGUMENT`, `FAILED_PRECONDITION`, `INTERNAL` and every other code count as a failed call",
	} {
		if !strings.Contains(anyLang, want) {
			t.Errorf("ANY_LANGUAGE.md no longer says %q, which core's host.go and adminlist.go make true", want)
		}
	}
}

// Core's D-87 (2026-09-24): the site's assistant saves a plugin's settings. The guide says it goes through the
// same save as the admin section, for a person who may configure the plugin — held to Core's tool: the gate is
// CanConfigure in the preview and the apply alike, the save is SaveAdminPage, and a secret's card says only
// that it will be replaced.
func TestTheAssistantSavesSettingsThroughTheSectionsOwnSave(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "module.go")
	flat := strings.Join(strings.Fields(src), " ")
	for _, want := range []string{
		"if !CanConfigure(can, key) {",
		"if err := s.SaveAdminPage(ctx, man.Key, page.Key, in.Values); err != nil {",
		// "a secret … is never read back or repeated": the preview's card, and the read tool's answer.
		`row["secret"] = "will be replaced; the value is not shown"`,
		`if f.Secret { row["secret"], row["set"] = true, f.Set } else { row["value"] = pg.Values[f.Key] }`,
		// "only after they approved the change": the tool stages a preview and needs a person's own sign-in.
		"Preview: s.previewSettingsSet, Invoke: module.Typed(s.toolSettingsSet), Authorize: \"core_auth\",",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("core's plugin settings tools no longer have %q — re-read the guide's \"Who saves them\"", want)
		}
	}
	// settingsTarget is the one gate, and both the preview and the apply ask it.
	if n := strings.Count(src, "s.settingsTarget(ctx, v, in)"); n != 2 {
		t.Errorf("core asks settingsTarget %d times, want the preview and the apply both", n)
	}
	guide := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " ")
	for _, want := range []string{
		"A person who may configure your plugin, on your section of the admin — or the site's assistant (`plugin_settings_set`), for a person who may configure your plugin and only after they approved the change. Both go through the same save",
		"`plugin_settings` shows the assistant — and so the AI provider behind it — every field's current value, except a `secret` one",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("PLUGIN_SDK.md no longer says %q, which core's module.go makes true", want)
		}
	}
}

// The example shop's fixtures are what their senders send (the 2026-09-24 review of the SDK). Its test says "a
// fixture in any other shape tests a hook Core never calls", and nothing held the shapes: content.saved is
// held to Core's main.go, and ecommerce.order_paid to the site's shop (commerce), each with the fields the
// example reads.
func TestTheShopExamplesFixturesAreWhatTheirSendersSend(t *testing.T) {
	shop := readText(t, "_examples/shop/shop.go")
	shopTest := readText(t, "_examples/shop/shop_test.go")
	for file, pair := range map[string][2]string{
		"content.saved": {
			`payload, err := json.Marshal(map[string]string{"content_id": id.String(), "type": ctype})`,
			`json.Marshal(map[string]string{"content_id": id, "type": typ})`,
		},
	} {
		if !strings.Contains(coreSource(t, "cmd", "server", "main.go"), pair[0]) {
			t.Errorf("core's main.go no longer sends %s as %s — re-read the example shop's fixture", file, pair[0])
		}
		if !strings.Contains(shopTest, pair[1]) {
			t.Errorf("the example shop's %s fixture is no longer %s", file, pair[1])
		}
	}
	for _, field := range []string{"`json:\"content_id\"`", "`json:\"type\"`", "`json:\"order_id\"`"} {
		if !strings.Contains(shop, field) {
			t.Errorf("the example shop no longer reads %s — re-read this guard's fixtures", field)
		}
	}
	if !strings.Contains(shopTest, `json.Marshal(map[string]any{"order_id": orderID, "total_cents": 4900})`) {
		t.Error("the example shop's ecommerce.order_paid fixture changed shape — re-read it against commerce's emit")
	}
	// The shop's emit last: skipped without commerce beside the SDK, which is most checkouts.
	if !strings.Contains(siblingSource(t, "commerce", "onlinepay.go"),
		`p.core.Emit(ctx, "ecommerce.order_paid", map[string]any{"order_id": order.ID.String(), "total_cents": order.TotalCents})`) {
		t.Error("commerce no longer emits ecommerce.order_paid as the example shop's fixture has it")
	}
}

// S-33 (Core's 2026-09-24 whole-plan review): the guide says `nilda plugin check` refuses an unknown field; it
// decoded plugin.json with plain json.Unmarshal and passed "capabilties". Held to the command's source.
func TestPluginCheckRefusesAnUnknownFieldAsTheGuideSays(t *testing.T) {
	src := coreSource(t, "internal", "cli", "plugin_dev.go")
	start := strings.Index(src, "func cmdPluginCheck(")
	if start < 0 {
		t.Fatal("core's plugin_dev.go no longer declares cmdPluginCheck — repoint this guard")
	}
	body := upTo(t, src[start:], "\n}\n", "core's cmdPluginCheck")
	if !strings.Contains(body, "plugin.ParseManifestStrict(raw)") {
		t.Error("core's `nilda plugin check` no longer reads plugin.json strictly — re-read the guide's \"An unknown field is REFUSED\"")
	}
	guide := strings.Join(strings.Fields(readText(t, "docs/PLUGIN_SDK.md")), " ")
	if !strings.Contains(guide, "An unknown field is REFUSED by `nilda plugin check`") {
		t.Error("PLUGIN_SDK.md no longer says check refuses an unknown field — drop this guard or restore the sentence")
	}
}
