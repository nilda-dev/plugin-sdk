# Working in plugin-sdk

This file exists because a coding agent that opens this repository cannot learn its constraints from
the code. This module is a **published contract**: Core imports it to host plugins and every plugin
imports it to be hosted, so a change here is a change to software you cannot see.

Read `README.md` first. Read `docs/PLUGIN_SDK.md` for the contract itself, `docs/ANY_LANGUAGE.md` for
the raw protocol, and `docs/CAPABILITY_PLAN.md` for which capabilities exist and which are
deliberately missing.

## The one rule everything else follows from

```
gRPC is how Core calls the plugin.   HTTP is how the plugin calls Core.
```

Lifecycle, hooks and events travel over gRPC. DATA — creating a product, editing a post, uploading an
image, querying with filters — goes over HTTP to Core's own API with a scoped token issued at `Init`.

Contract v1 tried to carry data on gRPC too, in five read-only methods with no way to write anything,
and no application-class plugin could be built against them. That is why v2 exists. If you find
yourself adding a data method to `contract/plugin.proto`, you are rebuilding v1.

## Changing the contract

`contract/` is generated from `plugin.proto`. The current contract is **v2** (`nilda.plugin.v2`,
`ProtocolVersion = 2`).

- **Adding** a field or an optional method is compatible. Do that.
- **Renaming or removing** anything, or changing a field number, breaks every deployed plugin at once
  — including plugins nobody in this repository has ever seen. That is a protocol version bump, not
  an edit.
- Regenerate rather than hand-editing `*.pb.go`.

## Releasing: a pin is only real if the tag is PUSHED and carries this module path

Read the README section before cutting anything. In short:

1. `go` reads the go.mod **at the tag**. A consumer's pin resolves only if that tag exists and its go.mod
   declares `gitlab.com/nildalabs/nilda-sdk/plugin-sdk`. Tags before `v0.7.0` carry the old path
   (renamed in `4ec856d`), so nothing may pin below `v0.7.0`.
2. **From inside the go.work workspace a bad pin is invisible**, because the workspace supplies this
   directory and never reads a pin. A green local build proves nothing about whether anyone else can
   compile a plugin.
3. `pins_test.go` checks every module checked out beside this one that requires it — it finds them
   itself — so a new plugin cannot slip past it by not being on a list.

So: prove a release from OUTSIDE the workspace (`GOWORK=off`, `go get …@<tag>`), not by building here.

## What "the plugin has an API" does not mean

`core.API()` is **nil unless a capability granted it**. Check `HasAPI()`. A plugin that assumes the
client exists crashes on an install where the owner did not grant the capability, which is the normal
case rather than an edge one.

## Packaging

A plugin ships as `<key>_<os>_<arch>.nplug` — a zip carrying `plugin.json` and `bin/plugin` — with its
signature in a `.sig` beside it, over the file's **own bytes**. The manifest is covered because it is
inside the file, and neither side needs a digest algorithm the other must match. Do not add a separate
manifest digest; that is the design being avoided.

## Before you say you are done

```bash
go test ./...
go vet ./...
```

If `pins_test.go` goes red, a consumer's pin cannot resolve — fix the pin or cut the tag; never "fix" it by
loosening what it checks. If you change
the contract or the developer surface, `docs/PLUGIN_SDK.md` changes in the same pass — it is the
document plugin authors build against, and a contract whose documentation lags is a contract nobody
can rely on.
