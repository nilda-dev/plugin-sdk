package nilda_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEveryPluginPinNamesAnSDKVersionThatCouldResolve.
//
// Every module checked out beside this one that requires it — the plugins, and Core itself — names it by
// version. Not one of the plugins ever reads that requirement:
// each has `replace gitlab.com/nildalabs/nilda-sdk/plugin-sdk => ../plugin-sdk`, and every developer works
// inside a go.work workspace that supplies this directory anyway. So the pin is consulted by exactly one
// kind of reader — someone who clones ONE repository: CI, the marketplace build, an outside contributor —
// and it is wrong for all of them without ever being wrong for us.
//
// That is not hypothetical. It is how commerce, forms and booking came to require plugin-sdk v0.1.0, a
// version this SDK has RETRACTED for speaking a contract Core refuses outright, and to declare module paths
// under `nilda.dev/plugins/*`, a host that serves nothing. Both survived for weeks. Nothing looked broken,
// because locally nothing was.
//
// This test lives HERE, in the SDK, rather than once per plugin, because the SDK is what owns the answer:
// which versions exist, and what module path each of them declares. A copy in every consumer would be
// one more copy per repository to keep in step, which is the shape of the problem, not a fix for it.
//
// Two facts are checked, and both have been false:
//
//  1. the version the plugin names is a tag that EXISTS here;
//  2. that tag's own go.mod declares the module path the plugin requires.
//
// The second is the subtle one and it is why a tag existing is not enough. `go` resolves a requirement by
// reading the go.mod AT that version: if the tag says `module gitlab.com/nilda-sdk/plugin-sdk` while the
// plugin requires `gitlab.com/nildalabs/nilda-sdk/plugin-sdk`, the build fails with a non-matching module
// path — and a directory `replace` fails the same way, so even the CI job that clones this repo at the tag
// cannot rescue it.
//
// Whether the tag is PUSHED is the third fact, and it is deliberately not here: it needs the network, and a
// test that reaches the network is a test people learn to skip. Each plugin's own CI clones this repo
// `--branch $SDK_VERSION`, which fails loudly when the tag exists on one developer's disk and nowhere else.
// This test is the fast local copy of that; the clone is the copy that cannot be skipped.
func TestEveryPluginPinNamesAnSDKVersionThatCouldResolve(t *testing.T) {
	modulePath := ownModulePath(t)
	tags := gitTags(t)

	// A `require` line for this module, and the version it names.
	requireRE := regexp.MustCompile(regexp.QuoteMeta(modulePath) + `\s+(v\S+)`)
	// Any require of a plugin-sdk under SOME other path — the mistake this ecosystem keeps making.
	strayRE := regexp.MustCompile(`(?m)^\s*(\S*plugin-sdk)\s+(v\S+)`)

	// THE CONSUMERS ARE DISCOVERED, NOT LISTED. This loop used to walk a fixed
	// []string{"commerce", "forms", "booking", "sso"}: two of those never became repositories, and `frames`
	// — which pins this module — was never added, so its pin went unchecked while the log said "2 plugin
	// pins checked". Any sibling module whose go.mod names a plugin-sdk is a consumer, Core included.
	gomods, err := filepath.Glob(filepath.Join("..", "*", "go.mod"))
	if err != nil {
		t.Fatalf("listing the sibling modules: %v", err)
	}
	sort.Strings(gomods)
	selfModule := regexp.MustCompile(`(?m)^module\s+` + regexp.QuoteMeta(modulePath) + `\s*$`)

	var checked int
	var names []string
	for _, gomod := range gomods {
		name := filepath.Base(filepath.Dir(gomod))
		raw, err := os.ReadFile(gomod)
		if err != nil {
			t.Fatalf("reading %s: %v", gomod, err)
		}
		src := string(raw)
		if selfModule.MatchString(src) {
			continue // this repository
		}

		m := requireRE.FindStringSubmatch(src)
		if m == nil {
			if stray := strayRE.FindStringSubmatch(src); stray != nil {
				checked++
				names = append(names, name)
				t.Errorf("%s requires %q — this SDK's module path is %q. A path that resolves to no host "+
					"reports itself as `unknown revision`, which reads exactly like a private-repo error, "+
					"and the two hide behind each other.", name, stray[1], modulePath)
			}
			continue // requires no plugin-sdk at all: not a consumer
		}
		checked++
		names = append(names, name)
		version := m[1]

		if !tags[version] {
			t.Errorf("%s pins plugin-sdk %s, which is not a tag in this repository. A pin naming a version "+
				"that was never cut resolves for nobody outside a go.work workspace.", name, version)
			continue
		}
		declared := modulePathAtTag(t, version)
		if declared != modulePath {
			t.Errorf("%s pins plugin-sdk %s, whose go.mod declares `module %s` — but the plugin requires "+
				"%s. `go` reads the go.mod AT the version, so this pin can never resolve: it fails with a "+
				"non-matching module path, and a directory `replace` fails the same way. The module path "+
				"was renamed after that tag was cut; the fix is a NEW tag from a commit that carries the "+
				"current path, pushed, and the pin moved to it.",
				name, version, declared, modulePath)
		}
	}

	if checked == 0 {
		t.Skip("no module that requires plugin-sdk is checked out beside it — the pins cannot be checked " +
			"from here; run this from a full nilda checkout")
	}
	t.Logf("%d pins checked against %d tags: %s", checked, len(tags), strings.Join(names, ", "))
}

// ownModulePath is this module's path, read from go.mod rather than hard-coded: hard-coding it would make
// this test agree with itself after a rename, which is the exact failure it exists to catch.
func ownModulePath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(string(raw))
	if m == nil {
		t.Fatal("go.mod declares no module path")
	}
	return m[1]
}

func gitTags(t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.Command("git", "tag", "--list").Output()
	if err != nil {
		t.Skipf("git is unavailable here, so which versions exist cannot be established: %v", err)
	}
	tags := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			tags[line] = true
		}
	}
	if len(tags) == 0 {
		// A shallow clone with no tags cannot answer the question, and answering it wrongly is worse than
		// saying so: every pin would look unresolvable, and the real ones would be lost in the noise.
		t.Skip("this checkout has no tags (a shallow clone?) — run `git fetch --tags` to check the pins")
	}
	return tags
}

// modulePathAtTag reads the go.mod as it was AT a tag. The bytes `go` would read, not a reconstruction.
func modulePathAtTag(t *testing.T, tag string) string {
	t.Helper()
	out, err := exec.Command("git", "show", tag+":go.mod").Output()
	if err != nil {
		t.Fatalf("reading go.mod at %s: %v", tag, err)
	}
	m := regexp.MustCompile(`(?m)^module\s+(\S+)`).FindStringSubmatch(string(out))
	if m == nil {
		t.Fatalf("go.mod at %s declares no module path", tag)
	}
	return m[1]
}
