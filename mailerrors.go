package nilda

import (
	"errors"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The reasons Core gives in a refused mail's ErrorInfo (core internal/plugin/mailstatus.go).
const (
	mailReasonBlocked  = "ADDRESS_BLOCKED"
	mailReasonDeferred = "MAIL_DEFERRED"
)

// MailAddressBlocked reports a transactional mail Core refused because its address bounced or complained: nothing was
// sent, and asking again will not change it — stop mailing that address and tell the owner. A receipt nobody can receive
// is a fact the shop must keep, not a success.
func MailAddressBlocked(err error) bool {
	st, ok := mailStatusOf(err)
	return ok && st.Code() == codes.FailedPrecondition && mailReason(st) == mailReasonBlocked
}

// MailDeferred reports a mail Core did not send now — the site's email paused by the platform, its daily allowance
// spent, or this plugin over its sending rate — and how long to wait before asking again (0 when Core named no time).
// Nothing was sent, and nothing was lost: ask again later, and do not treat the mail as delivered in the meantime (a gift
// card's code, a licence key).
func MailDeferred(err error) (retryAfter time.Duration, ok bool) {
	st, found := mailStatusOf(err)
	if !found {
		return 0, false
	}
	switch {
	case st.Code() == codes.Unavailable && mailReason(st) == mailReasonDeferred:
	case st.Code() == codes.ResourceExhausted:
	default:
		return 0, false
	}
	for _, d := range st.Details() {
		if info, isRetry := d.(*errdetails.RetryInfo); isRetry {
			return info.GetRetryDelay().AsDuration(), true
		}
	}
	return 0, true
}

// mailStatusOf is the gRPC status inside err, however many times the plugin wrapped it.
func mailStatusOf(err error) (*status.Status, bool) {
	if err == nil {
		return nil, false
	}
	var g interface{ GRPCStatus() *status.Status }
	if !errors.As(err, &g) {
		return nil, false
	}
	return g.GRPCStatus(), true
}

// mailReason is the reason in a status's ErrorInfo, "" when it has none.
func mailReason(st *status.Status) string {
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info.GetReason()
		}
	}
	return ""
}
