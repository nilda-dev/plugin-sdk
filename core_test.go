package nilda

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"gitlab.com/nilda-sdk/plugin-sdk/contract"
)

// fakeHost is an in-process HostService used to prove the contract + Core wrappers round-trip.
//
// It is small because HostService is now small: contract v2 removed the five data-read methods in favour
// of a scoped token onto Core's own API, leaving this transport with what belongs on it — the plugin's own
// KV namespace and event emission. Data access is covered by api_test.go instead.
type fakeHost struct {
	contract.UnimplementedHostServiceServer
	kv       map[string]string
	emitted  []string
	emitFail error
}

func (f *fakeHost) KVSet(_ context.Context, req *contract.KVSetRequest) (*contract.KVSetResponse, error) {
	f.kv[req.Key] = req.Value
	return &contract.KVSetResponse{}, nil
}

func (f *fakeHost) KVGet(_ context.Context, req *contract.KVGetRequest) (*contract.KVGetResponse, error) {
	v, ok := f.kv[req.Key]
	return &contract.KVGetResponse{Value: v, Found: ok}, nil
}

func (f *fakeHost) KVDel(_ context.Context, req *contract.KVDelRequest) (*contract.KVDelResponse, error) {
	delete(f.kv, req.Key)
	return &contract.KVDelResponse{}, nil
}

func (f *fakeHost) EmitEvent(_ context.Context, req *contract.EmitEventRequest) (*contract.EmitEventResponse, error) {
	if f.emitFail != nil {
		return nil, f.emitFail
	}
	f.emitted = append(f.emitted, req.Type)
	return &contract.EmitEventResponse{}, nil
}

// TestContractRoundTrip proves the generated contract + the typed Core wrappers marshal correctly
// over a real gRPC connection (in-process bufconn; the out-of-process path is Core's host tests).
func TestContractRoundTrip(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	host := &fakeHost{kv: map[string]string{}}
	contract.RegisterHostServiceServer(s, host)
	go func() { _ = s.Serve(ln) }()
	defer s.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	core := NewCoreForTest("refplugin", []string{"kv", "events"}, contract.NewHostServiceClient(conn))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := core.KVSet(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	v, found, err := core.KVGet(ctx, "k")
	if err != nil || !found || v != "v" {
		t.Fatalf("KVGet = (%q,%v,%v), want (v,true,nil)", v, found, err)
	}
	if err := core.KVDel(ctx, "k"); err != nil {
		t.Fatalf("KVDel: %v", err)
	}
	if _, found, _ := core.KVGet(ctx, "k"); found {
		t.Fatal("a deleted key is still present")
	}

	if err := core.Emit(ctx, "shop.order.placed", map[string]any{"id": 7}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if len(host.emitted) != 1 || host.emitted[0] != "shop.order.placed" {
		t.Fatalf("emitted = %v", host.emitted)
	}

	if !core.HasCapability("kv") || core.HasCapability("route") {
		t.Fatal("HasCapability mirror wrong")
	}

	// A plugin built for tests has no API unless one is wired — HasAPI is the check that keeps that from
	// surfacing as a nil dereference three calls into a handler.
	if core.HasAPI() {
		t.Fatal("NewCoreForTest handed out API access nobody granted")
	}
}

// The handshake NEGOTIATES a protocol version; it does not demand one exact number.
//
// This is what stops a protocol bump from being a flag day. With a single fixed version, the day Core
// moves to 3 is the day every plugin anyone has published stops loading — simultaneously, on every site,
// until each author rebuilds and each owner updates. go-plugin's VersionedPlugins lets a host serve
// several versions at once and settle on the highest both ends know, so a v2 plugin keeps working on a
// Core that also speaks v3.
//
// The test is about the SHAPE, because the shape is the whole change: a map keyed by version means adding
// the next protocol is a line, and a single set means it is a migration.
func TestTheProtocolSetIsNegotiable(t *testing.T) {
	set := VersionedPluginMap(nil)
	if len(set) == 0 {
		t.Fatal("no protocol versions offered — nothing could ever handshake")
	}
	for _, v := range SupportedProtocols {
		plugins, ok := set[v]
		if !ok {
			t.Errorf("protocol %d is declared supported but is not in the plugin set", v)
			continue
		}
		if _, ok := plugins[PluginSetName]; !ok {
			t.Errorf("protocol %d offers no %q plugin", v, PluginSetName)
		}
	}
	// The current version must be among them, or a freshly built plugin could not talk to a freshly built
	// Core — the one pairing that has to work.
	if _, ok := set[ProtocolVersion]; !ok {
		t.Errorf("the current protocol (%d) is not in the negotiable set", ProtocolVersion)
	}
	// And a version nobody supports must be absent rather than silently accepted.
	if _, ok := set[ProtocolVersion+1]; ok {
		t.Error("an unsupported protocol version is offered; a plugin built against it would load and then " +
			"fail on a method that does not exist")
	}
}
