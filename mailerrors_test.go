package nilda

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// statusWith builds the status Core answers a refused mail with: a code, a reason, and optionally when to ask again.
func statusWith(code codes.Code, reason string, retry time.Duration) error {
	st := status.New(code, "refused")
	if reason != "" {
		st, _ = st.WithDetails(&errdetails.ErrorInfo{Reason: reason, Domain: "nilda.dev"})
	}
	if retry > 0 {
		st, _ = st.WithDetails(&errdetails.RetryInfo{RetryDelay: durationpb.New(retry)})
	}
	return st.Err()
}

func TestMailDeferredReadsRetryInfo(t *testing.T) {
	after, ok := MailDeferred(statusWith(codes.Unavailable, "MAIL_DEFERRED", time.Hour))
	if !ok || after != time.Hour {
		t.Fatalf("a paused site's refusal: %v %v, want one hour and true", after, ok)
	}
	// Wrapped, as a plugin's own code wraps it.
	if after, ok := MailDeferred(fmt.Errorf("send the receipt: %w", statusWith(codes.Unavailable, "MAIL_DEFERRED", 30*time.Minute))); !ok || after != 30*time.Minute {
		t.Errorf("a wrapped refusal: %v %v", after, ok)
	}
	// Core named no time: deferred, and zero.
	if after, ok := MailDeferred(statusWith(codes.Unavailable, "MAIL_DEFERRED", 0)); !ok || after != 0 {
		t.Errorf("no RetryInfo: %v %v, want zero and true", after, ok)
	}
}

func TestMailAddressBlockedIsNotDeferred(t *testing.T) {
	err := statusWith(codes.FailedPrecondition, "ADDRESS_BLOCKED", 0)
	if !MailAddressBlocked(err) {
		t.Error("a bounced address was not read as blocked")
	}
	if _, ok := MailDeferred(err); ok {
		t.Error("a bounced address was read as worth asking again")
	}
	if MailAddressBlocked(statusWith(codes.Unavailable, "MAIL_DEFERRED", time.Hour)) {
		t.Error("a paused site was read as a blocked address")
	}
}

// The per-plugin sending rate is a deferral too: ResourceExhausted with RetryInfo.
func TestARateLimitIsDeferred(t *testing.T) {
	after, ok := MailDeferred(statusWith(codes.ResourceExhausted, "", 2*time.Second))
	if !ok || after != 2*time.Second {
		t.Fatalf("a rate limit: %v %v, want 2s and true", after, ok)
	}
}

func TestAPlainErrorIsNeither(t *testing.T) {
	for _, err := range []error{nil, errors.New("boom"), statusWith(codes.Unavailable, "MAIL_FAILED", 0), statusWith(codes.Internal, "", 0)} {
		if MailAddressBlocked(err) {
			t.Errorf("%v read as blocked", err)
		}
		if _, ok := MailDeferred(err); ok {
			t.Errorf("%v read as deferred", err)
		}
	}
}
