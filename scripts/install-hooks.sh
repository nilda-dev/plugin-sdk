#!/bin/sh
# install-hooks.sh — opt in to the two git gates of this clone:
#   pre-push    every `git push` first runs scripts/test-fast --committed-only (GitHub CI is unpaid until launch,
#               so this hook is the gate);
#   commit-msg  every commit message goes through scripts/commit-msg-check: no Co-Authored-By trailer, no
#               "Generated with" signature, no tool name (BUILD_RULES → GIT AUTHORSHIP).
#
# NOTHING installs them automatically; run this once per clone:   sh scripts/install-hooks.sh
# (remove again: sh scripts/install-hooks.sh --uninstall). Same file in core, plugin-sdk, plugin-booking, mobile.
#
# They are written into <hooks dir>, whatever git is configured to use (core.hooksPath when `make hooks` set it,
# else .git/hooks). The written files are git-ignored in core, so they are never committed. Skip once with
# `git push --no-verify` / `git commit --no-verify`. A hook that is not ours is never overwritten.

root=$(git rev-parse --show-toplevel) || exit 2
cd "$root" || exit 2
hooks=$(git rev-parse --git-path hooks)
marker="# installed by scripts/install-hooks.sh"

case "${1:-}" in
  --uninstall)
    for name in pre-push commit-msg; do
      hook="$hooks/$name"
      if [ -f "$hook" ] && grep -q "$marker" "$hook"; then rm "$hook" && echo "removed $hook"; else echo "no hook of ours at $hook"; fi
    done
    exit 0 ;;
  "") ;;
  *) echo "usage: install-hooks.sh [--uninstall]" >&2; exit 2 ;;
esac

for name in pre-push commit-msg; do
  hook="$hooks/$name"
  if [ -f "$hook" ] && ! grep -q "$marker" "$hook"; then
    echo "install-hooks: $hook exists and is not ours; not overwriting it" >&2
    exit 1
  fi
done
mkdir -p "$hooks"

cat > "$hooks/pre-push" <<HOOK
#!/bin/sh
$marker — push gate: the fast test lane on what this push changes.
# Skip once: git push --no-verify
exec "\$(git rev-parse --show-toplevel)/scripts/test-fast" --committed-only
HOOK
chmod +x "$hooks/pre-push"
echo "installed $hooks/pre-push (runs scripts/test-fast --committed-only before every push)"

cat > "$hooks/commit-msg" <<HOOK
#!/bin/sh
$marker — no AI trailer, signature or tool name in a commit message.
# Skip once: git commit --no-verify
exec sh "\$(git rev-parse --show-toplevel)/scripts/commit-msg-check" "\$1"
HOOK
chmod +x "$hooks/commit-msg"
echo "installed $hooks/commit-msg (refuses a Co-Authored-By trailer, a \"Generated with\" line or a tool name)"
