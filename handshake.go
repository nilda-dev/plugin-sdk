// Package nilda is the Nilda plugin SDK (SPEC_98): the versioned contract BOTH Core and every plugin
// import, and the developer surface that hides all gRPC/go-plugin plumbing — a plugin author writes
// plain Go (Terraform-provider shape: repo-per-plugin + this shared SDK).
//
// A plugin is a self-contained Go binary running OUT-OF-PROCESS: its main() calls nilda.Serve(h).
// Core spawns it, performs the go-plugin handshake below, and talks gRPC. The plugin's ONLY door
// into Core is the HostService handed to it at Init (capability-enforced, deny-by-default).
package nilda

import (
	"github.com/hashicorp/go-plugin"
)

// ProtocolVersion is the Core<->plugin contract version (Terraform-style). Core rejects a plugin
// built against an incompatible protocol AT LOAD with a clear error — never silent breakage.
// Bump ONLY on a breaking change to contract/plugin.proto.
//
// 2: HostService's five data-read methods were removed in favour of a scoped token onto Core's own API
// (InitRequest.api_token). v1 plugins could read a page by slug and page through one content type and
// could write nothing at all, so nothing of WooCommerce's class could be built; a v1 binary is refused at
// the handshake with a clear message rather than failing later on a method that no longer exists.
const ProtocolVersion = 2

// PluginSetName is the key under which the Nilda plugin is served in the go-plugin plugin set.
const PluginSetName = "nilda"

// Handshake is the go-plugin handshake shared by Core (client) and every plugin (server). The magic
// cookie is NOT security (the sandbox is); it only prevents a user from executing a plugin binary
// directly and getting a confusing gRPC dump instead of a clear "run me under Nilda" error.
var Handshake = plugin.HandshakeConfig{
	ProtocolVersion:  ProtocolVersion,
	MagicCookieKey:   "NILDA_PLUGIN",
	MagicCookieValue: "1b6cf7a2e4nilda98d3f5c0a9b8e7d61",
}
