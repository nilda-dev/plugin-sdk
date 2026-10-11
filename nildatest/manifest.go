package nildatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// WriteManifestEnv is the environment variable that makes ManifestAbilities write plugin.json instead of failing.
const WriteManifestEnv = "NILDA_WRITE_MANIFEST"

// manifestAbility is one ability as plugin.json declares it — the four fields the marketplace review reads and
// Core compares with what Init returns.
type manifestAbility struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Class       string `json:"class"`
}

// ManifestAbilities holds plugin.json's "abilities" to the abilities your Init returns.
//
// A plugin with the `abilities` capability declares each ability in plugin.json — its name, label, description and
// class — because the marketplace's review reads the package and never runs it, and a Core built on v0.11.0 offers
// the assistant only an ability declared there exactly as Init returns it. You do not type that list: call this
// from a test with the abilities your plugin returns, and run the test once with NILDA_WRITE_MANIFEST=1 to write
// them into plugin.json. Without it the test fails while the two disagree, so a changed description cannot ship
// with an old manifest.
//
//	func TestPluginJSONDeclaresTheAbilities(t *testing.T) {
//		nildatest.ManifestAbilities(t, "plugin.json", (&Shop{}).abilities())
//	}
//
// Writing replaces only the "abilities" value (or adds it before the closing brace), so the rest of the file keeps
// its order and spacing.
func ManifestAbilities(t testing.TB, manifestPath string, abilities []nilda.Ability) {
	t.Helper()
	want := make([]manifestAbility, 0, len(abilities))
	for _, a := range abilities {
		want = append(want, manifestAbility{Name: a.Name, Label: a.Label, Description: a.Description, Class: string(a.Class)})
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read %s: %v", manifestPath, err)
	}
	start, end, err := abilitiesSpan(raw)
	if err != nil {
		t.Fatalf("%s: %v", manifestPath, err)
	}
	var have []manifestAbility
	if start >= 0 {
		if err := json.Unmarshal(raw[start:end], &have); err != nil {
			t.Fatalf("%s: \"abilities\" is not a list of abilities: %v", manifestPath, err)
		}
	}
	diff := abilityDiff(have, want)
	if diff == "" {
		return
	}
	if os.Getenv(WriteManifestEnv) == "" {
		t.Fatalf("%s does not declare the abilities Init returns (%s) — run this test with %s=1 to write them",
			manifestPath, diff, WriteManifestEnv)
	}
	out, err := withAbilities(raw, start, end, want)
	if err != nil {
		t.Fatalf("%s: %v", manifestPath, err)
	}
	if err := os.WriteFile(manifestPath, out, 0o644); err != nil {
		t.Fatalf("write %s: %v", manifestPath, err)
	}
	t.Logf("wrote %d abilities into %s (%s)", len(want), manifestPath, diff)
}

// abilitiesSpan finds the byte span of the top-level "abilities" value, or -1, -1 when there is none.
func abilitiesSpan(raw []byte) (start, end int, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return 0, 0, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0, err
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return 0, 0, err
		}
		if tok == "abilities" {
			end := int(dec.InputOffset())
			return end - len(v), end, nil
		}
	}
	return -1, -1, nil
}

// withAbilities puts the list in place of the old value, or adds the key before the object's closing brace.
func withAbilities(raw []byte, start, end int, list []manifestAbility) ([]byte, error) {
	val, err := json.MarshalIndent(list, "  ", "  ")
	if err != nil {
		return nil, err
	}
	if start >= 0 {
		return append(append(append([]byte{}, raw[:start]...), val...), raw[end:]...), nil
	}
	closing := bytes.LastIndexByte(raw, '}')
	body := bytes.TrimRight(raw[:closing], " \t\r\n")
	sep := ","
	if bytes.HasSuffix(body, []byte("{")) {
		sep = ""
	}
	out := append([]byte{}, body...)
	out = append(out, []byte(sep+"\n  \"abilities\": ")...)
	out = append(out, val...)
	out = append(out, '\n')
	return append(out, raw[closing:]...), nil
}

// abilityDiff says, shortly, how the declared list differs from the one Init returns; "" when they agree.
func abilityDiff(have, want []manifestAbility) string {
	byName := map[string]manifestAbility{}
	for _, h := range have {
		byName[h.Name] = h
	}
	var parts []string
	for _, w := range want {
		h, ok := byName[w.Name]
		switch {
		case !ok:
			parts = append(parts, w.Name+" is not declared")
		case h != w:
			parts = append(parts, w.Name+" differs")
		}
		delete(byName, w.Name)
	}
	extra := make([]string, 0, len(byName))
	for name := range byName {
		extra = append(extra, name+" is declared but not returned")
	}
	sort.Strings(extra)
	parts = append(parts, extra...)
	if len(parts) == 0 && len(have) != len(want) {
		parts = append(parts, "a name is declared twice")
	}
	return strings.Join(parts, ", ")
}
