package nilda_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryRepositoryURLTheDocsClaimIsUnderTheRightGroup.
//
// Every Nilda repository lives under the `nildalabs` GitLab group, and every one of them is nested a level
// deeper than the obvious guess — `nildalabs/nildacms/core`, `nildalabs/nilda-sdk/plugin-sdk`,
// `nildalabs/nilda-plugins/commerce`. Dropping the group is the single mistake this ecosystem keeps making,
// and it is invisible from inside: everything resolves through go.work or a replace directive, so a wrong
// path is only ever wrong for the stranger reading the document.
//
// It cost a real bug already. The SDK's own module was `gitlab.com/nilda-sdk/plugin-sdk` — a group that does
// not exist — so `go build` on a freshly scaffolded plugin failed with `unknown revision`, indistinguishable
// from the private-repo error, and the two hid behind each other. The README then repeated the same mistake
// in EVERY line of its repository list: core, central, theme-sdk and all three plugins.
//
// A line that says a path is the OLD one is not a claim about where anything lives, so it is skipped —
// recording a rename is the honest thing to do and must not be punished.
func TestEveryRepositoryURLTheDocsClaimIsUnderTheRightGroup(t *testing.T) {
	url := regexp.MustCompile(`gitlab\.com/[a-zA-Z0-9._/-]+`)
	saysItIsOld := regexp.MustCompile(`(?i)\bwas\b|\bused to\b|former|old path|renamed`)

	var checked int
	for _, doc := range docsIn(t) {
		raw, err := os.ReadFile(doc)
		if err != nil {
			t.Fatalf("reading %s: %v", doc, err)
		}
		for n, line := range strings.Split(string(raw), "\n") {
			if saysItIsOld.MatchString(line) {
				continue
			}
			for _, u := range url.FindAllString(line, -1) {
				checked++
				if !strings.HasPrefix(u, "gitlab.com/nildalabs/") {
					t.Errorf("%s:%d claims %q — every Nilda repository is under gitlab.com/nildalabs/. "+
						"A path missing the group resolves for nobody outside the team, and reports itself "+
						"as `unknown revision`, which reads like a private-repo error.",
						filepath.Base(doc), n+1, u)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no repository URLs found in the docs — the extractor broke, and a broken one passes")
	}
	t.Logf("%d repository URLs checked", checked)
}

// docsIn lists this repo's markdown.
func docsIn(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, dir := range []string{".", "docs"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".md") {
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	if len(out) < 4 {
		t.Fatalf("found only %d markdown files — this repo has more", len(out))
	}
	return out
}
