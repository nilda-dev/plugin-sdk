package nilda

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

// THE HTTPS HALF OF PLUGIN IDENTIFICATION.
//
// Core's egress proxy allows a host only if the CALLING plugin declared it, so it has to know who is
// calling. For a plain http request that is a header on the request. For https it cannot be: Go opens the
// tunnel with CONNECT and sends everything else inside TLS, where the proxy sees nothing.
//
// The SDK shipped with only the request header. Identification therefore worked for the calls nobody makes
// and failed for every call to a real provider — a payment gateway, an SMS API, an identity provider. The
// symptom was a proxy log reading "host not declared in the manifest" with an EMPTY plugin key while the
// manifest declared the host perfectly.
//
// This test runs a real CONNECT proxy in front of a real TLS server, because nothing smaller would have
// caught it.

func TestThePluginIsIdentifiedOnAnHTTPSCallThroughTheProxy(t *testing.T) {
	t.Setenv("NILDA_PLUGIN_KEY", "signin_demo")

	// The destination: a TLS server the proxy will tunnel to.
	dest := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer dest.Close()

	// The proxy: records who asked on the CONNECT, then tunnels.
	seen := make(chan string, 1)
	proxy := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusBadRequest)
			return
		}
		select {
		case seen <- r.Header.Get("X-Nilda-Plugin"):
		default:
		}
		upstream, err := net.DialTimeout("tcp", r.URL.Host, 5*time.Second)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		client, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client); upstream.Close() }()
		go func() { _, _ = io.Copy(client, upstream); client.Close() }()
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() { _ = proxy.Serve(ln) }()

	// A client built the way a plugin's is, pointed at the proxy and trusting the test server's cert.
	pool := x509.NewCertPool()
	pool.AddCert(dest.Certificate())
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.Proxy = func(*http.Request) (*url.URL, error) { return url.Parse("http://" + ln.Addr().String()) }
	base.TLSClientConfig = &tls.Config{RootCAs: pool}

	c := &http.Client{Timeout: 10 * time.Second, Transport: NewTransport(base)}
	res, err := c.Get(dest.URL)
	if err != nil {
		t.Fatalf("request through the proxy: %v", err)
	}
	defer res.Body.Close()

	select {
	case got := <-seen:
		if got != "signin_demo" {
			t.Fatalf("the CONNECT carried X-Nilda-Plugin=%q — the egress proxy cannot tell who is calling, "+
				"so every https request to a DECLARED host is refused", got)
		}
	default:
		t.Fatal("the proxy was never reached")
	}
}
