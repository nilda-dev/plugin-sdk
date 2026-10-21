#!/bin/sh
# commit-msg-check_test.sh — fixture messages through scripts/commit-msg-check, and the hook install/uninstall in
# a throwaway repository. Run: sh scripts/commit-msg-check_test.sh   (exit 0 = all cases pass)

here=$(cd "$(dirname "$0")" && pwd)
check="$here/commit-msg-check"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
fail=0
n=0

# case <want exit> <name> <message...>: the message is printf'd with \n line breaks.
case_() {
  want=$1 name=$2; shift 2
  n=$((n + 1))
  printf "$@" > "$tmp/msg"
  sh "$check" "$tmp/msg" 2>/dev/null
  got=$?
  if [ "$got" != "$want" ]; then
    echo "FAIL: $name: exit $got, want $want" >&2
    fail=1
  fi
}

# Accepted.
case_ 0 "plain subject"            'R4: every public route caps its body\n\nThe router caps each request.\n'
case_ 0 "CLAUDE.md is a file"      'R4: reword the branch rule in CLAUDE.md\n'
case_ 0 "a path with .claude"      'Docs: .claude/commands/verify.md names the gate\n'
case_ 0 "core/CLAUDE.md path"      'R4: core/CLAUDE.md short-branch rule\n'
case_ 0 "comment lines ignored"    'R4: gates\n# Co-Authored-By: Claude <noreply@anthropic.com>\n# Generated with Claude Code\n'
case_ 0 "under the scissors"       'R4: gates\n# ------------------------ >8 ------------------------\n+Co-Authored-By: Claude\n'
case_ 0 "words that contain a name" 'Fix the claudette and codexes typo\n'
case_ 0 "regenerated with"         'contract/ was regenerated with protoc 29.3\n'
case_ 0 "the product names its AI"  "App: the sheet names Anthropic's Claude as the site's AI service\\n"
case_ 0 "a Signed-off-by is fine"  'R4: gates\n\nSigned-off-by: Owner <owner@example.com>\n'

# Refused.
case_ 1 "Co-Authored-By trailer"   'R4: gates\n\nCo-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>\n'
case_ 1 "lower-case trailer"       'R4: gates\n\nco-authored-by: Someone <a@b.c>\n'
case_ 1 "indented trailer"         'R4: gates\n\n  Co-Authored-By: Someone <a@b.c>\n'
case_ 1 "Generated with"           'R4: gates\n\n\360\237\244\226 Generated with [Some Tool](https://example.com)\n'
case_ 1 "generated with, lower"    'R4: gates\n\ngenerated with a helper\n'
case_ 1 "Claude named"             'R4: gates written by Claude\n'
case_ 1 "Claude at a sentence end" 'R4: gates\n\nReviewed by Claude.\n'
case_ 1 "CODEX upper case"         'Fix from CODEX\n'
case_ 1 "Copilot suggestion"       'Apply the copilot suggestion\n'
case_ 1 "claude-code word"         'R4: made with claude-code\n'

# The hook: install writes commit-msg (and pre-push), a refused message stops the commit, uninstall removes ours.
repo="$tmp/repo"
git init -q "$repo" && mkdir -p "$repo/scripts" && cp "$here/install-hooks.sh" "$here/commit-msg-check" "$repo/scripts/"
printf '#!/bin/sh\nexit 0\n' > "$repo/scripts/test-fast" && chmod +x "$repo/scripts/test-fast"
(
  cd "$repo" || exit 1
  git config user.email t@example.com && git config user.name t
  sh scripts/install-hooks.sh >/dev/null || exit 1
  [ -x .git/hooks/commit-msg ] || { echo "no commit-msg hook installed" >&2; exit 1; }
  echo a > a && git add a
  if git commit -q -m 'Add a' -m 'Co-Authored-By: Claude <x@y.z>' 2>/dev/null; then echo "a trailer commit got through" >&2; exit 1; fi
  git commit -q -m 'Add a' || { echo "a clean commit was refused" >&2; exit 1; }
  sh scripts/install-hooks.sh --uninstall >/dev/null
  [ ! -e .git/hooks/commit-msg ] && [ ! -e .git/hooks/pre-push ] || { echo "uninstall left a hook" >&2; exit 1; }
  printf '#!/bin/sh\nexit 0\n' > .git/hooks/commit-msg
  if sh scripts/install-hooks.sh >/dev/null 2>&1; then echo "a foreign commit-msg hook was overwritten" >&2; exit 1; fi
  grep -q 'exit 0' .git/hooks/commit-msg || { echo "the foreign hook changed" >&2; exit 1; }
) || fail=1
n=$((n + 1))

if [ "$fail" = 0 ]; then echo "commit-msg-check: $n cases pass"; fi
exit $fail
