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

A plugin ships as ONE archive — `<key>_<os>_<arch>.nplug` — carrying `plugin.json`, `bin/plugin`, and a
detached `signature` over a digest of both. One artifact to build, upload, review, sign and install.

gRPC carries lifecycle, hooks and events. Data — creating a product, editing a post, uploading
an image, querying with filters — goes over HTTP to Core's own API with a scoped token issued
at `Init`. Contract v1 tried to carry data on gRPC too, in five read-only methods with no way
to write anything, and no application-class plugin could be built against them.

## Use
```go
import nilda "gitlab.com/nilda-sdk/plugin-sdk"

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

## Docs
`docs/` — **`PLUGIN_SDK.md`** (the contract, the developer surface, capabilities, the manifest,
what Core guarantees and what it does not) · `PLUGINS.md` (plugin catalog).

## Nilda ecosystem
This repo is one of several. How they fit together:

```
core            — CMS (Go backend + admin panel + default theme)   needs → plugin-sdk, plugin-manifest
central         — nilda.dev control-plane (pay/license/market)     needs → plugin-manifest
plugin-sdk      — plugin gRPC contract + API client (Go)           used by core + plugins
plugin-manifest — the manifest schema + package format (0 deps)    used by core + central + plugin-sdk
theme-sdk   — headless SDK (@nilda/client, @nilda/react)       reads Core's public API
commerce · booking · forms — paid plugins                      need → plugin-sdk
```

Repositories:
- `gitlab.com/nildacms/core` · `gitlab.com/nildacms/central`
- `gitlab.com/nilda-sdk/plugin-sdk` · `gitlab.com/nilda-sdk/plugin-manifest` · `gitlab.com/nilda-sdk/theme-sdk`
- `gitlab.com/nilda-plugins/commerce` · `…/booking` · `…/forms`

Ecosystem-wide docs (architecture, roadmap, spec index) live in **core** (`docs/files/`).

## License
Public. © Mohsen Khorrami.
