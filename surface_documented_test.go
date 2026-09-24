package nilda

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// M78 (2026-09-23 plugin hunt): what the surface freezes, the guide documents — the other direction of
// TestDocumentedIdentifiersExist. Five of the hunt's documentation findings were this one defect: the
// surface froze a promise the guide never made (CurrentUser, SendLocalizedEmail, the `field` capability,
// CommerceQuery's paging, the tax flag), so an author — or an agent reading only the guide — could not
// learn what they were being promised.
//
// Every func, type and *Core method in surface.txt is named in docs/PLUGIN_SDK.md, or is on the list below
// with the reason an author never meets it. The list can only shrink.
const maxUndocumentedOnPurpose = 5

var undocumentedOnPurpose = map[string]string{
	"GRPCPlugin":         "Core's side of the transport: the go-plugin shape the host and Serve exchange",
	"PluginClient":       "Core's side of the transport: what the host dispenses to call a plugin",
	"HostPluginMap":      "Core's side of the transport: the map the host launches with; Serve wires the plugin end",
	"ProtocolForSDK":     "Core's check: which protocol a manifest's sdk_version speaks",
	"SetSettingsForTest": "the seam under nildatest.SetSettings, which is the call an author makes",
}

func TestEveryFrozenEntryPointIsDocumented(t *testing.T) {
	// Only CODE counts as naming an entry point — an inline `span` or a fenced block — never prose. A word
	// match anywhere let an ordinary English word stand in for an identifier ("Handler", "Viewer", "Payments"
	// read in a sentence), so an entry point could pass without the guide ever showing it as the thing an
	// author types.
	raw := readText(t, "docs/PLUGIN_SDK.md")
	var code []string
	for _, m := range regexp.MustCompile("(?s)```[a-z]*\\n(.*?)```").FindAllStringSubmatch(raw, -1) {
		code = append(code, m[1])
	}
	prose := regexp.MustCompile("(?s)```[a-z]*\\n.*?```").ReplaceAllString(raw, "")
	for _, m := range regexp.MustCompile("`([^`\\n]+)`").FindAllStringSubmatch(prose, -1) {
		code = append(code, m[1])
	}
	doc := strings.Join(code, "\n")
	entry := regexp.MustCompile(`^(?:func (\w+)|type (\w+)|method \(\*?Core\) (\w+))`)
	inSurface := map[string]bool{}
	var missing []string
	for _, line := range strings.Split(readText(t, "surface.txt"), "\n") {
		m := entry.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		name := m[1] + m[2] + m[3]
		inSurface[name] = true
		documented := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(doc)
		_, exempt := undocumentedOnPurpose[name]
		switch {
		case !documented && !exempt:
			missing = append(missing, name)
		case documented && exempt:
			t.Errorf("%s is documented now — take it off undocumentedOnPurpose", name)
		}
	}
	if len(inSurface) < 50 {
		t.Fatalf("read %d entry points from surface.txt — its format changed; repoint this guard", len(inSurface))
	}
	for name := range undocumentedOnPurpose {
		if !inSurface[name] {
			t.Errorf("%s is on undocumentedOnPurpose and no longer in the surface — remove it", name)
		}
	}
	if len(undocumentedOnPurpose) > maxUndocumentedOnPurpose {
		t.Errorf("undocumentedOnPurpose holds %d names, past its bound of %d — document the new one instead",
			len(undocumentedOnPurpose), maxUndocumentedOnPurpose)
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("frozen in surface.txt and never named in docs/PLUGIN_SDK.md — an author cannot learn what "+
			"they are promised:\n  %s", strings.Join(missing, "\n  "))
	}
}
