package nilda

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// printedEverywhere is v through every way a plugin author prints or logs a handle: fmt's verbs, the SDK's own logger
// (directly and nested in a struct), and slog's standard text and JSON handlers.
func printedEverywhere(t *testing.T, v any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x"} {
		out["fmt "+verb] = fmt.Sprintf(verb, v)
	}
	out["fmt Sprint"] = fmt.Sprint(v)
	out["fmt Sprintln"] = fmt.Sprintln(v)

	var sdk bytes.Buffer
	log := newLogger(&sdk, "shop")
	log.Info("init", "core", v)
	log.Info("nested", "state", struct{ Handle any }{v})
	log.Info("slice", "all", []any{v})
	out["sdk logger"] = sdk.String()

	var text, js bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("init", "core", v)
	slog.New(slog.NewJSONHandler(&js, nil)).Info("init", "core", v)
	out["slog text"] = text.String()
	out["slog json"] = js.String()
	return out
}

func assertNoSecret(t *testing.T, what string, printed map[string]string, secrets ...string) {
	t.Helper()
	for how, line := range printed {
		for _, s := range secrets {
			if strings.Contains(line, s) {
				t.Errorf("%s through %s printed the secret %q:\n%s", what, how, s, line)
			}
		}
	}
}

// THE DATASTORE PASSWORD NEVER REACHES A LOG LINE (the 2026-11-16 hunt, finding 1). Core.DatastoreDSN is an exported
// field holding the plugin's Postgres role and its password; `core.Log().Info("x", "core", core)` and %+v wrote it in
// clear. The owner's settings (which can be an API key) and the API token rode along through %+v.
func TestCorePrintsWithoutItsDatastorePassword(t *testing.T) {
	core := NewCoreForTest("shop", []string{"datastore"}, nil, "http://core.local", "nlda_apitoken", nil)
	core.DatastoreDSN = "postgres://plugin_shop:s3cr3t-pw@db.internal:5432/nilda?sslmode=disable"
	core.SiteURL = "https://shop.example"
	SetSettingsForTest(core, map[string]any{"stripe_key": "sk_live_ownerkey"})

	for _, v := range []any{core, *core} {
		printed := printedEverywhere(t, v)
		assertNoSecret(t, fmt.Sprintf("%T", v), printed, "s3cr3t-pw", "nlda_apitoken", "sk_live_ownerkey")
		// Still useful: the role, the host and the key are what an operator reading the line needs.
		for _, how := range []string{"fmt %+v", "sdk logger", "slog text"} {
			if line := printed[how]; !strings.Contains(line, "plugin_shop") || !strings.Contains(line, "db.internal") ||
				!strings.Contains(line, "shop") || !strings.Contains(line, redactedMark) {
				t.Errorf("%T through %s lost what it should show: %s", v, how, line)
			}
		}
	}

	var nilCore *Core
	if got := fmt.Sprintf("%+v", nilCore); got != "<nil>" {
		t.Errorf("a nil *Core printed %q", got)
	}
	var buf bytes.Buffer
	newLogger(&buf, "").Info("x", "core", nilCore)
	if !strings.Contains(buf.String(), `"core":"\u003cnil\u003e"`) {
		t.Errorf("a nil *Core logged %s", buf.String())
	}
}

func TestRedactDSNCoversBothPostgresForms(t *testing.T) {
	for dsn, want := range map[string]string{
		"":                                       "",
		"postgres://role:pw@h:5432/db":           "postgres://role:[redacted]@h:5432/db",
		"postgresql://role@h/db?password=pw":     "postgresql://role@h/db?password=[redacted]",
		"postgres://role@h/db?sslmode=verify":    "postgres://role@h/db?sslmode=verify",
		"host=h user=role password=pw dbname=db": "host=h user=role password=[redacted] dbname=db",
		"host=h password='p w\\'x' dbname=db":    "host=h password=[redacted] dbname=db",
		"host=h PASSWORD = pw":                   "host=h PASSWORD = [redacted]",
	} {
		if got := redactDSN(dsn); got != want {
			t.Errorf("redactDSN(%q) = %q; want %q", dsn, got, want)
		}
	}
}
