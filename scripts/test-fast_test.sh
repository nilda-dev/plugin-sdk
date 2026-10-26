#!/bin/sh
# Test for scripts/test-fast: builds a throw-away Go module in a temp git repo, changes files, and checks which
# packages the script selects (--list) and that a real run returns the right exit code. Run:
#   sh scripts/test-fast_test.sh      (exit 0 = all passed; needs only `go` and `git`, no network)

here=$(cd "$(dirname "$0")" && pwd)
script="$here/test-fast"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
export GOFLAGS=-mod=mod GOWORK=off GOCACHE="${GOCACHE:-$tmp/gocache}"
pass=0; failed=0

ok()   { pass=$((pass + 1)); echo "ok   $1"; }
bad()  { failed=$((failed + 1)); echo "FAIL $1"; }
# expect NAME "pkgs that must be listed" "pkgs that must NOT be listed" -- uses $out
expect() {
  name=$1; must=$2; mustnot=$3; good=1
  for p in $must; do printf '%s\n' "$out" | grep -q " example.com/m/$p\$" || good=0; done
  for p in $mustnot; do printf '%s\n' "$out" | grep -q " example.com/m/$p\$" && good=0; done
  if [ "$good" = 1 ]; then ok "$name"; else bad "$name: got:
$out"; fi
}

repo="$tmp/repo"; mkdir -p "$repo/a" "$repo/b" "$repo/c" "$repo/d" "$repo/e/testdata" "$repo/internal/archtest" "$repo/docs"
cd "$repo" || exit 1
git init -q . && git config user.email t@t && git config user.name t
mkdir scripts
cp "$here/test-fast" "$here/heavy-guard" scripts/
printf 'internal/archtest\n' > scripts/test-fast.always
cat > go.mod <<'X'
module example.com/m

go 1.21
X
echo 'package a
func A() int { return 1 }' > a/a.go
echo 'package b
import "example.com/m/a"
func B() int { return a.A() }' > b/b.go
echo 'package c
func C() int { return 3 }' > c/c.go
cat > c/c_test.go <<'X'
package c_test

import (
	"testing"

	"example.com/m/b"
)

func TestC(t *testing.T) { _ = b.B() }
X
echo 'package d
func D() int { return 4 }' > d/d.go
echo 'package e
func E() int { return 5 }' > e/e.go
echo '{}' > e/testdata/x.json
cat > e/e_test.go <<'X'
package e

import "testing"

func TestE(t *testing.T) {}
X
echo 'package archtest' > internal/archtest/a.go
echo 'notes' > docs/n.md
echo 'package m' > m.go
git add -A && git commit -q -m base && git branch -q -M main && git update-ref refs/remotes/origin/main HEAD

list() { out=$(sh scripts/test-fast --list "$@" 2>&1); }

list; expect "no change selects nothing" "" "a b c d e internal/archtest"

echo 'package a
func A() int { return 2 }' > a/a.go
list; expect "uncommitted change to a: a changed, b dependent, c dependent through its test import" "a b c internal/archtest" "d e"
list --committed-only; expect "--committed-only ignores the uncommitted change" "" "a b c"
git add -A && git commit -q -m changeA
list --committed-only; expect "a committed change is seen by --committed-only" "a b c" "d e"

git reset -q --hard origin/main
echo '{"k":1}' > e/testdata/x.json
list; expect "a testdata file selects only its own package" "e internal/archtest" "a b c d"
git checkout -q -- .
echo '// t' >> e/e_test.go
list; expect "a test file selects only its own package" "e internal/archtest" "a b c d"
git checkout -q -- . ; echo more >> docs/n.md
echo '# s' >> scripts/helper.sh
list; expect "a shell script under scripts/ selects nothing" "" "a b c d e"
printf '%s\n' "$out" | grep -q ' example.com/m$' && bad "a shell script under scripts/ selected the ROOT package" || ok "the root package is not selected by scripts/ files"
git checkout -q -- .; rm -f scripts/helper.sh
mkdir -p scripts/tool; printf 'package main\nfunc main() {}\n' > scripts/tool/main.go; git add -A; git commit -q -m tool; git update-ref refs/remotes/origin/main HEAD
echo '// t' >> scripts/tool/main.go
list; expect "a Go package living under scripts/ is selected" "scripts/tool" "a b c d e"
git checkout -q -- .; git reset -q --hard HEAD
list; echo more >> docs/n.md
list; expect "a markdown file selects nothing" "" "a b c d e"
git checkout -q -- .
echo 'package d
func D2() int { return 6 }' > d/new.go
list; expect "an untracked new file is seen" "d internal/archtest" "a b c e"
rm d/new.go
echo '// touch' >> go.mod
list; expect "a go.mod change selects every package" "a b c d e" ""
git checkout -q -- .

