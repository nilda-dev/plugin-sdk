#!/bin/sh
# Test for scripts/heavy-guard. Stubs df, uptime, ps, du and go first on PATH, so nothing real is read,
# cleaned or killed. Run: sh scripts/heavy-guard_test.sh   (exit 0 = all passed)

here=$(cd "$(dirname "$0")" && pwd)
guard="$here/heavy-guard"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
mkdir "$tmp/bin"
export TEST_PID=$$
export STUB_LOG="$tmp/log"
: > "$STUB_LOG"

cat > "$tmp/bin/df" <<'EOF'
#!/bin/sh
echo "Filesystem 1024-blocks Used Available Capacity Mounted on"
echo "/dev/x 480000000 100000 ${STUB_FREE_KB:-100000000} 10% /"
EOF
cat > "$tmp/bin/uptime" <<'EOF'
#!/bin/sh
echo "${STUB_UPTIME:-14:46  up 20 days,  7:08, 1 user, load averages: 2.73 4.52 4.76}"
EOF
# ps: the real process table minus any REAL heavy job of this machine (so the test does not depend on what
# else is running), with this test script's own entry rewritten to look like a heavy `go test`
# (an ancestor of the guard, which must be ignored), plus STUB_PS_EXTRA lines for "other" jobs.
cat > "$tmp/bin/ps" <<'EOF'
#!/bin/sh
/bin/ps -Ao pid=,ppid=,args= | awk -v t="$TEST_PID" '$1 == t { print $1, $2, "/usr/local/go/bin/go test ./..."; next } $3 ~ /(^|\/)(go|staticcheck|deadcode|flutter|dart|opencode|opencode\.exe)$/ { next } { print }'
[ -n "$STUB_PS_EXTRA" ] && printf '%s\n' "$STUB_PS_EXTRA"
exit 0
EOF
cat > "$tmp/bin/du" <<'EOF'
#!/bin/sh
echo "${STUB_CACHE_KB:-1000}	$2"
EOF
cat > "$tmp/bin/go" <<'EOF'
#!/bin/sh
case "$1 $2" in
  "env GOCACHE") echo "$STUB_CACHE_DIR" ;;
  "clean -cache") echo "go clean -cache" >> "$STUB_LOG" ;;
  *) echo "unexpected go call: $*" >> "$STUB_LOG" ;;
esac
EOF
chmod +x "$tmp/bin/"*
mkdir "$tmp/cache"
export STUB_CACHE_DIR="$tmp/cache"
PATH="$tmp/bin:$PATH"

pass=0; failed=0
# check NAME EXPECTED_EXIT [expected-substring] -- args...   (env is whatever the caller set before)
run() {
  name=$1; want=$2; sub=$3; shift 3
  out=$("$guard" "$@" 2>&1); got=$?
  ok=1
  [ "$got" = "$want" ] || ok=0
  if [ -n "$sub" ]; then printf '%s' "$out" | grep -q -- "$sub" || ok=0; fi
  if [ "$ok" = 1 ]; then pass=$((pass + 1)); echo "ok   $name"; else failed=$((failed + 1)); echo "FAIL $name: exit $got (want $want), output: $out"; fi
}

# The test script's own entry says `go test` but is an ancestor of the guard, so the guard must ignore it.
unset STUB_PS_EXTRA STUB_UPTIME STUB_FREE_KB STUB_CACHE_KB
run "all clear passes (own ancestor ignored)" 0 "heavy-guard: ok"

STUB_FREE_KB=$((19 * 1048576)); export STUB_FREE_KB
run "free disk 19 GB refused" 1 "free disk is 19 GB"
STUB_FREE_KB=$((20 * 1048576)); export STUB_FREE_KB
run "free disk exactly 20 GB passes" 0 "heavy-guard: ok"
unset STUB_FREE_KB

STUB_UPTIME="up 1 day, load averages: 12.50 9.00 8.00"; export STUB_UPTIME
run "load 12.5 refused" 1 "load average is 12.50"
STUB_UPTIME="up 1 day, load average: 11.90, 9.00, 8.00"; export STUB_UPTIME
run "linux-style load 11.9 passes" 0 "heavy-guard: ok"
STUB_UPTIME="up 1 day, load averages: 112.00 90.00 80.00"; export STUB_UPTIME
run "load 112 refused" 1 "REFUSED"
unset STUB_UPTIME

