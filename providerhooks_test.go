package nilda

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// IMPLEMENTING A PROVIDER INTERFACE HAS TO BE ENOUGH TO BE CALLED.
//
// Core refuses a hook the plugin did not subscribe to, by name, before the capability is consulted
// (Host.Call: `plugin %q is not subscribed to hook %q`). withProvidedHooks used to subscribe one
// interface of six, so a plugin implementing WidgetProvider under plain Serve compiled, loaded, was
// granted `widget`, ran, and was never once asked what widgets it had.
//
// This asserts the behaviour per interface — that the hooks come back — rather than that the table has
// the right shape, because the table is what was wrong.
func TestEveryProviderInterfaceSubscribesItsHooks(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		h     Handler
		hooks []string
	}{
		{"AssetProvider", assetOnly{}, []string{HookRenderAssets}},
		{"WidgetProvider", widgetOnly{}, []string{HookWidgetDescribe, HookWidgetRender}},
		{"AuthProvider", authOnly{}, []string{HookAuthDescribe, HookAuthStart, HookAuthComplete}},
		{"FieldProvider", fieldOnly{}, []string{HookFieldChoices, HookFieldValidate}},
		{"SearchProvider", searchOnly{}, []string{HookSearchConfigure, HookSearchIndex, HookSearchRemove,
			HookSearchTruncate, HookSearchQuery, HookSearchHealthy}},
		{"Commerce", commerceOnly{}, []string{HookCommerceProducts, HookCommerceProduct,
			HookCommerceEndpoints, HookCommerceCount}},
		{"PaymentGateway", gatewayOnly{}, []string{HookPaymentDescribe, HookPaymentStart, HookPaymentRefund}},
		{"PaymentConsumer", consumerOnly{}, []string{HookPaymentConfirm, HookPaymentSessionUpdated,
			HookPaymentRefundUpdated}},
	} {
		got := withProvidedHooks(c.h, nil)
		for _, want := range c.hooks {
			if !slices.Contains(got, want) {
				t.Errorf("a plugin implementing %s does not subscribe %q, so Core answers "+
					"\"not subscribed\" and the plugin is never asked — got %v", c.name, want, got)
			}
		}
	}
}

// TestASubscriptionIsNeverDuplicated — a repeated name is a double dispatch, and the single-purpose Serve
// wrappers already list their own hooks, so every one of them passes through here with them present.
func TestASubscriptionIsNeverDuplicated(t *testing.T) {
	t.Parallel()
	got := withProvidedHooks(widgetOnly{}, []string{HookWidgetDescribe, "content.saved"})
	seen := map[string]int{}
	for _, h := range got {
		seen[h]++
	}
	for h, n := range seen {
		if n > 1 {
			t.Errorf("%q is subscribed %d times; Core would dispatch it that many times", h, n)
		}
	}
	if !slices.Contains(got, "content.saved") {
		t.Errorf("the plugin's own subscriptions were dropped: %v", got)
	}
}

// TestAPlainPluginSubscribesNothingExtra. Adding to this table must never subscribe a plugin to a hook it
// cannot answer — that is a call it will fail, on every dispatch, forever.
func TestAPlainPluginSubscribesNothingExtra(t *testing.T) {
	t.Parallel()
	if got := withProvidedHooks(plainOnly{}, []string{"content.saved"}); len(got) != 1 {
		t.Errorf("a plugin implementing no provider was subscribed to %v", got)
	}
}

