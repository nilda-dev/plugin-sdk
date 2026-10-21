# The release train — how a seam change travels

The plugin contract crosses four repositories: **plugin-sdk** (the Go types and the manifest vocabulary), **core**
(reads the manifest, resolves it for the app, runs `nilda plugin check`), **the plugin** (declares a `plugin.json`) and
**mobile** (draws what core resolved). Before the 2026-10-21 reform a manifest change was four hand steps in three
repos, and every SDK change fanned out into tags, pin bumps and corpus refreshes in all of them. These rules keep that
cheap. Each one names what holds it; a rule with no holder says so.

## The rules

1. **Additive only.** A release adds optional fields, methods, constants and hooks. It never renames, removes or
   re-types anything: that breaks plugins nobody here has seen, and is a protocol bump the owner decides
   (`AGENTS.md`, "Changing the contract"). Held by: `surface.txt` / `nildatest/surface.txt` (every exported name is a
   line; `TestThePublicSurfaceIsTheOneWeMeantToPromise`) and `TestTheSurfaceOnlyGrowsBetweenTags` (every line of the
   nearest tag behind HEAD must still be there; `SDK_ALLOW_SURFACE_REMOVAL=1` is the owner's escape hatch).
2. **One tag per milestone.** Work lands on `main` without a tag; a tag is cut when a milestone's SDK changes are all
   in, not per field. **Security and money fixes tag at once** — a plugin that takes or moves money must be able to pin
   the fix the same day. Held by: the CHANGELOG guard `TestTheChangelogDatesEveryTaggedVersion` (a tagged version has a
   dated heading; only the newest may read `— unreleased`, and only while its tag does not exist), and `pins_test.go`
   (every pin beside this repo resolves to a tag that exists and carries this module path).
3. **Between tags, develop with a local go.work.** Core, the plugin and the SDK are used together through the workspace
   (`go work use ../plugin-sdk`), never through a `replace` in a go.mod and never through a tag you have not cut. The
   CHANGELOG's first section reads `## vX.Y.Z — unreleased (what it brings)` meanwhile; the commit the tag points at
   dates it. A green workspace build proves nothing about a consumer who clones one repo, so prove the tag from outside
   (`README.md`, "Releasing"). Core's pin must never be older than a plugin's:
   `core/scripts/contract-refresh.sh` refuses to run when it is, and names the plugin.
4. **Core's conformance asserts only what core uses.** Core's conformance suite (`core/internal/plugin/appconformance_test.go`)
   resolves every real manifest the way the app receives it and keeps three goldens per manifest:
   `resolved/` (the app payload: the plugin's shape and headline, every page's block, label, actions, form and
   confirmation text, with secrets and control characters held out), `notes/` (what `plugin check` says about reach and
   listing) and `phone/` (the drawn phone). Mobile reads the `resolved/` goldens. A field core does not resolve does not
   change a golden. The **corpus copy** (`testdata/conformance/real/<plugin>.json`) is the whole `plugin.json`, byte for
   byte, so any manifest edit makes core's drift test red until the copy is refreshed. That is deliberate, and is why the
   refresh is one command.
5. **A UI need that fits the existing vocabulary touches only the plugin.** A new page, column, action, form field or
   `mobile` hint built from fields the SDK already has is a `plugin.json` change in the plugin repo, then
   `scripts/contract-publish` there. No SDK change, no tag, no core change beyond the corpus and goldens that command
   regenerates.
6. **A need for new SDK fields is queued, and the owner is told first.** Before work starts, say "reaches N repos",
   where N counts the repositories that must change: this one (always), core (if it must read or resolve the field),
   mobile (if the app must draw it), Central (if the marketplace badge or listing reads it), and each plugin that will
   use it. The owner decides whether it starts now or rides the next milestone tag. Held by: nothing mechanical; this is
   the one rule that is a conversation.

## The commands

| You changed | Run | Where |
|---|---|---|
| a plugin's `plugin.json` or SDK pin | `sh scripts/contract-publish` (read-only: `--check`) | the plugin repo (booking has it) |
| core's conformance code, or several manifests at once | `make contract-refresh` (read-only: `make contract-refresh CHECK=1`) | core |
| the SDK (new fields) | rule 6 first; then tag per rule 2, move the pins, then the line above | this repo, then the consumers |

Both commands refresh the corpus, regenerate the goldens, copy them beside the app, build a fresh Core and run
`plugin check`; they print what changed per repo and the commit lines, and **never commit or push**. Push order: the
plugin, then core (the corpus), then the app (the goldens). The cheap `--check` is what a pre-push hook or the fast lane
calls.

## Not done yet

The plugin contract as a generated artifact that core and mobile read at a pinned tag (so the corpus copy disappears) is
a separate, larger plan and needs the owner's word. Until then the corpus copy and these commands are the mechanism.