STUB_PS_EXTRA="4242 1 /usr/local/go/bin/go test -count=1 ./internal/pagebuilder/"; export STUB_PS_EXTRA
run "other go test refused" 1 "another heavy job"
STUB_PS_EXTRA="4243 1 /opt/homebrew/bin/staticcheck ./..."; export STUB_PS_EXTRA
run "staticcheck refused" 1 "staticcheck"
STUB_PS_EXTRA="4244 1 flutter test test/x_test.dart"; export STUB_PS_EXTRA
run "flutter test refused" 1 "flutter"
STUB_PS_EXTRA="4250 1 /opt/homebrew/share/flutter/bin/cache/dart-sdk/bin/dart --packages=/x/package_config.json /x/flutter_tools.snapshot test test/a_test.dart"; export STUB_PS_EXTRA
run "flutter tool snapshot test refused" 1 "bin/dart"
STUB_PS_EXTRA="4251 1 /opt/homebrew/share/flutter/bin/cache/dart-sdk/bin/dart language-server --protocol=lsp
4252 1 /opt/homebrew/share/flutter/bin/cache/dart-sdk/bin/dart devtools --machine"; export STUB_PS_EXTRA
run "IDE dart language server and devtools are not heavy" 0 "heavy-guard: ok"
STUB_PS_EXTRA="4245 1 opencode run --standalone --auto build it"; export STUB_PS_EXTRA
run "opencode run refused" 1 "opencode"
STUB_PS_EXTRA="4246 1 /opt/homebrew/lib/node_modules/@opencode/cli/bin/opencode.exe serve --stdio --port 0"; export STUB_PS_EXTRA
run "opencode serve is not a heavy job" 0 "heavy-guard: ok"
STUB_PS_EXTRA="4247 1 tail -Fn0 /var/folders/opencode/m1-walk.log
4248 1 /bin/sh /x/Pods/FirebaseCrashlytics/run --flutter-project /x/app_id_file.json
4249 1 go vet ./..."; export STUB_PS_EXTRA
run "tail, a path containing flutter, go vet are not heavy" 0 "heavy-guard: ok"
unset STUB_PS_EXTRA

# Build cache: 26 GB cleans; 24 GB does not; --check-only never cleans.
: > "$STUB_LOG"; STUB_CACHE_KB=$((26 * 1048576)); export STUB_CACHE_KB
run "26 GB cache gets cleaned" 0 "running 'go clean -cache'"
grep -q "go clean -cache" "$STUB_LOG" && { pass=$((pass + 1)); echo "ok   clean was called"; } || { failed=$((failed + 1)); echo "FAIL clean was not called"; }
: > "$STUB_LOG"
run "check-only reports but does not clean" 0 "would clean" --check-only
grep -q "go clean" "$STUB_LOG" && { failed=$((failed + 1)); echo "FAIL check-only cleaned"; } || { pass=$((pass + 1)); echo "ok   check-only did not clean"; }
: > "$STUB_LOG"; STUB_CACHE_KB=$((24 * 1048576)); export STUB_CACHE_KB
run "24 GB cache is left alone" 0 "heavy-guard: ok"
grep -q "go clean" "$STUB_LOG" && { failed=$((failed + 1)); echo "FAIL 24 GB cache was cleaned"; } || { pass=$((pass + 1)); echo "ok   24 GB not cleaned"; }
unset STUB_CACHE_KB

# Only `go clean -cache` is ever called, nothing else (no docker, no other go call).
grep -q "unexpected" "$STUB_LOG" && { failed=$((failed + 1)); echo "FAIL unexpected call: $(cat "$STUB_LOG")"; } || { pass=$((pass + 1)); echo "ok   no unexpected tool call"; }

run "bad flag is a usage error" 2 "usage" --nope

echo "$pass passed, $failed failed"
[ "$failed" = 0 ]
