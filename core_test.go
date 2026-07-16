package nilda

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"nilda.dev/plugin-sdk/contract"
)

// fakeHost is an in-process HostService used to prove the contract + Core wrappers round-trip.
type fakeHost struct {
	contract.UnimplementedHostServiceServer
	kv map[string]string
}

func (f *fakeHost) ContentSite(ctx context.Context, _ *contract.SiteRequest) (*contract.Site, error) {
	return &contract.Site{Title: "Nilda", BaseUrl: "https://example.com", Locale: "en"}, nil
}

func (f *fakeHost) KVSet(ctx context.Context, req *contract.KVSetRequest) (*contract.KVSetResponse, error) {
	f.kv[req.Key] = req.Value
	return &contract.KVSetResponse{}, nil
}

func (f *fakeHost) KVGet(ctx context.Context, req *contract.KVGetRequest) (*contract.KVGetResponse, error) {
	v, ok := f.kv[req.Key]
	return &contract.KVGetResponse{Value: v, Found: ok}, nil
}

func (f *fakeHost) ContentPageBySlug(ctx context.Context, req *contract.PageBySlugRequest) (*contract.PageBySlugResponse, error) {
	if req.Slug != "hello" {
		return &contract.PageBySlugResponse{Found: false}, nil
	}
	return &contract.PageBySlugResponse{Found: true, Page: &contract.Page{
		Id: "p1", Type: "post", Title: "Hello", Slug: "hello",
		BodyJson: []byte(`{"blocks":[]}`), PublishedAtUnix: 1700000000, AuthorId: "u1",
	}}, nil
}

// TestContractRoundTrip proves the generated contract + the typed Core wrappers marshal correctly
// over a real gRPC connection (in-process bufconn; the out-of-process path is Core's host tests).
func TestContractRoundTrip(t *testing.T) {
	ln := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	contract.RegisterHostServiceServer(s, &fakeHost{kv: map[string]string{}})
	go func() { _ = s.Serve(ln) }()
	defer s.Stop()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return ln.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	core := NewCoreForTest("refplugin", []string{"kv", "content.read"}, contract.NewHostServiceClient(conn))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	site, err := core.Site(ctx)
	if err != nil || site.Title != "Nilda" || site.BaseURL != "https://example.com" {
		t.Fatalf("Site round-trip: %+v err=%v", site, err)
	}

	if err := core.KVSet(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("KVSet: %v", err)
	}
	v, found, err := core.KVGet(ctx, "k")
	if err != nil || !found || v != "v" {
		t.Fatalf("KVGet = (%q,%v,%v), want (v,true,nil)", v, found, err)
	}

	page, found, err := core.PageBySlug(ctx, "hello")
	if err != nil || !found || page.ID != "p1" || string(page.Body) != `{"blocks":[]}` ||
		!page.PublishedAt.Equal(time.Unix(1700000000, 0).UTC()) {
		t.Fatalf("PageBySlug round-trip: %+v found=%v err=%v", page, found, err)
	}
	if _, found, _ := core.PageBySlug(ctx, "missing"); found {
		t.Fatal("missing slug must report found=false")
	}

	if !core.HasCapability("kv") || core.HasCapability("route") {
		t.Fatal("HasCapability mirror wrong")
	}
}
