package ratelimit

import (
	"context"

	"github.com/Jiusheng01/campus-ai-gateway/internal/model"
)

// RebuildResult reports how many counters a rebuild wrote back, for the
// operator log and the manual reconciliation path.
type RebuildResult struct {
	DailyTokens   int // daily token counters set
	MonthlyTokens int // monthly token counters set
	MonthlyReqs   int // monthly request counters set
}

// RebuildQuotaCounters resets the tenant quota counters to the settled usage
// derived from the request log (the durable truth of the system).
//
// settled is one row per (tenant, UTC day), produced by aggregating
// gateway_request_log. Every counter is written with SET, not INCRBY: a rebuild
// replaces the cache with the log-derived baseline, so a stale or inflated live
// counter (in-flight reservations that never settled, or a cache that was
// cleared) cannot push the gate's view away from the truth. In-flight usage is
// deliberately absent from settled and is therefore not counted; it reconciles
// or disappears with the request that holds it.
//
// The month counters are derived as the sum of the day rows, and the period key
// of a month is the day key's prefix, so the reconstructed keys are exactly the
// ones the limiter's hot path reads (see quotaRequestsKey / quotaDailyTokensKey
// / quotaMonthlyTokensKey). Triggered at startup, when a counter is found
// missing, and on manual reconciliation.
func (l *Limiter) RebuildQuotaCounters(ctx context.Context, settled []model.QuotaSettledRow) (RebuildResult, error) {
	var res RebuildResult

	// Daily token counters: one per settled (tenant, day), overwriting whatever
	// lived in the cache before.
	dayTokens := make(map[string]int64, len(settled))

	// Monthly aggregates folded from the day rows, keyed by (tenant, month) so
	// the two month counters (tokens and requests) accumulate across the month's
	// days without confusing tenants. A struct key avoids any delimiter-parsing
	// ambiguity in the tenant segment.
	type monthKey struct{ tenant, month string }
	type monthly struct{ tokens, requests int64 }
	months := make(map[monthKey]monthly)

	for _, r := range settled {
		if r.TenantID == "" || r.Day == "" {
			continue
		}
		dayTokens[quotaDailyTokensKey(r.TenantID, r.Day)] += r.Tokens
		if len(r.Day) < 6 {
			continue
		}
		k := monthKey{tenant: r.TenantID, month: r.Day[:6]}
		m := months[k]
		m.tokens += r.Tokens
		m.requests += r.Requests
		months[k] = m
	}

	pipe := l.rdb.TxPipeline()
	var keys []string
	for key, tokens := range dayTokens {
		pipe.Set(ctx, key, tokens, quotaDailyTTL)
		keys = append(keys, key)
	}
	res.DailyTokens = len(keys)

	for k, m := range months {
		pipe.Set(ctx, quotaMonthlyTokensKey(k.tenant, k.month), m.tokens, quotaMonthlyTTL)
		pipe.Set(ctx, quotaRequestsKey(k.tenant, k.month), m.requests, quotaMonthlyTTL)
		res.MonthlyTokens++
		res.MonthlyReqs++
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return res, err
	}
	return res, nil
}
