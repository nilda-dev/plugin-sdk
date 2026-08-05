package nilda

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"

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
	// Abilities is what this plugin can DO, offered to the site's AI agent (requires `abilities`). Each
	// becomes an agent tool named "<plugin-key>.<name>". An ability with a Run function is dispatched for
	// you; one without arrives at HandleHook under AbilityHook(name). See abilities.go.
	Abilities []Ability
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
	// abilityRun holds the Run functions from Init, keyed by ability name. Written once in Init and read
	// by HandleHook; go-plugin serialises Init before any hook arrives.
	abilityRun map[string]func(context.Context, json.RawMessage) (any, error)
	broker     *plugin.GRPCBroker // injected by GRPCPlugin.GRPCServer
	conn       *grpc.ClientConn   // the dialed HostService broker stream
}

func (s *pluginServer) Init(ctx context.Context, req *contract.InitRequest) (*contract.InitResponse, error) {
	conn, err := s.broker.Dial(req.HostBrokerId)
	if err != nil {
		return nil, fmt.Errorf("dial host service: %w", err)
	}
	s.conn = conn
	core := &Core{
		PluginKey:       req.PluginKey,
		NildaVersion:    req.NildaVersion,
		Granted:         req.GrantedCapabilities,
		DatastoreDSN:    req.DatastoreDsn,
		logger:          newLogger(os.Stderr, req.PluginKey),
		settings:        decodeSettings(req.GetSettingsJson(), req.PluginKey),
		PreviousVersion: req.GetPreviousVersion(),
		KVNamespace:     req.KvNamespace,
		RoutePrefix:     req.GetRoutePrefix(),
		host:            contract.NewHostServiceClient(conn),
		api:             newAPI(req.ApiBaseUrl, req.ApiToken, req.ApiScopes),
	}
	res, err := s.handler.Init(ctx, core)
	if err != nil {
		return nil, err
	}
	out := &contract.InitResponse{
		RouteAddr: res.RouteAddr,
		// Subscriptions the optional interfaces imply are added here rather than left to the author, because
		// implementing AssetProvider and forgetting to list the hook yields a plugin that runs and does
		// nothing. See withProvidedHooks.
		Hooks:  withProvidedHooks(s.handler, res.Hooks),
		Events: res.Events,
	}
	for _, sc := range res.Schedules {
		out.Schedules = append(out.Schedules, &contract.Schedule{Name: sc.Name, Cron: sc.Cron})
	}
	for _, ab := range res.Abilities {
		out.Abilities = append(out.Abilities, &contract.Ability{
			Name: ab.Name, Label: ab.Label, Description: ab.Description,
			Class: string(ab.Class), InputSchema: string(ab.InputSchema), ReadOnly: ab.ReadOnly,
		})
	}
	// Remember the ones that brought their own Run so HandleHook can dispatch them without the author
	// wiring anything: declaring an ability and then forgetting to route its hook is a plugin that
	// advertises an action and fails every time an agent tries it.
	s.abilityRun = abilityRunners(res.Abilities)
	return out, nil
}

func (s *pluginServer) HandleHook(ctx context.Context, req *contract.HookRequest) (*contract.HookResponse, error) {
	// Abilities first: "ability:" is a reserved hook namespace, so it must never reach the author's
	// HandleHook whether or not they declared a runner for that name.
	if out, handled, err := dispatchAbility(ctx, s.abilityRun, req.Hook, req.Payload); handled {
		if err != nil {
			return nil, err
		}
		return &contract.HookResponse{Payload: out}, nil
	}
	// The optional interfaces get first refusal, so a WidgetProvider never sees a hook name or a byte
	// slice. Falls through when the handler implements neither, which is every plugin that does not
	// contribute markup.
	if out, ok, err := dispatchProvided(ctx, s.handler, req.Hook, req.Payload); ok {
		if err != nil {
			return nil, err
		}
		return &contract.HookResponse{Payload: out}, nil
	}
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

// decodeSettings turns Core's settings blob into the map the Setting* accessors read.
//
// Unparseable settings are a WARNING and an empty map, not a failed Init. A plugin that cannot start
// because one stored value is malformed is a plugin the owner cannot get back to the screen to FIX it on —
// the section in the sidebar is served by Core, but a plugin stuck in a restart loop is disabled by the
// supervisor and its pages go with it.
func decodeSettings(raw []byte, key string) map[string]any {
	out := map[string]any{}
	if len(raw) == 0 {
		return out
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		newLogger(os.Stderr, key).Warn("this plugin's settings did not parse; starting with none", "error", err)
		return map[string]any{}
	}
	return out
}
