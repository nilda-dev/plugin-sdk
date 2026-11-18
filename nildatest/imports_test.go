package nildatest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	nilda "github.com/nilda-dev/plugin-sdk"
)

// THE IMPORT DOORS, through the SDK's typed client, as Core answers them (core internal/pluginimport rest.go, redirects.go,
// checkpoint.go): what an earlier import made, by source and id; old addresses written as rules — never one leaving the site,
// one the site serves, or one already there — and taken back only while each is exactly the rule written; a safety copy, or
// the refusal that must stop an import; and nothing for a plugin without `import`.
func TestTheImportDoorsAnswerAsCoresDo(t *testing.T) {
	ctx := context.Background()
	doors := NewImports()
	doors.Map("wordpress", "shop.example#media:68", "media", "0b9f0c9e-68aa-4a11-9a3c-000000000068")
	doors.Serve("/shop/")
	core, _, srv := doors.Core("site_import", "import")
	defer srv.Close()
	api := core.API()

	found, err := api.LookupImported(ctx, "wordpress", []string{"shop.example#media:68", "shop.example#media:69"})
	if err != nil || len(found) != 2 || !found[0].Found || found[0].Kind != "media" || found[0].TargetID == "" || found[1].Found {
		t.Fatalf("the lookup: %+v %v", found, err)
	}
	if _, err := api.LookupImported(ctx, "wordpress", nil); err == nil {
		t.Error("a lookup of no ids was sent")
	}
	if _, err := api.LookupImported(ctx, "wordpress", make([]string, nilda.MaxImportBatch+1)); err == nil {
		t.Error("a lookup past the bound was sent")
	}

	res, err := api.AddImportRedirects(ctx, []nilda.ImportRedirect{{From: "/product/apron/", To: "/shop/apron/"},
		{From: "https://evil.example/", To: "/shop/"}, {From: "/old/", To: "//evil.example/"}, {From: "/shop/", To: "/shop/apron/"},
		{From: "/product/apron/", To: "/shop/other/"}})
	if err != nil || len(res) != 5 {
		t.Fatalf("the rules: %+v %v", res, err)
	}
	if !res[0].Written || res[0].ID == "" || res[1].Refused != "from_not_local" || res[2].Refused != "to_not_local" ||
		res[3].Written || res[4].Written {
		t.Errorf("the rules' outcomes %+v", res)
	}
	if rules := doors.Redirects(); len(rules) != 1 || rules[0].From != "/product/apron/" || rules[0].To != "/shop/apron/" {
		t.Errorf("the site holds %+v", rules)
	}
	gone, err := api.RemoveImportRedirects(ctx, []nilda.ImportRedirectRemoval{{ID: res[0].ID, From: "/product/apron/", To: "/shop/changed/"},
		{ID: res[0].ID, From: "/product/apron/", To: "/shop/apron/"}})
	if err != nil || len(gone) != 2 || gone[0] || !gone[1] || len(doors.Redirects()) != 0 {
		t.Errorf("the removal: %v %v (left %+v)", gone, err, doors.Redirects())
	}

	copy1, err := api.ImportCheckpoint(ctx, "shop import job-7")
	if err != nil || copy1.ID == "" || doors.Checkpoints() != 1 {
		t.Fatalf("the safety copy: %+v %v", copy1, err)
	}
	if _, err := api.ImportCheckpoint(ctx, "  "); err == nil {
		t.Error("a safety copy with no reason was asked")
	}
	doors.RefuseCheckpoints(true)
	var apiErr *nilda.APIError
	if _, err := api.WithMaxRetries(0).ImportCheckpoint(ctx, "shop import job-7"); !errors.As(err, &apiErr) ||
		apiErr.Status != http.StatusServiceUnavailable {
		t.Errorf("a site that cannot make a copy: %v", err)
	}

	stranger, _, srv2 := doors.Core("stranger", "content.read")
	defer srv2.Close()
	if _, err := stranger.API().LookupImported(ctx, "wordpress", []string{"x"}); !errors.As(err, &apiErr) ||
		apiErr.Status != http.StatusForbidden || !strings.Contains(apiErr.Body, "import") {
		t.Errorf("a plugin without import: %v", err)
	}
	if doors.Lookups() != 1 {
		t.Errorf("%d lookups answered, want the granted plugin's one", doors.Lookups())
	}
}
