package nilda

import "testing"

// THE VERSION THIS MODULE IS ABOUT TO BECOME MUST NOT BE "UNKNOWN".
//
// ProtocolForSDK stopped at v0 and answered `unknown` for everything above it, with a comment saying a
// v1+ SDK did not exist yet. That is right for a version nobody has built and wrong for the NEXT one:
// tagging v1.0.0 would have made `nilda plugin check` tell an author that the SDK the scaffold had just
// pinned in their go.mod is not a version this Nilda knows about — a failure that fires on the tag, which
// is the one moment nobody re-reads this function.
func TestTheNextMajorIsNotUnknown(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"v1.0.0", "v1.0.0-rc.1", "1.2.3", "v1.7.0+build.9"} {
		got, known := ProtocolForSDK(v)
		if !known {
			t.Errorf("ProtocolForSDK(%q) is unknown; an author on the first stable SDK would be told their "+
				"own version is not one this Nilda knows about", v)
			continue
		}
		if got != ProtocolVersion {
			t.Errorf("ProtocolForSDK(%q) = %d, and this build speaks %d", v, got, ProtocolVersion)
		}
	}
}

// …and the honest unknown is still honest. A version above the last one anybody has built must NOT be
// guessed: a check that passes for a pairing nobody has run is worse than one that says it cannot tell.
func TestAVersionNobodyHasBuiltIsStillUnknown(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"v2.0.0", "v9.1.0"} {
		if _, known := ProtocolForSDK(v); known {
			t.Errorf("ProtocolForSDK(%q) claims to know a protocol for an SDK major that does not exist", v)
		}
	}
	for _, v := range []string{"", "v1", "banana", "v1.x"} {
		if _, known := ProtocolForSDK(v); known {
			t.Errorf("ProtocolForSDK(%q) accepted an unparseable version", v)
		}
	}
}
