package nilda

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// provider implements both optional interfaces plus the plain Handler, which is the realistic shape: a
// plugin that contributes widgets almost always also handles ordinary hooks.
type provider struct {
	gotRender WidgetRenderRequest
	gotAssets RenderAssetsRequest
	plainHook string
}

func (p *provider) Init(context.Context, *Core) (InitResult, error) { return InitResult{}, nil }
func (p *provider) HandleEvent(context.Context, string, []byte) error {
	return nil
}

func (p *provider) HandleHook(_ context.Context, hook string, payload []byte) ([]byte, error) {
	p.plainHook = hook
	return payload, nil
}

func (p *provider) Widgets() []WidgetDef {
	return []WidgetDef{{
		Type:  "product-grid",
		Label: "Product grid",
		Fields: []WidgetField{
			{Key: "count", Label: "How many", Type: FieldNumber, Required: true},
		},
	}}
}

func (p *provider) RenderWidget(_ context.Context, req WidgetRenderRequest) (string, error) {
	p.gotRender = req
	return "<div>" + req.Type + "</div>", nil
}

func (p *provider) FooterScripts(_ context.Context, req RenderAssetsRequest) []ScriptAsset {
	p.gotAssets = req
	return []ScriptAsset{{Src: "/shop/cart.js", Defer: true}}
}

