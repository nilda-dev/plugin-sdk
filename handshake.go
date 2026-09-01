// Package nilda is the Nilda plugin SDK (SPEC_98): the versioned contract BOTH Core and every plugin
// import, and the developer surface that hides all gRPC/go-plugin plumbing — a plugin author writes
// plain Go (Terraform-provider shape: repo-per-plugin + this shared SDK).
//
// A plugin is a self-contained Go binary running OUT-OF-PROCESS: its main() calls nilda.Serve(h).
// Core spawns it, performs the go-plugin handshake below, and talks gRPC. The plugin's ONLY door
// into Core is the HostService handed to it at Init (capability-enforced, deny-by-default).
package nilda

import (
	"strconv"
	"strings"

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

// SupportedProtocols is every protocol version this build can speak, and it is what both sides negotiate
// over (see Serve and Core's host).
//
// A SET rather than the single number, and the reason is a marketplace with third-party developers. With
// one fixed version, the day Core moves to 3 is the day every plugin anyone has published stops loading —
// simultaneously, on every site, until each author rebuilds and each owner updates. There is no staged
// version of that, and no amount of notice makes it not a flag day.
//
// go-plugin's VersionedPlugins exists for exactly this: a host can serve several protocol versions at once
// and the handshake picks the highest both ends understand, so a v2 plugin keeps working on a Core that
// also speaks v3. Terraform arrived at the same place, and for the same reason — it acquired third-party
// providers it did not control.
//
// Today the set holds one entry. Adding it now is a few lines; adding it after packages exist in the wild
// is the migration it was meant to avoid.
var SupportedProtocols = []int{ProtocolVersion}

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

// ProtocolForSDK maps an SDK version to the protocol it speaks.
//
// It exists so `nilda plugin check` can say "this SDK speaks protocol 2, this Core speaks protocol 3"
// while an author is at their keyboard, instead of that mismatch first appearing as a handshake failure on
// someone else's server. sdk_version was written into every manifest, sent to the marketplace, and read by
// nothing — a field that looks like a compatibility gate and is not is worse than no field, because an
// author believes it is protecting them.
//
// Only the MINOR is significant while the SDK is v0: that is where a v0 module's breaking changes land.
// From v1 the MAJOR carries them, which is what the second case below is for.
//
// THE ANSWER FOR V1 IS WRITTEN NOW, NOT ON THE DAY OF THE TAG.
//
// This used to end at `default: return 0, false` with the note "a v1+ SDK does not exist yet". Honest for
// an unknown FUTURE version, wrong for the next one: tagging v1.0.0 made `nilda plugin check` answer
// "sdk_version v1.0.0 is not a version this Nilda knows about" to an author holding the exact SDK the
// scaffold had just written into their go.mod. The failure fires on the tag, and the tag is the one moment
// nobody is looking at this function.
//
// v1 speaks protocol 2 because tagging v1.0.0 is a promise about the GO API, not a contract change: the
// wire protocol moves when contract/plugin.proto moves, and ProtocolVersion is what says so. The two
// numbers are deliberately independent, and this is the table that keeps them from being confused.
//
// v2+ is still "unknown", and that is still the honest answer — but it is now the only unknown, and
// TestTheScaffoldedSDKVersionSpeaksThisProtocol in Core fails the build if the version the scaffold hands
// out ever falls into it.
func ProtocolForSDK(sdkVersion string) (int, bool) {
	major, minor, ok := majorMinor(sdkVersion)
	if !ok {
		return 0, false
	}
	switch {
	case major == 0 && minor <= 1:
		return 1, true // contract v1: five read-only methods, no writes. Retracted; Core refuses it.
	case major == 0:
		return 2, true // contract v2: scoped API token, real reads and writes.
	case major == 1:
		return 2, true // v1.x: the Go API froze; the wire contract did not move with it.
	default:
		// A v2+ SDK does not exist. Reporting "unknown" beats guessing: a check that passed for a pairing
		// nobody has ever run is worse than one that says it cannot tell.
		return 0, false
	}
}

// majorMinor parses the leading two numbers of a version string, tolerating a leading 'v' and any
// pre-release or build suffix.
func majorMinor(v string) (int, int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return major, minor, true
}