# a real run: green -> 0 ; a failing test -> non-zero ; a refusing guard -> 3 and nothing runs
echo 'package e
func E() int { return 7 }' > e/e.go
sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && grep -q "elapsed" "$tmp/run.out" && ok "green run exits 0 and prints elapsed" || { bad "green run: rc=$rc"; cat "$tmp/run.out"; }
[ ! -e .git/nilda-way/fast-lane-ok ] && grep -q "green on uncommitted changes: no marker for HEAD" "$tmp/run.out" && ok "a green run on a dirty tree (uncommitted change in the run set) writes no marker and still exits 0" || { bad "dirty green run wrote a marker or did not say so"; cat "$tmp/run.out"; }
# The fast lane answers an unchanged green package from Go's test cache; --no-cache (the full lane's way) runs it.
sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && grep -q "(cached)" "$tmp/run.out" && ok "a second run of an unchanged package is answered from the cache" || { bad "cached run: rc=$rc"; cat "$tmp/run.out"; }
sh scripts/test-fast --no-guard --no-cache >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && ! grep -q "(cached)" "$tmp/run.out" && grep -q -- "-count=1" "$tmp/run.out" && ok "--no-cache runs every selected test for real" || { bad "no-cache run: rc=$rc"; cat "$tmp/run.out"; }
cat > e/e_test.go <<'X'
package e

import "testing"

func TestE(t *testing.T) { t.Fatal("red") }
X
sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" != 0 ] && grep -q "go test FAILED" "$tmp/run.out" && ok "a red test makes the run fail" || { bad "red run: rc=$rc"; cat "$tmp/run.out"; }
HEAVY_GUARD_MIN_FREE_GB=100000000 sh scripts/test-fast >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 3 ] && ! grep -q "go vet \.\.\." "$tmp/run.out" && ok "a refusing guard stops the run before anything starts" || { bad "guard: rc=$rc"; cat "$tmp/run.out"; }

# --- install-hooks.sh ---------------------------------------------------------------------------------------
git checkout -q -- . 2>/dev/null; git clean -fdq
cp "$here/install-hooks.sh" "$here/commit-msg-check" scripts/   # the hooks it writes call both
sh scripts/install-hooks.sh >/dev/null 2>&1
[ -x .git/hooks/pre-push ] && grep -q 'test-fast" --committed-only' .git/hooks/pre-push && ok "install-hooks writes an executable pre-push" || bad "install-hooks did not write the hook"
sh scripts/install-hooks.sh >/dev/null 2>&1 && ok "install-hooks is idempotent" || bad "second install failed"
# The hook really runs the lane: with a refusing guard the hook (and so the push) fails with 3.
echo '// pushed change' >> a/a.go; git add a/a.go; git commit -q -m pushed
HEAVY_GUARD_MIN_FREE_GB=100000000 .git/hooks/pre-push origin url >/dev/null 2>&1
[ $? = 0 ] && bad "hook passed although the guard refused" || ok "the installed hook fails when the lane fails"
sh scripts/install-hooks.sh --uninstall >/dev/null 2>&1
[ -e .git/hooks/pre-push ] && bad "uninstall left the hook" || ok "uninstall removes our hook"
printf '#!/bin/sh\necho foreign\n' > .git/hooks/pre-push
sh scripts/install-hooks.sh >/dev/null 2>&1 && bad "overwrote a foreign hook" || ok "a foreign pre-push is never overwritten"
mkdir -p custom-hooks; git config core.hooksPath custom-hooks; rm .git/hooks/pre-push
sh scripts/install-hooks.sh >/dev/null 2>&1
[ -x custom-hooks/pre-push ] && ok "core.hooksPath is honoured" || bad "hooksPath not honoured"

