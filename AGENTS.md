# Working in plugin-sdk

This file exists because a coding agent that opens this repository cannot learn its constraints from
the code. This module is a **published contract**: Core imports it to host plugins and every plugin
imports it to be hosted, so a change here is a change to software you cannot see.

Read `README.md` first. Read `docs/PLUGIN_SDK.md` for the contract itself, `docs/ANY_LANGUAGE.md` for
the raw protocol, `docs/PAYMENTS.md` for the payment contract (a gateway, or whatever takes the money),
and `docs/CAPABILITY_PLAN.md` for which capabilities exist and which are deliberately missing.

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

`core.API()` is **nil unless a capability granted it**. Check `HasAPI()` — or better, `HasCapability` for
the one you need. Every `*API` method is nil-safe, so a plugin that assumes the client exists does not
crash: every call it makes returns an error ("this plugin has no API access"), on an install where the
owner did not grant the capability — the normal case rather than an edge one. Fail at `Init` with a
sentence naming the capability instead of failing every call later.

## Packaging

A plugin ships as `<key>_<os>_<arch>.nplug` — a zip carrying `plugin.json` and `bin/plugin` — with its
signature in a `.sig` beside it, over the file's **own bytes**. The manifest is covered because it is
inside the file, and neither side needs a digest algorithm the other must match. Do not add a separate
manifest digest; that is the design being avoided.

## Before you say you are done

What CI runs (`.github/workflows/ci.yml`), plus two steps it does not — vetting the examples (CI builds and tests them,
`cd _examples && go build ./... && go test ./...`) and the Core-reading tests (below):

```bash
GOWORK=off go build ./...              # the module builds alone, as a consumer and CI see it
test -z "$(gofmt -l .)"                # gofmt-clean
go vet ./...
go test -race ./...
go vet ./_examples/gateway ./_examples/shop       # NOT in CI. ./... never reaches _examples: the Go tool
go test -count=1 ./_examples/gateway ./_examples/shop   # skips an underscore directory
```

If you changed `contract/plugin.proto`, regenerate `contract/*.pb.go` with `protoc` exactly as the
`contract-is-generated` job does and commit the result — that job fails on any difference.

**The tests that read Core's source skip without it.** `docs_truth_test.go`, `guide_truth_test.go`,
`limits_truth_test.go`, `manifest_truth_test.go`, `payments_truth_test.go`, `abilities_test.go` and the
Core-reading tests in `nildatest/` hold this module's documents and mirrors to `../core` (and one to
`../commerce`, the site's shop, when it is beside this directory); CI clones this
repository alone, so there they all skip. Run them with Core checked out beside this directory, and read
the skips: `go test -count=1 -v ./... | grep 'core is not checked out'` must print nothing (`go test -v`
prints a skip's reason on the line BEFORE its `--- SKIP`, so grepping the `--- SKIP` lines never shows it). With
Core beside it, a file a guard reads that has moved FAILS the guard rather than skipping it. A change that
passes CI and was never run beside Core has not been checked against Core.

If `pins_test.go` goes red, a consumer's pin cannot resolve — fix the pin or cut the tag; never "fix" it by
loosening what it checks. If you change
the contract or the developer surface, `docs/PLUGIN_SDK.md` changes in the same pass — it is the
document plugin authors build against, and a contract whose documentation lags is a contract nobody
can rely on.
