# Nilda Plugin SDK
> The versioned Go contract every Nilda plugin builds against.

**What it is.** The public gRPC / `go-plugin` contract shared by Core (the host) and every
plugin, plus the HTTP client a plugin uses to read and write Nilda's data. It hides all the
plumbing so a plugin author writes plain Go.

**Role in Nilda.** The linchpin between Core and plugins — Core imports it to host plugins;
every plugin imports it to be hosted. Semantically versioned. Contract **v2**
(`nilda.plugin.v2`, `ProtocolVersion = 2`).

## The rule

```
gRPC is how Core calls the plugin.   HTTP is how the plugin calls Core.
```

A plugin ships as `<key>_<os>_<arch>.nplug` — a zip carrying `plugin.json` and `bin/plugin` — with its
signature in a `.sig` beside it, over the file's own bytes. The manifest is covered because it is inside
the file, and neither side needs a digest algorithm the other must match.

gRPC carries lifecycle, hooks and events. Data — creating a product, editing a post, uploading
an image, querying with filters — goes over HTTP to Core's own API with a scoped token issued
at `Init`. Contract v1 tried to carry data on gRPC too, in five read-only methods with no way
to write anything, and no application-class plugin could be built against them.

## Use
```go
import nilda "gitlab.com/nildalabs/nilda-sdk/plugin-sdk"

func main() { nilda.Serve(&MyPlugin{}) }

func (p *MyPlugin) Init(ctx context.Context, core *nilda.Core) (nilda.InitResult, error) {
    p.api = core.API()                       // nil unless a capability granted it — check HasAPI()
    return nilda.InitResult{Hooks: []string{"content.saved"}}, nil
}
```

Creating content:
```go
err := p.api.Post(ctx, "/content/product", map[string]any{"title": "Blue Widget"}, &out)
```

## What v1.0 will promise — and what it deliberately will not

This module is `v0`. In Go that is a stated absence of a compatibility promise, and it is the only reason
the surface can still be changed at all. **Tagging `v1.0.0` is not a version bump, it is the promise** —
so it is written down here first, before anyone can hold us to something nobody decided.

**Two numbers, on purpose.**

| | what it versions | what moves it |
|---|---|---|
| the module version (`v0.6.0`) | the **Go API** — the names and signatures a plugin author compiles against | a breaking change to any of them |
| `ProtocolVersion` (`2`) | the **wire contract** — `contract/plugin.proto` | a breaking change to the proto |

They are independent, and confusing them is the mistake this section exists to prevent. A plugin built
against protocol 2 keeps loading on a Core that also speaks 3 (`SupportedProtocols`), which is what stops
every published plugin failing on one afternoon. That only works while the generated types are **not**
part of the Go API — see below.

**Covered by the promise from v1.0.** Everything in `surface.txt`, except what the next paragraph names.
That file is generated from the AST and checked on every run: exported funcs, types, consts, vars, and the
exported methods and fields of exported types — 407 entries today. Adding to it is a normal change and
stays cheap. Removing from it after v1.0 is a breaking change for somebody, and the guard says so in those
words.

**NOT covered, and named rather than left ambiguous:**

- **`contract/`** — generated from `plugin.proto`. It moves with `ProtocolVersion`, not with the module
  version. Three exported names unavoidably mention its types and are listed as exceptions in
  `surface_test.go`: `GRPCPlugin.Impl` and `PluginClient.Plugin`, which are the Core↔plugin transport
  seam and therefore *are* the protobuf, and `NewCoreForTest`, which no other package can write because
  `Core`'s fields are unexported.
- **`NewCoreForTest`** — plumbing for `nildatest`. Write plugin tests against `nildatest`; it is the
  supported kit and it does not name a gRPC type.
- **`_examples/`** — not a package of this module (the underscore keeps the Go tool out), so it is not
  importable and not frozen. It changes whenever the thing it teaches changes. CI still builds and runs it.

**Before the tag.** `ProtocolForSDK` must already know the version being tagged — Core's
`TestTheScaffoldedSDKVersionSpeaksThisProtocol` fails the build if the scaffold's pin lands in a range the
compatibility checker cannot classify. That check exists because the first answer for `v1.0.0` was
"not a version this Nilda knows about", to an author holding exactly what our own scaffold gave them.

## Releasing — a pin is only real if the tag is pushed AND carries this module path

`pins_test.go` checks what the four plugin repositories require of this one. It is **red today**, and the
reason is worth reading before cutting the next tag.

Every published tag — `v0.1.0` through `v0.6.0` — was cut before commit `4ec856d`, which renamed the module
from `gitlab.com/nilda-sdk/plugin-sdk` to `gitlab.com/nildalabs/nilda-sdk/plugin-sdk` (the group was
missing, so the old path served nothing). `go` resolves a requirement by reading the go.mod **at** the
version, so a plugin requiring the new path against any existing tag fails with a non-matching module path
— and a directory `replace` fails the same way, so cloning this repo at the tag does not rescue it either.

This was also recorded as "`v0.4.0`–`v0.6.0` exist on one developer's disk and were never pushed; the
newest tag on `origin` is `v0.3.0`". **Measured 2026-09-01 and no longer true** — `git ls-remote --tags`
shows every tag through `v0.6.0` on `origin`, at the same commits as the local ones. Whether that was
fixed or never right, the surviving fault is the one above and only that one: the tags are published and
every one of them carries the old module path. From inside the go.work workspace it stays invisible,
because the workspace supplies this directory and never reads a pin.

So the next release is not optional bookkeeping — it is what makes the four plugins buildable by anyone who
is not us:

1. tag a commit **at or after `4ec856d`**, so the tag's go.mod declares `gitlab.com/nildalabs/…`;
2. `git push --tags` — an unpushed tag is indistinguishable from a correct pin, from in here;
3. move `commerce`, `forms`, `booking` and `sso` to that version.

`pins_test.go` goes green when all three are done, and each plugin's own CI clones this repo at the version
its go.mod names, which is the check that cannot be skipped.

## Docs
`docs/` — **`PLUGIN_SDK.md`** (the contract, the developer surface, capabilities, the manifest,
what Core guarantees and what it does not) · `ANY_LANGUAGE.md` (the raw protocol, for a plugin
written in something other than Go) · `CAPABILITY_PLAN.md` (which capabilities exist, which are
deliberately missing, and why) · `PLUGINS.md` (plugin catalog).

## Nilda ecosystem
This repo is one of several. How they fit together:

```
core            — CMS (Go backend + admin panel + default theme)   needs → plugin-sdk
central         — nilda.dev control-plane (pay/license/market)     standalone
plugin-sdk      — plugin gRPC contract + API client (Go)           used by core + plugins
commerce · booking · forms · sso — plugins                      need → plugin-sdk
```

Repositories — every one under the `nildalabs` group, checked against the remotes on 2026-08-06:
- `gitlab.com/nildalabs/nildacms/core` · `gitlab.com/nildalabs/nildacms/central`
- `gitlab.com/nildalabs/nilda-sdk/plugin-sdk`
- `gitlab.com/nildalabs/nilda-plugins/commerce` · `…/booking` · `…/forms` · `…/sso`

Ecosystem-wide docs (architecture, roadmap, spec index) live in **core** (`docs/files/`).

## License

**MIT** — see [LICENSE](LICENSE). © Mohsen Khorrami.

Inbound = outbound: a contribution is offered under the same terms. The NAME is not licensed with the
code — see Core's `TRADEMARKS.md`; a fork renames.