# --- the green marker (<git common dir>/nilda-way/fast-lane-ok) ---------------------------------------------
git config --unset core.hooksPath; rm -rf custom-hooks; git checkout -q -- . 2>/dev/null; git clean -fdq
marker="$repo/.git/nilda-way/fast-lane-ok"; rm -rf "$repo/.git/nilda-way"
head=$(git rev-parse HEAD)
sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && grep -qx "$head" "$marker" 2>/dev/null && ok "a green run on a clean tree records HEAD's sha in the common git dir" || { bad "clean green run: rc=$rc"; cat "$tmp/run.out"; }
sh scripts/test-fast --no-guard --list >/dev/null 2>&1; git commit -q --allow-empty -m e1; head=$(git rev-parse HEAD)
sh scripts/test-fast --no-guard --list >/dev/null 2>&1
grep -qx "$head" "$marker" && bad "--list wrote a marker" || ok "--list never writes a marker"
HEAVY_GUARD_MIN_FREE_GB=100000000 sh scripts/test-fast >/dev/null 2>&1; rc=$?
[ "$rc" = 3 ] && ! grep -qx "$head" "$marker" && ok "a guard refusal never writes a marker" || bad "guard refusal: rc=$rc"
echo 'package d
func D3() int { return 8 }' > d/untracked.go
sh scripts/test-fast --no-guard --committed-only >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && grep -qx "$head" "$marker" && ok "--committed-only: a dirty file outside the run set does not block the marker" || { bad "unrelated dirty file: rc=$rc"; cat "$tmp/run.out"; }
rm d/untracked.go
git commit -q --allow-empty -m e2; head=$(git rev-parse HEAD)
echo '// dirty' >> a/a.go
sh scripts/test-fast --no-guard --committed-only >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" = 0 ] && ! grep -qx "$head" "$marker" && grep -q "no marker for HEAD" "$tmp/run.out" && ok "--committed-only on a tree that is dirty inside the run set writes no marker" || { bad "committed-only dirty: rc=$rc"; cat "$tmp/run.out"; }
git checkout -q -- a/a.go
# a git worktree: `.git` there is a FILE; the marker still lands in the shared common dir
wt="$tmp/wt"; git worktree add -q "$wt" -b wtbranch HEAD
(cd "$wt" && git commit -q --allow-empty -m wt && sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1); rc=$?
whead=$(git -C "$wt" rev-parse HEAD)
[ -f "$wt/.git" ] && [ "$rc" = 0 ] && grep -qx "$whead" "$marker" && [ ! -e "$wt/.git/nilda-way" ] && ok "a worktree run records its commit in the common git dir" || { bad "worktree marker: rc=$rc"; cat "$tmp/run.out"; }
git worktree remove --force "$wt"; git branch -q -D wtbranch
# bounded: only the last 200 shas are kept
i=0; : > "$marker"; while [ $i -lt 250 ]; do printf '%040d\n' $i >> "$marker"; i=$((i + 1)); done
sh scripts/test-fast --no-guard >/dev/null 2>&1; rc=$?
[ "$rc" = 0 ] && [ "$(wc -l < "$marker" | tr -d ' ')" = 200 ] && grep -qx "$(git rev-parse HEAD)" "$marker" && ok "the marker file keeps only the last 200 shas" || bad "bounded marker: rc=$rc lines=$(wc -l < "$marker")"
# a red test never writes a marker
cat > c/c_test.go <<'X'
package c_test

import "testing"

func TestC(t *testing.T) { t.Fatal("red") }
X
git add -A; git commit -q -m red; head=$(git rev-parse HEAD)
sh scripts/test-fast --no-guard >"$tmp/run.out" 2>&1; rc=$?
[ "$rc" != 0 ] && ! grep -qx "$head" "$marker" && ok "a failing run writes no marker" || { bad "red run marker: rc=$rc"; cat "$tmp/run.out"; }

echo "$pass passed, $failed failed"
[ "$failed" = 0 ]
