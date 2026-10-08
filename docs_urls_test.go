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
// Every Nilda repository lives on GitHub under the `nilda-dev` organisation, one level deep:
// `github.com/nilda-dev/core`, `github.com/nilda-dev/plugin-sdk`, `github.com/nilda-dev/plugin-commerce`.
// The GitLab group (`gitlab.com/nildalabs/...`) is deleted (2026-10-06), so a GitLab address in a document
// sends the reader to nothing. Everything resolves through go.work or a replace directive, so a wrong path is
// only ever wrong for the stranger reading the document — which is why this reads the documents.
//
// It cost a real bug already. The SDK's own module was once `gitlab.com/nilda-sdk/plugin-sdk` — a group that
// did not exist — so `go build` on a freshly scaffolded plugin failed with `unknown revision`,
// indistinguishable from the private-repo error, and the two hid behind each other.
//
// A line that says a path is the OLD one is not a claim about where anything lives, so it is skipped —
// recording a rename is the honest thing to do and must not be punished.
func TestEveryRepositoryURLTheDocsClaimIsUnderTheRightGroup(t *testing.T) {
	gitlab := regexp.MustCompile(`gitlab\.com[/:][a-zA-Z0-9._/-]+`)
	ours := regexp.MustCompile(`github\.com[/:]([a-zA-Z0-9._-]*nilda[a-zA-Z0-9._-]*)/[a-zA-Z0-9._/-]+`)
	saysItIsOld := regexp.MustCompile(`(?i)\bwas\b|\bused to\b|former|old path|renamed|retired|deleted`)

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
			for _, u := range gitlab.FindAllString(line, -1) {
				checked++
				t.Errorf("%s:%d claims %q — GitLab is gone; every Nilda repository is under github.com/nilda-dev/.",
					filepath.Base(doc), n+1, u)
			}
			for _, m := range ours.FindAllStringSubmatch(line, -1) {
				checked++
				if m[1] != "nilda-dev" {
					t.Errorf("%s:%d claims %q — every Nilda repository is under github.com/nilda-dev/. "+
						"A path under another owner resolves for nobody, and reports itself as `unknown revision`, "+
						"which reads like a private-repo error.", filepath.Base(doc), n+1, m[0])
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