// TestDescribeAnswersWithTheCatalogue proves an author never touches the envelope.
func TestDescribeAnswersWithTheCatalogue(t *testing.T) {
	out, ok, err := dispatchProvided(context.Background(), &provider{}, HookWidgetDescribe, []byte(`{}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	var res struct {
		Widgets []struct {
			Type   string `json:"type"`
			Label  string `json:"label"`
			Fields []struct {
				Key      string `json:"key"`
				Type     string `json:"type"`
				Required bool   `json:"required"`
			} `json:"fields"`
		} `json:"widgets"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Widgets) != 1 || res.Widgets[0].Type != "product-grid" {
		t.Fatalf("widgets: %s", out)
	}
	if len(res.Widgets[0].Fields) != 1 || !res.Widgets[0].Fields[0].Required {
		t.Fatalf("fields: %s", out)
	}
}

// TestDescribeNeverAnswersNull pins the empty case. Core unmarshals this, and a plugin that offers nothing
// should say so rather than send a null the next reader has to interpret.
func TestDescribeNeverAnswersNull(t *testing.T) {
	h := &emptyProvider{}
	out, _, err := dispatchProvided(context.Background(), h, HookWidgetDescribe, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"widgets":[]`) {
		t.Fatalf("want an empty list, got %s", out)
	}
}

type emptyProvider struct{ provider }

func (e *emptyProvider) Widgets() []WidgetDef { return nil }

// TestRenderReceivesThePageContext is the test that would have caught a silently mismatched field name:
// Core's JSON goes in, the author's struct comes out, and every field has to survive.
func TestRenderReceivesThePageContext(t *testing.T) {
	p := &provider{}
	// Byte-for-byte what Core marshals in widgets.go — copied, not constructed, so a rename on either side
	// fails here rather than in production.
	payload := []byte(`{"type":"product-grid","config":{"count":3},"kind":"home",` +
		`"locale":"fa","rtl":true,"title":"Home","url":"/","site_url":"https://example.com"}`)
	out, ok, err := dispatchProvided(context.Background(), p, HookWidgetRender, payload)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.gotRender.Type != "product-grid" || p.gotRender.Kind != "home" {
		t.Fatalf("type/kind lost: %+v", p.gotRender)
	}
	if p.gotRender.Locale != "fa" || !p.gotRender.RTL {
		t.Fatalf("locale/rtl lost: %+v", p.gotRender)
	}
	if p.gotRender.SiteURL != "https://example.com" || p.gotRender.URL != "/" {
		t.Fatalf("urls lost: %+v", p.gotRender)
	}
	if n, _ := p.gotRender.Config["count"].(float64); n != 3 {
		t.Fatalf("config lost: %+v", p.gotRender.Config)
	}
	var res struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if res.HTML != "<div>product-grid</div>" {
		t.Fatalf("html: %q", res.HTML)
	}
}

func TestFooterScriptsRoundTrip(t *testing.T) {
	p := &provider{}
	out, ok, err := dispatchProvided(context.Background(), p,
		HookRenderAssets, []byte(`{"page_kind":"single","locale":"fa"}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if p.gotAssets.PageKind != "single" || p.gotAssets.Locale != "fa" {
		t.Fatalf("request lost: %+v", p.gotAssets)
	}
	var res struct {
		FooterScripts []struct {
			Src   string `json:"src"`
			Defer bool   `json:"defer"`
		} `json:"footer_scripts"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.FooterScripts) != 1 || res.FooterScripts[0].Src != "/shop/cart.js" {
		t.Fatalf("scripts: %s", out)
	}
	if !res.FooterScripts[0].Defer {
		t.Fatalf("defer lost: %s", out)
	}
}

// TestAnUnrelatedHookFallsThrough is what keeps the interception composable: implementing WidgetProvider
// must not swallow the hooks the author handles themselves.
func TestAnUnrelatedHookFallsThrough(t *testing.T) {
	_, ok, err := dispatchProvided(context.Background(), &provider{}, "content.saved", []byte(`{}`))
	if ok || err != nil {
		t.Fatalf("content.saved was intercepted: ok=%v err=%v", ok, err)
	}
}

// TestAPlainHandlerIsUntouched — a plugin implementing neither interface must see every hook itself, or
// adding this file would have broken every existing plugin.
func TestAPlainHandlerIsUntouched(t *testing.T) {
	for _, hook := range []string{HookWidgetDescribe, HookWidgetRender, HookRenderAssets} {
		if _, ok, _ := dispatchProvided(context.Background(), &plain{}, hook, []byte(`{}`)); ok {
			t.Fatalf("%s was intercepted for a handler that implements no provider", hook)
		}
	}
}

type plain struct{}

func (plain) Init(context.Context, *Core) (InitResult, error)   { return InitResult{}, nil }
func (plain) HandleEvent(context.Context, string, []byte) error { return nil }
func (plain) HandleHook(_ context.Context, _ string, p []byte) ([]byte, error) {
	return p, nil
}

// TestTheAssetSubscriptionIsAddedForYou covers the footgun the interface exists to remove.
//
// Membership, not position. This asserted `len(got) == 2 && got[1] == HookRenderAssets`, which held only
// while render.assets was the ONE subscription added for anybody — and that was the defect: `provider`
// here implements WidgetProvider too, and its widget hooks were never subscribed, so Core answered "not
// subscribed" every time it asked what widgets this plugin had. A positional assertion on a set fails the
// moment the set is right.
func TestTheAssetSubscriptionIsAddedForYou(t *testing.T) {
	has := func(list []string, want string) bool {
		for _, s := range list {
			if s == want {
				return true
			}
		}
		return false
	}

	got := withProvidedHooks(&provider{}, []string{"content.saved"})
	if !has(got, HookRenderAssets) {
		t.Fatalf("render.assets was not subscribed: %v", got)
	}
	if !has(got, "content.saved") {
		t.Fatalf("the plugin's own subscription was dropped: %v", got)
	}

	// Declared by hand as well? Still exactly once — a duplicate subscription is a double dispatch.
	got = withProvidedHooks(&provider{}, []string{HookRenderAssets})
	n := 0
	for _, s := range got {
		if s == HookRenderAssets {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("duplicated the subscription: %v", got)
	}

	// A plugin with no provider interface gets nothing added.
	if got := withProvidedHooks(&plain{}, nil); got != nil {
		t.Fatalf("subscribed a plugin that provides nothing: %v", got)
	}
}

// TestANumericWidgetFieldKeepsItsBOUNDS.
//
// `Min, Max, Step *float64 ` + "`json:\"min,omitempty\"`" + “ gave all three fields the tag "min", and Go's
// encoder drops a duplicated tag rather than picking one — so the whole group serialised as `{}`. A 1-to-5
// rating with 0.5 steps reached Core as a number field with no bounds at all, in every direction, for as
// long as the field has existed. `go vet` names it; no test did, because no test looked at the JSON.
func TestANumericWidgetFieldKeepsItsBounds(t *testing.T) {
	one, five, half := 1.0, 5.0, 0.5
	b, err := json.Marshal(WidgetField{
		Key: "rating", Type: FieldNumber, Min: &one, Max: &five, Step: &half,
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]float64{"min": 1, "max": 5, "step": 0.5} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v — the bound never reached Core: %s", k, got[k], want, b)
		}
	}
}
