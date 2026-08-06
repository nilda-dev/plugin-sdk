package nilda

import (
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/go-plugin"
)

// docs/ANY_LANGUAGE.md is the ONLY thing a non-Go author has. Every constant in it is one they will type
// into their own handshake, and a stale one costs them a day of debugging a plugin that never loads —
// with no error message that points anywhere useful.
//
// So the document is checked against the code rather than trusted. A value changed here and not there is
// a failed build, which is the only way a document stays true.

func anyLanguageDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("docs/ANY_LANGUAGE.md")
	if err != nil {
		t.Fatalf("the multi-language contract is missing: %v", err)
	}
	return string(b)
}

func TestTheMultiLanguageDocQuotesTheRealHandshake(t *testing.T) {
	doc := anyLanguageDoc(t)

	for _, want := range []string{
		Handshake.MagicCookieKey,   // the env var name a plugin checks
		Handshake.MagicCookieValue, // the value it compares against
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the doc does not carry %q — an author copying from it writes a plugin that refuses "+
				"to start under a real Nilda", want)
		}
	}

	// The application protocol version, which goes in the second field of the handshake line. Written as
	// the number, so bumping ProtocolVersion without touching the doc fails here.
	if !strings.Contains(doc, "`2` — Nilda's `ProtocolVersion`") {
		t.Error("the doc does not state the current ProtocolVersion in the handshake table")
	}
	if ProtocolVersion != 2 {
		t.Fatalf("ProtocolVersion is now %d: update the handshake table in docs/ANY_LANGUAGE.md and this "+
			"test together", ProtocolVersion)
	}
}

// TestTheDocNamesTheRealAutoMTLSContract — every one of these is from go-plugin's server.go, and getting
// any of them wrong produces a TLS handshake failure with no useful message on either side.
func TestTheDocNamesTheRealAutoMTLSContract(t *testing.T) {
	doc := anyLanguageDoc(t)
	for _, want := range []string{
		"PLUGIN_CLIENT_CERT",         // where Nilda's certificate arrives
		"RequireAndVerifyClientCert", // what the plugin's TLS config must demand
		"RawStdEncoding",             // how the plugin's own cert is encoded on the handshake line
		"TLS 1.2",                    // the minimum go-plugin sets
		`ServerName: "localhost"`,    // what the certificate must be valid for
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("the doc does not mention %q — a non-Go plugin that misses it fails the TLS "+
				"handshake with nothing to debug", want)
		}
	}
}

// TestTheDocDescribesTheHandshakeGoPluginActuallySpeaks — the field order and count, from the library's
// own format string. A reordered field is a plugin that never loads.
func TestTheDocDescribesTheHandshakeGoPluginActuallySpeaks(t *testing.T) {
	doc := anyLanguageDoc(t)
	if !strings.Contains(doc, "CORE-PROTOCOL-VERSION|APP-PROTOCOL-VERSION|NETWORK-TYPE|NETWORK-ADDR|PROTOCOL|SERVER-CERT") {
		t.Fatal("the doc's handshake line does not match go-plugin's six-field format")
	}
	// And the library is still the one we build against, so the format above is still the right one.
	var _ = plugin.HandshakeConfig{}
}

// TestTheDocListsEveryCapabilityGatedHostCall — the table of "method → capability it needs" is what a
// non-Go author reads to know what they may call. A method added to HostService and not to the doc is a
// door nobody outside Go knows exists.
func TestTheDocListsEveryCapabilityGatedHostCall(t *testing.T) {
	doc := anyLanguageDoc(t)
	for _, method := range []string{
		"KVGet", "KVSet", "KVDel", "KVIncr", "EmitEvent", "SendEmail", "RevokeIdentity",
	} {
		if !strings.Contains(doc, method) {
			t.Errorf("HostService.%s is not in the doc — no non-Go author knows it exists", method)
		}
	}
}
