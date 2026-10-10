package nildatest_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
	"github.com/nilda-dev/plugin-sdk/nildatest"
)

func TestASecretIsKeptReadBackAndRemoved(t *testing.T) {
	sec := nildatest.NewSecrets()
	core, _, srv := sec.Core("higgsfield", "secrets")
	defer srv.Close()
	ctx := context.Background()
	s := core.API().Secrets()

	if v, found, err := s.Get(ctx, "refresh_token"); err != nil || found || v != "" {
		t.Fatalf("a secret never set: %q %v %v", v, found, err)
	}
	if err := s.Set(ctx, "refresh_token", "rt_1"); err != nil {
		t.Fatal(err)
	}
	// A refresh token rotates on every use: the second write replaces the first.
	if err := s.Set(ctx, "refresh_token", "rt_2"); err != nil {
		t.Fatal(err)
	}
	if v, found, err := s.Get(ctx, "refresh_token"); err != nil || !found || v != "rt_2" {
		t.Fatalf("read back %q %v %v", v, found, err)
	}
	if v, ok := sec.Get("higgsfield", "refresh_token"); !ok || v != "rt_2" {
		t.Fatalf("Core holds %q %v", v, ok)
	}
	if removed, err := s.Delete(ctx, "refresh_token"); err != nil || !removed {
		t.Fatalf("delete: %v %v", removed, err)
	}
	if removed, err := s.Delete(ctx, "refresh_token"); err != nil || removed {
		t.Fatalf("deleting twice must find nothing and not fail: %v %v", removed, err)
	}
	if _, found, _ := s.Get(ctx, "refresh_token"); found {
		t.Fatal("a deleted secret was found")
	}
}

func TestOnePluginNeverSeesAnothersSecret(t *testing.T) {
	sec := nildatest.NewSecrets()
	a, _, sa := sec.Core("alpha", "secrets")
	defer sa.Close()
	b, _, sb := sec.Core("beta", "secrets")
	defer sb.Close()
	ctx := context.Background()

	if err := a.API().Secrets().Set(ctx, "token", "alpha-only"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := b.API().Secrets().Get(ctx, "token"); err != nil || found {
		t.Fatalf("beta read alpha's secret: %v %v", found, err)
	}
	if removed, err := b.API().Secrets().Delete(ctx, "token"); err != nil || removed {
		t.Fatalf("beta removed alpha's secret: %v %v", removed, err)
	}
	if v, _, _ := a.API().Secrets().Get(ctx, "token"); v != "alpha-only" {
		t.Fatalf("alpha's secret did not survive: %q", v)
	}
}

func TestAPluginThatNeverDeclaredSecretsIsRefused(t *testing.T) {
	sec := nildatest.NewSecrets()
	// content.read gives the plugin an API client and no place to keep credentials.
	core, _, srv := sec.Core("nosecrets", "content.read")
	defer srv.Close()
	err := core.API().Secrets().Set(context.Background(), "token", "x")
	var apiErr *nilda.APIError
	if !errors.As(err, &apiErr) || !apiErr.Forbidden() {
		t.Fatalf("a plugin without the capability: %v", err)
	}
	// The refusal names the capability to declare, as Core's does (the JSON escapes its quotes).
	if !strings.Contains(apiErr.Error(), "secrets") || !strings.Contains(apiErr.Error(), "capability") {
		t.Fatalf("the refusal does not name the capability: %v", err)
	}
}

func TestABadNameOrValueNeverLeavesThePlugin(t *testing.T) {
	sec := nildatest.NewSecrets()
	core, _, srv := sec.Core("alpha", "secrets")
	defer srv.Close()
	ctx := context.Background()
	s := core.API().Secrets()

	for _, n := range []string{"Bad-Name", "", "has space", strings.Repeat("a", 65)} {
		if err := s.Set(ctx, n, "v"); err == nil {
			t.Errorf("the name %q was accepted", n)
		}
		if _, _, err := s.Get(ctx, n); err == nil {
			t.Errorf("the name %q was read", n)
		}
		if _, err := s.Delete(ctx, n); err == nil {
			t.Errorf("the name %q was deleted", n)
		}
	}
	if err := s.Set(ctx, "token", ""); err == nil {
		t.Error("an empty value was accepted")
	}
	if err := s.Set(ctx, "token", strings.Repeat("x", 4097)); err == nil {
		t.Error("an oversize value was accepted")
	}
	if len(sec.Names("alpha")) != 0 {
		t.Fatalf("a refused call still stored something: %v", sec.Names("alpha"))
	}
}

func TestAPluginAtTheLimitCanRotateButNotAdd(t *testing.T) {
	sec := nildatest.NewSecrets()
	core, _, srv := sec.Core("alpha", "secrets")
	defer srv.Close()
	ctx := context.Background()
	s := core.API().Secrets()

	for i := 0; i < 32; i++ {
		if err := s.Set(ctx, fmt.Sprintf("n%02d", i), "v"); err != nil {
			t.Fatalf("name %d: %v", i, err)
		}
	}
	err := s.Set(ctx, "one_too_many", "v")
	var apiErr *nilda.APIError
	if !errors.As(err, &apiErr) || apiErr.Forbidden() || apiErr.NotFound() {
		t.Fatalf("the 33rd name: %v", err)
	}
	if err := s.Set(ctx, "n00", "rotated"); err != nil {
		t.Fatalf("a full plugin could not rotate a token it already kept: %v", err)
	}
}

func TestAnUninstallTakesTheSecretsAway(t *testing.T) {
	sec := nildatest.NewSecrets()
	core, _, srv := sec.Core("alpha", "secrets")
	defer srv.Close()
	if err := core.API().Secrets().Set(context.Background(), "token", "v"); err != nil {
		t.Fatal(err)
	}
	sec.Uninstall("alpha")
	if _, found, _ := core.API().Secrets().Get(context.Background(), "token"); found {
		t.Fatal("the secret outlived the plugin")
	}
}

// A plugin with no API at all gets the client's own error, not a nil dereference.
func TestSecretsOnAPluginWithNoAPIFailWithAnError(t *testing.T) {
	core, _ := nildatest.New("alpha", "kv")
	if core.HasAPI() {
		t.Fatal("kv granted API access: this test no longer pins a plugin with none")
	}
	if err := core.API().Secrets().Set(context.Background(), "token", "v"); err == nil {
		t.Fatal("a plugin with no API kept a secret")
	}
}
