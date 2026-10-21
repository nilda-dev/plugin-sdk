package nilda_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// The release-train rules (docs/RELEASE_TRAIN.md, process reform R5) that a test can hold.
//
// Rule: ONE tag per milestone, and a tagged version is a dated one. The CHANGELOG is what a plugin author reads to
// learn what a pin buys them; a version that is tagged but whose heading has no date (or no heading) is a release
// nobody can place, and a heading with a date for a version that was never tagged is a promise that is not kept.
// Two guards: the CHANGELOG's headings, and the surface only growing since the last tag.

var changelogHeading = regexp.MustCompile(`(?m)^## (v\d+\.\d+\.\d+) — (\d{4}-\d{2}-\d{2}|unreleased)\b`)

// TestTheChangelogDatesEveryTaggedVersion: every "## vX.Y.Z" heading is "— YYYY-MM-DD" (a real day), except the FIRST,
// which may read "— unreleased" while its tag does not exist yet; the moment the tag exists the heading is dated.
func TestTheChangelogDatesEveryTaggedVersion(t *testing.T) {
	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	var headings []string
	for _, l := range strings.Split(text, "\n") {
		if strings.HasPrefix(l, "## v") {
			headings = append(headings, l)
		}
	}
	if len(headings) == 0 {
		t.Fatal("CHANGELOG.md has no `## vX.Y.Z — date` section")
	}
	matches := changelogHeading.FindAllStringSubmatch(text, -1)
	if len(matches) != len(headings) {
		for _, h := range headings {
			if !changelogHeading.MatchString(h) {
				t.Errorf("CHANGELOG heading %q must read `## vX.Y.Z — YYYY-MM-DD (what it brings)` (or `— unreleased` for the first)", h)
			}
		}
		return
	}
	for i, m := range matches {
		version, when := m[1], m[2]
		if when == "unreleased" {
			if i != 0 {
				t.Errorf("%s is marked unreleased but is not the first section: only the newest can be waiting for its tag", version)
			}
			continue
		}
		if _, err := time.Parse("2006-01-02", when); err != nil {
			t.Errorf("%s: %q is not a real date", version, when)
		}
	}
	first := matches[0]
	if first[2] != "unreleased" {
		return
	}
	// The newest section says unreleased: that is only true while its tag does not exist.
	out, err := exec.Command("git", "tag", "--list", first[1]).Output()
	if err != nil {
		t.Skipf("git is unavailable here, so whether %s is tagged cannot be established: %v", first[1], err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("%s is tagged, but the CHANGELOG still calls it unreleased: date the heading in the commit the tag points at", first[1])
	}
}

// TestTheSurfaceOnlyGrowsBetweenTags: every line of surface.txt (and nildatest/surface.txt) at the nearest tag behind
// HEAD is still there. Additions are free; a removed or renamed name breaks every plugin that uses it — that is a
// protocol bump the owner decides (docs/RELEASE_TRAIN.md), not an edit. The ledger test above makes the change
// visible; this one makes it fail. SDK_ALLOW_SURFACE_REMOVAL=1 is the escape hatch for a removal the owner decided.
func TestTheSurfaceOnlyGrowsBetweenTags(t *testing.T) {
	out, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "--match", "v[0-9]*").Output()
	if err != nil {
		t.Skipf("no reachable tag (a shallow clone, or git is unavailable): %v", err)
	}
	tag := strings.TrimSpace(string(out))
	for _, file := range []string{"surface.txt", "nildatest/surface.txt"} {
		old, err := exec.Command("git", "show", tag+":"+file).Output()
		if err != nil {
			continue // the ledger did not exist at that tag: nothing was promised yet
		}
		now, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		have := map[string]bool{}
		for _, l := range strings.Split(string(now), "\n") {
			have[l] = true
		}
		var gone []string
		for _, l := range strings.Split(string(old), "\n") {
			if l != "" && !have[l] {
				gone = append(gone, l)
			}
		}
		if len(gone) > 0 && os.Getenv("SDK_ALLOW_SURFACE_REMOVAL") != "1" {
			t.Errorf("%s lost %d line(s) that %s promised, e.g. %q: additive changes only between tags "+
				"(docs/RELEASE_TRAIN.md). A deliberate removal is the owner's decision: then run with SDK_ALLOW_SURFACE_REMOVAL=1.",
				file, len(gone), tag, gone[0])
		}
	}
}
