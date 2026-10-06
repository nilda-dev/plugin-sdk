package nilda

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// GRPCPlugin is the go-plugin gRPC glue for BOTH sides. On the plugin side (Serve) it registers the
// author's PluginService implementation; on the Core side (host) GRPCClient yields a *PluginClient
// carrying the raw gRPC client AND the broker — Core serves HostService on a broker stream and hands
// the stream id to the plugin in Init (the go-plugin bidirectional pattern).
type GRPCPlugin struct {
	plugin.Plugin
	// Impl is the plugin-side PluginService implementation (nil on the Core side).
	Impl contract.PluginServiceServer
}

// GRPCServer registers the plugin-side service (runs INSIDE the plugin process).
func (p *GRPCPlugin) GRPCServer(broker *plugin.GRPCBroker, s *grpc.Server) error {
	if srv, ok := p.Impl.(*pluginServer); ok {
		srv.broker = broker // Init dials the host's broker stream through this
	}
	contract.RegisterPluginServiceServer(s, p.Impl)
	return nil
}

// GRPCClient builds the Core-side handle (runs INSIDE Core).
func (p *GRPCPlugin) GRPCClient(ctx context.Context, broker *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &PluginClient{Plugin: contract.NewPluginServiceClient(c), Broker: broker}, nil
}

// PluginClient is what Core dispenses for a running plugin: the Core->plugin gRPC client plus the
// broker used to serve HostService (plugin->Core) on a per-plugin stream.
type PluginClient struct {
	Plugin contract.PluginServiceClient
	Broker *plugin.GRPCBroker
}

// pluginMap returns the go-plugin plugin set. impl is the plugin-side server (nil on the Core side).
//
// Unexported: nothing outside this file ever called it, and every exported signature naming a
// contract.* type puts the GENERATED protobuf inside whatever compatibility promise this module makes.
// handshake.go's whole design is that the wire contract evolves behind ProtocolVersion — which it cannot
// do if the generated types are part of the Go API.
func pluginMap(impl contract.PluginServiceServer) map[string]plugin.Plugin {
	return map[string]plugin.Plugin{PluginSetName: &GRPCPlugin{Impl: impl}}
}

// versionedPluginMap is the plugin set keyed by PROTOCOL VERSION, which is what both sides of the
// handshake negotiate over.
//
// One entry today. It is a map rather than a single set so that adding protocol 3 later means adding a
// line here — a Core that speaks both keeps loading every v2 plugin already published, instead of every
// plugin in the marketplace failing on the same afternoon. See SupportedProtocols for why that matters
// more once third parties have shipped things.
func versionedPluginMap(impl contract.PluginServiceServer) map[int]plugin.PluginSet {
	out := make(map[int]plugin.PluginSet, len(SupportedProtocols))
	for _, v := range SupportedProtocols {
		out[v] = pluginMap(impl)
	}
	return out
}

// HostPluginMap is the same set for the HOST side — Core, which has no plugin-side implementation to
// pass. It takes no arguments for exactly that reason: `VersionedPluginMap(nil)` was the only call Core
// ever made, and its parameter existed solely to be passed nil, while naming a generated protobuf type
// in the one signature the host end has to import.
//
// The plugin end does not need either: Serve wires the map itself.
func HostPluginMap() map[int]plugin.PluginSet { return versionedPluginMap(nil) }
