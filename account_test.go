package nilda

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type fakeAccount struct {
	finished AccountFinish
	gone     bool
}

func (f *fakeAccount) AccountStart(_ context.Context, in AccountStart) (AccountStartResult, error) {
	return AccountStartResult{AuthorizeURL: "https://higgsfield.ai/authorize?redirect_uri=" + in.RedirectURI, State: "s1"}, nil
}

func (f *fakeAccount) AccountFinish(_ context.Context, in AccountFinish) (AccountStatus, error) {
	f.finished = in
	if in.Error != "" {
		return AccountStatus{}, errors.New("Higgsfield was not connected")
	}
	return AccountStatus{Connected: true, Who: "lena@lume.at"}, nil
}

func (f *fakeAccount) AccountStatus(context.Context) (AccountStatus, error) {
	return AccountStatus{Connected: !f.gone}, nil
}

func (f *fakeAccount) AccountDisconnect(context.Context) error { f.gone = true; return nil }

// The four account hooks reach the plugin's handler with Core's words and come back as Core reads them; any other
// hook is left for the plugin's own.
func TestTheAccountHooksRouteToTheHandler(t *testing.T) {
	ctx := context.Background()
	h := &fakeAccount{}
	out, handled, err := DispatchAccount(ctx, AccountStartHook, []byte(`{"redirect_uri":"https://lume.at/cb"}`), h)
	var start AccountStartResult
	if !handled || err != nil || json.Unmarshal(out, &start) != nil || start.State != "s1" ||
		start.AuthorizeURL != "https://higgsfield.ai/authorize?redirect_uri=https://lume.at/cb" {
		t.Fatalf("start: %s %v %v", out, handled, err)
	}
	out, _, err = DispatchAccount(ctx, AccountFinishHook, []byte(`{"state":"s1","code":"c"}`), h)
	var st AccountStatus
	if err != nil || json.Unmarshal(out, &st) != nil || !st.Connected || st.Who != "lena@lume.at" || h.finished.Code != "c" {
		t.Fatalf("finish: %s %v", out, err)
	}
	if _, _, err := DispatchAccount(ctx, AccountFinishHook, []byte(`{"state":"s1","error":"access_denied"}`), h); err == nil {
		t.Fatal("a refused sign-in was reported as connected")
	}
	if _, _, err := DispatchAccount(ctx, AccountDisconnectHook, nil, h); err != nil || !h.gone {
		t.Fatalf("disconnect: %v", err)
	}
	out, _, _ = DispatchAccount(ctx, AccountStatusHook, nil, h)
	if string(out) != `{"connected":false}` {
		t.Fatalf("status after disconnect: %s", out)
	}
	if _, handled, _ := DispatchAccount(ctx, AdminActionHook, nil, h); handled {
		t.Fatal("a row action was taken as an account hook")
	}
	if _, handled, err := DispatchAccount(ctx, AccountStatusHook, nil, nil); !handled || err == nil {
		t.Fatal("a plugin with no handler must answer the hook with an error, not pass it on")
	}
}
