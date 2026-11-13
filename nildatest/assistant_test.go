package nildatest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Core's holes, as AssistantHoles answers them, are the holes Core's own check names (`nilda plugin check`,
// CheckForAssistant) for every first-party manifest of Core's conformance corpus: the surfaces its golden marks
// pending, with the same unit, and no other.
func TestAssistantHolesAreCoresHoles(t *testing.T) {
	real, _ := filepath.Glob(filepath.Join(coreConformance, "real", "*.json"))
	if len(real) == 0 {
		t.Skip("core is not checked out beside the SDK; its conformance corpus is what this is held to")
	}
	sawHole := false
	for _, path := range real {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			golden, err := os.ReadFile(filepath.Join(coreConformance, "assistant", name+".json"))
			if err != nil {
				t.Fatalf("Core has no assistant check of %s: %v", name, err)
			}
			var check struct {
				Surfaces []struct {
					Where   string `json:"where"`
					Pending string `json:"pending"`
				} `json:"surfaces"`
				Findings []struct {
					Where string `json:"where"`
					What  string `json:"what"`
					Core  bool   `json:"core"`
				} `json:"findings"`
			}
			if err := json.Unmarshal(golden, &check); err != nil {
				t.Fatalf("Core's assistant check of %s: %v", name, err)
			}
			type hole struct{ Where, Unit string }
			want := []hole{}
			for _, s := range check.Surfaces {
				if s.Pending != "" {
					want = append(want, hole{s.Where, s.Pending})
				}
			}
			got, err := AssistantHoles(raw)
			if err != nil {
				t.Fatal(err)
			}
			have := []hole{}
			for _, h := range got {
				have = append(have, hole{h.Where, h.Unit})
			}
			if !reflect.DeepEqual(have, want) {
				t.Errorf("the SDK says %s's holes are %v, Core says %v", name, have, want)
			}
			// The gap in the author's words is Core's own finding for that surface (Core says one gap once, so a
			// surface whose gap another already said has no finding of its own to compare).
			for _, h := range got {
				for _, f := range check.Findings {
					if f.Core && f.Where == h.Where && f.What != h.Gap {
						t.Errorf("%s: the SDK says the gap is %q, Core says %q", h.Where, h.Gap, f.What)
					}
				}
			}
			if len(want) > 0 {
				sawHole = true
			}
		})
	}
	// While Core has a hole a real manifest uses, the corpus must show it; once none does, this says so rather than
	// passing on an empty comparison.
	if len(coreHoles) > 0 && !sawHole {
		t.Logf("no manifest of Core's corpus uses one of Core's holes %v; the comparison above held only absences", coreHoles)
	}
}

// coreHoles is Core's agentDoors' Pending entries, door for door and unit for unit: a unit that lands deletes its
// Pending in Core (agentdoors.go), and this goes red until coreHoles follows.
func TestCoreHolesAreCoresPendingDoors(t *testing.T) {
	src := coreSource(t, "agentdoors.go")
	pending := regexp.MustCompile(`(manifest|page|kind)Door\(("[^"]*"|\w+)\):\s*\{Pending:\s*&PendingDoor\{Unit:\s*"([^"]+)"`)
	got := map[string]string{}
	for _, m := range pending.FindAllStringSubmatch(src, -1) {
		got[m[1]+"."+strings.Trim(m[2], `"`)] = m[3]
	}
	if n := strings.Count(src, "&PendingDoor{"); n != len(got) {
		t.Fatalf("core's agentdoors.go has %d Pending doors and this guard read %d — teach it the new shape", n, len(got))
	}
	units := map[string]string{}
	for where, h := range coreHoles {
		units[where] = h.unit
	}
	if !reflect.DeepEqual(got, units) {
		t.Errorf("core's pending doors are %v, the SDK's coreHoles %v", got, units)
	}
	for where := range coreHoles {
		if !strings.HasPrefix(where, "manifest.") {
			t.Errorf("%s is a hole AssistantHoles cannot see: it reads plugin.json's top-level keys only — teach it", where)
		}
	}
}

func TestAssistantHoles(t *testing.T) {
	hole := func(where string) AssistantHole {
		return AssistantHole{Where: where, Unit: coreHoles[where].unit, Gap: coreHoles[where].gap}
	}
	cases := []struct {
		name, raw string
		want      []AssistantHole
	}{
		{"none", `{"key":"shop","capabilities":["abilities"]}`, nil},
		{"both, in order", `{"key":"shop","editor_commands":[{"name":"x"}],"account":{"name":"Acme"}}`,
			[]AssistantHole{hole("manifest.account"), hole("manifest.editor_commands")}},
		// What Core reads as unset (its struct's zero value) is no hole: null, "", false and 0.
		{"null", `{"key":"shop","account":null}`, nil},
		{"empty string", `{"key":"shop","account":""}`, nil},
		{"false", `{"key":"shop","account":false}`, nil},
		{"zero", `{"key":"shop","editor_commands":0}`, nil},
		{"zero float", `{"key":"shop","editor_commands":0.0}`, nil},
		// An empty list or object is a value Core holds (a non-nil slice or pointer): it is the surface, used.
		{"empty list", `{"key":"shop","editor_commands":[]}`, []AssistantHole{hole("manifest.editor_commands")}},
		{"empty object", `{"key":"shop","account":{}}`, []AssistantHole{hole("manifest.account")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := AssistantHoles([]byte(c.raw))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
			if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Where < got[j].Where }) {
				t.Errorf("not in order: %v", got)
			}
		})
	}
	for name, raw := range map[string]string{"not JSON": "account: yes", "a list": `[{"account":{}}]`, "empty": ""} {
		if got, err := AssistantHoles([]byte(raw)); err == nil {
			t.Errorf("%s: no error, got %v", name, got)
		}
	}
	// A hole says what the assistant cannot do yet, and the unit of Core's plan that closes it.
	got, _ := AssistantHoles([]byte(`{"account":{}}`))
	if len(got) != 1 || got[0].Gap == "" || got[0].Unit != "C3" {
		t.Errorf("a hole names no gap or no unit: %+v", got)
	}
}
