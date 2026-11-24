package nilda

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// widget.check (v0.73.0): subscribed only for a WidgetProvider that implements WidgetChecker, answered as
// {"warnings":[...]}, and bounded.

type checkingWidgets struct {
	widgetOnly
	got  WidgetCheckRequest
	warn []WidgetWarning
}

func (c *checkingWidgets) CheckWidget(_ context.Context, req WidgetCheckRequest) ([]WidgetWarning, error) {
	c.got = req
	return c.warn, nil
}

func TestWidgetCheckIsAnsweredAndSubscribed(t *testing.T) {
	c := &checkingWidgets{warn: []WidgetWarning{{Path: "rows[0].title", Code: "too_short", Message: "Say more."}}}
	if got := withProvidedHooks(c, nil); !slices.Contains(got, HookWidgetCheck) {
		t.Errorf("a WidgetProvider that implements WidgetChecker does not subscribe %q: %v", HookWidgetCheck, got)
	}
	if got := withProvidedHooks(widgetOnly{}, nil); slices.Contains(got, HookWidgetCheck) {
		t.Errorf("a WidgetProvider without WidgetChecker subscribes %q: %v", HookWidgetCheck, got)
	}
	out, ok, err := dispatchProvided(context.Background(), c, HookWidgetCheck,
		[]byte(`{"type":"list","config":{"rows":[]},"locale":"fa"}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if c.got.Type != "list" || c.got.Locale != "fa" || c.got.Config["rows"] == nil {
		t.Errorf("the checker was asked %+v", c.got)
	}
	if want := `{"warnings":[{"path":"rows[0].title","code":"too_short","message":"Say more."}]}`; string(out) != want {
		t.Errorf("answered %s, want %s", out, want)
	}
	c.warn = nil
	if out, _, _ := dispatchProvided(context.Background(), c, HookWidgetCheck, []byte(`{"type":"list"}`)); string(out) != `{"warnings":[]}` {
		t.Errorf("no warnings answered %s, want an empty list", out)
	}
	// Past a bound, the answer is an error rather than a list Core would refuse.
	c.warn = []WidgetWarning{{Code: "x", Message: strings.Repeat("ا", MaxWidgetCheckMessageLen+1)}}
	if _, _, err := dispatchProvided(context.Background(), c, HookWidgetCheck, []byte(`{"type":"list"}`)); err == nil {
		t.Error("a message past MaxWidgetCheckMessageLen was answered")
	}
}

func TestWidgetWarningsAreBounded(t *testing.T) {
	one := WidgetWarning{Path: "title", Code: "too_long", Message: "Shorten the title."}
	if err := ValidateWidgetWarnings([]WidgetWarning{one, {Code: "empty", Message: "Whole widget."}}); err != nil {
		t.Fatal(err)
	}
	many := make([]WidgetWarning, MaxWidgetCheckWarnings+1)
	for i := range many {
		many[i] = one
	}
	if err := ValidateWidgetWarnings(many[:MaxWidgetCheckWarnings]); err != nil {
		t.Errorf("%d warnings: %v", MaxWidgetCheckWarnings, err)
	}
	for want, bad := range map[string][]WidgetWarning{
		"warnings": many,
		"message":  {{Code: "x", Message: ""}},
		"runes":    {{Code: "x", Message: strings.Repeat("ا", MaxWidgetCheckMessageLen+1)}},
		"path":     {{Code: "x", Message: "m", Path: strings.Repeat("a", MaxWidgetCheckPathLen+1)}},
		"code":     {{Code: "Not A Code", Message: "m"}},
	} {
		if err := ValidateWidgetWarnings(bad); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("got %v, want an error saying %q", err, want)
		}
	}
	// Exactly at the bounds, in runes, not bytes.
	edge := WidgetWarning{Code: "x", Message: strings.Repeat("ا", MaxWidgetCheckMessageLen), Path: strings.Repeat("ب", MaxWidgetCheckPathLen)}
	if err := ValidateWidgetWarnings([]WidgetWarning{edge}); err != nil {
		t.Errorf("a warning at the bounds: %v", err)
	}
}

// A warning's message and path are plain text Core shows an owner: no control character (a newline forging a second
// line, an escape) and no bidi embedding, override or isolate, which shows a sentence as something it is not (review
// of #75). Persian text itself is fine.
func TestAWidgetWarningIsPlainText(t *testing.T) {
	for _, bad := range []string{"line\nforged", "tab\there", "\x1b[31mred", "a‮b", "a‪b", "a⁦b", "a⁩b", "a\x7fb"} {
		if err := ValidateWidgetWarnings([]WidgetWarning{{Code: "x", Message: bad}}); err == nil || !strings.Contains(err.Error(), "message") {
			t.Errorf("message %q: got %v, want a refusal naming the message", bad, err)
		}
		if err := ValidateWidgetWarnings([]WidgetWarning{{Code: "x", Message: "m", Path: bad}}); err == nil || !strings.Contains(err.Error(), "path") {
			t.Errorf("path %q: got %v, want a refusal naming the path", bad, err)
		}
	}
	if err := ValidateWidgetWarnings([]WidgetWarning{{Code: "x", Message: "یک ردیف اضافه کنید.", Path: "rows[0].عنوان"}}); err != nil {
		t.Errorf("Persian text: %v", err)
	}
}
