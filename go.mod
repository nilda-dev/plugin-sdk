module gitlab.com/nildalabs/nilda-sdk/plugin-sdk

go 1.25.0

// Both pre-v0.2.0 tags speak contract v1, which Core rejects outright: ProtocolVersion is 2 since the five
// read-only data RPCs were removed in favour of a scoped token onto Core's real API.
//
// Both were also tagged under the module's OLD path, gitlab.com/nilda-sdk/plugin-sdk — every tag before
// v0.7.0 was (README.md, "History worth keeping") — so a requirement of THIS path at v0.1.0 or v0.1.1 does
// not resolve at all: `go` reads the go.mod at the tag and finds a different module path. These two lines
// therefore guard nothing a consumer can reach today. They stay because a retraction costs nothing, because
// Core's archtest reads this block to refuse a plugin pinned to either version, and because `retract` is
// the only lever a published version has — proxy.golang.org caches what it has served, and a retraction
// works by being read from the LATEST version's go.mod, which is why it must survive every release.
retract (
	v0.1.1 // contract v1: Core refuses it (ProtocolVersion 1 ≠ 2)
	v0.1.0 // contract v1: Core refuses it (ProtocolVersion 1 ≠ 2)
)

require (
	github.com/hashicorp/go-plugin v1.8.0
	google.golang.org/grpc v1.82.1
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/fatih/color v1.13.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/hashicorp/go-hclog v1.6.3 // indirect
	github.com/hashicorp/yamux v0.1.2 // indirect
	github.com/mattn/go-colorable v0.1.12 // indirect
	github.com/mattn/go-isatty v0.0.17 // indirect
	github.com/oklog/run v1.1.0 // indirect
	golang.org/x/net v0.53.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
	golang.org/x/text v0.36.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260414002931-afd174a4e478 // indirect
)
