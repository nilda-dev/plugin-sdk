package nilda

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"testing"
)

// A MEMBERS-ONLY VIEW ASKS FOR SIGN-IN THE WAY CORE'S SHELL LISTENS FOR (docs/COMMERCE.md "Account views"). The
// shell draws Core's sign-in panel only for a 401 that carries Nilda-Sign-In: required; any other refusal is a
// failure it reports. The answer must not be cached: the same address answers the view once the buyer signs in.
func TestSignInRequiredSetsTheHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	CommerceSignInRequired(rec)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status %d, want 401", rec.Code)
	}
	if got := rec.Header().Get(CommerceSignInHeader); got != "required" || CommerceSignInHeader != "Nilda-Sign-In" {
		t.Errorf("%s: %q, want \"required\" under the header Core's shell reads (Nilda-Sign-In)", CommerceSignInHeader, got)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q, want no-store", cc)
	}
}

// coreAccountVocabulary is Core's own list: the account widget's style parts (core
// internal/pagebuilder/styleparts.go accountVocabularyParts), held to the same thirteen by Core's
// TestAccountStylePartsDeclared: twelve classes (thirteen parts — the current link is the link with
// aria-current="page"). A class on one side only is a piece one side cannot style.
var coreAccountVocabulary = []string{
	"pb-account-nav", "pb-account-link", "pb-account-card", "pb-account-row", "pb-account-chip",
	"pb-account-step", "pb-account-primary", "pb-account-secondary", "pb-account-field",
	"pb-account-error", "pb-account-success", "pb-account-table",
}

func TestTheVocabularyListIsCoresOwn(t *testing.T) {
	got := slices.Clone(AccountVocabulary)
	want := slices.Clone(coreAccountVocabulary)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("AccountVocabulary = %v\nCore declares     %v", got, want)
	}
	// The guide lists the same classes, each once.
	doc, err := os.ReadFile("docs/COMMERCE.md")
	if err != nil {
		t.Fatalf("reading docs/COMMERCE.md: %v", err)
	}
	inDoc := map[string]int{}
	for _, m := range regexp.MustCompile("`\\.(pb-account-[a-z]+)`").FindAllStringSubmatch(string(doc), -1) {
		inDoc[m[1]]++
	}
	for _, c := range AccountVocabulary {
		if inDoc[c] != 1 {
			t.Errorf("docs/COMMERCE.md names .%s %d times in its vocabulary table, want once", c, inDoc[c])
		}
	}
	for c := range inDoc {
		if !slices.Contains(AccountVocabulary, c) {
			t.Errorf("docs/COMMERCE.md names .%s, which is not in AccountVocabulary", c)
		}
	}
}

// The event a shop claims a guest's orders on is Core's name, spelled once.
func TestTheEmailVerifiedEventIsCoresName(t *testing.T) {
	if EventUserEmailVerified != "user.email_verified" {
		t.Fatalf("EventUserEmailVerified = %q, want Core's user.email_verified", EventUserEmailVerified)
	}
}
