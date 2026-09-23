package nilda

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
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

	doc := readText(t, "docs/PLUGIN_SDK.md")
	start := strings.Index(doc, "### Being the site's shop")
	if start < 0 {
		t.Fatal(`docs/PLUGIN_SDK.md has no "### Being the site's shop" section — repoint this guard`)
	}
	section := doc[start:]
	if next := strings.Index(section[1:], "\n### "); next >= 0 {
		section = section[:next+1]
	}

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
