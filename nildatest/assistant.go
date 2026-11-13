package nildatest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// AssistantHole is a surface of a manifest the site's assistant cannot reach yet: a hole Core itself still has, not
// one the author can fix. Core's plan names the unit that closes each, and when it lands the hole leaves this list.
type AssistantHole struct {
	Where string // the surface, as Core names it: "manifest.<plugin.json key>"
	Unit  string // the unit of Core's plan that closes it (docs/plans/agent-control-2026-11-13 in Core)
	Gap   string // what the assistant cannot do yet, in the owner's words
}

// coreHole is one of Core's pending doors.
type coreHole struct{ unit, gap string }

// coreHoles mirrors the Pending entries of Core's agent doors (core internal/plugin/agentdoors.go): every surface
// of a manifest the assistant cannot reach yet. TestCoreHolesAreCoresPendingDoors holds it to Core's source, and
// TestAssistantHolesAreCoresHoles to Core's own check of every first-party manifest.
//
// Empty since Core's C3 (core #437) gave the last two — a plugin's account and its "/" editor commands — their
// tools: every surface a manifest declares now reaches the assistant. A hole Core opens again is added here.
var coreHoles = map[string]coreHole{}

// AssistantHoles answers which of Core's own holes a plugin.json uses: the surfaces it declares that the site's
// assistant — in the panel's chat and over MCP — cannot operate yet, each with the unit of Core's plan that closes
// it. They are what `nilda plugin check` lists as Core's, never the author's: nothing in the plugin is wrong, and
// no install is refused for them. A plugin can say them in its own documentation, or keep a test that notices when
// one closes:
//
//	func TestTheAssistantsHoles(t *testing.T) {
//		raw, _ := os.ReadFile("plugin.json")
//		holes, err := nildatest.AssistantHoles(raw)
//		if err != nil {
//			t.Fatal(err)
//		}
//		for _, h := range holes {
//			t.Logf("%s: %s (Core closes it in %s)", h.Where, h.Gap, h.Unit)
//		}
//	}
//
// A key counts as declared the way Core reads it: null, "", false and 0 are unset, and any other value — an empty
// list or object included — is the surface, used. The holes come in order of Where. A raw that is not a JSON object
// is an error.
func AssistantHoles(raw []byte) ([]AssistantHole, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("plugin.json is not a JSON object: %w", err)
	}
	if top == nil {
		return nil, fmt.Errorf("plugin.json is not a JSON object: it is null")
	}
	var out []AssistantHole
	for key, v := range top {
		h, ok := coreHoles["manifest."+key]
		if !ok || !present(v) {
			continue
		}
		out = append(out, AssistantHole{Where: "manifest." + key, Unit: h.unit, Gap: h.gap})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Where < out[j].Where })
	return out, nil
}

// present reports whether a value holds something, as Core's parsed manifest does (reflect's IsZero on the field it
// decodes into): null, "", false and 0 are its zero; a list or an object, even an empty one, is not.
func present(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	switch {
	case len(v) == 0, bytes.Equal(v, []byte("null")), bytes.Equal(v, []byte(`""`)), bytes.Equal(v, []byte("false")):
		return false
	case v[0] == '-' || (v[0] >= '0' && v[0] <= '9'):
		var n float64
		if err := json.Unmarshal(v, &n); err == nil && n == 0 {
			return false
		}
	}
	return true
}
