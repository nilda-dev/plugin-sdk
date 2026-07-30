package nilda

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"gitlab.com/nilda-sdk/plugin-sdk/contract"
)

// Handler is what a plugin implements — plain Go, no gRPC. Light plugins implement the hooks/events
// they care about; app plugins additionally start their resident HTTP server in Init and return its
// address (Core reverse-proxies the declared route prefix to it).
type Handler interface {
	// Init receives the runtime context (granted capabilities, scoped datastore DSN, the typed Core
	// client) exactly once. Return what to wire: hook names, event subscriptions, route address.
	Init(ctx context.Context, core *Core) (InitResult, error)
	// HandleHook handles one hook invocation (filter-style: return the possibly-modified payload;
	// echo it back unchanged if not filtering).
	HandleHook(ctx context.Context, hook string, payload []byte) ([]byte, error)
	// HandleEvent handles one subscribed event.
	HandleEvent(ctx context.Context, eventType string, data []byte) error
}

// InitResult is what a plugin asks Core to wire after Init.
type InitResult struct {
	RouteAddr string   // "host:port" of the resident HTTP server; empty unless the `route` capability
	Hooks     []string // hook names to receive (requires `hooks`)
	Events    []string // event types to receive (requires `events`)
	// Schedules is recurring work Core runs on the plugin's behalf (requires `schedule`). The plugin is a
	// resident process and could run its own ticker, but Core-owned scheduling is visible to the site
	// owner, survives a restart, and does not double-fire when the plugin is relaunched. Core calls back
	// through HandleHook with hook = "schedule:<name>".
	Schedules []Schedule
}

// Schedule is one recurring callback.
type Schedule struct {
	Name string // stable identifier, e.g. "reminders"
	Cron string // standard cron spec, e.g. "0 9 * * *"
}

// ScheduleHook is the hook name Core uses when a schedule fires.
func ScheduleHook(name string) string { return "schedule:" + name }

// Serve is the plugin's main() entrypoint: handshake + gRPC serving, fully managed. It never returns.
//
// VersionedPlugins rather than Plugins: the plugin announces every protocol version it can speak and the
// handshake settles on the highest the host also understands. With a single fixed version, the day Core
// bumps the protocol is the day every published plugin stops loading at once — see SupportedProtocols.
func Serve(h Handler) {
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig:  Handshake,
		VersionedPlugins: VersionedPluginMap(&pluginServer{handler: h}),
		GRPCServer:       plugin.DefaultGRPCServer,
	})
}

// StartHTTP starts the plugin's resident HTTP server on an ephemeral localhost port and returns its
// address for InitResult.RouteAddr. Localhost-only: the ONLY way traffic reaches it in production is
// Core's reverse proxy of the declared prefix.
func StartHTTP(h http.Handler) (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("plugin http listen: %w", err)
	}
	go func() { _ = http.Serve(ln, h) }()
	return ln.Addr().String(), nil
}

// pluginServer adapts the author's Handler onto the gRPC contract (runs inside the plugin process).
type pluginServer struct {
	contract.UnimplementedPluginServiceServer
	handler Handler
	broker  *plugin.GRPCBroker // injected by GRPCPlugin.GRPCServer
	conn    *grpc.ClientConn   // the dialed HostService broker stream
}

func (s *pluginServer) Init(ctx context.Context, req *contract.InitRequest) (*contract.InitResponse, error) {
	conn, err := s.broker.Dial(req.HostBrokerId)
	if err != nil {
		return nil, fmt.Errorf("dial host service: %w", err)
	}
	s.conn = conn
	core := &Core{
		PluginKey:    req.PluginKey,
		NildaVersion: req.NildaVersion,
		Granted:      req.GrantedCapabilities,
		DatastoreDSN: req.DatastoreDsn,
		KVNamespace:  req.KvNamespace,
		host:         contract.NewHostServiceClient(conn),
		api:          newAPI(req.ApiBaseUrl, req.ApiToken, req.ApiScopes),
	}
	res, err := s.handler.Init(ctx, core)
	if err != nil {
		return nil, err
	}
	out := &contract.InitResponse{RouteAddr: res.RouteAddr, Hooks: res.Hooks, Events: res.Events}
	for _, sc := range res.Schedules {
		out.Schedules = append(out.Schedules, &contract.Schedule{Name: sc.Name, Cron: sc.Cron})
	}
	return out, nil
}

func (s *pluginServer) HandleHook(ctx context.Context, req *contract.HookRequest) (*contract.HookResponse, error) {
	out, err := s.handler.HandleHook(ctx, req.Hook, req.Payload)
	if err != nil {
		return nil, err
	}
	return &contract.HookResponse{Payload: out}, nil
}

func (s *pluginServer) HandleEvent(ctx context.Context, req *contract.EventRequest) (*contract.EventResponse, error) {
	if err := s.handler.HandleEvent(ctx, req.Type, req.DataJson); err != nil {
		return nil, err
	}
	return &contract.EventResponse{}, nil
}

func (s *pluginServer) Health(ctx context.Context, _ *contract.HealthRequest) (*contract.HealthResponse, error) {
	return &contract.HealthResponse{Ok: true}, nil
}
