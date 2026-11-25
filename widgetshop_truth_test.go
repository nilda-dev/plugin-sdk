package nilda

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// THE SHOP'S DOORS AND THE WIDGET'S PAGE ARE CORE'S STRUCTS ON THE WIRE. Core marshals its own CommerceEndpointSet into
// the widgets it draws and its own WidgetRenderRequest into a plugin's render, and the SDK unmarshals these. A key the
// SDK names that Core's struct does not carry is a door a plugin author is promised and never sent: the shop sets it,
// Core drops it at sameOriginOnly, and the widget that reads it finds nothing. Held to Core's source key by key; a key
// Core has not landed yet is on aheadOfCore (coreahead_test.go), and only there.
func TestTheShopsDoorsAndTheWidgetsPageAreCores(t *testing.T) {
	for _, c := range []struct {
		sdk    any
		file   []string
		core   string // Core's struct
		prefix string // the aheadOfCore word for one of its keys
	}{
		{CommerceEndpoints{}, []string{"internal", "plugin", "commerce.go"}, "CommerceEndpointSet", "commerce endpoint "},
		{WidgetRenderRequest{}, []string{"internal", "plugin", "widgets.go"}, "WidgetRenderRequest", "widget render "},
	} {
		src := coreSource(t, c.file...)
		start := strings.Index(src, "type "+c.core+" struct {")
		if start < 0 {
			t.Fatalf("core's %s no longer declares %s — repoint this guard", strings.Join(c.file, "/"), c.core)
		}
		body := upTo(t, src[start:], "\n}", "core's "+c.core)
		coreKeys := map[string]bool{}
		for _, m := range regexp.MustCompile("json:\"([a-z_]+)").FindAllStringSubmatch(body, -1) {
			coreKeys[m[1]] = true
		}
		rt := reflect.TypeOf(c.sdk)
		for i := range rt.NumField() {
			key, _, _ := strings.Cut(rt.Field(i).Tag.Get("json"), ",")
			if key == "" || key == "-" {
				continue
			}
			ahead := coreLacks(c.prefix + key)
			switch {
			case !coreKeys[key] && !ahead:
				t.Errorf("the SDK's %s.%s (%q) is not on core's %s: Core never sends it — mirror it there, or put "+
					"%q on aheadOfCore with the step that lands it", rt.Name(), rt.Field(i).Name, key, c.core, c.prefix+key)
			case coreKeys[key] && ahead:
				t.Errorf("core's %s carries %q now: take %q off aheadOfCore", c.core, key, c.prefix+key)
			}
		}
	}
}
