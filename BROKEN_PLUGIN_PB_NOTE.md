# ✅ RESOLVED in v0.1.1 (2026-07-22)

The corrupt descriptor is fixed and released as **v0.1.1** (this commit). `core` now requires
`plugin-sdk v0.1.1`. The broken **v0.1.0** tag is immutable and left in place (Go module versions
cannot be rewritten); do not use it. History below for the record.

---

# contract/plugin.pb.go — FIXED in this repo, but NOT YET PUBLISHED

**Status:** the source file is repaired at HEAD (commit `08c3edc`). What remains is a **release**:
`v0.1.0` — the only tag, and the version `core` resolves — still carries the corrupt descriptor.

## Symptom (when consuming the published v0.1.0)
Any binary importing `gitlab.com/nilda-sdk/plugin-sdk/contract` panics at init:

```
panic: runtime error: slice bounds out of range [-5:]
  google.golang.org/protobuf/internal/filedesc.(*File).unmarshalSeed
  gitlab.com/nilda-sdk/plugin-sdk/contract.file_contract_plugin_proto_init()
```

Independent of Go toolchain and protobuf runtime version — the descriptor bytes themselves are malformed.

## Root cause (confirmed byte-for-byte)
Commit `990669e` ("Rewrite internal imports … builds standalone") rewrote import paths with a text
substitution that also edited the `go_package` string **inside the embedded, length-prefixed descriptor**.
The string grew to 40 bytes but its length prefixes were left at the old value, so every following field
shifted and `unmarshalSeed` read a negative length.

Exactly one line differs between the two versions:

```
v0.1.0 (broken):   …ResponseB\x1fZ\x1dgitlab.com/nilda-sdk/plugin-sdk/contractb\x06proto3
HEAD   (correct):  …ResponseB*Z(gitlab.com/nilda-sdk/plugin-sdk/contractb\x06proto3
```

`Z\x1d` declares a 29-byte `go_package`; `Z(` declares 40 — the true length of
`gitlab.com/nilda-sdk/plugin-sdk/contract`. (`B\x1f`→`B*` is the enclosing `FileOptions` length.)

Note: the trailing `contractb` is **not** corruption — `b` is `0x62`, the tag for field 12 (`syntax`),
followed by `\x06proto3`. An earlier revision of this note misread it as damage.

## Verification
Re-running the generator at HEAD produces a **byte-identical** file, confirming HEAD is already correct:

```sh
protoc --go_out=. --go_opt=paths=source_relative \
       --go-grpc_out=. --go-grpc_opt=paths=source_relative \
       contract/plugin.proto   # → no diff
```

With the `nilda/go.work` workspace (which uses this local module) `core`'s suites pass. With `GOWORK=off`
— the convention for `core`'s tests — Go resolves the published `v0.1.0` and the panic returns, which
blocks the SPEC_90 and SPEC_98 mandatory test suites.

## Remaining step (needs a publish)
1. Tag and push a release containing `08c3edc`, e.g. `v0.1.1`.
2. In `core/`: `go get gitlab.com/nilda-sdk/plugin-sdk@v0.1.1 && go mod tidy`.

Until then, do not add a `replace` directive in `core/go.mod` as a workaround — it would break standalone
and CI builds of `core`, trading a test-only failure for a distribution defect.

**Rule reaffirmed:** generated `.pb.go` files are never hand-edited or sed-rewritten; change
`option go_package` in the `.proto` and regenerate.
