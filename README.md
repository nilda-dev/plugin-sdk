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
exported methods and fields of exported types — 573 entries today. Adding to it is a normal change and
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

`go` resolves a requirement by reading the go.mod **at** the version, so a pin is only real when the tag
exists AND that tag's go.mod declares the module path the consumer requires. From inside the go.work
workspace neither fact is ever consulted — the workspace supplies this directory — so a green local build
says nothing about whether anyone else can compile a plugin.

`pins_test.go` checks both facts for every module checked out beside this one that requires it (it finds
them; nobody keeps a list): the plugins and Core itself. Whether the tag is PUSHED it deliberately leaves to
each plugin's own CI, which clones this repo at the pinned version (`git clone --branch "$SDK_VERSION"`) and
fails loudly on a tag that exists on one disk only.

Cutting a release:

1. tag the commit, `git push --tags`;
2. prove it from OUTSIDE the workspace: a throwaway module with `GOWORK=off` that runs
   `go get gitlab.com/nildalabs/nilda-sdk/plugin-sdk@<tag>` and builds — the only view an outside
   developer ever gets;
3. move the consumers' pins to it, and `pins_test.go` confirms each one resolves.

**History worth keeping:** the module was renamed from `gitlab.com/nilda-sdk/plugin-sdk` in `4ec856d`, and
every tag before `v0.7.0` still declares the old path — so no consumer may pin below `v0.7.0` under the
current path. That fault is closed; the rule it taught is the section above.

## Docs
`docs/` — **`PLUGIN_SDK.md`** (the contract, the developer surface, capabilities, the manifest,
what Core guarantees and what it does not) · `PAYMENTS.md` (the payment contract: a gateway plugin,
and whatever takes the money) · `ANY_LANGUAGE.md` (the raw protocol, for a plugin
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
