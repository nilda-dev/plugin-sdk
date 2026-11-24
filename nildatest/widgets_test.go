package nildatest

import (
	"context"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

type widgets struct {
	defs []nilda.WidgetDef
}

func (w widgets) Widgets() []nilda.WidgetDef { return w.defs }
func (w widgets) RenderWidget(context.Context, nilda.WidgetRenderRequest) (string, error) {
	return "", nil
}

func failure(tb testing.TB, fn func(testing.TB)) (failed string) {
	f := &fakeT{TB: tb}
	defer func() {
		if r := recover(); r != nil && r != f {
			panic(r)
		}
		failed = f.failed
	}()
	fn(f)
	return ""
}

func TestWidgetsHoldsTheLimitsAPluginDeclares(t *testing.T) {
	good := widgets{defs: []nilda.WidgetDef{{Type: "list", Label: "List", MaxHTMLBytes: 100 << 10,
		Fields: []nilda.WidgetField{{Key: "rows", Type: nilda.FieldRepeater, MaxItems: 12}}}}}
	if got := failure(t, func(tb testing.TB) { Widgets(tb, good) }); got != "" {
		t.Errorf("a widget whose limits hold failed: %s", got)
	}
	bad := widgets{defs: []nilda.WidgetDef{{Type: "list", Label: "List",
		Fields: []nilda.WidgetField{{Key: "title", Type: nilda.FieldText, Pattern: "("}}}}}
	if got := failure(t, func(tb testing.TB) { Widgets(tb, bad) }); !strings.Contains(got, `"title"`) {
		t.Errorf("an uncompilable pattern passed, or the failure does not name the field: %q", got)
	}
}
