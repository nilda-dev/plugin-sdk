package nilda

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"github.com/nilda-dev/plugin-sdk/contract"
)

// M3-1 (Core's WordPress milestone 3, threat rows SH24 and SH27): a plugin serving one of Core's calls names that call
// on what it sends back to Core — its API requests carry CallHeader, its events the same id in their gRPC metadata —
// so Core can tell an importer's own callbacks from everything else the plugin does. Outside a call, nothing.

// TestTheAPIClientNamesTheCallItServes: every verb, over plain http and over https alike (the API base is Core's
// loopback address, and the header is a label, not a secret — unlike X-Nilda-Plugin it is not held to plain http).
func TestTheAPIClientNamesTheCallItServes(t *testing.T) {
	for _, scheme := range []string{"http", "https"} {
		t.Run(scheme, func(t *testing.T) {
			// seen holds, per verb, the header's values on the last request: nil when it was absent.
			var mu sync.Mutex
			seen := map[string][]string{}
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				seen[r.Method] = r.Header.Values(CallHeader)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			})
			var srv *httptest.Server
			if scheme == "https" {
				srv = httptest.NewTLSServer(h)
			} else {
				srv = httptest.NewServer(h)
			}
			defer srv.Close()
			api := newAPI(srv.URL, "nlda_secret", nil)
			if scheme == "https" {
				api.http = srv.Client()
			}

			calls := func(ctx context.Context) {
				t.Helper()
				if err := api.Get(ctx, "/content", nil, nil); err != nil {
					t.Fatalf("GET: %v", err)
				}
				if err := api.Post(ctx, "/content", map[string]any{"title": "x"}, nil); err != nil {
					t.Fatalf("POST: %v", err)
				}
				if err := api.Patch(ctx, "/content/1", map[string]any{"title": "y"}, nil); err != nil {
					t.Fatalf("PATCH: %v", err)
				}
				if err := api.Delete(ctx, "/content/1"); err != nil {
					t.Fatalf("DELETE: %v", err)
				}
			}
			verbs := []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete}

			calls(WithCallInfo(context.Background(), CallInfo{RequestID: "r1"}))
			for _, v := range verbs {
				if got := seen[v]; len(got) != 1 || got[0] != "r1" {
					t.Errorf("%s inside call r1 sent %s = %q, want [r1]", v, CallHeader, got)
				}
			}

			// Outside a call — and inside one whose request Core did not name — the header is absent, not empty.
			for _, ctx := range []context.Context{
				context.Background(),
				WithCallInfo(context.Background(), CallInfo{Caller: "other", CallerKind: CallerKindImporter}),
			} {
				seen = map[string][]string{}
				calls(ctx)
				for _, v := range verbs {
					if got, ok := seen[v]; !ok || got != nil {
						t.Errorf("%s outside a named call sent %s = %q (reached: %v), want no header", v, CallHeader, got, ok)
					}
				}
			}
		})
	}
}

// metaHost is a HostService that keeps the incoming gRPC metadata of each EmitEvent.
type metaHost struct {
	contract.UnimplementedHostServiceServer
	mu    sync.Mutex
	calls [][]string
}

func (m *metaHost) EmitEvent(ctx context.Context, _ *contract.EmitEventRequest) (*contract.EmitEventResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	m.mu.Lock()
	m.calls = append(m.calls, md.Get("x-nilda-call"))
	m.mu.Unlock()
	return &contract.EmitEventResponse{}, nil
}

// TestAnEventNamesTheCallItServes: an HTTP header cannot mark an event, which travels over gRPC — so Emit puts the
// same id in EmitEvent's gRPC metadata, under x-nilda-call, over a real gRPC connection.
func TestAnEventNamesTheCallItServes(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	host := &metaHost{}
	contract.RegisterHostServiceServer(s, host)
	go func() { _ = s.Serve(ln) }()
	defer s.Stop()
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	core := NewCoreForTest("shop", []string{"events"}, contract.NewHostServiceClient(conn), "", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := core.Emit(WithCallInfo(ctx, CallInfo{RequestID: "r1"}), "shop.changed", nil); err != nil {
		t.Fatalf("Emit inside a call: %v", err)
	}
	if err := core.Emit(ctx, "shop.changed", nil); err != nil {
		t.Fatalf("Emit outside a call: %v", err)
	}
	if err := core.Emit(WithCallInfo(ctx, CallInfo{Caller: "other"}), "shop.changed", nil); err != nil {
		t.Fatalf("Emit inside an unnamed call: %v", err)
	}
	if len(host.calls) != 3 {
		t.Fatalf("Core received %d events, want 3", len(host.calls))
	}
	if got := host.calls[0]; len(got) != 1 || got[0] != "r1" {
		t.Errorf("an event emitted inside call r1 carried x-nilda-call = %v, want [r1]", got)
	}
	for i, got := range host.calls[1:] {
		if len(got) != 0 {
			t.Errorf("event %d, emitted outside a named call, carried x-nilda-call = %v", i+2, got)
		}
	}
}

// TestTheCallHeaderIsInTheSurface: the header's name is a promise to Core (which reads it) — frozen in surface.txt,
// spelled as Core reads it, and named in the guide an author reads.
func TestTheCallHeaderIsInTheSurface(t *testing.T) {
	if CallHeader != "X-Nilda-Call" {
		t.Fatalf("CallHeader = %q: Core reads X-Nilda-Call", CallHeader)
	}
	if !strings.Contains("\n"+readText(t, surfaceLedger), "\nconst CallHeader\n") {
		t.Error("surface.txt does not freeze const CallHeader")
	}
	if !strings.Contains(readText(t, "docs/PLUGIN_SDK.md"), "`CallHeader`") {
		t.Error("docs/PLUGIN_SDK.md does not name `CallHeader`")
	}
}
