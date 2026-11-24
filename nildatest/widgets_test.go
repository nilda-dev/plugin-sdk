package nildatest

import (
	"context"
	"errors"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

type widgets struct {
	defs []nilda.WidgetDef
	warn []nilda.WidgetWarning
	err  error
}

func (w widgets) Widgets() []nilda.WidgetDef { return w.defs }
func (w widgets) RenderWidget(context.Context, nilda.WidgetRenderRequest) (string, error) {
	return "", nil
}
func (w widgets) CheckWidget(context.Context, nilda.WidgetCheckRequest) ([]nilda.WidgetWarning, error) {
	return w.warn, w.err
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

func TestCheckWidgetAnswersOrFails(t *testing.T) {
	req := nilda.WidgetCheckRequest{Type: "list"}
	w := widgets{warn: []nilda.WidgetWarning{{Path: "rows", Code: "empty", Message: "Add a row."}}}
	var got []nilda.WidgetWarning
	if f := failure(t, func(tb testing.TB) { got = CheckWidget(tb, w, req) }); f != "" || len(got) != 1 {
		t.Errorf("failed %q, answered %v", f, got)
	}
	for _, bad := range []widgets{
		{warn: []nilda.WidgetWarning{{Code: "Bad Code", Message: "m"}}},
		{err: errors.New("down")},
	} {
		if f := failure(t, func(tb testing.TB) { CheckWidget(tb, bad, req) }); f == "" {
			t.Errorf("%+v passed", bad)
		}
	}
}