// TestEveryExportedProviderInterfaceIsInTheTable reads the SOURCE, because the defect was a MISSING row
// and a table cannot notice its own gap. Every exported interface in this package is either wired in
// providerHooks or named here with the reason it is not.
func TestEveryExportedProviderInterfaceIsInTheTable(t *testing.T) {
	t.Parallel()
	notAProvider := map[string]string{
		"Handler": "the mandatory interface itself; Serve takes it, so there is nothing to imply",
		// commerce.count is only ever asked of a plugin that answers commerce.products, and
		// DispatchCommerceHook reports "not handled" when the counter is absent.
		"CommerceCounter": "rides on Commerce, which subscribes commerce.count for it",
		// Handler plus PaymentGateway, named so ServePaymentGateway's parameter is checked by the compiler;
		// a plugin implementing it implements PaymentGateway, whose row subscribes it.
		"PaymentGatewayPlugin": "Handler and PaymentGateway together; PaymentGateway's row subscribes it",
	}
	wired := map[string]bool{}
	for _, p := range providerHooks {
		wired[p.name] = true
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parsing the package: %v", err)
	}
	var found int
	for _, f := range pkgs["nilda"].Files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				if _, isIface := ts.Type.(*ast.InterfaceType); !isIface {
					continue
				}
				found++
				name := ts.Name.Name
				if wired[name] {
					continue
				}
				if _, excused := notAProvider[name]; excused {
					continue
				}
				t.Errorf("exported interface %s is neither in providerHooks nor listed as not being a "+
					"provider — if a plugin implements it, nothing subscribes the hooks Core would need "+
					"to call it, and it fails the way WidgetProvider did: silently", name)
			}
		}
	}
	if found < 6 {
		t.Fatalf("only found %d exported interfaces; the parse is broken, not the table", found)
	}
}

// Minimal handlers, one per interface, implementing nothing but the shape.
type plainOnly struct{}

func (plainOnly) Init(context.Context, *Core) (InitResult, error)                  { return InitResult{}, nil }
func (plainOnly) HandleHook(_ context.Context, _ string, p []byte) ([]byte, error) { return p, nil }
func (plainOnly) HandleEvent(context.Context, string, []byte) error                { return nil }

type assetOnly struct{ plainOnly }

func (assetOnly) FooterScripts(context.Context, RenderAssetsRequest) []ScriptAsset { return nil }

type widgetOnly struct{ plainOnly }

func (widgetOnly) Widgets() []WidgetDef { return nil }
func (widgetOnly) RenderWidget(context.Context, WidgetRenderRequest) (string, error) {
	return "", nil
}

type authOnly struct{ plainOnly }

func (authOnly) Describe(context.Context, *Core) []AuthProviderStatus { return nil }
func (authOnly) Start(context.Context, *Core, AuthStartRequest) (string, error) {
	return "", nil
}
func (authOnly) Complete(context.Context, *Core, AuthCompleteRequest) (AuthAssertion, error) {
	return AuthAssertion{}, nil
}

type fieldOnly struct{ plainOnly }

func (fieldOnly) Choices(context.Context, *Core, FieldChoicesRequest) ([]FieldChoice, error) {
	return nil, nil
}
func (fieldOnly) Validate(context.Context, *Core, FieldValidateRequest) string { return "" }

type searchOnly struct{ plainOnly }

func (searchOnly) Configure(context.Context) error          { return nil }
func (searchOnly) Index(context.Context, []SearchDoc) error { return nil }
func (searchOnly) Remove(context.Context, []string) error   { return nil }
func (searchOnly) Truncate(context.Context) error           { return nil }
func (searchOnly) Healthy(context.Context) bool             { return true }
func (searchOnly) Query(context.Context, SearchQuery) (SearchResults, error) {
	return SearchResults{}, nil
}

type gatewayOnly struct{ plainOnly }

func (gatewayOnly) DescribePayments(context.Context) ([]PaymentMethod, error) { return nil, nil }
func (gatewayOnly) StartPayment(context.Context, PaymentSession) (PaymentStartResult, error) {
	return PaymentStartResult{}, nil
}
func (gatewayOnly) RefundPayment(context.Context, PaymentRefund, PaymentSession) (PaymentRefundResult, error) {
	return PaymentRefundResult{}, nil
}

type consumerOnly struct{ plainOnly }

func (consumerOnly) ConfirmPayment(context.Context, PaymentSession) (PaymentConfirmResult, error) {
	return PaymentConfirmResult{}, nil
}
func (consumerOnly) PaymentUpdated(context.Context, PaymentSession) error               { return nil }
func (consumerOnly) RefundUpdated(context.Context, PaymentRefund, PaymentSession) error { return nil }

type commerceOnly struct{ plainOnly }

func (commerceOnly) Products(context.Context, CommerceQuery) ([]CommerceProduct, error) {
	return nil, nil
}
func (commerceOnly) Product(context.Context, string) (CommerceProduct, bool) {
	return CommerceProduct{}, false
}
func (commerceOnly) Endpoints(context.Context) CommerceEndpoints { return CommerceEndpoints{} }
