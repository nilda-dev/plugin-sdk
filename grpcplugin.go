package nilda

import (
	"context"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"nilda.dev/plugin-sdk/contract"
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

// PluginMap returns the go-plugin plugin set. impl is the plugin-side server (nil on the Core side).
func PluginMap(impl contract.PluginServiceServer) map[string]plugin.Plugin {
	return map[string]plugin.Plugin{PluginSetName: &GRPCPlugin{Impl: impl}}
}
