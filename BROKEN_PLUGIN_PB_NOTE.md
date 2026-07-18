# ⚠️ contract/plugin.pb.go is CORRUPT — regenerate it (cross-repo note from core phase-1)

**Status:** blocks running `core` (the server panics at startup). Noted here, NOT fixed — per the
phase-1 rule (finish `core` only; record cross-repo work, don't do it now).

## Symptom
Any binary that imports `gitlab.com/nilda-sdk/plugin-sdk/contract` panics at init:

```
panic: runtime error: slice bounds out of range [-5:]
  google.golang.org/protobuf/internal/filedesc.(*File).unmarshalSeed
  gitlab.com/nilda-sdk/plugin-sdk/contract.file_contract_plugin_proto_init()
    contract/plugin.pb.go:1842
```

Reproduces under **every** Go toolchain (1.25.0 / 1.25.11 / 1.26.5) and **every** protobuf runtime
(v1.36.8 / v1.36.10 / v1.36.11) — so it is neither a Go-version nor a protobuf-version problem. The
generated descriptor bytes themselves are malformed.

## Root cause
Commit `990669e "Rewrite internal imports to gitlab.com/nilda-sdk/plugin-sdk — builds standalone"`
rewrote import paths with a text substitution that ALSO edited the `go_package` string **inside the
embedded descriptor** in `contract/plugin.pb.go`. That descriptor is length-prefixed: making the string
longer without updating its length prefix shifts every following field, so `unmarshalSeed` reads a bogus
(negative) length. Evidence: the rawDesc const contains the token `nilda-sdk/plugin-sdk/contractb` — the
trailing `b` is the next descriptor byte that got merged in.

Generated `.pb.go` files must never be hand-edited or sed-rewritten; only the `option go_package` in the
`.proto` should change, then regenerate.

## Fix (one command — protoc 29.3 + protoc-gen-go are already installed)
From `plugin-sdk/`:

```sh
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       contract/plugin.proto
```

(Confirm `option go_package = "gitlab.com/nilda-sdk/plugin-sdk/contract";` in `contract/plugin.proto`
first.) Then `GOWORK=off go build ./...` in plugin-sdk should be clean, and `core` will boot.
