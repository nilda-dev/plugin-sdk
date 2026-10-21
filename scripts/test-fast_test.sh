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
cp "$here/install-hooks.sh" scripts/
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

echo "$pass passed, $failed failed"
[ "$failed" = 0 ]
