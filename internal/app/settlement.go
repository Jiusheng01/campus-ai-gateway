package app

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/ratelimit"
)

// clientClosedRequest is the non-standard status nginx uses for a client that
// hung up before the gateway answered. The response cannot be written (nobody
// is listening); the status exists so the request log records the truth instead
// of a 200 the fallback would imply.
const clientClosedRequest = 499

// quotaOutcome is the single decision the deferred settlement makes about the
// tokens reserved during admission.
type quotaOutcome int

const (
	// quotaSettle: the channel answered and reported usage, so the reservation
	// is replaced by what the request actually used.
	quotaSettle quotaOutcome = iota
	// quotaEstimate: the channel answered but reported no usage. The reservation
	// stands as the charge. Keeping the estimate is deliberate: a provider that
	// stopped reporting usage must not turn into free traffic.
	quotaEstimate
	// quotaRefund: the request was not served, so nothing is charged.
	quotaRefund
)

// quotaOutcomeFor decides how a finished request settles. Only a 2xx counts as
// served: a 4xx/5xx is the gateway's or the channel's failure, and charging the
// caller for it is exactly the bug this function exists to prevent.
func quotaOutcomeFor(status int, usage usageResult) quotaOutcome {
	switch {
	case status < 200 || status >= 300:
		return quotaRefund
	case usage.HasUsage && usage.Total > 0:
		return quotaSettle
	default:
		return quotaEstimate
	}
}

// settleQuotaReservations applies one outcome to every chain charged at
// admission. The reservations are separate chains (tokens and TPM live on
// different keys but both span the tenant hierarchy), and they must settle
// together or the counters drift apart.
func settleQuotaReservations(ctx context.Context, outcome quotaOutcome, total int64, reservations ...*ratelimit.ChainReservation) {
	if outcome == quotaEstimate {
		return
	}
	for _, r := range reservations {
		if r == nil {
			continue
		}
		if outcome == quotaSettle {
			_ = r.Reconcile(ctx, total)
			continue
		}
		_ = r.Refund(ctx)
	}
}
