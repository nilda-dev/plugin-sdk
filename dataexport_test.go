package nilda

import (
	"reflect"
	"regexp"
	"sort"
	"testing"
)

// Each struct's JSON names are Core's, all of them (core internal/plugin/dataexport.go).
func TestTheDataExportIsCoresShape(t *testing.T) {
	src := coreSource(t, "internal", "plugin", "dataexport.go")
	for name, ours := range map[string]any{"SchemaExport": SchemaExport{}, "TableExport": TableExport{}} {
		body := upTo(t, from(t, src, "type "+name+" struct {", "core dataexport.go"), "\n}", name)
		var core []string
		for _, m := range regexp.MustCompile("json:\"([a-z_]+)").FindAllStringSubmatch(body, -1) {
			core = append(core, m[1])
		}
		var sdk []string
		rt := reflect.TypeOf(ours)
		for i := 0; i < rt.NumField(); i++ {
			sdk = append(sdk, regexp.MustCompile(`^[a-z_]+`).FindString(rt.Field(i).Tag.Get("json")))
		}
		sort.Strings(core)
		sort.Strings(sdk)
		if !reflect.DeepEqual(core, sdk) {
			t.Errorf("%s: Core writes %v, the SDK reads %v", name, core, sdk)
		}
	}
}
