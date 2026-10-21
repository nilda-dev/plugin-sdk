#!/bin/sh
# install-hooks.sh — opt in to the pre-push gate: every `git push` first runs scripts/test-fast --committed-only.
#
# GitHub CI is unpaid until launch, so this hook is the gate. NOTHING installs it automatically; run this once
# per clone:   sh scripts/install-hooks.sh        (remove again: sh scripts/install-hooks.sh --uninstall)
#
# It writes <hooks dir>/pre-push, where the hooks dir is whatever git is configured to use (core.hooksPath when
# `make hooks` set it, else .git/hooks). The written file is git-ignored in core, so it is never committed.
# Skip it once with `git push --no-verify`. A pre-push hook that is not ours is never overwritten.

root=$(git rev-parse --show-toplevel) || exit 2
cd "$root" || exit 2
hooks=$(git rev-parse --git-path hooks)
hook="$hooks/pre-push"
marker="# installed by scripts/install-hooks.sh"

case "${1:-}" in
  --uninstall)
    if [ -f "$hook" ] && grep -q "$marker" "$hook"; then rm "$hook" && echo "removed $hook"; else echo "no hook of ours at $hook"; fi
    exit 0 ;;
  "") ;;
  *) echo "usage: install-hooks.sh [--uninstall]" >&2; exit 2 ;;
esac

if [ -f "$hook" ] && ! grep -q "$marker" "$hook"; then
  echo "install-hooks: $hook exists and is not ours; not overwriting it" >&2
  exit 1
fi
mkdir -p "$hooks"
cat > "$hook" <<HOOK
#!/bin/sh
$marker — push gate: the fast test lane on what this push changes.
# Skip once: git push --no-verify
exec "\$(git rev-parse --show-toplevel)/scripts/test-fast" --committed-only
HOOK
chmod +x "$hook"
echo "installed $hook (runs scripts/test-fast --committed-only before every push)"
