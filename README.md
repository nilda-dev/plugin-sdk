# Nilda Plugin SDK
> The versioned Go contract every Nilda plugin builds against.

**What it is.** The public gRPC / `go-plugin` contract shared by Core (the host) and every
plugin. It hides all gRPC plumbing so a plugin author writes plain Go.

**Role in Nilda.** The linchpin between Core and plugins — Core imports it to host plugins;
every plugin imports it to be hosted. Semantically versioned (current: **v0.1.0**).

## Use
```go
import nilda "gitlab.com/nilda-sdk/plugin-sdk"
func main() { nilda.Serve(&MyPlugin{}) }
```

## Docs
`docs/` — `PLUGIN_SDK.md` (contract & repo topology), `PLUGINS.md` (plugin catalog).

## Nilda ecosystem
This repo is one of several. How they fit together:

```
core        — CMS (Go backend + admin panel + default theme)   needs → plugin-sdk
central     — nilda.dev control-plane (pay/license/market/AI)  standalone
plugin-sdk  — plugin gRPC contract (Go)                        used by core + plugins
theme-sdk   — headless SDK (@nilda/client, @nilda/react)       reads Core's public API
commerce · booking · forms — paid plugins                      need → plugin-sdk
```

Repositories:
- `gitlab.com/nildacms/core` · `gitlab.com/nildacms/central`
- `gitlab.com/nilda-sdk/plugin-sdk` · `gitlab.com/nilda-sdk/theme-sdk`
- `gitlab.com/nilda-plugins/commerce` · `…/booking` · `…/forms`

Ecosystem-wide docs (architecture, roadmap, spec index) live in **core** (`Doc/files/`).

## License
Public. © Mohsen Khorrami.
