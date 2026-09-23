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
	for _, f := range sdkTextFiles(t) {
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
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk, so its package format cannot be read from here — " +
			"run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	constant := func(name string) string {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(string(raw))
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

// TestTheAbilityGateTheDocsNameIsTheOneCoreApplies is the documentation half of the hunt's C10.
//
// Core runs a plugin's ability only for a person who holds one of plugin.ConfigurePermissions — the list the
// plugin's own admin section is gated on — and the abilities section tells an author who that is. The truth is
// Core's source, read when Core is checked out beside this repository, so a Core that changes the gate turns this
// red instead of leaving the sentence behind.
func TestTheAbilityGateTheDocsNameIsTheOneCoreApplies(t *testing.T) {
	pages := filepath.Join("..", "core", "internal", "plugin", "adminpages.go")
	raw, err := os.ReadFile(pages)
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk, so its ability gate cannot be read from here — " +
			"run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatalf("reading %s: %v", pages, err)
	}
	src := string(raw)

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
	manage := regexp.MustCompile(`\bPermPluginManage\s*=\s*"([^"]+)"`).FindStringSubmatch(readText(t, perms))
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

// TestTheKVLimitsTheDocsNameAreCores holds the capability table's `kv` row to Core's own caps (the 2026-09-23
// plugin hunt's C19), read from core's internal/plugin/kvquota.go.
func TestTheKVLimitsTheDocsNameAreCores(t *testing.T) {
	path := filepath.Join("..", "core", "internal", "plugin", "kvquota.go")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	num := func(name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9_]+)(?:\s*<<\s*([0-9]+))?`).FindStringSubmatch(string(raw))
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
// believed the doc stopped using the two hooks the policy exists to preserve. Held to Core's source: if
// Render ever moves to another policy, this fails and the sentence gets re-read.
func TestTheWidgetSanitizerTheDocsDescribeIsCores(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "core", "internal", "plugin", "widgets.go"))
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "security.SanitizeComponentHTML(res.HTML)") {
		t.Fatal("core's WidgetCatalog.Render no longer sanitizes with the component policy — re-read what the docs say is kept")
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
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	num := func(name string) int {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9_]+)(?:\s*<<\s*([0-9]+))?`).FindStringSubmatch(string(raw))
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
	raw, err := os.ReadFile(filepath.Join("..", "core", "pkg", "config", "config.go"))
	if os.IsNotExist(err) {
		t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
	}
	if err != nil {
		t.Fatal(err)
	}
	seconds := func(env string) string {
		m := regexp.MustCompile(`parseDuration\("` + env + `",\s*([0-9]+)\s*\*\s*time\.Second\)`).FindStringSubmatch(string(raw))
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

// M23–M34 (2026-09-23 plugin hunt): what the admin_page section tells an author Core does and refuses — each
// sentence beside the Core source that makes it true, and each number held to Core's constant, so the section
// cannot keep a rule Core dropped or a bound Core moved.
func TestTheAdminPageRulesTheDocsNameAreCores(t *testing.T) {
	read := func(file string) string {
		raw, err := os.ReadFile(filepath.Join("..", "core", "internal", "plugin", file))
		if os.IsNotExist(err) {
			t.Skip("core is not checked out beside plugin-sdk — run this from a full nilda checkout")
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	manifest, report, list, tables := read("schema_manifest.go"), read("adminreport.go"), read("adminlist.go"), read("tables.go")
	num := func(src, name string) string {
		m := regexp.MustCompile(`\b` + name + `\s*=\s*([0-9]+)\b`).FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("core no longer declares %s as a number — repoint this guard", name)
		}
		return m[1]
	}
	section := strings.Join(strings.Fields(mdSection(t, "docs/PLUGIN_SDK.md", "### Your own section of the admin")), " ")
	for _, c := range []struct{ src, marker, says string }{
		{manifest, "the 'admin_page' capability is declared but no admin page is", "and the capability without a page"},
		{manifest, "admin pages are declared but the 'admin_page' capability is not", "`admin_pages` without the `admin_page` capability"},
		{manifest, "is also a field of the settings page", "One field key on two settings pages"},
		{list, "ReasonActionOutcomeUnknown", "Core cannot tell \"never started\" from \"finished, answer lost\""},
		{list, "primaryKeyOf(table)", "then by the table's primary key"},
		{tables, "func TablesWithListIndexes", "Core creates one with your tables"},
	} {
		if !strings.Contains(c.src, c.marker) {
			t.Errorf("core no longer has %q — re-read the admin_page section's sentence %q", c.marker, c.says)
		}
		if !strings.Contains(section, c.says) {
			t.Errorf("the admin_page section no longer says %q, which core's %q makes true", c.says, c.marker)
		}
	}
	for _, want := range []string{
		"More than " + num(manifest, "maxAdminPages") + " pages",
		num(manifest, "maxPageFields") + " fields on a settings page",
		num(report, "maxReportParams") + " filters on a report page",
		num(manifest, "maxListColumns") + " columns or " + num(manifest, "maxListColumns") + " search columns",
		num(manifest, "maxRowActions") + " row actions",
		num(manifest, "maxSettingChoices") + " choices on a field",
		"longer than " + num(manifest, "maxDeclaredLabelLen") + " characters",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the admin_page section does not say %q, which is Core's bound", want)
		}
	}
}
